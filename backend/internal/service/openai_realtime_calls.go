package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAIRealtimeRESTDefaultBaseURL = "https://api.openai.com"
)

type OpenAIRealtimeRESTModelRef struct {
	Path  string
	Value string
}

type openAIRealtimeRESTPart struct {
	header textproto.MIMEHeader
	body   []byte
	name   string
}

type openAIRealtimeBackendCallRequest struct {
	SDP     string          `json:"sdp"`
	Session json.RawMessage `json:"session"`
}

func OpenAIRealtimeCallIDFromLocation(location string) (string, error) {
	return liveCallIDFromLocation(location)
}

type OpenAIRealtimeRESTRequest struct {
	CallID            string
	Body              []byte
	Model             string
	Endpoint          string
	Action            string
	ScheduleModelPath string
	ModelRefs         []OpenAIRealtimeRESTModelRef
	multipartBoundary string
	multipartParts    []openAIRealtimeRESTPart
}

func (r *OpenAIRealtimeRESTRequest) StickySessionSeed() string {
	if r == nil {
		return ""
	}
	if r.CallID != "" {
		return OpenAIRealtimeCallSessionHash(r.CallID)
	}
	return strings.Join([]string{
		"openai-realtime-rest",
		strings.TrimSpace(r.Endpoint),
		strings.TrimSpace(r.CallID),
		strings.TrimSpace(r.Model),
	}, "|")
}

func ParseOpenAIRealtimeRESTRequest(c *gin.Context, body []byte) (*OpenAIRealtimeRESTRequest, error) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return nil, fmt.Errorf("missing request context")
	}
	endpoint, callID, action, err := normalizeOpenAIRealtimeRESTEndpoint(c.Request.URL.Path)
	if err != nil {
		return nil, err
	}

	parsed := &OpenAIRealtimeRESTRequest{
		CallID:   callID,
		Body:     body,
		Endpoint: endpoint,
		Action:   action,
	}

	if action == "accept" {
		if len(body) == 0 {
			return nil, fmt.Errorf("request body is empty")
		}
		if !gjson.ValidBytes(body) {
			return nil, fmt.Errorf("failed to parse request body")
		}
		if sessionType := strings.TrimSpace(gjson.GetBytes(body, "type").String()); sessionType != "realtime" {
			return nil, fmt.Errorf("type must be realtime")
		}
		model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
		if model == "" {
			return nil, fmt.Errorf("model is required")
		}
		parsed.Model = model
		parsed.ScheduleModelPath = "model"
		parsed.ModelRefs = []OpenAIRealtimeRESTModelRef{{Path: "model", Value: model}}
		return parsed, nil
	}
	mediaType, params, contentTypeErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if strings.EqualFold(mediaType, "multipart/form-data") {
		if contentTypeErr != nil || params["boundary"] == "" {
			return nil, fmt.Errorf("multipart boundary is required")
		}
		parsed.multipartBoundary = params["boundary"]
		reader := multipart.NewReader(bytes.NewReader(body), parsed.multipartBoundary)
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("read multipart body: %w", err)
			}
			partBody, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				return nil, fmt.Errorf("read multipart part: %w", err)
			}
			parsed.multipartParts = append(parsed.multipartParts, openAIRealtimeRESTPart{header: part.Header, body: partBody, name: part.FormName()})
			if part.FormName() == "session" {
				if !gjson.ValidBytes(partBody) {
					return nil, fmt.Errorf("failed to parse session body")
				}
				for _, path := range []string{"model", "audio.input.transcription.model"} {
					if model := strings.TrimSpace(gjson.GetBytes(partBody, path).String()); model != "" {
						parsed.ModelRefs = append(parsed.ModelRefs, OpenAIRealtimeRESTModelRef{Path: "session." + path, Value: model})
					}
				}
			}
		}
		if len(parsed.ModelRefs) > 0 {
			parsed.Model = parsed.ModelRefs[0].Value
			parsed.ScheduleModelPath = parsed.ModelRefs[0].Path
		}
		return parsed, nil
	}

	if len(body) == 0 || !gjson.ValidBytes(body) {
		return parsed, nil
	}
	parsed.ModelRefs = extractOpenAIRealtimeRESTModelRefs(endpoint, body)
	if len(parsed.ModelRefs) > 0 {
		parsed.Model = parsed.ModelRefs[0].Value
		parsed.ScheduleModelPath = parsed.ModelRefs[0].Path
	}
	return parsed, nil
}

