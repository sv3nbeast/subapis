package antigravity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	sessionRand      = rand.New(rand.NewSource(time.Now().UnixNano()))
	sessionRandMutex sync.Mutex
)

// generateStableSessionID 基于用户消息内容生成稳定的 session ID
func generateStableSessionID(contents []GeminiContent) string {
	// 查找第一个 user 消息的文本
	for _, content := range contents {
		if content.Role == "user" && len(content.Parts) > 0 {
			if text := content.Parts[0].Text; text != "" {
				h := sha256.Sum256([]byte(text))
				n := int64(binary.BigEndian.Uint64(h[:8])) & 0x7FFFFFFFFFFFFFFF
				return "-" + strconv.FormatInt(n, 10)
			}
		}
	}
	// 回退：生成随机 session ID
	sessionRandMutex.Lock()
	n := sessionRand.Int63n(9_000_000_000_000_000_000)
	sessionRandMutex.Unlock()
	return "-" + strconv.FormatInt(n, 10)
}

type TransformOptions struct {
	EnableIdentityPatch bool
	// IdentityPatch 可选：自定义注入到 systemInstruction 开头的身份防护提示词；
	// 为空时使用默认模板（包含 [IDENTITY_PATCH] 及 SYSTEM_PROMPT_BEGIN 标记）。
	IdentityPatch string
	EnableMCPXML  bool
	// SessionID / RequestID / UserAgent 用于对齐 Cloud Code 原生会话身份与请求谱系。
	// 为空时回退到旧行为。
	SessionID string
	RequestID string
	UserAgent string
	// ToolUseSignatures 可选：按 tool_use.id 回填历史上游返回的真实 signature，
	// 用于在客户端未保留 signature 时恢复 Cloud Code 的函数调用签名链。
	ToolUseSignatures map[string]string
	// ToolNameClientToOfficial 可选：将客户端工具名归一化为官方 Antigravity 工具名。
	ToolNameClientToOfficial map[string]string
}

func DefaultTransformOptions() TransformOptions {
	return TransformOptions{
		EnableIdentityPatch: true,
		EnableMCPXML:        true,
	}
}

// webSearchFallbackModel web_search 请求使用的降级模型
const webSearchFallbackModel = "gemini-2.5-flash"

// MaxTokensBudgetPadding max_tokens 自动调整时在 budget_tokens 基础上增加的额度
// Claude API 要求 max_tokens > thinking.budget_tokens，否则返回 400 错误
const MaxTokensBudgetPadding = 1000

// Gemini 2.5 Flash thinking budget 上限
const Gemini25FlashThinkingBudgetLimit = 24576

// 对于 Antigravity 的 Claude（budget-only）模型，该语义最终等价为 thinkingBudget=24576。
// 这里复用相同数值以保持行为一致。
const ClaudeAdaptiveHighThinkingBudgetTokens = Gemini25FlashThinkingBudgetLimit

// ensureMaxTokensGreaterThanBudget 确保 max_tokens > budget_tokens
// Claude API 要求启用 thinking 时，max_tokens 必须大于 thinking.budget_tokens
// 返回调整后的 maxTokens 和是否进行了调整
func ensureMaxTokensGreaterThanBudget(maxTokens, budgetTokens int) (int, bool) {
	if budgetTokens > 0 && maxTokens <= budgetTokens {
		return budgetTokens + MaxTokensBudgetPadding, true
	}
	return maxTokens, false
}

// TransformClaudeToGemini 将 Claude 请求转换为 v1internal Gemini 格式
func TransformClaudeToGemini(claudeReq *ClaudeRequest, projectID, mappedModel string) ([]byte, error) {
	return TransformClaudeToGeminiWithOptions(claudeReq, projectID, mappedModel, DefaultTransformOptions())
}

