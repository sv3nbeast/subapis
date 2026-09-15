package cursor

import (
	"os"
	"strings"

	"github.com/google/uuid"
)

// ChatMessage is one OpenAI-shaped turn handed to the Cursor agent. Cursor's
// Ask mode carries plain text only, so callers flatten content parts first.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Official agent.v1 field numbers from Cursor 3.16.
const (
	fieldAgentClientRunRequest  = 1
	fieldAgentClientKV          = 3
	fieldAgentClientHeartbeat   = 7
	fieldAgentServerInteraction = 1
	fieldAgentServerKV          = 4

	fieldRunConversationState = 1
	fieldRunAction            = 2
	fieldRunModelDetails      = 3
	fieldRunMcpTools          = 4
	fieldRunConversationID    = 5
	fieldRunRequestedModel    = 9
	fieldRunCustomSystem      = 8
	fieldRunID                = 25

	fieldConvStateMode = 10

	fieldActionUserMessage = 1

	fieldUserMsgActionMessage = 1
	fieldUserMsgActionContext = 2
	fieldUserMsgActionPrepend = 4

	fieldUserMsgText = 1
	fieldUserMsgID   = 2
	fieldUserMsgMode = 4

	fieldReqCtxEnv = 4

	// MCP 工具定义（run field 4 的每个条目）。name 同时出现在 1 与 5：
	// Cursor 用 5 作为调用时回传的标识，1 是展示名。
	fieldToolDefName        = 1
	fieldToolDefDescription = 2
	fieldToolDefCallName    = 5
	fieldToolDefSchema      = 6
	fieldMcpToolEntry       = 1

	// 工具调用事件（interaction 内）。
	fieldInteractionToolStart  = 2
	fieldInteractionToolEnd    = 3
	fieldInteractionToolDelta  = 7
	fieldInteractionToolDelta2 = 15

	// 工具调用的载荷结构（2026-09-15 对真实账号 composer-2.5 抓包实测）：
	//   tool_start = { 1: callID, 2: { 15: { 1: <invocation> } } }
	//   exec       = { 11: <invocation> }        ← 工具调用本身走这条通道
	//   <invocation> = { 1: name, 2: <参数>, 3: callID, 5: callName }
	//   <参数>       = 重复的 { 1: key, 2: <值> }，值为 { 3: string } 等
	fieldToolCallID       = 1
	fieldToolCallPayload  = 2
	fieldToolCallMcpWrap  = 15
	fieldToolInvocation   = 1
	fieldExecInvocation   = 11
	fieldInvocationName   = 1
	fieldInvocationArgs   = 2
	fieldInvocationCallID = 3
	fieldInvocationArgKey = 1
	fieldInvocationArgVal = 2
	fieldArgValueString   = 3
	fieldArgValueNumber   = 2
	fieldArgValueBool     = 4

	fieldNALEnvOSVersion = 1
	fieldNALEnvShell     = 3
	fieldNALEnvTimezone  = 10

	fieldModelID = 1

	fieldInteractionTextDelta     = 1
	fieldInteractionThinkingDelta = 4
	fieldInteractionTokenDelta    = 8
	fieldInteractionHeartbeat     = 13
	fieldInteractionTurnEnded     = 14

	fieldTextDeltaText         = 1
	fieldTextDeltaServerNotice = 2
	fieldThinkingDeltaText     = 1
	fieldTokenDeltaTokens      = 1

	fieldTurnEndedInputTokens      = 1
	fieldTurnEndedOutputTokens     = 2
	fieldTurnEndedCacheReadTokens  = 3
	fieldTurnEndedCacheWriteTokens = 4
	fieldTurnEndedReasoningTokens  = 5

	fieldKVId          = 1
	fieldKVGetBlobArgs = 2
	fieldKVSetBlobArgs = 3
	fieldKVGetResult   = 2
	fieldKVSetResult   = 3

	fieldBlobID       = 1
	fieldBlobData     = 1 // GetBlobResult.blob_data; SetBlobArgs uses 2
	fieldSetBlobID    = 1
	fieldSetBlobData  = 2
	fieldBlobError    = 2
	fieldErrorMessage = 1

	// AgentMode ASK is the chat-without-tools path.
	AgentModeUnspecified = 0
	AgentModeAgent       = 1
	AgentModeAsk         = 2
)