func normalizeOpenAIRealtimeRESTEndpoint(path string) (endpoint string, callID string, action string, err error) {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return "", "", "", fmt.Errorf("realtime endpoint is required")
	}
	if idx := strings.LastIndex(trimmed, "/realtime/"); idx >= 0 {
		trimmed = "/v1" + trimmed[idx:]
	}

	switch trimmed {
	case "/v1/realtime/client_secrets",
		"/v1/realtime/translations/client_secrets",
		"/v1/realtime/calls",
		"/v1/realtime/translations/calls",
		"/v1/realtime/sessions",
		"/v1/realtime/transcription_sessions":
		return trimmed, "", "", nil
	}

	const callsPrefix = "/v1/realtime/calls/"
	if !strings.HasPrefix(trimmed, callsPrefix) {
		return "", "", "", fmt.Errorf("unsupported realtime endpoint")
	}
	rest := strings.TrimPrefix(trimmed, callsPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("unsupported realtime call endpoint")
	}
	callID = strings.TrimSpace(parts[0])
	action = strings.TrimSpace(parts[1])
	if callID == "" {
		return "", "", "", fmt.Errorf("call_id is required")
	}
	switch action {
	case "accept", "hangup", "refer", "reject":
		return trimmed, callID, action, nil
	default:
		return "", "", "", fmt.Errorf("unsupported realtime call action")
	}
}

func extractOpenAIRealtimeRESTModelRefs(endpoint string, body []byte) []OpenAIRealtimeRESTModelRef {
	var paths []string
	switch endpoint {
	case "/v1/realtime/client_secrets":
		paths = []string{"session.model", "session.audio.input.transcription.model"}
	case "/v1/realtime/translations/client_secrets":
		paths = []string{"session.model", "session.audio.input.transcription.model"}
	case "/v1/realtime/calls", "/v1/realtime/translations/calls":
		paths = []string{"model", "session.model"}
	case "/v1/realtime/sessions":
		paths = []string{"model"}
	case "/v1/realtime/transcription_sessions":
		paths = []string{"input_audio_transcription.model"}
	default:
		return nil
	}

	refs := make([]OpenAIRealtimeRESTModelRef, 0, len(paths))
	for _, path := range paths {
		value := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if value == "" {
			continue
		}
		refs = append(refs, OpenAIRealtimeRESTModelRef{Path: path, Value: value})
	}
	return refs
}

func (s *OpenAIGatewayService) ForwardRealtimeREST(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIRealtimeRESTRequest,
	channelMappedModels map[string]string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed realtime REST request is required")
	}
	if account == nil {
		return nil, fmt.Errorf("account is required")
	}
	if account.Platform != PlatformOpenAI || (account.Type != AccountTypeAPIKey && account.Type != AccountTypeOAuth) {
		return nil, fmt.Errorf("realtime REST endpoint requires an OpenAI API key or OAuth account")
	}

	startTime := time.Now()
	convertMultipart := account.Type == AccountTypeOAuth && parsed.Endpoint == "/v1/realtime/calls" && parsed.multipartBoundary != ""
	if convertMultipart {
		backendBody := openAIRealtimeBackendCallRequest{}
		for _, part := range parsed.multipartParts {
			switch part.name {
			case "sdp":
				backendBody.SDP = string(part.body)
			case "session":
				backendBody.Session = part.body
			}
		}
		body, err := json.Marshal(backendBody)
		if err != nil {
			return nil, fmt.Errorf("encode realtime backend call: %w", err)
		}
		backendParsed := *parsed
		backendParsed.Body = body
		backendParsed.multipartBoundary = ""
		backendParsed.multipartParts = nil
		parsed = &backendParsed
	}
	requestModel, upstreamModel, forwardBody, err := buildOpenAIRealtimeRESTForwardBody(parsed, account, channelMappedModels)
	if err != nil {
		return nil, err
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIRealtimeRESTRequest(ctx, c, account, parsed.Endpoint, forwardBody, token)
	if err != nil {
		return nil, err
	}
	if convertMultipart {
		upstreamReq.Header.Set("Content-Type", "application/json")
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "request_error",
			Message:            safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(ctx, resp, account, respBody, upstreamModel)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return s.handleErrorResponse(ctx, resp, c, account, forwardBody)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)

	return &OpenAIForwardResult{
		RequestID:       resp.Header.Get("x-request-id"),
		Model:           requestModel,
		UpstreamModel:   upstreamModel,
		Stream:          false,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
	}, nil
}