// TransformClaudeToGeminiWithOptions 将 Claude 请求转换为 v1internal Gemini 格式（可配置身份补丁等行为）
func TransformClaudeToGeminiWithOptions(claudeReq *ClaudeRequest, projectID, mappedModel string, opts TransformOptions) ([]byte, error) {
	// 用于存储 tool_use id -> name 映射
	toolIDToName := make(map[string]string)
	clientToOfficial, _ := buildOfficialAntigravityToolNameMaps(claudeReq.Tools)
	if len(opts.ToolNameClientToOfficial) > 0 {
		clientToOfficial = opts.ToolNameClientToOfficial
	}

	// 检测是否有 web_search 工具
	hasWebSearchTool := hasWebSearchTool(claudeReq.Tools)
	requestType := "agent"
	targetModel := mappedModel
	if hasWebSearchTool {
		requestType = "web_search"
		if targetModel != webSearchFallbackModel {
			targetModel = webSearchFallbackModel
		}
	}

	// 检测是否启用 thinking。agent-vibes 会在 thinking 模型上默认发送 thinkingConfig。
	isThinkingEnabled := shouldEnableThinkingForContent(claudeReq, targetModel)

	// 只有 Gemini 模型支持 dummy thought workaround
	// Claude 模型通过 Vertex/Google API 需要有效的 thought signatures
	allowDummyThought := strings.HasPrefix(targetModel, "gemini-")

	// 1. 构建 contents
	contents, strippedThinking, err := buildContents(claudeReq.Messages, toolIDToName, isThinkingEnabled, allowDummyThought, opts.ToolUseSignatures, clientToOfficial)
	if err != nil {
		return nil, fmt.Errorf("build contents: %w", err)
	}

	// 2. 构建 generationConfig
	reqForConfig := claudeReq
	if strippedThinking {
		// If we had to downgrade thinking blocks to plain text due to missing/invalid signatures,
		// disable upstream thinking mode to avoid signature/structure validation errors.
		reqCopy := *claudeReq
		reqCopy.Thinking = nil
		reqCopy.DisableDefaultThinking = true
		reqForConfig = &reqCopy
	}
	if targetModel != "" && targetModel != reqForConfig.Model {
		reqCopy := *reqForConfig
		reqCopy.Model = targetModel
		reqForConfig = &reqCopy
	}
	generationConfig := buildGenerationConfig(reqForConfig)

	// 3. 构建 tools
	tools := buildTools(claudeReq.Tools)
	toolConfig := buildToolConfig(claudeReq.ToolChoice)

	// 4. 构建 systemInstruction（用户 system 在前，官方 Antigravity prompt 在后）
	systemInstruction := buildSystemInstruction(
		claudeReq.System,
		targetModel,
		opts,
		claudeReq.Tools,
		len(tools) > 0 && generationConfig != nil && generationConfig.ThinkingConfig != nil,
	)

	// 5. 构建内部请求
	innerRequest := GeminiRequest{
		Contents: contents,
		// 总是生成 sessionId，基于用户消息内容
		SessionID: generateStableSessionID(contents),
	}

	if systemInstruction != nil {
		innerRequest.SystemInstruction = systemInstruction
	}
	if generationConfig != nil {
		innerRequest.GenerationConfig = generationConfig
	}
	if len(tools) > 0 {
		innerRequest.Tools = tools
		innerRequest.ToolConfig = toolConfig
	}

	// 如果提供了 metadata.user_id，优先使用
	if strings.TrimSpace(opts.SessionID) != "" {
		innerRequest.SessionID = strings.TrimSpace(opts.SessionID)
	} else if claudeReq.Metadata != nil && claudeReq.Metadata.UserID != "" {
		innerRequest.SessionID = claudeReq.Metadata.UserID
	}

	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = "agent-" + uuid.New().String()
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = "antigravity"
	}

	// 6. 包装为 v1internal 请求
	v1Req := V1InternalRequest{
		Project:            projectID,
		RequestID:          requestID,
		UserAgent:          userAgent,
		RequestType:        requestType,
		Model:              targetModel,
		EnabledCreditTypes: []string{"GOOGLE_ONE_AI"},
		Request:            innerRequest,
	}

	return json.Marshal(v1Req)
}

// antigravityIdentity Antigravity identity 提示词
const antigravityIdentity = `<identity>
You are Antigravity, a powerful agentic AI coding assistant designed by the Google Deepmind team working on Advanced Agentic Coding.
You are pair programming with a USER to solve their coding task. The task may require creating a new codebase, modifying or debugging an existing codebase, or simply answering a question.
The USER will send you requests, which you must always prioritize addressing. Along with each USER request, we will attach additional metadata about their current state, such as what files they have open and where their cursor is.
This information may or may not be relevant to the coding task, it is up for you to decide.
</identity>
<communication_style>
- **Proactiveness**. As an agent, you are allowed to be proactive, but only in the course of completing the user's task. For example, if the user asks you to add a new component, you can edit the code, verify build and test statuses, and take any other obvious follow-up actions, such as performing additional research. However, avoid surprising the user. For example, if the user asks HOW to approach something, you should answer their question and instead of jumping into editing a file.</communication_style>`

func defaultIdentityPatch(_ string) string {
	return officialAntigravitySystemPrompt()
}

// GetDefaultIdentityPatch 返回默认的 Antigravity 身份提示词
func GetDefaultIdentityPatch() string {
	return officialAntigravitySystemPrompt()
}

