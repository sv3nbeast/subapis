package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// Cursor 订阅账号经 agent.v1.AgentService/Run 转发。该端点是 Cursor IDE 的
// Ask 模式对话协议：只接受文本轮次，不接受工具定义，也不回传 tool_use。
// 三条入站协议（Anthropic / Chat Completions / Responses）因此共用同一条
// "压平成文本 → 消费流 → 按入站协议回写"的管线。

// cursorRunRequest 是三条入站协议归一后的上游输入。
type cursorRunRequest struct {
	Messages      []cursor.ChatMessage
	RequestModel  string // 客户端请求的模型（用于回写与计费口径）
	Opts          cursor.RunOpts
	HasTools      bool
	HasImages     bool
	RequestStream bool
	StartTime     time.Time
}

// cursorRunStream 是一次成功建立的上游流。
type cursorRunStream struct {
	Body          io.ReadCloser
	UpstreamModel string
}

// forwardCursorMessages 服务 Anthropic /v1/messages。
func (s *GatewayService) forwardCursorMessages(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, startTime time.Time) (*ForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("cursor forward: missing parsed request")
	}
	var req apicompat.AnthropicRequest
	if err := json.Unmarshal(parsed.Body.Bytes(), &req); err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("cursor anthropic: parse body: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("cursor anthropic: model is required")
	}

	ccReq, err := apicompat.AnthropicToChatCompletionsRequest(&req)
	if err != nil {
		writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("cursor anthropic: convert: %w", err)
	}

	messages, droppedImages := cursorMessagesFromChat(ccReq.Messages)
	run := cursorRunRequest{
		Messages:      messages,
		RequestModel:  req.Model,
		Opts:          cursorRunOptsFromAnthropic(&req),
		HasTools:      len(req.Tools) > 0,
		HasImages:     droppedImages,
		RequestStream: req.Stream,
		StartTime:     startTime,
	}
	if capability := cursorUnsupportedCapability(run); capability != "" {
		mixed := cursorMixedScheduled(ctx, account)
		err := cursorCapabilityError(capability, mixed)
		// 混合调度下不写响应体：写出即锁死 failover，请求就再也换不到
		// 同组的真 Anthropic 账号了。
		if !mixed {
			writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return nil, err
	}

	stream, err := s.startCursorRun(ctx, c, account, run)
	if err != nil {
		return nil, err
	}
	defer stream.Body.Close()

	if req.Stream {
		return s.streamCursorAsAnthropic(c, stream, run)
	}
	return s.bufferCursorAsAnthropic(c, stream, run)
}

