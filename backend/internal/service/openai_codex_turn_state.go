package service

import (
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// openAICodexTurnStateHeader 是 Codex 的回合状态头。上游在响应头中铸造该
// 不透明 blob，客户端在同一回合的后续请求中原样回带（codex-rs 侧从
// /responses SSE、/responses/compact JSON 与 WS 握手三种响应中捕获，见
// codex-api/src/sse/responses.rs 与 endpoint/compact.rs）。
const openAICodexTurnStateHeader = "x-codex-turn-state"

// Track the last delivered state per downstream session without storing the
// opaque blob. Credential ownership and its digest distinguish a known echo
// from unknown client state; they do not authenticate the provider's payload.
type openAICodexTurnStateOrigin struct {
	credentialKey string
	stateDigest   [sha256.Size]byte
	expiresAt     time.Time
}

// The row fallback stays inside ephemeral state ownership; it is never emitted
// upstream. Credential shadows use their resolved parent, while duplicate rows
// with the same upstream account and user share the existing credential namespace.
func openAICodexTurnStateCredentialKey(c *gin.Context, account *Account) string {
	source := codexAccountIdentitySource(c, account)
	if source == nil {
		return ""
	}
	if namespace := codexAccountIdentityNamespace(source); namespace != "" {
		return namespace
	}
	if source.ID <= 0 {
		return ""
	}
	return "local-owner:" + strconv.FormatInt(source.ID, 10)
}

// openAICodexTurnStateSeed 返回溯源表键：API Key + 客户端原始会话标识。
// 客户端会话标识取自请求头（与指纹收敛的 thread 派生同源，见
// extractClientSessionID），确保同一下游会话的记录/守卫两侧使用同一键。
// 无会话标识时返回空串，表示不做跟踪（保持透传现状）。
func openAICodexTurnStateSeed(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	sessionID := extractClientSessionID(c.Request.Header)
	if sessionID == "" {
		return ""
	}
	return strconv.FormatInt(getAPIKeyIDFromContext(c), 10) + "\x00" + sessionID
}

// relayOpenAICodexTurnState 将上游响应中的 turn-state 显式写入下游响应头，
// 并记录铸造账号。必须在响应头提交点调用（WriteHeader 之前、且确认本次
// 上游响应就是将要写回客户端的响应之后）。上游无该头时主动清除 writer 上
// 可能残留的上一 failover attempt 的值——否则换号后旧账号的 blob 会粘到
// 新账号的响应上，这正是本文件要防止的跨账号矛盾。
func (s *OpenAIGatewayService) relayOpenAICodexTurnState(c *gin.Context, account *Account, upstream http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		c.Writer.Header().Del(canonical)
		return
	}
	c.Writer.Header().Set(canonical, state)
	s.noteOpenAICodexTurnStateProvenance(c, account, state)
}

// stageOpenAICodexTurnState 将上游 turn-state 暂存到延迟提交的响应头集合
// （首输出守卫路径先缓存头、见到首个输出事件才提交）。此处**不**记录铸造
// 账号：该 attempt 仍可能在首输出超时后 failover，暂存头会被整体丢弃，
// 客户端从未收到该 blob。溯源必须在真正提交时记录，见
// noteStagedOpenAICodexTurnStateCommitted。
func stageOpenAICodexTurnState(dst *http.Header, upstream http.Header) {
	if dst == nil {
		return
	}
	canonical := http.CanonicalHeaderKey(openAICodexTurnStateHeader)
	state := extractOpenAICodexTurnState(upstream)
	if state == "" {
		if *dst != nil {
			dst.Del(canonical)
		}
		return
	}
	if *dst == nil {
		*dst = http.Header{}
	}
	dst.Set(canonical, state)
}