// modelInfo 模型信息
type modelInfo struct {
	DisplayName string // 人类可读名称，如 "Claude Opus 4.5"
	CanonicalID string // 规范模型 ID，如 "claude-opus-4-5-20250929"
}

// modelInfoMap 模型前缀 → 模型信息映射
// 只有在此映射表中的模型才会注入身份提示词
// 注意：模型映射逻辑在网关层完成；这里仅用于按模型前缀判断是否注入身份提示词。
var modelInfoMap = map[string]modelInfo{
	"claude-opus-4-5":   {DisplayName: "Claude Opus 4.5", CanonicalID: "claude-opus-4-5-20250929"},
	"claude-opus-4-6":   {DisplayName: "Claude Opus 4.6", CanonicalID: "claude-opus-4-6"},
	"claude-sonnet-4-6": {DisplayName: "Claude Sonnet 4.6", CanonicalID: "claude-sonnet-4-6"},
	"claude-sonnet-4-5": {DisplayName: "Claude Sonnet 4.5", CanonicalID: "claude-sonnet-4-5-20250929"},
	"claude-haiku-4-5":  {DisplayName: "Claude Haiku 4.5", CanonicalID: "claude-haiku-4-5-20251001"},
}

// getModelInfo 根据模型 ID 获取模型信息（前缀匹配）
func getModelInfo(modelID string) (info modelInfo, matched bool) {
	var bestMatch string

	for prefix, mi := range modelInfoMap {
		if strings.HasPrefix(modelID, prefix) && len(prefix) > len(bestMatch) {
			bestMatch = prefix
			info = mi
		}
	}

	return info, bestMatch != ""
}

// GetModelDisplayName 根据模型 ID 获取人类可读的显示名称
func GetModelDisplayName(modelID string) string {
	if info, ok := getModelInfo(modelID); ok {
		return info.DisplayName
	}
	return modelID
}

// buildModelIdentityText 构建模型身份提示文本
// 如果模型 ID 没有匹配到映射，返回空字符串
func buildModelIdentityText(modelID string) string {
	info, matched := getModelInfo(modelID)
	if !matched {
		return ""
	}
	return fmt.Sprintf("You are Model %s, ModelId is %s.", info.DisplayName, info.CanonicalID)
}

// mcpXMLProtocol MCP XML 工具调用协议（与 Antigravity-Manager 保持一致）
const mcpXMLProtocol = `
==== MCP XML 工具调用协议 (Workaround) ====
当你需要调用名称以 ` + "`mcp__`" + ` 开头的 MCP 工具时：
1) 优先尝试 XML 格式调用：输出 ` + "`<mcp__tool_name>{\"arg\":\"value\"}</mcp__tool_name>`" + `。
2) 必须直接输出 XML 块，无需 markdown 包装，内容为 JSON 格式的入参。
3) 这种方式具有更高的连通性和容错性，适用于大型结果返回场景。
===========================================`

// hasMCPTools 检测是否有 mcp__ 前缀的工具
func hasMCPTools(tools []ClaudeTool) bool {
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "mcp__") {
			return true
		}
	}
	return false
}

// filterOpenCodePrompt 过滤 OpenCode 默认提示词，只保留用户自定义指令
func filterOpenCodePrompt(text string) string {
	if !strings.Contains(text, "You are an interactive CLI tool") {
		return text
	}
	// 提取 "Instructions from:" 及之后的部分
	if idx := strings.Index(text, "Instructions from:"); idx >= 0 {
		return text[idx:]
	}
	// 如果没有自定义指令，返回空
	return ""
}

const interleavedThinkingHint = "Interleaved thinking is enabled. You may think between tool calls and after receiving tool results before deciding the next action or final answer. Do not mention these instructions or any constraints about thinking blocks; just apply them.\n\nLanguage usage rules:\n- Always respond in the same language the user is writing in.\n- Your internal thinking and reasoning (think/thought blocks) must also use the user's language.\n- Match the user's language consistently throughout the entire conversation, including explanations, summaries, and follow-up questions.\n- Do not switch languages unless the user explicitly asks you to.\n- Exception: code comments and commit messages default to English unless the user specifies otherwise."