// forwardCursorAsChatCompletions 服务 OpenAI /v1/chat/completions。
func (s *GatewayService) forwardCursorAsChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte, startTime time.Time) (*ForwardResult, error) {
	var req apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("cursor chat: parse body: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("cursor chat: model is required")
	}

	messages, droppedImages := cursorMessagesFromChat(req.Messages)
	run := cursorRunRequest{
		Messages:      messages,
		RequestModel:  req.Model,
		Opts:          cursorRunOptsFromChat(&req),
		HasTools:      len(req.Tools) > 0 || len(req.Functions) > 0,
		HasImages:     droppedImages,
		RequestStream: req.Stream,
		StartTime:     startTime,
	}
	if capability := cursorUnsupportedCapability(run); capability != "" {
		mixed := cursorMixedScheduled(ctx, account)
		err := cursorCapabilityError(capability, mixed)
		if !mixed {
			writeGatewayCCError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return nil, err
	}

	stream, err := s.startCursorRun(ctx, c, account, run)
	if err != nil {
		return nil, err
	}
	defer stream.Body.Close()

	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	if req.Stream {
		return s.streamCursorAsChatCompletions(c, stream, run, includeUsage)
	}
	return s.bufferCursorAsChatCompletions(c, stream, run)
}

// forwardCursorAsResponses 服务 OpenAI /v1/responses。
func (s *GatewayService) forwardCursorAsResponses(ctx context.Context, c *gin.Context, account *Account, body []byte, startTime time.Time) (*ForwardResult, error) {
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return nil, fmt.Errorf("cursor responses: parse body: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("cursor responses: model is required")
	}

	ccReq, err := apicompat.ResponsesToChatCompletionsRequest(&req)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("cursor responses: convert: %w", err)
	}

	messages, droppedImages := cursorMessagesFromChat(ccReq.Messages)
	run := cursorRunRequest{
		Messages:      messages,
		RequestModel:  req.Model,
		Opts:          cursorRunOptsFromResponses(&req),
		HasTools:      len(req.Tools) > 0,
		HasImages:     droppedImages,
		RequestStream: req.Stream,
		StartTime:     startTime,
	}
	if capability := cursorUnsupportedCapability(run); capability != "" {
		mixed := cursorMixedScheduled(ctx, account)
		err := cursorCapabilityError(capability, mixed)
		if !mixed {
			writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return nil, err
	}

	stream, err := s.startCursorRun(ctx, c, account, run)
	if err != nil {
		return nil, err
	}
	defer stream.Body.Close()

	if req.Stream {
		return s.streamCursorAsResponses(c, stream, run)
	}
	return s.bufferCursorAsResponses(c, stream, run)
}

// cursorUnsupportedCapability 描述这条请求用到了 Ask 模式承载不了的能力。
//
// 工具：Run 在 Ask 模式下既不接受工具定义也不产出 tool_use，静默丢弃会让
// Claude Code / Codex 一直等一个永远不会到来的工具调用。
// 图片：Run 的用户消息没有图片字段（Cursor 的图片走另一条 StreamUnifiedChat
// 链路），继续发只会让模型看不到图却照常作答，答案错得毫无提示。
//
// 返回空串表示这条请求 Cursor 能承载。
func cursorUnsupportedCapability(run cursorRunRequest) string {
	switch {
	case run.HasTools:
		return "tool use"
	case run.HasImages:
		return "image input"
	default:
		return ""
	}
}

// cursorMixedScheduled 报告这个 Cursor 账号是否由非 cursor 分组顺带选中。
// 分组平台不是 cursor 就意味着同组还有其它平台的账号可以接手。
func cursorMixedScheduled(ctx context.Context, account *Account) bool {
	if account == nil || account.Platform != PlatformCursor {
		return false
	}
	// 只有确认是专属 cursor 分组时才走"直接告知客户端"的分支。分组信息缺失或
	// 未水合时按混合调度处理：让请求还能转移，代价只是少一条解释性错误；反过来
	// 猜错则会把一个本可由同组账号服务的请求打成 400。
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) {
		return group.Platform != PlatformCursor
	}
	return true
}

// cursorCapabilityError 决定能力不匹配时怎么失败，这取决于账号是怎么被选中的。
//
// 混合调度下 Cursor 账号是被 anthropic 分组顺带选中的——用户的意图是"用这个
// 分组"，不是"用 Cursor"。此时必须返回可转移错误且不写任何响应体，让网关把
// 请求交给同组的真 Anthropic 账号；写出 400 会让 c.Writer.Size() 变化，
// failover 判定随即锁死，用户吃到一个本可避免的错误。
//
// 专属 Cursor 分组下没有别的账号可让位，转移只会白跑一轮，此时直接把原因写给
// 客户端更有用。
func cursorCapabilityError(capability string, mixedScheduled bool) error {
	if mixedScheduled {
		return &UpstreamFailoverError{
			StatusCode:             http.StatusBadRequest,
			ResponseBody:           []byte(fmt.Sprintf("cursor account cannot serve %s; deferring to another account in this group", capability)),
			RequestScopedTransient: true,
		}
	}
	return fmt.Errorf("cursor accounts do not support %s: the upstream Ask-mode endpoint accepts plain text turns only", capability)
}