// Tool is one tool definition offered to the Cursor agent. Schema must be the
// JSON Schema text for the tool's parameters, exactly as the client sent it.
type Tool struct {
	Name        string
	Description string
	Schema      string
}

// BuildAgentClientMessage encodes agent.v1.AgentClientMessage{run_request}.
//
// Passing tools switches the turn to Agent mode: Ask mode neither accepts tool
// definitions nor emits tool calls, so a tool-carrying request has to run as an
// agent turn to come back with anything the client can act on.
func BuildAgentClientMessage(messages []ChatMessage, model string, tools []Tool) (payload []byte, conversationID, runID string) {
	if model == "" {
		model = "default"
	}
	conversationID = uuid.New().String()
	runID = uuid.New().String()

	mode := AgentModeAsk
	if len(tools) > 0 {
		mode = AgentModeAgent
	}

	systemPrompt, userText, prior := splitAskMessages(messages)

	var state ProtobufWriter
	state.Varint(fieldConvStateMode, mode)

	var userMsg ProtobufWriter
	userMsg.String(fieldUserMsgText, userText)
	userMsg.String(fieldUserMsgID, uuid.New().String())
	userMsg.Varint(fieldUserMsgMode, mode)

	var env ProtobufWriter
	if rel := osRelease(); rel != "" {
		env.String(fieldNALEnvOSVersion, rel)
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		env.String(fieldNALEnvShell, sh)
	}
	env.String(fieldNALEnvTimezone, clientTimezone())

	var reqCtx ProtobufWriter
	reqCtx.Bytes(fieldReqCtxEnv, env.Result())

	var userAction ProtobufWriter
	userAction.Bytes(fieldUserMsgActionMessage, userMsg.Result())
	userAction.Bytes(fieldUserMsgActionContext, reqCtx.Result())
	for _, p := range prior {
		var pre ProtobufWriter
		pre.String(fieldUserMsgText, p)
		pre.String(fieldUserMsgID, uuid.New().String())
		pre.Varint(fieldUserMsgMode, mode)
		userAction.Bytes(fieldUserMsgActionPrepend, pre.Result())
	}

	var action ProtobufWriter
	action.Bytes(fieldActionUserMessage, userAction.Result())

	var modelDetails ProtobufWriter
	modelDetails.String(fieldModelID, model)

	var requested ProtobufWriter
	requested.String(fieldModelID, model)

	var run ProtobufWriter
	run.Bytes(fieldRunConversationState, state.Result())
	run.Bytes(fieldRunAction, action.Result())
	run.Bytes(fieldRunModelDetails, modelDetails.Result())
	run.Bytes(fieldRunMcpTools, encodeTools(tools))
	run.String(fieldRunConversationID, conversationID)
	if systemPrompt != "" {
		run.String(fieldRunCustomSystem, systemPrompt)
	}
	run.Bytes(fieldRunRequestedModel, requested.Result())
	run.String(fieldRunID, runID)

	var client ProtobufWriter
	client.Bytes(fieldAgentClientRunRequest, run.Result())
	return client.Result(), conversationID, runID
}

func encodeClientHeartbeat() []byte {
	var w ProtobufWriter
	w.Bytes(fieldAgentClientHeartbeat, nil)
	return w.Result()
}