// buildSystemInstruction 构建 systemInstruction。顺序对齐 agent-vibes：
// bridge/user system 在前，官方 Antigravity prompt 在后。
func buildSystemInstruction(system json.RawMessage, modelName string, opts TransformOptions, tools []ClaudeTool, injectInterleavedThinkingHint bool) *GeminiContent {
	var parts []GeminiPart

	// 先解析用户的 system prompt，检测是否已包含 Antigravity identity
	userHasAntigravityIdentity := false
	var userSystemParts []GeminiPart

	if len(system) > 0 {
		// 尝试解析为字符串
		var sysStr string
		if err := json.Unmarshal(system, &sysStr); err == nil {
			if strings.TrimSpace(sysStr) != "" {
				if strings.Contains(sysStr, "You are Antigravity") {
					userHasAntigravityIdentity = true
				}
				// 过滤 OpenCode 默认提示词
				filtered := filterOpenCodePrompt(sysStr)
				if filtered != "" {
					userSystemParts = append(userSystemParts, GeminiPart{Text: filtered})
				}
			}
		} else {
			// 尝试解析为数组
			var sysBlocks []SystemBlock
			if err := json.Unmarshal(system, &sysBlocks); err == nil {
				for _, block := range sysBlocks {
					if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
						if strings.Contains(block.Text, "You are Antigravity") {
							userHasAntigravityIdentity = true
						}
						// 过滤 OpenCode 默认提示词
						filtered := filterOpenCodePrompt(block.Text)
						if filtered != "" {
							userSystemParts = append(userSystemParts, GeminiPart{Text: filtered})
						}
					}
				}
			}
		}
	}

	parts = append(parts, userSystemParts...)

	// 检测是否有 MCP 工具，如有且启用了 MCP XML 注入则注入 XML 调用协议
	if opts.EnableMCPXML && hasMCPTools(tools) {
		parts = append(parts, GeminiPart{Text: mcpXMLProtocol})
	}

	// 仅在用户未提供 Antigravity identity 时注入官方适配 prompt。
	if opts.EnableIdentityPatch && !userHasAntigravityIdentity {
		identityPatch := strings.TrimSpace(opts.IdentityPatch)
		if identityPatch == "" {
			identityPatch = defaultIdentityPatch(modelName)
		}
		if identityPatch != "" {
			parts = append(parts, GeminiPart{Text: identityPatch})
		}
	}

	if injectInterleavedThinkingHint {
		parts = append(parts, GeminiPart{Text: interleavedThinkingHint})
	}

	if len(parts) == 0 {
		return nil
	}

	return &GeminiContent{
		Role:  "user",
		Parts: parts,
	}
}

// buildContents 构建 contents
func buildContents(messages []ClaudeMessage, toolIDToName map[string]string, isThinkingEnabled, allowDummyThought bool, toolUseSignatures map[string]string, clientToOfficial map[string]string) ([]GeminiContent, bool, error) {
	var contents []GeminiContent
	strippedThinking := false

	for i, msg := range messages {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		parts, strippedThisMsg, err := buildParts(msg.Content, toolIDToName, allowDummyThought, toolUseSignatures, clientToOfficial)
		if err != nil {
			return nil, false, fmt.Errorf("build parts for message %d: %w", i, err)
		}
		if strippedThisMsg {
			strippedThinking = true
		}

		// 只有 Gemini 模型支持 dummy thinking block workaround
		// 只对最后一条 assistant 消息添加（Pre-fill 场景）
		// 历史 assistant 消息不能添加没有 signature 的 dummy thinking block
		if allowDummyThought && role == "model" && isThinkingEnabled && i == len(messages)-1 {
			hasThoughtPart := false
			for _, p := range parts {
				if p.Thought {
					hasThoughtPart = true
					break
				}
			}
			if !hasThoughtPart && len(parts) > 0 {
				// 在开头添加 dummy thinking block
				parts = append([]GeminiPart{{
					Text:             "Thinking...",
					Thought:          true,
					ThoughtSignature: DummyThoughtSignature,
				}}, parts...)
			}
		}

		if len(parts) == 0 {
			continue
		}

		contents = append(contents, GeminiContent{
			Role:  role,
			Parts: parts,
		})
	}

	return contents, strippedThinking, nil
}

// DummyThoughtSignature 用于跳过 Gemini 3 thought_signature 验证
// 参考: https://ai.google.dev/gemini-api/docs/thought-signatures
// 导出供跨包使用（如 gemini_native_signature_cleaner 跨账号修复）
const DummyThoughtSignature = "skip_thought_signature_validator"