// startCursorRun 解析模型、拿到有效凭证并建立上游流；命中 401 时强制轮换一次重试。
func (s *GatewayService) startCursorRun(ctx context.Context, c *gin.Context, account *Account, run cursorRunRequest) (*cursorRunStream, error) {
	if account == nil {
		return nil, fmt.Errorf("cursor forward: missing account")
	}

	accessToken, account, err := s.cursorAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusUnauthorized,
			ResponseBody: []byte(fmt.Sprintf("Cursor credential error: %v", err)),
		}
	}

	upstreamModel := s.resolveCursorRunModel(ctx, c, account, run)
	resp, err := cursorStreamRun(ctx, account, accessToken, run.Messages, upstreamModel)
	if err != nil && isCursorAuthError(err) {
		refreshedToken, refreshedAccount, refreshErr := s.cursorForceRefresh(ctx, account)
		if refreshErr != nil {
			logger.LegacyPrintf("service.cursor", "[Cursor] auth retry refresh account=%d: %v", account.ID, refreshErr)
		} else {
			account = refreshedAccount
			resp, err = cursorStreamRun(ctx, account, refreshedToken, run.Messages, upstreamModel)
		}
	}
	if err != nil {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusBadGateway,
			ResponseBody: []byte(fmt.Sprintf("Cursor upstream error: %v", err)),
		}
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &UpstreamFailoverError{
			StatusCode:      resp.StatusCode,
			ResponseBody:    respBody,
			ResponseHeaders: resp.Header,
		}
	}
	return &cursorRunStream{Body: resp.Body, UpstreamModel: upstreamModel}, nil
}

// cursorAccessToken 优先走带锁的 provider；未装配 provider 时回落到账号凭证，
// 便于测试与只导入静态 token 的账号。
func (s *GatewayService) cursorAccessToken(ctx context.Context, account *Account) (string, *Account, error) {
	if s != nil && s.cursorTokenProvider != nil && account.Type == AccountTypeOAuth {
		return s.cursorTokenProvider.GetAccessToken(ctx, account)
	}
	accessToken := normalizeCursorAccessToken(account.GetCredential("access_token"))
	if accessToken == "" {
		return "", account, fmt.Errorf("access_token not found in credentials")
	}
	return accessToken, account, nil
}

func (s *GatewayService) cursorForceRefresh(ctx context.Context, account *Account) (string, *Account, error) {
	if s == nil || s.cursorTokenProvider == nil {
		return "", account, fmt.Errorf("cursor token provider is not configured")
	}
	return s.cursorTokenProvider.ForceRefreshAccessToken(ctx, account)
}

// cursorStreamRun 用账号凭证 + 当前 access_token 发起一次 AgentService/Run。
func cursorStreamRun(ctx context.Context, account *Account, accessToken string, messages []cursor.ChatMessage, model string) (*http.Response, error) {
	creds := CursorCredentialsFromAccount(account)
	creds.AccessToken = accessToken
	client := cursor.NewClient(creds)
	client.ProxyURL = cursorAccountProxyURL(account)
	return client.StreamChat(ctx, messages, model)
}

// cursorAccountProxyURL 取账号已水合的代理地址。
func cursorAccountProxyURL(account *Account) string {
	if account == nil || account.ProxyID == nil || account.Proxy == nil {
		return ""
	}
	return account.Proxy.URL()
}

func isCursorAuthError(err error) bool {
	if err == nil {
		return false
	}
	blob := strings.ToLower(err.Error())
	return strings.Contains(blob, "status 401") ||
		strings.Contains(blob, "unauthenticated") ||
		strings.Contains(blob, "invalid_token") ||
		strings.Contains(blob, "token expired")
}

// resolveCursorRunModel 把客户端模型名映射成 AgentService/Run 接受的参数化 slug。
func (s *GatewayService) resolveCursorRunModel(ctx context.Context, c *gin.Context, account *Account, run cursorRunRequest) string {
	requested := account.GetMappedModel(run.RequestModel)
	resolved := cursor.ResolveRunModel(requested, run.Opts, cursorRunCatalog(ctx, account))
	if resolved.RunSlug == "" {
		return requested
	}
	if c != nil && !strings.EqualFold(requested, resolved.RunSlug) {
		c.Header("X-Sub2API-Model-Variant", requested+" -> "+resolved.RunSlug)
	}
	if resolved.AliasFallback {
		logger.LegacyPrintf("service.cursor", "[Cursor] model fallback requested=%s picker=%s slug=%s", requested, resolved.PickerID, resolved.RunSlug)
	}
	return resolved.RunSlug
}

