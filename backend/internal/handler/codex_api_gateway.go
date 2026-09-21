package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// The caller's path selects the gateway contract, regardless of client identity.
func codexAPITarget(r *http.Request) string {
	path := r.URL.Path
	if r.Method == http.MethodGet {
		switch path {
		case "/backend-api/codex/models":
			return path
		case "/models", "/v1/models":
			return "/v1/models"
		}
	}
	if r.Method == http.MethodPost {
		switch path {
		case "/responses/compact", "/v1/responses/compact", "/backend-api/codex/responses/compact":
			return "/v1/responses/compact"
		case "/backend-api/codex/responses":
			return path
		case "/responses", "/v1/responses":
			return "/v1/responses"
		}
	}
	return ""
}

// CodexAPIDispatch runs after authentication, before middleware that reads or
// transforms model payloads. A dedicated group never enters ordinary routing.
func (h *OpenAIGatewayHandler) CodexAPIDispatch(c *gin.Context) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key.GroupID == nil || key.Group == nil || key.Group.Platform != service.PlatformOpenAI {
		c.Next()
		return
	}
	accounts, dedicated, err := h.gatewayService.CodexAPIGroupAccounts(c.Request.Context(), *key.GroupID)
	if err != nil {
		c.Abort()
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Account group unavailable")
		return
	}
	if !dedicated {
		c.Next()
		return
	}
	c.Abort()
	target := codexAPITarget(c.Request)
	if target == "" {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Endpoint not supported by Codex API accounts")
		return
	}
	h.forwardCodexAPI(c, key, accounts, target)
}

func (h *OpenAIGatewayHandler) forwardCodexAPI(c *gin.Context, key *service.APIKey, accounts []service.Account, target string) {
	ctx := c.Request.Context()
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "User context not found")
		return
	}
	start := time.Now()
	userRelease, acquired, err := h.concurrencyHelper.TryAcquireUserSlotForAPIKey(ctx, subject.UserID, subject.Concurrency, key.ID)
	if err != nil || !acquired {
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "User concurrency limit reached")
		return
	}
	defer userRelease()
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if c.Request.Method == http.MethodPost {
		if err := h.billingCacheService.CheckBillingEligibility(ctx, key.User, key, key.Group, subscription, service.QuotaPlatform(ctx, key)); err != nil {
			status, code, message, _ := billingErrorDetails(err)
			h.errorResponse(c, status, code, message)
			return
		}
	}
	var body []byte
	if c.Request.Method == http.MethodPost {
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "Unable to read request body")
			return
		}
		// Decode only an inspection copy; retain the original wire representation.
		inspection := c.Request.Clone(ctx)
		inspection.Body = io.NopCloser(bytes.NewReader(raw))
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(inspection)
		encoding := strings.ToLower(strings.TrimSpace(c.Request.Header.Get("Content-Encoding")))
		// The shared decoder truncates at its expansion ceiling. Refuse to
		// forward an encoded payload whose security inspection may be incomplete.
		inspectionTruncated := encoding != "" && encoding != "identity" && len(body) >= 64<<20
		if err != nil || inspectionTruncated {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Unable to inspect request body")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		model := gjson.GetBytes(body, "model").String()
		if h.rejectIfCyberSessionBlocked(c, key, body, model, cyberBlockFormatResponses) {
			return
		}
		if decision := h.checkSecurityAudit(c, requestLogger(c, "handler.codex_api"), key, subject, service.ContentModerationProtocolOpenAIResponses, model, body); decision != nil && !decision.AllowNextStage {
			h.openAISecurityAuditError(c, decision)
			return
		}
		if service.IsExplicitImageGenerationIntent("/v1/responses", model, body) {
			if !service.GroupAllowsImageGeneration(key.Group) {
				h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
				return
			}
			release, acquired := h.acquireImageGenerationSlot(c, false)
			if !acquired {
				return
			}
			if release != nil {
				defer release()
			}
		}
	}
	var account *service.Account
	for i := range accounts {
		candidate := &accounts[i]
		if !candidate.IsSchedulable() || candidate.IsQuotaExceeded() {
			continue
		}
		release, acquired, err := h.concurrencyHelper.TryAcquireAccountSlot(ctx, candidate.ID, candidate.Concurrency)
		if err != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Account concurrency unavailable")
			return
		}
		if acquired {
			defer release()
			account = candidate
			break
		}
	}
	if account == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "No available Codex API account")
		return
	}
	response, err := h.gatewayService.RoundTripCodexAPI(ctx, account, c.Request, target)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		h.errorResponse(c, http.StatusBadGateway, "api_error", "Codex API request failed")
		return
	}
	responseBody := response.Body
	defer func() {
		if err := responseBody.Close(); err != nil {
			logger.L().Warn("codex_api.response_body_close_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	}()
	observer := service.NewCodexAPIUsageObserver(response.Header.Get("Content-Type"), response.Header.Get("Content-Encoding"))
	response.Body = codexAPIObservedBody{Reader: io.TeeReader(response.Body, observer), Closer: response.Body}
	relayErr := relayCodexAPIResponse(c, response)
	if c.Request.Method != http.MethodPost || response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	result, observed := observer.Result()
	if !observed {
		logger.L().Warn("codex_api.usage_unavailable", zap.Int64("account_id", account.ID))
		return
	}
	result.Model = gjson.GetBytes(body, "model").String()
	if tier := gjson.GetBytes(body, "service_tier"); tier.Type == gjson.String {
		value := tier.String()
		result.ServiceTier = &value
	}
	if effort := gjson.GetBytes(body, "reasoning.effort"); effort.Type == gjson.String {
		value := effort.String()
		result.ReasoningEffort = &value
		result.RequestedReasoningEffort = &value
	}
	result.Duration = time.Since(start)
	result.ClientDisconnect = relayErr != nil
	result.UpstreamEndpoint = target
	result.UpstreamHeaders = response.Header.Clone()
	result.Stream = strings.Contains(response.Header.Get("Content-Type"), "text/event-stream")
	quotaPlatform := service.QuotaPlatform(ctx, key)
	inbound := GetInboundEndpoint(c)
	userAgent := c.Request.UserAgent()
	clientIP := ip.GetClientIP(c)
	sessionID := service.ExtractClientSessionID(c)
	payloadHash := service.HashUsageRequestPayload(body)
	h.submitOpenAIUsageRecordTask(ctx, result, func(recordCtx context.Context) {
		if err := h.gatewayService.RecordUsage(recordCtx, &service.OpenAIRecordUsageInput{
			Result: result, APIKey: key, User: key.User, Account: account,
			Subscription: subscription, InboundEndpoint: inbound, UpstreamEndpoint: target,
			APIKeyService: h.apiKeyService, QuotaPlatform: quotaPlatform, UserAgent: userAgent,
			PricingAt: start,
			IPAddress: clientIP, SessionID: sessionID, RequestPayloadHash: payloadHash,
		}); err != nil {
			logger.L().Error("codex_api.record_usage_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
	})
}

type codexAPIObservedBody struct {
	io.Reader
	io.Closer
}

func relayCodexAPIResponse(c *gin.Context, response *http.Response) error {
	headers := response.Header.Clone()
	service.StripCodexAPIHopHeaders(headers)
	for name, values := range headers {
		c.Writer.Header()[name] = values
	}
	c.Status(response.StatusCode)
	c.Writer.WriteHeaderNow()
	buffer := make([]byte, 32*1024)
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			c.Writer.Flush()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