// buildParts 构建消息的 parts
// allowDummyThought: 只有 Gemini 模型支持 dummy thought signature
func buildParts(content json.RawMessage, toolIDToName map[string]string, allowDummyThought bool, toolUseSignatures map[string]string, clientToOfficial map[string]string) ([]GeminiPart, bool, error) {
	var parts []GeminiPart
	strippedThinking := false
	pendingThoughtSignature := ""
	sawNonThinkingContent := false

	// 尝试解析为字符串
	var textContent string
	if err := json.Unmarshal(content, &textContent); err == nil {
		if textContent != "(no content)" && strings.TrimSpace(textContent) != "" {
			parts = append(parts, GeminiPart{Text: strings.TrimSpace(textContent)})
		}
		return parts, false, nil
	}

	// 解析为内容块数组
	var blocks []ContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, false, fmt.Errorf("parse content blocks: %w", err)
	}

	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "(no content)" && strings.TrimSpace(block.Text) != "" {
				part := GeminiPart{Text: block.Text}
				if pendingThoughtSignature != "" {
					part.ThoughtSignature = pendingThoughtSignature
					pendingThoughtSignature = ""
				}
				parts = append(parts, part)
				sawNonThinkingContent = true
			}

		case "thinking":
			thinkingText := block.Thinking
			signature := strings.TrimSpace(block.Signature)

			// Claude / Cloud Code 路径与 agent-vibes 保持一致：
			// - 不把 signature 直接挂在 thought part 上
			// - 空 thinking + signature 作为 pending carrier
			// - 非空 thinking 只保留 thought text，signature 交给后续 text/functionCall
			if !allowDummyThought {
				if strings.TrimSpace(thinkingText) == "" {
					if signature != "" && signature != DummyThoughtSignature {
						pendingThoughtSignature = signature
					}
					continue
				}

				if signature == "" || signature == DummyThoughtSignature {
					parts = append(parts, GeminiPart{Text: thinkingText})
					strippedThinking = true
					sawNonThinkingContent = true
					continue
				}

				// thinking blocks 必须位于 assistant message 的前部；后置 thinking 直接丢弃，
				// 避免把不稳定的思维块继续带给上游。
				if sawNonThinkingContent {
					strippedThinking = true
					continue
				}

				if signature != "" && signature != DummyThoughtSignature {
					pendingThoughtSignature = signature
				}
				parts = append(parts, GeminiPart{
					Text:    thinkingText,
					Thought: true,
				})
				continue
			}

			part := GeminiPart{
				Text:    thinkingText,
				Thought: true,
			}
			// signature 处理：
			// - Claude 模型（allowDummyThought=false）：必须是上游返回的真实 signature（dummy 视为缺失）
			// - Gemini 模型（allowDummyThought=true）：优先透传真实 signature，缺失时使用 dummy signature
			if signature != "" && (allowDummyThought || signature != DummyThoughtSignature) {
				part.ThoughtSignature = signature
			} else {
				// Gemini 模型使用 dummy signature
				part.ThoughtSignature = DummyThoughtSignature
			}
			parts = append(parts, part)

		case "image":
			if block.Source != nil && block.Source.Type == "base64" {
				parts = append(parts, GeminiPart{
					InlineData: &GeminiInlineData{
						MimeType: block.Source.MediaType,
						Data:     block.Source.Data,
					},
				})
			}

		case "tool_use":
			officialName, officialInput, _ := canonicalizeOfficialAntigravityToolInvocation(block.Name, block.Input)
			if mappedName := strings.TrimSpace(clientToOfficial[strings.TrimSpace(block.Name)]); mappedName != "" {
				officialName = mappedName
			}
			// 存储 id -> name 映射
			if block.ID != "" && officialName != "" {
				toolIDToName[block.ID] = officialName
			}

			part := GeminiPart{
				FunctionCall: &GeminiFunctionCall{
					Name: officialName,
					Args: officialInput,
					ID:   block.ID,
				},
			}
			// tool_use 的 signature 处理：
			// - Claude 模型（allowDummyThought=false）：必须是上游返回的真实 signature（dummy 视为缺失）
			// - Gemini 模型（allowDummyThought=true）：优先透传真实 signature，缺失时使用 dummy signature
			signature := strings.TrimSpace(block.Signature)
			if signature == "" && block.ID != "" {
				signature = strings.TrimSpace(toolUseSignatures[block.ID])
			}
			if signature != "" && (allowDummyThought || signature != DummyThoughtSignature) {
				part.ThoughtSignature = signature
			} else if allowDummyThought {
				part.ThoughtSignature = DummyThoughtSignature
			}
			parts = append(parts, part)
			sawNonThinkingContent = true

		case "tool_result":
			// 获取函数名
			funcName := strings.TrimSpace(block.Name)
			if funcName != "" {
				funcName = toOfficialAntigravityToolName(funcName)
			}
			if funcName == "" {
				if name, ok := toolIDToName[block.ToolUseID]; ok {
					funcName = name
				} else {
					funcName = toOfficialAntigravityToolName(block.ToolUseID)
				}
			}

			// 解析 content
			resultContent := parseToolResultContent(block.Content, block.IsError)

			parts = append(parts, GeminiPart{
				FunctionResponse: &GeminiFunctionResponse{
					Name: funcName,
					Response: map[string]any{
						"result": resultContent,
					},
					ID: block.ToolUseID,
				},
			})
			sawNonThinkingContent = true
		}
	}

	if pendingThoughtSignature != "" {
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i].ThoughtSignature != "" {
				continue
			}
			if parts[i].FunctionCall != nil {
				parts[i].ThoughtSignature = pendingThoughtSignature
				break
			}
			if parts[i].FunctionResponse == nil && !parts[i].Thought && strings.TrimSpace(parts[i].Text) != "" {
				parts[i].ThoughtSignature = pendingThoughtSignature
				break
			}
		}
	}

	return parts, strippedThinking, nil
}