func buildOpenAIRealtimeRESTForwardBody(
	parsed *OpenAIRealtimeRESTRequest,
	account *Account,
	channelMappedModels map[string]string,
) (requestModel string, upstreamModel string, body []byte, err error) {
	if parsed == nil {
		return "", "", nil, nil
	}
	body = parsed.Body
	requestModel = strings.TrimSpace(parsed.Model)
	upstreamModel = resolveOpenAIRealtimeRESTModel(account, requestModel, channelMappedModels[parsed.ScheduleModelPath])
	if requestModel == "" {
		upstreamModel = ""
	}

	mappedModels := make(map[string]string, len(parsed.ModelRefs))
	for _, ref := range parsed.ModelRefs {
		original := strings.TrimSpace(ref.Value)
		if original == "" || strings.TrimSpace(ref.Path) == "" {
			continue
		}
		nextModel := resolveOpenAIRealtimeRESTModel(account, original, channelMappedModels[ref.Path])
		if nextModel == "" || nextModel == original {
			continue
		}
		mappedModels[ref.Path] = nextModel
		if parsed.multipartBoundary != "" {
			continue
		}
		updated, err := sjson.SetBytes(body, ref.Path, nextModel)
		if err != nil {
			continue
		}
		body = updated
	}
	if parsed.multipartBoundary != "" && len(mappedModels) > 0 {
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		if err := writer.SetBoundary(parsed.multipartBoundary); err != nil {
			return "", "", nil, err
		}
		for _, part := range parsed.multipartParts {
			partBody := part.body
			if part.name == "session" {
				for path, model := range mappedModels {
					partBody, err = sjson.SetBytes(partBody, strings.TrimPrefix(path, "session."), model)
					if err != nil {
						return "", "", nil, err
					}
				}
			}
			target, err := writer.CreatePart(part.header)
			if err != nil {
				return "", "", nil, err
			}
			if _, err := target.Write(partBody); err != nil {
				return "", "", nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return "", "", nil, err
		}
		body = buffer.Bytes()
	}
	if upstreamModel == "" && len(parsed.ModelRefs) > 0 {
		upstreamModel = strings.TrimSpace(parsed.ModelRefs[0].Value)
	}
	return requestModel, upstreamModel, body, nil
}

func resolveOpenAIRealtimeRESTModel(account *Account, requestedModel string, channelMappedModel string) string {
	model := strings.TrimSpace(requestedModel)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		model = mapped
	}
	if account != nil {
		if mapped := strings.TrimSpace(account.GetMappedModel(model)); mapped != "" {
			model = mapped
		}
	}
	return model
}

func (s *OpenAIGatewayService) buildOpenAIRealtimeRESTRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint string,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := buildOpenAIRealtimeRESTURL("", endpoint)
	if account.Type == AccountTypeOAuth && endpoint == "/v1/realtime/calls" {
		targetURL = strings.TrimSuffix(chatgptCodexURL, "/responses") + "/realtime/calls"
	} else if baseURL := account.GetOpenAIBaseURL(); baseURL != "" {
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = buildOpenAIRealtimeRESTURL(validatedURL, endpoint)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c != nil && c.Request != nil {
		if c.Request.URL != nil {
			query := req.URL.Query()
			for _, key := range []string{"intent", "architecture"} {
				if values, ok := c.Request.URL.Query()[key]; ok {
					query[key] = values
				}
			}
			req.URL.RawQuery = query.Encode()
		}
		for key, values := range c.Request.Header {
			lowerKey := strings.ToLower(key)
			if !openaiPassthroughAllowedHeaders[lowerKey] && lowerKey != "openai-alpha" && lowerKey != "x-session-id" && lowerKey != "x-oai-attestation" {
				continue
			}
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	req.Header.Del("X-Goog-Api-Key")
	req.Header.Set("Authorization", "Bearer "+token)
	if strings.TrimSpace(req.Header.Get("Content-Type")) == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if account.Type == AccountTypeOAuth {
		setOpenAIChatGPTAccountHeaders(req.Header, account)
		isCodexOfficialClient := false
		if c != nil {
			isCodexOfficialClient = openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
		}
		req.Header.Set("originator", resolveOpenAIUpstreamOriginator(c, isCodexOfficialClient))
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	} else if account.Type == AccountTypeOAuth {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	return req, nil
}

func buildOpenAIRealtimeRESTURL(base string, endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "/v1/realtime"
	}
	if strings.TrimSpace(base) == "" {
		return buildOpenAIEndpointURL(openAIRealtimeRESTDefaultBaseURL, endpoint)
	}
	return buildOpenAIEndpointURL(base, endpoint)
}
