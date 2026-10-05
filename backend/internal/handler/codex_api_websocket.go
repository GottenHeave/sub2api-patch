package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type codexAPIWSFrameConn struct {
	conn *coderws.Conn
}

func (c codexAPIWSFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	return c.conn.Read(ctx)
}

func (c codexAPIWSFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, body []byte) error {
	return c.conn.Write(ctx, kind, body)
}

func (c codexAPIWSFrameConn) Close() error { return c.conn.CloseNow() }

func (h *OpenAIGatewayHandler) forwardCodexAPIWebSocket(c *gin.Context, key *service.APIKey, subject middleware2.AuthSubject, account *service.Account, target string, start time.Time) {
	ctx := c.Request.Context()
	maxIngress := 0
	if h.cfg != nil {
		maxIngress = h.cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
	}
	lease, acquired, err := h.concurrencyHelper.AcquireOpenAIWSIngressLease(ctx, key.ID, maxIngress)
	if err != nil || !acquired {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "WebSocket ingress capacity unavailable")
		return
	}
	if lease != nil {
		defer lease.Release()
		ctx = lease.Context()
	}
	upstream, response, err := h.gatewayService.DialCodexAPI(ctx, account, c.Request, target)
	if err != nil {
		if response != nil && response.Body != nil {
			defer func() { _ = response.Body.Close() }()
			_ = relayCodexAPIResponse(c, response)
		} else if ctx.Err() == nil {
			h.errorResponse(c, http.StatusBadGateway, "api_error", "Codex API WebSocket connection failed")
		}
		return
	}
	defer func() { _ = upstream.CloseNow() }()
	headers := response.Header.Clone()
	service.StripCodexAPIHopHeaders(headers)
	for name, values := range headers {
		if !strings.HasPrefix(strings.ToLower(name), "sec-websocket-") && !strings.EqualFold(name, "Content-Length") {
			c.Writer.Header()[name] = values
		}
	}
	protocols := []string(nil)
	if protocol := upstream.Subprotocol(); protocol != "" {
		protocols = []string{protocol}
	}
	client, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{Subprotocols: protocols})
	if err != nil {
		return
	}
	defer func() { _ = client.CloseNow() }()
	client.SetReadLimit(min(service.ResolveOpenAIWSClientReadLimitBytes(h.cfg), 16<<20))
	setOpenAIClientTransportWS(c)
	readContext := c.Copy()
	recordContext := c.Copy()
	var imageRelease func()
	var imageMu sync.Mutex
	imageClosed := false
	defer func() {
		imageMu.Lock()
		defer imageMu.Unlock()
		imageClosed = true
		if imageRelease != nil {
			imageRelease()
		}
	}()
	lastBilledID := ""
	_, _ = openaiwsv2.Relay(ctx, codexAPIWSFrameConn{client}, codexAPIWSFrameConn{upstream}, nil, openaiwsv2.RelayOptions{
		FirstMessageSent: true,
		IdleTimeout:      5 * time.Minute,
		ReadClientFrame: func(frameCtx context.Context, conn openaiwsv2.FrameConn) (coderws.MessageType, []byte, error) {
			kind, payload, err := conn.ReadFrame(frameCtx)
			if err != nil {
				return kind, payload, err
			}
			inspection := c.Request.Clone(frameCtx)
			inspection.Body = io.NopCloser(bytes.NewReader(payload))
			body, err := pkghttputil.ReadRequestBodyWithPrealloc(inspection)
			if err != nil || len(body) >= 64<<20 {
				_ = client.Close(coderws.StatusPolicyViolation, "Unable to inspect request frame")
				return kind, nil, fmt.Errorf("unable to inspect Codex API frame")
			}
			model := gjson.GetBytes(body, "model").String()
			if h.findBlockedCyberSessionForAPIKey(readContext, key, body) != "" {
				writeCyberSessionBlockedWSError(frameCtx, client)
				_ = client.Close(coderws.StatusPolicyViolation, "session blocked by cyber-security policy")
				return kind, nil, fmt.Errorf("blocked Codex API session")
			}
			if decision := h.checkSecurityAuditStage(readContext, requestLogger(readContext, "handler.codex_api.websocket"), key, subject, service.ContentModerationProtocolOpenAIResponses, model, body, "subsequent_turn"); decision != nil && !decision.AllowNextStage {
				writeSecurityAuditWSError(frameCtx, client, decision)
				_ = client.Close(securityAuditWSCloseStatus(decision), securityAuditWSCloseReason(decision))
				return kind, nil, fmt.Errorf("codex API frame rejected by security audit")
			}
			resourceAccount, err := h.gatewayService.CodexAPIResourceAccount(frameCtx, key, body, "")
			if err != nil || resourceAccount != 0 && resourceAccount != account.ID {
				_ = client.Close(coderws.StatusPolicyViolation, "Resource unavailable on this connection")
				return kind, nil, fmt.Errorf("codex API frame resource identity mismatch")
			}
			if service.IsExplicitImageGenerationIntent("/v1/responses", model, body) {
				if !service.GroupAllowsImageGeneration(key.Group) {
					_ = client.Close(coderws.StatusPolicyViolation, service.ImageGenerationPermissionMessage())
					return kind, nil, fmt.Errorf("codex API image permission denied")
				}
				imageMu.Lock()
				defer imageMu.Unlock()
				if imageClosed {
					return kind, nil, context.Canceled
				}
				if imageRelease == nil && h.cfg != nil && h.imageLimiter != nil {
					cfg := h.cfg.Gateway.ImageConcurrency
					release, acquired := h.imageLimiter.TryAcquire(cfg.Enabled, cfg.MaxConcurrentRequests)
					if !acquired {
						_ = client.Close(coderws.StatusTryAgainLater, "Image concurrency limit reached")
						return kind, nil, fmt.Errorf("codex API image concurrency full")
					}
					imageRelease = release
				}
			}
			return kind, payload, nil
		},
		BeforeClientWrite: func(_ coderws.MessageType, payload []byte) {
			switch gjson.GetBytes(payload, "type").String() {
			case "response.completed", "response.done", "response.incomplete":
			default:
				return
			}
			observer := service.NewCodexAPIUsageObserver("application/json", "")
			_, _ = observer.Write(payload)
			result, observed := observer.Result()
			if id := observer.ResourceID(); id != "" {
				if err := h.gatewayService.BindCodexAPIResource(ctx, key, id, account.ID); err != nil {
					logger.L().Warn("codex_api.resource_binding_failed", zap.Error(err))
				}
			}
			if !observed || result.ResponseID != "" && result.ResponseID == lastBilledID {
				return
			}
			lastBilledID = result.ResponseID
			result.UpstreamTerminalEvent = gjson.GetBytes(payload, "type").String()
			result.Model = result.UpstreamResponseModel
			h.recordCodexAPIUsage(recordContext, key, account, target, nil, response.Header, start, false, result)
		},
		BeforeRelayCancel: func(exit openaiwsv2.RelayExit) {
			var closeError coderws.CloseError
			if errors.As(exit.Err, &closeError) {
				_ = client.Close(closeError.Code, closeError.Reason)
			}
		},
	})
}