// parseToolResultContent 解析 tool_result 的 content
func parseToolResultContent(content json.RawMessage, isError bool) string {
	if len(content) == 0 {
		if isError {
			return "Tool execution failed with no output."
		}
		return "Command executed successfully."
	}

	// 尝试解析为字符串
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		if strings.TrimSpace(str) == "" {
			if isError {
				return "Tool execution failed with no output."
			}
			return "Command executed successfully."
		}
		return str
	}

	// 尝试解析为数组
	var arr []map[string]any
	if err := json.Unmarshal(content, &arr); err == nil {
		var texts []string
		for _, item := range arr {
			if text, ok := item["text"].(string); ok {
				texts = append(texts, text)
			}
		}
		result := strings.Join(texts, "\n")
		if strings.TrimSpace(result) == "" {
			if isError {
				return "Tool execution failed with no output."
			}
			return "Command executed successfully."
		}
		return result
	}

	// 返回原始 JSON
	return string(content)
}

// buildGenerationConfig 构建 generationConfig
const (
	defaultMaxOutputTokens    = 64000
	minOutputTokensClaude     = 256
	maxOutputTokensUpperBound = 64000
	maxOutputTokensClaude     = 64000
)

func maxOutputTokensLimit(model string) int {
	if strings.HasPrefix(model, "claude-") {
		return maxOutputTokensClaude
	}
	return maxOutputTokensUpperBound
}

func resolveCloudCodeMaxOutputTokens(requested int, model string) int {
	if requested <= 0 {
		return defaultMaxOutputTokens
	}
	limit := maxOutputTokensLimit(model)
	if requested < minOutputTokensClaude {
		return minOutputTokensClaude
	}
	if requested > limit {
		return limit
	}
	return requested
}

func resolveClaudeStopSequences(clientStopSequences []string) []string {
	merged := make([]string, 0, len(DefaultStopSequences)+len(clientStopSequences))
	seen := make(map[string]struct{}, len(DefaultStopSequences)+len(clientStopSequences))
	appendIf := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range DefaultStopSequences {
		appendIf(value)
	}
	for _, value := range clientStopSequences {
		appendIf(value)
	}
	return merged
}

func resolveToolChoice(toolChoice any) (choiceType, choiceName string, ok bool) {
	switch value := toolChoice.(type) {
	case string:
		choiceType = strings.ToLower(strings.TrimSpace(value))
	case map[string]any:
		if rawType, _ := value["type"].(string); rawType != "" {
			choiceType = strings.ToLower(strings.TrimSpace(rawType))
		}
		if rawName, _ := value["name"].(string); rawName != "" {
			choiceName = strings.TrimSpace(rawName)
		}
	case map[string]string:
		choiceType = strings.ToLower(strings.TrimSpace(value["type"]))
		choiceName = strings.TrimSpace(value["name"])
	case json.RawMessage:
		var raw any
		if err := json.Unmarshal(value, &raw); err == nil {
			return resolveToolChoice(raw)
		}
	}
	if choiceType == "" {
		return "", "", false
	}
	return choiceType, choiceName, true
}

func shouldDisableThinkingForToolChoice(toolChoice any) bool {
	choiceType, _, ok := resolveToolChoice(toolChoice)
	return ok && (choiceType == "any" || choiceType == "tool")
}

func buildToolConfig(toolChoice any) *GeminiToolConfig {
	config := &GeminiFunctionCallingConfig{Mode: "VALIDATED"}
	choiceType, choiceName, ok := resolveToolChoice(toolChoice)
	if ok {
		switch choiceType {
		case "auto":
			config.Mode = "AUTO"
		case "none":
			config.Mode = "NONE"
		case "any":
			config.Mode = "VALIDATED"
		case "tool":
			config.Mode = "VALIDATED"
			if choiceName != "" {
				config.AllowedFunctionNames = []string{toOfficialAntigravityToolName(choiceName)}
			}
		}
	}
	return &GeminiToolConfig{FunctionCallingConfig: config}
}