func splitAskMessages(messages []ChatMessage) (systemPrompt, userText string, prior []string) {
	var systems []string
	var rest []ChatMessage
	for _, m := range messages {
		if strings.EqualFold(m.Role, "system") {
			if strings.TrimSpace(m.Content) != "" {
				systems = append(systems, m.Content)
			}
			continue
		}
		rest = append(rest, m)
	}
	systemPrompt = strings.Join(systems, "\n\n")
	if len(rest) == 0 {
		return systemPrompt, "", nil
	}
	if len(rest) == 1 && strings.EqualFold(rest[0].Role, "user") {
		return systemPrompt, rest[0].Content, nil
	}

	last := rest[len(rest)-1]
	if strings.EqualFold(last.Role, "user") {
		for _, m := range rest[:len(rest)-1] {
			prior = append(prior, rolePrefix(m.Role)+m.Content)
		}
		return systemPrompt, last.Content, prior
	}

	var b strings.Builder
	for i, m := range rest {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(rolePrefix(m.Role))
		b.WriteString(m.Content)
	}
	return systemPrompt, b.String(), nil
}

func rolePrefix(role string) string {
	switch strings.ToLower(role) {
	case "assistant":
		return "Assistant: "
	case "user":
		return "User: "
	default:
		return role + ": "
	}
}

type kvServerOp struct {
	id      uint64
	getBlob []byte
	setBlob []byte
	setData []byte
}

func parseAgentKV(data []byte) *kvServerOp {
	kv := GetNested(data, fieldAgentServerKV)
	if kv == nil {
		return nil
	}
	op := &kvServerOp{}
	pr := NewProtobufReader(kv)
	found := false
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		switch f.Num {
		case fieldKVId:
			op.id = f.Varint
			found = true
		case fieldKVGetBlobArgs:
			op.getBlob = GetNested(f.Data, fieldBlobID)
			if op.getBlob == nil {
				op.getBlob = f.Data
			}
			found = true
		case fieldKVSetBlobArgs:
			op.setBlob = GetNested(f.Data, fieldSetBlobID)
			op.setData = GetNested(f.Data, fieldSetBlobData)
			found = true
		}
	}
	if !found {
		return nil
	}
	return op
}

func encodeKVGetResult(id uint64, blob []byte, errMsg string) []byte {
	var result ProtobufWriter
	if errMsg != "" {
		var e ProtobufWriter
		e.String(fieldErrorMessage, errMsg)
		result.Bytes(fieldBlobError, e.Result())
	} else {
		result.Bytes(fieldBlobData, blob)
	}
	var kv ProtobufWriter
	kv.Varint(fieldKVId, int(id))
	kv.Bytes(fieldKVGetResult, result.Result())
	var client ProtobufWriter
	client.Bytes(fieldAgentClientKV, kv.Result())
	return client.Result()
}

func encodeKVSetResult(id uint64) []byte {
	var kv ProtobufWriter
	kv.Varint(fieldKVId, int(id))
	kv.Bytes(fieldKVSetResult, nil)
	var client ProtobufWriter
	client.Bytes(fieldAgentClientKV, kv.Result())
	return client.Result()
}

// encodeTools encodes the MCP tool list carried in run field 4. Returns nil for
// an empty list so the field stays the empty message Ask mode expects.
func encodeTools(tools []Tool) []byte {
	if len(tools) == 0 {
		return nil
	}
	var mcp ProtobufWriter
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		var def ProtobufWriter
		def.String(fieldToolDefName, name)
		def.String(fieldToolDefDescription, tool.Description)
		// Cursor echoes field 5 back as the call name; keep both in sync so a
		// returned tool_call can be matched to what the client asked for.
		def.String(fieldToolDefCallName, name)
		if schema := strings.TrimSpace(tool.Schema); schema != "" {
			def.String(fieldToolDefSchema, schema)
		}
		mcp.Bytes(fieldMcpToolEntry, def.Result())
	}
	return mcp.Result()
}

