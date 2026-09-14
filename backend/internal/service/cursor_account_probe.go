package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
)

// cursorTestPrompt 是账号连通性探测的默认问题：短、无歧义、不依赖工具。
const cursorTestPrompt = "Reply with OK."

// testCursorAccountConnection 用一次真实的 AgentService/Run 验证 Cursor 凭证。
// 它刻意走与网关相同的路径（凭证 → 模型解析 → 流消费），这样 checksum、
// telemetry id 与 token 任一无效都会在这里暴露，而不是等到用户请求时。
func (s *AccountTestService) testCursorAccountConnection(c *gin.Context, account *Account, modelID, prompt string) error {
	ctx := c.Request.Context()

	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = "default"
	}
	if mapped := strings.TrimSpace(account.GetMappedModel(testModelID)); mapped != "" {
		testModelID = mapped
	}

	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = cursorTestPrompt
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	creds := CursorCredentialsFromAccount(account)
	if creds.AccessToken == "" {
		return s.sendErrorAndEnd(c, "No Cursor access token available")
	}
	if creds.MachineID == "" || creds.MacMachineID == "" {
		return s.sendErrorAndEnd(c, "Cursor machine_id / mac_machine_id are required for the checksum header")
	}

	// 目录拉不到不算失败：解析会回落到内置快照，Run 仍可能成功。
	catalog, err := FetchCursorAvailableModels(ctx, account)
	if err != nil {
		catalog = nil
	}
	upstreamModel := cursor.ResolveRunModel(testModelID, cursor.RunOpts{}, catalog).RunSlug
	if upstreamModel == "" {
		upstreamModel = testModelID
	}

	client := cursor.NewClient(creds)
	client.ProxyURL = cursorAccountProxyURL(account)
	resp, err := client.StreamChat(ctx, []cursor.ChatMessage{{Role: "user", Content: testPrompt}}, upstreamModel)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Cursor request failed: %s", err.Error()))
	}
	defer resp.Body.Close()

	var text strings.Builder
	_, connectErr := cursor.ConsumeAssistantStream(resp.Body, func(ev cursor.StreamEvent) error {
		if ev.Type == "text" {
			text.WriteString(ev.Text)
		}
		return nil
	})
	if connectErr != "" {
		_, _, message := classifyCursorConnectError(connectErr)
		return s.sendErrorAndEnd(c, fmt.Sprintf("Cursor upstream error: %s", message))
	}

	reply := strings.TrimSpace(text.String())
	if reply == "" {
		// 流干净地结束却没有任何内容，说明凭证过了鉴权但这一轮没产出，
		// 报成功会掩盖问题。
		return s.sendErrorAndEnd(c, "Cursor returned an empty response")
	}

	s.sendEvent(c, TestEvent{Type: "content", Text: reply})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