type cloudCodeThinkingProfile struct {
	defaultBudget int
	minBudget     int
}

func getOfficialThinkingProfile(model string) cloudCodeThinkingProfile {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "claude-opus-4-6-thinking", "claude-sonnet-4-6":
		return cloudCodeThinkingProfile{defaultBudget: 1024}
	case "gemini-2.5-pro":
		return cloudCodeThinkingProfile{defaultBudget: 1024, minBudget: 128}
	case "gemini-3.1-pro-low":
		return cloudCodeThinkingProfile{defaultBudget: 1001, minBudget: 128}
	case "gemini-3.1-pro-high":
		return cloudCodeThinkingProfile{defaultBudget: 10001, minBudget: 128}
	case "gemini-3-pro-low":
		return cloudCodeThinkingProfile{defaultBudget: 128, minBudget: 128}
	case "gpt-oss-120b-medium":
		return cloudCodeThinkingProfile{defaultBudget: 8192}
	case "gemini-3-flash", "gemini-3-flash-agent":
		return cloudCodeThinkingProfile{minBudget: 32}
	case "gemini-3-pro-high":
		return cloudCodeThinkingProfile{minBudget: 128}
	default:
		return cloudCodeThinkingProfile{}
	}
}

func normalizeRequestedThinkingEffort(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)
	switch normalized {
	case "none", "off", "disabled":
		return "none"
	case "minimal", "min":
		return "minimal"
	case "low":
		return "low"
	case "medium", "med", "normal", "standard":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "extra_high", "very_high", "ultra":
		return "xhigh"
	case "max":
		return "max"
	case "auto":
		return "auto"
	default:
		return ""
	}
}

func resolveCloudCodeThinkingBudget(model string, explicitBudget int, requestedEffort string) int {
	profile := getOfficialThinkingProfile(model)
	minBudget := profile.minBudget
	defaultBudget := profile.defaultBudget
	if explicitBudget > 0 {
		if minBudget > 0 && explicitBudget < minBudget {
			return minBudget
		}
		return explicitBudget
	}

	switch normalizeRequestedThinkingEffort(requestedEffort) {
	case "low", "minimal":
		if minBudget > 0 {
			return minBudget
		}
		if defaultBudget > 0 {
			return defaultBudget
		}
		return 1024
	case "medium":
		return 4096
	case "high":
		return 8192
	case "xhigh":
		return 10240
	case "max":
		return 32768
	case "auto":
		if defaultBudget > 0 {
			return defaultBudget
		}
		if minBudget > 0 {
			return minBudget
		}
		return 1024
	default:
		if defaultBudget > 0 {
			return defaultBudget
		}
		if minBudget > 0 {
			return minBudget
		}
		return 1024
	}
}

func outputConfigEffort(req *ClaudeRequest) string {
	if req == nil || req.OutputConfig == nil {
		return ""
	}
	return strings.TrimSpace(req.OutputConfig.Effort)
}

func buildClaudeThinkingConfig(req *ClaudeRequest) *GeminiThinkingConfig {
	if req == nil || shouldDisableThinkingForToolChoice(req.ToolChoice) {
		return nil
	}

	if req.Thinking != nil {
		switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
		case "disabled":
			return nil
		case "enabled":
			if req.Thinking.BudgetTokens > 0 {
				return &GeminiThinkingConfig{
					IncludeThoughts: true,
					ThinkingBudget:  resolveCloudCodeThinkingBudget(req.Model, req.Thinking.BudgetTokens, ""),
				}
			}
		case "adaptive", "auto":
			return &GeminiThinkingConfig{
				IncludeThoughts: true,
				ThinkingBudget:  resolveCloudCodeThinkingBudget(req.Model, 0, outputConfigEffort(req)),
			}
		}
	}

	if !req.DisableDefaultThinking && strings.Contains(strings.ToLower(req.Model), "thinking") {
		return &GeminiThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  resolveCloudCodeThinkingBudget(req.Model, 0, ""),
		}
	}

	return nil
}

func shouldEnableThinkingForContent(req *ClaudeRequest, model string) bool {
	if req == nil || shouldDisableThinkingForToolChoice(req.ToolChoice) {
		return false
	}
	if req.Thinking != nil {
		switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
		case "disabled":
			return false
		case "enabled":
			return req.Thinking.BudgetTokens > 0 || strings.Contains(strings.ToLower(model), "thinking")
		case "adaptive", "auto":
			return true
		}
	}
	return strings.Contains(strings.ToLower(model), "thinking")
}