// Agent 模式下上游不只是回内容，它会反过来要求客户端做事：执行命令
// （AgentServerMessage field 2）或回答一个交互式询问（field 7）。不应答会让
// 这一轮永远停在等待上，所以必须逐条回复。字段号来自 PR #7121 的抓包。
const (
	fieldAgentServerExec  = 2
	fieldAgentServerQuery = 7

	fieldAgentClientExecControl = 5
	fieldAgentClientQueryReply  = 6

	fieldExecControlThrow = 2
	fieldExecThrowID      = 1
	fieldExecThrowMessage = 2

	fieldQueryReplyID       = 1
	fieldQueryReplyApprove  = 1
	fieldQueryReplyRejected = 2
	fieldQueryRejectReason  = 1
)

// ServerControl 是一条需要客户端应答的上游控制消息。
type ServerControl struct {
	Kind       string // "exec" | "query"
	ID         uint64
	QueryField uint32
	// CarriesToolCall 表示这条 exec 里装的是工具调用而不是待执行命令。
	// 这类不能拒绝：拒绝等于告诉模型"工具执行失败"。
	CarriesToolCall bool
}

// ParseServerControls 提取本帧里需要应答的控制消息。
func ParseServerControls(data []byte) []ServerControl {
	var controls []ServerControl
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		if f.WireType != WireBytes {
			continue
		}
		switch f.Num {
		case fieldAgentServerExec:
			controls = append(controls, ServerControl{
				Kind:            "exec",
				ID:              getVarint(f.Data, fieldExecThrowID),
				CarriesToolCall: GetNested(f.Data, fieldExecInvocation) != nil,
			})
		case fieldAgentServerQuery:
			controls = append(controls, ServerControl{
				Kind:       "query",
				ID:         getVarint(f.Data, fieldQueryReplyID),
				QueryField: firstNestedFieldNum(f.Data),
			})
		}
	}
	return controls
}

// firstNestedFieldNum 返回除 id 之外的第一个嵌套字段号，它标明这是哪一类询问。
func firstNestedFieldNum(data []byte) uint32 {
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			return 0
		}
		if f.WireType == WireBytes && f.Num != fieldQueryReplyID {
			return f.Num
		}
	}
}

// IsNetworkQuery 报告该询问是否只是"上游要自己联网"。这类可以批准：动作发生在
// Cursor 侧，网关不需要做任何事。其余询问需要一个真实答案，网关代答等于编造。
func IsNetworkQuery(queryField uint32) bool {
	switch queryField {
	case 2, 5, 6, 9:
		return true
	default:
		return false
	}
}

// EncodeQueryReply 编码一次询问应答。
func EncodeQueryReply(queryID uint64, queryField uint32, approve bool, reason string) []byte {
	var inner ProtobufWriter
	if approve {
		inner.Bytes(fieldQueryReplyApprove, nil)
	} else {
		var rejected ProtobufWriter
		rejected.String(fieldQueryRejectReason, reason)
		inner.Bytes(fieldQueryReplyRejected, rejected.Result())
	}
	var resp ProtobufWriter
	resp.Varint(fieldQueryReplyID, int(queryID))
	resp.Bytes(queryField, inner.Result())
	var client ProtobufWriter
	client.Bytes(fieldAgentClientQueryReply, resp.Result())
	return client.Result()
}

// EncodeExecReject 拒绝一次命令执行请求。
//
// 这个拒绝不是可配置项。上游要求的"执行"发生在收到消息的一侧——在这里就是
// 网关容器。批准它等于让 Cursor 上游在生产服务器上跑任意命令。真正的工具执行
// 必须由客户端完成：网关把 tool_use 交给 Claude Code，由它在用户机器上征得
// 许可后执行。
func EncodeExecReject(execID uint64, message string) []byte {
	var throw ProtobufWriter
	throw.Varint(fieldExecThrowID, int(execID))
	throw.String(fieldExecThrowMessage, message)
	var control ProtobufWriter
	control.Bytes(fieldExecControlThrow, throw.Result())
	var client ProtobufWriter
	client.Bytes(fieldAgentClientExecControl, control.Result())
	return client.Result()
}