// cursorRunCatalog 返回账号的在线模型目录（带进程级缓存）。取不到时返回 nil，
// 变体解析会回落到内置快照。缓存是进程级的，所以这里不依赖任何 receiver——
// 网关与管理端共用同一份。
func cursorRunCatalog(ctx context.Context, account *Account) []cursor.AvailableModel {
	if account == nil {
		return nil
	}
	if models, ok := cursorCatalogs.get(account.ID); ok {
		return models
	}
	models, err := fetchCursorAvailableModels(ctx, account, cursorCatalogFetchTimeout)
	if err != nil {
		logger.LegacyPrintf("service.cursor", "[Cursor] AvailableModels account=%d: %v", account.ID, err)
		return nil
	}
	cursorCatalogs.put(account.ID, models)
	return models
}

// FetchCursorAvailableModels 拉取账号的 Cursor picker 目录，供管理端模型同步使用。
// 管理端路径不设短超时：它不在请求热路径上。
func FetchCursorAvailableModels(ctx context.Context, account *Account) ([]cursor.AvailableModel, error) {
	return fetchCursorAvailableModels(ctx, account, 0)
}

// cursorCatalogFetchTimeout 限制请求热路径上的目录拉取。缓存过期后的首个请求
// 会同步重取，超时即回落到内置快照，不让客户端为目录 RPC 卡住。
const cursorCatalogFetchTimeout = 5 * time.Second

func fetchCursorAvailableModels(ctx context.Context, account *Account, timeout time.Duration) ([]cursor.AvailableModel, error) {
	accessToken := normalizeCursorAccessToken(account.GetCredential("access_token"))
	if accessToken == "" {
		return nil, fmt.Errorf("cursor: missing access_token")
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	creds := CursorCredentialsFromAccount(account)
	creds.AccessToken = accessToken
	client := cursor.NewClient(creds)
	client.ProxyURL = cursorAccountProxyURL(account)
	return client.AvailableModels(ctx)
}

// cursorPickerModelIDs 从一组账号里取出第一份可用的在线 picker id 列表，
// 供网关的 /v1/models 与管理端的分组模型候选共用。
func cursorPickerModelIDs(ctx context.Context, accounts []Account) []string {
	for i := range accounts {
		account := &accounts[i]
		if account.Platform != PlatformCursor {
			continue
		}
		models := cursorRunCatalog(ctx, account)
		if ids := cursor.ModelIDs(models); len(ids) > 0 {
			return ids
		}
	}
	return nil
}

// cursorCatalogCache 按账号缓存在线模型目录。目录是进程级派生数据，放在这里
// 而不是 GatewayService 字段上，避免把一次性缓存混进网关的生命周期状态。
type cursorCatalogCache struct {
	mu      sync.Mutex
	entries map[int64]cursorCatalogEntry
}

type cursorCatalogEntry struct {
	models []cursor.AvailableModel
	expiry time.Time
}

const cursorCatalogTTL = 5 * time.Minute

var cursorCatalogs = &cursorCatalogCache{entries: make(map[int64]cursorCatalogEntry)}

func (c *cursorCatalogCache) get(accountID int64) ([]cursor.AvailableModel, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[accountID]
	if !ok || time.Now().After(entry.expiry) {
		return nil, false
	}
	return entry.models, true
}

func (c *cursorCatalogCache) put(accountID int64, models []cursor.AvailableModel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[accountID] = cursorCatalogEntry{
		models: models,
		expiry: time.Now().Add(cursorCatalogTTL),
	}
}

// cursorMessagesFromChat 把 Chat Completions 轮次压平成 Cursor 的纯文本轮次，
// 并报告是否丢弃了图片——Ask 模式的 Run 请求没有图片字段可放。
func cursorMessagesFromChat(messages []apicompat.ChatMessage) (out []cursor.ChatMessage, droppedImages bool) {
	out = make([]cursor.ChatMessage, 0, len(messages))
	for _, message := range messages {
		text, hadImage := cursorContentText(message.Content)
		droppedImages = droppedImages || hadImage
		out = append(out, cursor.ChatMessage{Role: message.Role, Content: text})
	}
	return out, droppedImages
}

// cursorContentText 把 string / content-parts 数组 / null 统一折叠成文本，
// 同时报告是否见到过图片部件。
func cursorContentText(raw json.RawMessage) (text string, hadImage bool) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	if raw[0] == '"' {
		var plain string
		if json.Unmarshal(raw, &plain) == nil {
			return plain, false
		}
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, part := range parts {
			switch {
			case part.Type == "" || part.Type == "text":
				b.WriteString(part.Text)
			case strings.HasPrefix(part.Type, "image") || strings.HasPrefix(part.Type, "input_image"):
				hadImage = true
			}
		}
		return b.String(), hadImage
	}
	return string(raw), false
}