// noteStagedOpenAICodexTurnStateCommitted 在暂存响应头真正写入下游时记录
// 铸造账号——只有此刻客户端才确定收到了该 blob，溯源表才与客户端持有的
// 值一致（否则被 failover 丢弃的 attempt 会污染溯源，导致后续误剥离）。
func (s *OpenAIGatewayService) noteStagedOpenAICodexTurnStateCommitted(c *gin.Context, account *Account, staged http.Header) {
	if staged == nil || strings.TrimSpace(staged.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	s.noteOpenAICodexTurnStateProvenance(c, account, extractOpenAICodexTurnState(staged))
}

type openAICodexTurnStateResponseWriter struct {
	gin.ResponseWriter
	onCommit func(string)
	once     sync.Once
}

func (w *openAICodexTurnStateResponseWriter) recordCommit(state string) {
	if w.Written() {
		w.once.Do(func() { w.onCommit(state) })
	}
}

func (w *openAICodexTurnStateResponseWriter) WriteHeaderNow() {
	if w.Written() {
		return
	}
	state := extractOpenAICodexTurnState(w.Header())
	w.ResponseWriter.WriteHeaderNow()
	w.recordCommit(state)
}

func (w *openAICodexTurnStateResponseWriter) WriteHeader(code int) {
	wasWritten := w.Written()
	state := extractOpenAICodexTurnState(w.Header())
	w.ResponseWriter.WriteHeader(code)
	if !wasWritten {
		w.recordCommit(state)
	}
}

func (w *openAICodexTurnStateResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.ResponseWriter.Write(data)
}

func (w *openAICodexTurnStateResponseWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	return w.ResponseWriter.WriteString(data)
}

func (w *openAICodexTurnStateResponseWriter) Flush() {
	w.WriteHeaderNow()
	w.ResponseWriter.Flush()
}

// Observe only HTTP response writers whose headers remain uncommitted. The
// emitted header value and selected credential are captured at that boundary,
// before body writes can expose the state to another request. Embedded writer
// methods retain Gin's hijacking, close notification and HTTP push behavior.
func (s *OpenAIGatewayService) observeOpenAICodexTurnStateCommit(c *gin.Context, account *Account) func() {
	if s == nil || c == nil || c.Writer == nil || c.Writer.Written() {
		return func() {}
	}
	seed := openAICodexTurnStateSeed(c)
	credentialKey := openAICodexTurnStateCredentialKey(c, account)
	if seed == "" || credentialKey == "" {
		return func() {}
	}
	writer := c.Writer
	c.Writer = &openAICodexTurnStateResponseWriter{
		ResponseWriter: writer,
		onCommit:       func(state string) { s.recordOpenAICodexTurnStateProvenance(seed, credentialKey, state) },
	}
	return func() { c.Writer = writer }
}

func extractOpenAICodexTurnState(upstream http.Header) string {
	if upstream == nil {
		return ""
	}
	return strings.TrimSpace(upstream.Get(openAICodexTurnStateHeader))
}

// Record only the state selected for downstream delivery, using the same
// resolved credential owner as the WS cache.
func (s *OpenAIGatewayService) noteOpenAICodexTurnStateProvenance(c *gin.Context, account *Account, state string) {
	if s == nil || account == nil || strings.TrimSpace(state) == "" {
		return
	}
	credentialKey := openAICodexTurnStateCredentialKey(c, account)
	seed := openAICodexTurnStateSeed(c)
	s.recordOpenAICodexTurnStateProvenance(seed, credentialKey, state)
}

func (s *OpenAIGatewayService) recordOpenAICodexTurnStateProvenance(seed, credentialKey, state string) {
	if s == nil || seed == "" || credentialKey == "" || strings.TrimSpace(state) == "" {
		return
	}
	s.openaiCodexTurnStateOrigins.Store(seed, openAICodexTurnStateOrigin{
		credentialKey: credentialKey,
		stateDigest:   sha256.Sum256([]byte(strings.TrimSpace(state))),
		expiresAt:     time.Now().Add(s.openAIWSSessionStickyTTL()),
	})
	s.sweepOpenAICodexTurnStateOrigins()
}

// guardOpenAICodexTurnStateEcho 出站守卫：客户端回带的 turn-state 若已知由
// 其他账号铸造则剥离，同账号或无溯源记录时保持原样。只剥离、不注入——
// /responses 路径的客户端是真实 Codex，会按自身回合语义自行回带；服务端
// 注入是 Claude 兼容桥（无法回带的客户端）的专属行为。
func (s *OpenAIGatewayService) guardOpenAICodexTurnStateEcho(c *gin.Context, account *Account, h http.Header) {
	if s == nil || h == nil || account == nil {
		return
	}
	if strings.TrimSpace(h.Get(openAICodexTurnStateHeader)) == "" {
		return
	}
	seed := openAICodexTurnStateSeed(c)
	if seed == "" {
		return
	}
	raw, ok := s.openaiCodexTurnStateOrigins.Load(seed)
	if !ok {
		return
	}
	origin, ok := raw.(openAICodexTurnStateOrigin)
	if !ok {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		return
	}
	if !origin.expiresAt.IsZero() && time.Now().After(origin.expiresAt) {
		s.openaiCodexTurnStateOrigins.Delete(seed)
		return
	}
	// A session can receive unknown client state independently of the last
	// response we delivered. Only that exact recorded blob has known ownership.
	if origin.stateDigest != sha256.Sum256([]byte(strings.TrimSpace(h.Get(openAICodexTurnStateHeader)))) {
		return
	}
	credentialKey := openAICodexTurnStateCredentialKey(c, account)
	if credentialKey != "" && origin.credentialKey != "" && origin.credentialKey != credentialKey {
		h.Del(openAICodexTurnStateHeader)
	}
}

// sweepOpenAICodexTurnStateOrigins 机会式清扫过期溯源记录：每 256 次写入
// 全量遍历一轮，防止仅靠读侧惰性删除导致的慢泄漏（会话键无上界）。
func (s *OpenAIGatewayService) sweepOpenAICodexTurnStateOrigins() {
	if s.openaiCodexTurnStateWrites.Add(1)%256 != 0 {
		return
	}
	now := time.Now()
	s.openaiCodexTurnStateOrigins.Range(func(key, value any) bool {
		origin, ok := value.(openAICodexTurnStateOrigin)
		if !ok || (!origin.expiresAt.IsZero() && now.After(origin.expiresAt)) {
			s.openaiCodexTurnStateOrigins.Delete(key)
		}
		return true
	})
}