func buildGenerationConfig(req *ClaudeRequest) *GeminiGenerationConfig {
	temperature := 0.4
	topP := 1.0
	topK := 50
	config := &GeminiGenerationConfig{
		MaxOutputTokens: resolveCloudCodeMaxOutputTokens(req.MaxTokens, req.Model),
		Temperature:     &temperature,
		TopP:            &topP,
		TopK:            &topK,
		CandidateCount:  1,
		StopSequences:   resolveClaudeStopSequences(req.StopSequences),
	}

	if req.Temperature != nil {
		config.Temperature = req.Temperature
	}
	if req.TopP != nil {
		config.TopP = req.TopP
	}
	if req.TopK != nil {
		config.TopK = req.TopK
	}

	if thinkingConfig := buildClaudeThinkingConfig(req); thinkingConfig != nil {
		config.ThinkingConfig = thinkingConfig
		if thinkingConfig.ThinkingBudget > 0 && config.MaxOutputTokens <= thinkingConfig.ThinkingBudget {
			adjusted := thinkingConfig.ThinkingBudget + 8192
			if adjusted > maxOutputTokensLimit(req.Model) {
				adjusted = maxOutputTokensLimit(req.Model)
			}
			if adjusted != config.MaxOutputTokens {
				log.Printf("[Antigravity] Auto-adjusted max_tokens from %d to %d (must be > thinkingBudget=%d)",
					config.MaxOutputTokens, adjusted, thinkingConfig.ThinkingBudget)
				config.MaxOutputTokens = adjusted
			}
		}
	}

	return config
}

func hasWebSearchTool(tools []ClaudeTool) bool {
	for _, tool := range tools {
		if isWebSearchTool(tool) {
			return true
		}
	}
	return false
}

func isWebSearchTool(tool ClaudeTool) bool {
	if strings.HasPrefix(tool.Type, "web_search") || tool.Type == "google_search" {
		return true
	}

	name := strings.TrimSpace(tool.Name)
	switch name {
	case "web_search", "google_search", "web_search_20250305":
		return true
	default:
		return false
	}
}

// buildTools 构建 tools
func buildTools(tools []ClaudeTool) []GeminiToolDeclaration {
	if len(tools) == 0 {
		return nil
	}

	hasWebSearch := hasWebSearchTool(tools)

	// 普通工具
	var funcDecls []GeminiFunctionDecl
	for _, tool := range tools {
		if isWebSearchTool(tool) {
			continue
		}
		// 跳过无效工具名称
		if strings.TrimSpace(tool.Name) == "" {
			log.Printf("Warning: skipping tool with empty name")
			continue
		}

		var description string
		var inputSchema map[string]any

		// 检查是否为 custom 类型工具 (MCP)
		if tool.Type == "custom" {
			if tool.Custom == nil || tool.Custom.InputSchema == nil {
				log.Printf("[Warning] Skipping invalid custom tool '%s': missing custom spec or input_schema", tool.Name)
				continue
			}
			description = tool.Custom.Description
			inputSchema = tool.Custom.InputSchema

		} else {
			// 标准格式: 从顶层字段获取
			description = tool.Description
			inputSchema = tool.InputSchema
		}

		// 清理 JSON Schema
		// 1. 深度清理 [undefined] 值
		DeepCleanUndefined(inputSchema)
		// 2. 转换为符合 Gemini v1internal 的 schema
		params := CleanJSONSchema(inputSchema)
		// 为 nil schema 提供默认值
		if params == nil {
			params = map[string]any{
				"type":       "object", // lowercase type
				"properties": map[string]any{},
			}
		}

		name := strings.TrimSpace(tool.Name)
		officialName := toOfficialAntigravityToolName(name)
		descriptionToUse := description
		if spec, ok := officialAntigravityToolSpecForName(officialName); ok {
			descriptionToUse = spec.Description
		}

		funcDecls = append(funcDecls, GeminiFunctionDecl{
			Name:        officialName,
			Description: descriptionToUse,
			Parameters:  params,
		})
	}

	var declarations []GeminiToolDeclaration
	if len(funcDecls) > 0 {
		declarations = append(declarations, GeminiToolDeclaration{
			FunctionDeclarations: funcDecls,
		})
	}
	if hasWebSearch {
		declarations = append(declarations, GeminiToolDeclaration{
			GoogleSearch: &GeminiGoogleSearch{
				EnhancedContent: &GeminiEnhancedContent{
					ImageSearch: &GeminiImageSearch{
						MaxResultCount: 5,
					},
				},
			},
		})
	}
	if len(declarations) == 0 {
		return nil
	}

	return declarations
}