func cursorRunOptsFromAnthropic(req *apicompat.AnthropicRequest) cursor.RunOpts {
	var opts cursor.RunOpts
	if req == nil {
		return opts
	}
	if req.OutputConfig != nil {
		opts.Effort = cursor.NormalizeEffort(req.OutputConfig.Effort)
	}
	if req.Thinking != nil {
		enabled := strings.EqualFold(req.Thinking.Type, "enabled") || strings.EqualFold(req.Thinking.Type, "adaptive")
		opts.Thinking = &enabled
	}
	return opts
}

func cursorRunOptsFromChat(req *apicompat.ChatCompletionsRequest) cursor.RunOpts {
	if req == nil {
		return cursor.RunOpts{}
	}
	return cursor.RunOpts{Effort: cursor.NormalizeEffort(req.ReasoningEffort)}
}

func cursorRunOptsFromResponses(req *apicompat.ResponsesRequest) cursor.RunOpts {
	if req == nil || req.Reasoning == nil {
		return cursor.RunOpts{}
	}
	return cursor.RunOpts{Effort: cursor.NormalizeEffort(req.Reasoning.Effort)}
}

// classifyCursorConnectError 把 Connect 端流错误映射成面向客户端的状态与文案。
func classifyCursorConnectError(raw string) (status int, errType, message string) {
	parsed, ok := cursor.ParseConnectError(raw)
	if !ok {
		return http.StatusBadGateway, "upstream_error", "Cursor upstream error"
	}
	message = strings.TrimSpace(parsed.Message)
	if message == "" {
		message = strings.TrimSpace(raw)
	}
	if parsed.IsBadModelName() {
		return http.StatusBadRequest, "invalid_request_error", message
	}
	return http.StatusBadGateway, "upstream_error", message
}

// cursorForwardResult 组装计费与统计所需的转发结果。
func cursorForwardResult(run cursorRunRequest, upstreamModel string, firstTokenMs *int, usage cursor.TokenUsage) *ForwardResult {
	result := &ForwardResult{
		Model:        run.RequestModel,
		Stream:       run.RequestStream,
		Duration:     time.Since(run.StartTime),
		FirstTokenMs: firstTokenMs,
		Usage:        claudeUsageFromCursor(usage),
	}
	if !strings.EqualFold(upstreamModel, run.RequestModel) {
		result.UpstreamModel = upstreamModel
	}
	return result
}

// claudeUsageFromCursor 把 turn_ended 计数映射到网关内部的 Anthropic 口径。
// Cursor 与 Anthropic 一样：input 不含缓存，缓存读写单列。
func claudeUsageFromCursor(u cursor.TokenUsage) ClaudeUsage {
	return ClaudeUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             cursorOutputTokens(u),
		CacheCreationInputTokens: u.CacheWriteTokens,
		CacheReadInputTokens:     u.CacheReadTokens,
	}
}

// cursorOutputTokens 在 turn_ended 只报了推理 token 时回落到该值，避免把一次
// 真实产出的轮次记成 0 输出。
func cursorOutputTokens(u cursor.TokenUsage) int {
	if u.OutputTokens == 0 {
		return u.ReasoningTokens
	}
	return u.OutputTokens
}

func chatUsageFromCursor(u cursor.TokenUsage) *apicompat.ChatUsage {
	if u.Empty() {
		return nil
	}
	prompt := u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
	completion := cursorOutputTokens(u)
	usage := &apicompat.ChatUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		usage.PromptTokensDetails = &apicompat.ChatTokenDetails{
			CachedTokens:     u.CacheReadTokens,
			CacheWriteTokens: u.CacheWriteTokens,
		}
	}
	if u.ReasoningTokens > 0 {
		usage.CompletionTokensDetails = &apicompat.ChatTokenDetails{ReasoningTokens: u.ReasoningTokens}
	}
	return usage
}
