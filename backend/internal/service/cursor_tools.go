package service

import (
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

// Cursor 的 Agent 模式接受 MCP 工具定义并回传工具调用。这里负责两侧的形状转换：
// 入站三种协议的工具定义 → cursor.Tool，以及上游的分片工具事件 → 一次完整的
// tool_calls（客户端只能处理完整参数，Cursor 是按片给的）。

// cursorToolsFromChat 把 Chat Completions 的 tools 转成 Cursor 的工具定义。
// 只转 function 工具：web_search 等服务端工具由上游自己决定是否具备，不该冒充成
// 一个 Cursor 会去调用的 MCP 工具。
func cursorToolsFromChat(tools []apicompat.ChatTool, functions []apicompat.ChatFunction) []cursor.Tool {
	out := make([]cursor.Tool, 0, len(tools)+len(functions))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		if t := strings.TrimSpace(tool.Type); t != "" && t != "function" {
			continue
		}
		out = append(out, cursor.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Schema:      rawJSONString(tool.Function.Parameters),
		})
	}
	// 兼容早期的 functions 字段，语义与 function 工具一致。
	for _, fn := range functions {
		out = append(out, cursor.Tool{
			Name:        fn.Name,
			Description: fn.Description,
			Schema:      rawJSONString(fn.Parameters),
		})
	}
	return out
}

// cursorToolsFromAnthropic 把 Anthropic 的 tools 转成 Cursor 的工具定义。
// 带 Type 的是 Anthropic 服务端工具（web_search 等），Cursor 无法代为提供。
func cursorToolsFromAnthropic(tools []apicompat.AnthropicTool) []cursor.Tool {
	out := make([]cursor.Tool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Type) != "" {
			continue
		}
		out = append(out, cursor.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      rawJSONString(tool.InputSchema),
		})
	}
	return out
}

func rawJSONString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

// cursorToolAggregator 把分片的工具事件拼成完整调用。
//
// Cursor 分三步给：start 带 id 与工具名、若干 delta 各带一段参数、end 收尾。
// 客户端（Anthropic tool_use / OpenAI tool_calls）需要的是完整参数，所以必须
// 在网关侧聚合，按 id 累加，等 end 或流结束时才交付。
type cursorToolAggregator struct {
	order []string
	calls map[string]*cursorToolAccumulator
}

type cursorToolAccumulator struct {
	id       string
	name     string
	args     strings.Builder
	finished bool
	emitted  bool
}

func newCursorToolAggregator() *cursorToolAggregator {
	return &cursorToolAggregator{calls: make(map[string]*cursorToolAccumulator)}
}

// Observe 吸收一个工具事件；返回刚刚完成、可以交付给客户端的调用。
func (a *cursorToolAggregator) Observe(call *cursor.ToolCall) *apicompat.ChatToolCall {
	if a == nil || call == nil {
		return nil
	}
	// 没有 id 的事件无法归属：Cursor 的 end 事件偶尔只带名字，按最后一个未完成
	// 的调用处理，避免凭空造出一个空调用。
	id := strings.TrimSpace(call.ID)
	if id == "" {
		id = a.lastUnfinishedID()
		if id == "" {
			return nil
		}
	}

	entry, ok := a.calls[id]
	if !ok {
		entry = &cursorToolAccumulator{id: id}
		a.calls[id] = entry
		a.order = append(a.order, id)
	}
	if name := strings.TrimSpace(call.Name); name != "" {
		entry.name = name
	}
	if call.ArgsRaw != "" {
		entry.args.WriteString(call.ArgsRaw)
	}
	if !call.Partial {
		entry.finished = true
		return a.deliver(entry)
	}
	return nil
}

// Pending 交付流结束时仍未收到 end 事件的调用。上游在 turn_ended 前就停发
// 工具事件是常见情况，丢掉它们会让客户端等一个不会到来的工具调用。
func (a *cursorToolAggregator) Pending() []apicompat.ChatToolCall {
	if a == nil {
		return nil
	}
	var out []apicompat.ChatToolCall
	for _, id := range a.order {
		entry := a.calls[id]
		if entry == nil || entry.emitted {
			continue
		}
		if delivered := a.deliver(entry); delivered != nil {
			out = append(out, *delivered)
		}
	}
	return out
}

// HasCalls 报告这一轮是否出现过工具调用（决定 finish_reason / stop_reason）。
func (a *cursorToolAggregator) HasCalls() bool {
	return a != nil && len(a.order) > 0
}

func (a *cursorToolAggregator) deliver(entry *cursorToolAccumulator) *apicompat.ChatToolCall {
	if entry == nil || entry.emitted || strings.TrimSpace(entry.name) == "" {
		return nil
	}
	entry.emitted = true
	args := strings.TrimSpace(entry.args.String())
	if args == "" {
		// 无参工具：客户端要求 arguments 是合法 JSON。
		args = "{}"
	}
	index := a.indexOf(entry.id)
	return &apicompat.ChatToolCall{
		Index:    &index,
		ID:       entry.id,
		Type:     "function",
		Function: apicompat.ChatFunctionCall{Name: entry.name, Arguments: args},
	}
}

func (a *cursorToolAggregator) indexOf(id string) int {
	for i, known := range a.order {
		if known == id {
			return i
		}
	}
	return 0
}

func (a *cursorToolAggregator) lastUnfinishedID() string {
	for i := len(a.order) - 1; i >= 0; i-- {
		if entry := a.calls[a.order[i]]; entry != nil && !entry.finished {
			return entry.id
		}
	}
	return ""
}
