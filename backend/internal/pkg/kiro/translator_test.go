package kiro

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildRuntimeUserAgentStable(t *testing.T) {
	key := BuildAccountKey("client-id", "", "", "", 1)
	machineID := BuildMachineID("refresh-token", "", "")
	ua1 := BuildRuntimeUserAgent(key, machineID)
	ua2 := BuildRuntimeUserAgent(key, machineID)
	amzUA := BuildRuntimeAmzUserAgent(key, machineID)

	require.Equal(t, ua1, ua2)
	require.Contains(t, ua1, "KiroIDE-")
	require.Contains(t, amzUA, "KiroIDE-")
	require.Contains(t, ua1, "KiroIDE-0.11.")
	require.Contains(t, ua1, "aws-sdk-js/1.0.34")
	require.Contains(t, ua1, "md/nodejs#22.22.0")
	require.Contains(t, ua1, machineID)
	require.Contains(t, amzUA, machineID)
}

func TestBuildKiroPayloadBasic(t *testing.T) {
	SetCachedWebSearchDescription("")
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"You are a test system prompt.",
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"web_search","description":"", "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "arn:aws:codewhisperer:us-east-1:123456789012:profile/test", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	require.Equal(t, "claude-sonnet-4.5", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "AI_EDITOR", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "remote_web_search", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Equal(t, remoteWebSearchDescription, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.description").String())
	require.Equal(t, "hello kiro", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<CRITICAL_OVERRIDE>")
	require.Contains(t, systemContent, "You must never say that you are Kiro")
	require.Contains(t, systemContent, "<identity>")
	require.Contains(t, systemContent, "You are a test system prompt.")
	require.NotContains(t, systemContent, "[Context: Current date is ")
	require.NotContains(t, systemContent, "[Context: Current time is ")
	require.Less(t, strings.Index(systemContent, "<CRITICAL_OVERRIDE>"), strings.Index(systemContent, "You are a test system prompt."))
	require.Equal(t, "I will follow these instructions.", gjson.GetBytes(payload, "conversationState.history.1.assistantResponseMessage.content").String())
}

func TestBuildKiroTemporalContextDefaultIsEmpty(t *testing.T) {
	t.Setenv("SUB2API_KIRO_TIME_CONTEXT", "")

	require.Empty(t, buildKiroTemporalContext())
}

func TestBuildKiroTemporalContextCanUseDateOrPreciseTime(t *testing.T) {
	t.Setenv("SUB2API_KIRO_TIME_CONTEXT", "date")
	require.Contains(t, buildKiroTemporalContext(), "[Context: Current date is ")

	t.Setenv("SUB2API_KIRO_TIME_CONTEXT", "none")
	require.Empty(t, buildKiroTemporalContext())

	t.Setenv("SUB2API_KIRO_TIME_CONTEXT", "precise")
	require.Contains(t, buildKiroTemporalContext(), "[Context: Current time is ")
}

func TestBuildKiroPayloadDefaultTemporalContextStableAcrossSeconds(t *testing.T) {
	t.Setenv("SUB2API_KIRO_TIME_CONTEXT", "")
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"stable sys",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	first, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	second, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	firstSystem := gjson.GetBytes(first.Payload, "conversationState.history.0.userInputMessage.content").String()
	secondSystem := gjson.GetBytes(second.Payload, "conversationState.history.0.userInputMessage.content").String()
	require.Equal(t, firstSystem, secondSystem)
	require.NotContains(t, firstSystem, "[Context: Current time is ")
}

func TestBuildKiroPayloadDerivesStableConversationIDAndIgnoresClientMetadata(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello","additional_kwargs":{"conversationId":"client-conv","continuationId":"client-cont"}}]
	}`)

	first, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	second, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	firstConversationID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	secondConversationID := gjson.GetBytes(second.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstConversationID)
	require.Equal(t, firstConversationID, secondConversationID)
	require.NotEqual(t, "client-conv", firstConversationID)

	firstContinuationID := gjson.GetBytes(first.Payload, "conversationState.agentContinuationId").String()
	secondContinuationID := gjson.GetBytes(second.Payload, "conversationState.agentContinuationId").String()
	require.NotEmpty(t, firstContinuationID)
	require.NotEmpty(t, secondContinuationID)
	require.NotEqual(t, "client-cont", firstContinuationID)
	require.NotEqual(t, firstContinuationID, secondContinuationID)
}

func TestBuildKiroPayloadUsesRandomConversationIDForSyntheticAnchor(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"assistant","content":"prefill"}]
	}`)

	first, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	second, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	firstConversationID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	secondConversationID := gjson.GetBytes(second.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstConversationID)
	require.NotEmpty(t, secondConversationID)
	require.NotEqual(t, firstConversationID, secondConversationID)
}

func TestBuildKiroPayloadTruncatesOversizedHistory(t *testing.T) {
	big := strings.Repeat("lorem ipsum dolor sit amet ", 80)
	messages := []map[string]string{
		{"role": "user", "content": "start the long task"},
	}
	for i := 0; i < 800; i++ {
		messages = append(messages,
			map[string]string{"role": "assistant", "content": "step result: " + big},
			map[string]string{"role": "user", "content": "next: " + big},
		)
	}
	messages = append(messages, map[string]string{"role": "user", "content": "FINAL: summarize everything above"})
	bodyMap := map[string]any{
		"model":    "claude-opus-4-8",
		"system":   "You are a helpful assistant.",
		"messages": messages,
	}
	body, err := json.Marshal(bodyMap)
	require.NoError(t, err)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	require.LessOrEqual(t, len(result.Payload), kiroMaxPayloadBytes)
	require.Contains(t, gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.content").String(), "FINAL: summarize everything above")

	history := gjson.GetBytes(result.Payload, "conversationState.history").Array()
	require.GreaterOrEqual(t, len(history), 3)
	require.Contains(t, history[0].Get("userInputMessage.content").String(), "helpful assistant")

	foundPlaceholder := false
	for _, item := range history {
		if strings.Contains(item.Get("userInputMessage.content").String(), "truncated to fit") {
			foundPlaceholder = true
			break
		}
	}
	require.True(t, foundPlaceholder)
}

func TestBuildKiroPayloadSmallPayloadDoesNotInsertTruncationPlaceholder(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"system":"You are helpful.",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"how are you?"}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	for _, item := range gjson.GetBytes(result.Payload, "conversationState.history").Array() {
		require.NotContains(t, item.Get("userInputMessage.content").String(), "truncated to fit")
	}
}

func TestBuildKiroPayloadDoesNotInsertUserDotBeforeLeadingAssistant(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"assistant","content":"prior assistant"},
			{"role":"user","content":"next user"}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	history := gjson.GetBytes(payload, "conversationState.history").Array()
	foundLeadingAssistant := false
	for _, msg := range history {
		require.NotEqual(t, ".", msg.Get("userInputMessage.content").String())
		if msg.Get("assistantResponseMessage.content").String() == "prior assistant" {
			foundLeadingAssistant = true
		}
	}
	require.True(t, foundLeadingAssistant)
	require.Equal(t, "next user", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
}

func TestBuildKiroPayloadSingleAssistantDoesNotInsertUserDot(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"assistant","content":"only assistant"}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	history := gjson.GetBytes(payload, "conversationState.history").Array()
	foundOnlyAssistant := false
	for _, msg := range history {
		require.NotEqual(t, ".", msg.Get("userInputMessage.content").String())
		if msg.Get("assistantResponseMessage.content").String() == "only assistant" {
			foundOnlyAssistant = true
		}
	}
	require.True(t, foundOnlyAssistant)
	require.Equal(t, "Continue", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
}

func TestBuildKiroPayloadOmitsImagesBeyondRecentHistory(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"first answer"},
			{"role":"user","content":[
				{"type":"text","text":"stale image"},
				{"type":"image","source":{"media_type":"image/png","data":"stale-image"}}
			]},
			{"role":"assistant","content":"second answer"},
			{"role":"user","content":"middle"},
			{"role":"assistant","content":"middle answer"},
			{"role":"user","content":"near"},
			{"role":"tool","content":"ignored separator"},
			{"role":"user","content":[
				{"type":"text","text":"current image"},
				{"type":"image","source":{"media_type":"image/jpeg","data":"current-image"}}
			]}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	staleUser := gjson.GetBytes(payload, "conversationState.history.4.userInputMessage")
	require.False(t, staleUser.Get("images").Exists())
	require.Contains(t, staleUser.Get("content").String(), "stale image")
	require.Contains(t, staleUser.Get("content").String(), "[This message contained 1 image(s), omitted from older conversation history.]")
	require.Equal(t, "current-image", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.images.0.source.bytes").String())
}

func TestBuildKiroPayloadKeepsImagesAtRecentHistoryBoundary(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"first"},
			{"role":"assistant","content":"first answer"},
			{"role":"user","content":[
				{"type":"text","text":"boundary image"},
				{"type":"image","source":{"media_type":"image/png","data":"boundary-image"}}
			]},
			{"role":"assistant","content":"second answer"},
			{"role":"user","content":"middle"},
			{"role":"assistant","content":"middle answer"},
			{"role":"tool","content":"ignored separator"},
			{"role":"user","content":"current"}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	boundaryUser := gjson.GetBytes(payload, "conversationState.history.4.userInputMessage")
	require.Equal(t, "boundary-image", boundaryUser.Get("images.0.source.bytes").String())
	require.NotContains(t, boundaryUser.Get("content").String(), "omitted from older conversation history")
}

func TestBuildKiroPayloadAcceptsImageURLContent(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"inspect this"},
			{"type":"image_url","image_url":{"url":"` + dataURL + `"}}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage")
	require.Equal(t, "inspect this", current.Get("content").String())
	require.Equal(t, "png", current.Get("images.0.format").String())
	require.Equal(t, strings.TrimPrefix(dataURL, "data:image/png;base64,"), current.Get("images.0.source.bytes").String())
}

func TestBuildKiroPayloadKeepsRemoteImageURLAsTextFallback(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"inspect this"},
			{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage")
	require.Equal(t, "inspect this\n[Image: https://example.com/image.png]", current.Get("content").String())
	require.False(t, current.Get("images").Exists())
}

func TestBuildKiroPayloadKeepsRemoteSourceImageURLAsTextFallback(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"url","url":"https://example.com/source.webp"}}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage")
	require.Equal(t, "[Image: https://example.com/source.webp]", current.Get("content").String())
	require.False(t, current.Get("images").Exists())
}

func TestBuildKiroPayloadAddsPDFDocumentTextFallback(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4\n1 0 obj\n<<>>\nstream\nBT (Quarterly Kiro PDF notes) Tj ET\nendstream\nendobj\n%%EOF")
	pdfData := base64.StdEncoding.EncodeToString(pdfBytes)
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"summarize attachment"},
			{"type":"document","name":"notes.pdf","source":{"type":"base64","media_type":"application/pdf","data":"` + pdfData + `"}}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	content := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String()
	require.Contains(t, content, "summarize attachment")
	require.Contains(t, content, "[Attached PDF document: notes.pdf, bytes=")
	require.Contains(t, content, "[Extracted PDF text]")
	require.Contains(t, content, "Quarterly Kiro PDF notes")
	require.Contains(t, content, "[/Extracted PDF text]")
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.images").Exists())
}

func TestBuildKiroPayloadAddsPDFDataURLDocumentTextFallback(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4\n2 0 obj\n<FEFF004B00690072006F00200064006100740061002000550052004C>\nendobj\n%%EOF")
	pdfDataURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdfBytes)
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"document","title":"inline.pdf","data":"` + pdfDataURL + `"}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	content := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String()
	require.Contains(t, content, "[Attached PDF document: inline.pdf, bytes=")
	require.Contains(t, content, "Kiro data URL")
}

func TestBuildKiroPayloadAcceptsAnthropicBase64SourceImage(t *testing.T) {
	const imageBytes = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"inspect this"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + imageBytes + `"}}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage")
	require.Equal(t, "inspect this", current.Get("content").String())
	require.Equal(t, "png", current.Get("images.0.format").String())
	require.Equal(t, imageBytes, current.Get("images.0.source.bytes").String())
}

func TestBuildKiroPayloadAttachesToolResultImageToCurrentMessage(t *testing.T) {
	const dataURL = "data:image/jpeg;base64,aW1hZ2UtYnl0ZXM="
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"read screenshot"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"read_image","input":{"path":"shot.jpg"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":[
				{"type":"image_url","image_url":{"url":"` + dataURL + `"}}
			]}]}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage")
	require.Equal(t, "Tool results provided.", current.Get("content").String())
	require.Equal(t, "jpeg", current.Get("images.0.format").String())
	require.Equal(t, "aW1hZ2UtYnl0ZXM=", current.Get("images.0.source.bytes").String())
	require.Equal(t, kiroToolResultImageText, current.Get("userInputMessageContext.toolResults.0.content.0.text").String())
	require.Equal(t, "tool_1", current.Get("userInputMessageContext.toolResults.0.toolUseId").String())
}

func TestBuildKiroPayloadWebSearchUsesCachedDescription(t *testing.T) {
	SetCachedWebSearchDescription("cached web search description")
	t.Cleanup(func() { SetCachedWebSearchDescription("") })

	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"web_search","description":"caller description", "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	require.Equal(t, "remote_web_search", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Equal(t, "cached web search description", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.description").String())
}

func TestBuildKiroPayloadAppendsChunkedWritePolicyToWriteAndEditTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[
			{"name":"Write","description":"write file", "input_schema":{"type":"object"}},
			{"name":"Edit","description":"edit file", "input_schema":{"type":"object"}},
			{"name":"read_file","description":"read file", "input_schema":{"type":"object"}}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	tools := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, tools, 3)
	require.Contains(t, tools[0].Get("toolSpecification.description").String(), writeToolDescriptionSuffix)
	require.Contains(t, tools[1].Get("toolSpecification.description").String(), editToolDescriptionSuffix)
	require.NotContains(t, tools[2].Get("toolSpecification.description").String(), "chunks of no more than 50 lines")
}

func TestBuildKiroPayloadChunkedWritePolicyIsIdempotentAndTruncated(t *testing.T) {
	longDescription := strings.Repeat("long description ", 900) + "\n" + writeToolDescriptionSuffix
	body := []byte(fmt.Sprintf(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"name":"write_to_file","description":%q, "input_schema":{"type":"object"}}]
	}`, longDescription))

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	description := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.description").String()
	require.LessOrEqual(t, len(description), kiroMaxToolDescLen)
	require.Equal(t, 1, strings.Count(description, writeToolDescriptionSuffix))
	require.Contains(t, description, writeToolDescriptionSuffix)
}

func TestBuildKiroPayloadInjectsChunkedWritePolicyIntoSystemPrompt(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":"Follow user instructions.",
		"thinking":{"type":"enabled","budget_tokens":2048},
		"messages":[{"role":"user","content":"hello"}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>enabled</thinking_mode>")
	require.Less(t, strings.Index(systemContent, "<thinking_mode>enabled</thinking_mode>"), strings.Index(systemContent, "<CRITICAL_OVERRIDE>"))
	require.Less(t, strings.Index(systemContent, "<CRITICAL_OVERRIDE>"), strings.Index(systemContent, "Follow user instructions."))
	require.Contains(t, systemContent, "Follow user instructions.")
	require.Contains(t, systemContent, systemChunkedWritePolicy)
	require.Equal(t, 1, strings.Count(systemContent, systemChunkedWritePolicy))
}

func TestBuildKiroPayloadInjectsThinkingIntoHistory(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"thinking":{"type":"enabled","budget_tokens":2048},
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	headers := http.Header{}
	headers.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", headers)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	require.Equal(t, "hello kiro", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>2048</max_thinking_length>")
	require.NotContains(t, systemContent, "[Context: Current time is ")
	require.Equal(t, "I will follow these instructions.", gjson.GetBytes(payload, "conversationState.history.1.assistantResponseMessage.content").String())
}

func TestBuildKiroPayloadInjectsAdaptiveThinkingForOpus46ThinkingModel(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6-thinking",
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-opus-4.6", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>adaptive</thinking_mode>\n<thinking_effort>high</thinking_effort>")
	require.NotContains(t, systemContent, "[Context: Current time is ")
}

func TestBuildKiroPayloadClampsSamplingAndMaxTokens(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":999999,
		"temperature":-0.3,
		"top_p":1.4,
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	require.Equal(t, int64(128000), gjson.GetBytes(result.Payload, "inferenceConfig.maxTokens").Int())
	require.Equal(t, 0.0, gjson.GetBytes(result.Payload, "inferenceConfig.temperature").Float())
	require.False(t, gjson.GetBytes(result.Payload, "inferenceConfig.topP").Exists(),
		"temperature and top_p should not both be forwarded to Kiro")
	require.Equal(t, 128000, result.Context.MaxOutputTokens)
}

func TestBuildKiroPayloadForcedToolChoiceDisablesThinkingPrompt(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"thinking":{"type":"enabled","budget_tokens":4096},
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"read_file"}
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
	require.NotContains(t, systemContent, "<thinking_mode>")
	require.False(t, result.Context.ThinkingEnabled)
	require.Contains(t, systemContent, "MUST use the tool named 'readFile'")
}

func TestBuildKiroPayloadOptionsForwardCLIWireFields(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"system":"You are Claude Code.\n<env>\nWorking directory: /Users/sven/project\nPlatform: darwin\n</env>",
		"output_config":{"effort":"xhigh"},
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)

	result, err := BuildKiroPayloadWithOptions(body, "claude-opus-4.8", "arn:aws:test", nil, KiroPayloadOptions{
		Origin:                     "KIRO_CLI",
		UseNativeEffort:            true,
		AttachEnvState:             true,
		InjectThinkingSystemPrompt: false,
	})
	require.NoError(t, err)
	payload := result.Payload

	require.Equal(t, "KIRO_CLI", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "xhigh", gjson.GetBytes(payload, "additionalModelRequestFields.output_config.effort").String())
	require.Equal(t, "macos", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.envState.operatingSystem").String())
	require.Equal(t, "/Users/sven/project", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.envState.currentWorkingDirectory").String())
	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.NotContains(t, systemContent, "<thinking_mode>")
	require.NotContains(t, systemContent, "<thinking_effort>")

	wire := string(payload)
	require.Less(t, strings.Index(wire, `"profileArn"`), strings.Index(wire, `"additionalModelRequestFields"`))
	require.Less(t, strings.Index(wire, `"envState"`), strings.Index(wire, `"tools"`))
}

func TestBuildKiroPayloadOptionsSupportsSonnet5NativeEffort(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-5",
		"output_config":{"effort":"max"},
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithOptions(body, "claude-sonnet-5", "", nil, KiroPayloadOptions{
		UseNativeEffort:            true,
		InjectThinkingSystemPrompt: false,
	})
	require.NoError(t, err)

	require.Equal(t, "claude-sonnet-5", gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.modelId").String())
	require.Equal(t, "max", gjson.GetBytes(result.Payload, "additionalModelRequestFields.output_config.effort").String())
}

func TestBuildKiroPayloadWithContextKeepsDefaultWireWithoutEnvStateOrNativeEffort(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"system":"<env>\nWorking directory: /Users/sven/project\nPlatform: darwin\n</env>",
		"output_config":{"effort":"xhigh"},
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	require.False(t, gjson.GetBytes(payload, "additionalModelRequestFields").Exists())
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.envState").Exists())
	require.Equal(t, "AI_EDITOR", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.origin").String())
}

// 客户端未请求 thinking 但模型是 Opus 4.7/4.8 时,解析器仍需开启 <thinking> tag 抽取,
// 否则上游 CoT 文本会原样泄漏到 assistant 正文。
func TestBuildKiroPayloadEnablesImplicitThinkingTagStrippingForOpus47And48(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		mapped  string
		wantStr bool
	}{
		{name: "opus-4.7 plain", model: "claude-opus-4-7", mapped: "claude-opus-4.7", wantStr: true},
		{name: "opus-4.8 plain", model: "claude-opus-4-8", mapped: "claude-opus-4.8", wantStr: true},
		{name: "sonnet-4.5 plain stays disabled", model: "claude-sonnet-4-5", mapped: "claude-sonnet-4.5", wantStr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"` + tc.model + `","messages":[{"role":"user","content":"hi"}]}`)
			result, err := BuildKiroPayloadWithContext(body, tc.mapped, "", "AI_EDITOR", nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantStr, result.Context.ThinkingEnabled,
				"ThinkingEnabled mismatch for model %q (mapped %q)", tc.model, tc.mapped)

			// 隐式开启不应在 system prompt 注入 <thinking_mode> 前缀,避免改变上游请求语义
			systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
			require.NotContains(t, systemContent, "<thinking_mode>",
				"implicit tag stripping must not inject <thinking_mode> prefix")
		})
	}
}

// kiroBuiltinIdentityPrompt 中的 {{identity}} 占位符必须被实际身份替换,
// 默认回退到 "Claude",避免模型直接复读模板字面量。
func TestBuildKiroPayloadRendersBuiltinIdentityPlaceholder(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hi"}]
	}`)
	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
	require.NotContains(t, systemContent, "{{identity}}",
		"placeholder must be rendered before sending to upstream")
	require.Contains(t, systemContent, "You are Claude,",
		"default identity should fall back to 'Claude'")
}

func TestBuildKiroPayloadAddsStructuredOutputTool(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"return status"}],
		"response_format":{
			"type":"json_schema",
			"json_schema":{
				"name":"result",
				"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}
			}
		}
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	require.Equal(t, "result", result.Context.StructuredOutputToolName)
	require.Equal(t, "result", gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Contains(t, gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.content").String(), "MUST call the 'result' tool")
	systemContent := gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "respond by calling the 'result' tool")
}

func TestBuildKiroPayloadInjectsThinkingForThinkingAliasModel(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5-20250929-thinking",
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>20000</max_thinking_length>")
}

func TestBuildKiroPayloadHeaderOnlyThinking(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello kiro"}]
	}`)

	headers := http.Header{}
	headers.Set("Anthropic-Beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14")

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", headers)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>16000</max_thinking_length>")
}

func TestBuildKiroPayloadInjectsToolChoiceHints(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"web_search","description":"search", "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"web_search"}
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "MUST use the tool named 'remote_web_search'")
}

func TestBuildKiroPayloadInjectsRequiredToolChoiceHint(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"web_search","description":"search", "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}],
		"tool_choice":{"type":"any"}
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "MUST use at least one of the available tools")
}

func TestBuildKiroPayloadToolChoiceNoneOmitsTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello kiro"}],
		"tools":[{"name":"web_search","description":"search", "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}],
		"tool_choice":{"type":"none"}
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "Do not use any tools. Respond with text only.")
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Exists())
}

func TestParseNonStreamingEventStream(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "hello from kiro",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens":  12,
				"outputTokens":         7,
				"cacheReadInputTokens": 3,
				"totalTokens":          22,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{
			"stop_reason": "end_turn",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 22, result.Usage.TotalTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)

	var response map[string]any
	require.NoError(t, json.Unmarshal(result.ResponseBody, &response))
	require.Equal(t, "end_turn", response["stop_reason"])
	content, _ := response["content"].([]any)
	require.NotEmpty(t, content)
	first, _ := content[0].(map[string]any)
	require.Equal(t, "text", first["type"])
	firstText, ok := first["text"].(string)
	require.True(t, ok)
	require.True(t, strings.Contains(firstText, "hello from kiro"))
}

func TestParseNonStreamingEventStreamAppliesStopSequences(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "hello STOP hidden",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{
		StopSequences: []string{"STOP"},
	})
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", gjson.GetBytes(result.ResponseBody, "stop_reason").String())
	require.Equal(t, "STOP", gjson.GetBytes(result.ResponseBody, "stop_sequence").String())
	require.Equal(t, "hello ", gjson.GetBytes(result.ResponseBody, "content.0.text").String())
}

func TestParseNonStreamingEventStreamReturnsStructuredOutputToolAsText(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_structured",
			"name":      "result",
			"input":     map[string]any{"ok": true},
			"stop":      true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{
		StructuredOutputToolName: "result",
	})
	require.NoError(t, err)
	require.Equal(t, "end_turn", gjson.GetBytes(result.ResponseBody, "stop_reason").String())
	require.Equal(t, "text", gjson.GetBytes(result.ResponseBody, "content.0.type").String())
	require.JSONEq(t, `{"ok":true}`, gjson.GetBytes(result.ResponseBody, "content.0.text").String())
	require.False(t, gjson.GetBytes(result.ResponseBody, "content.1").Exists())
}

func TestParseNonStreamingEventStreamCapturesKiroCredits(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "hello from kiro",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 12,
				"outputTokens":        7,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": 0.12},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": "0.05"},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.False(t, gjson.GetBytes(result.ResponseBody, "usage.kiro_credits").Exists())
	require.False(t, gjson.GetBytes(result.ResponseBody, "usage._sub2api_kiro_credits").Exists())
}

func TestUpdateUsageFromEventCapturesKiroCreditsAliases(t *testing.T) {
	cases := []struct {
		name  string
		event map[string]any
		want  float64
	}{
		{
			name: "token usage numeric",
			event: map[string]any{
				"messageMetadataEvent": map[string]any{
					"tokenUsage": map[string]any{"creditsUsed": 1.25},
				},
			},
			want: 1.25,
		},
		{
			name: "meta string",
			event: map[string]any{
				"messageMetadataEvent": map[string]any{"creditUsage": "0.071"},
			},
			want: 0.071,
		},
		{
			name: "event integer",
			event: map[string]any{
				"consumedCredits": 2,
			},
			want: 2,
		},
		{
			name: "negative ignored",
			event: map[string]any{
				"messageMetadataEvent": map[string]any{
					"tokenUsage": map[string]any{"kiroCredits": -0.1},
				},
			},
			want: 0,
		},
		{
			name: "nan ignored",
			event: map[string]any{
				"messageMetadataEvent": map[string]any{"credits": "NaN"},
			},
			want: 0,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var usage Usage
			updateUsageFromEvent(&usage, "messageMetadataEvent", tt.event)
			require.InDelta(t, tt.want, usage.KiroCredits, 0.000001)
		})
	}
}

func TestUpdateUsageFromEventAccumulatesMeteringCredits(t *testing.T) {
	var usage Usage

	updateUsageFromEvent(&usage, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": 0.12},
	})
	updateUsageFromEvent(&usage, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": "0.05"},
	})
	updateUsageFromEvent(&usage, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": -1},
	})

	require.InDelta(t, 0.17, usage.KiroCredits, 0.000001)
}

func TestExtractThinkingBlocksIgnoresLiteralTags(t *testing.T) {
	content := strings.Join([]string{
		"Use `<thinking>` literally.",
		"Quote \"<thinking>\" and '</thinking>'.",
		"> <thinking>quoted</thinking>",
		"```",
		"<thinking>code</thinking>",
		"```",
	}, "\n")

	blocks := extractThinkingBlocks(content)
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0]["type"])
	require.Equal(t, content, blocks[0]["text"])
}

func TestExtractThinkingBlocksParsesRealTags(t *testing.T) {
	blocks := extractThinkingBlocks("<thinking>\nreason</thinking>\n\nfinal text")

	require.Len(t, blocks, 2)
	require.Equal(t, "thinking", blocks[0]["type"])
	require.Equal(t, "reason", blocks[0]["thinking"])
	require.NotEmpty(t, blocks[0]["signature"])
	oldSHA := sha256.Sum256([]byte("reason"))
	require.NotEqual(t, base64.StdEncoding.EncodeToString(oldSHA[:]), blocks[0]["signature"])
	require.Equal(t, "text", blocks[1]["type"])
	require.Equal(t, "final text", blocks[1]["text"])
}

func TestParseNonStreamingEventStreamPureThinkingFallback(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "<thinking>reason only</thinking>",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Contains(t, err.Error(), "no deliverable assistant output")
}

func TestParseNonStreamingEventStreamThinkingWithTextKeepsEndTurn(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "<thinking>reason</thinking>\n\nfinal",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", gjson.GetBytes(result.ResponseBody, "stop_reason").String())
	require.Equal(t, "thinking", gjson.GetBytes(result.ResponseBody, "content.0.type").String())
	require.Equal(t, "text", gjson.GetBytes(result.ResponseBody, "content.1.type").String())
	require.Equal(t, "final", gjson.GetBytes(result.ResponseBody, "content.1.text").String())
}

func TestParseNonStreamingEventStreamThinkingWithToolUseKeepsToolUseStopReason(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "<thinking>reason only</thinking>",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_search",
			"name":      "remote_web_search",
			"input":     `{"query":"golang"}`,
			"stop":      true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", gjson.GetBytes(result.ResponseBody, "stop_reason").String())
	require.Equal(t, "thinking", gjson.GetBytes(result.ResponseBody, "content.0.type").String())
	require.Equal(t, "tool_use", gjson.GetBytes(result.ResponseBody, "content.1.type").String())
	require.False(t, gjson.GetBytes(result.ResponseBody, "content.2.text").Exists())
}

func TestParseNonStreamingEventStreamExtractsEmbeddedToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `Before [Called web_search with args: {"query":"golang concurrency"}] After`,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.NotContains(t, string(result.ResponseBody), "[Called")

	content := gjson.GetBytes(result.ResponseBody, "content").Array()
	require.Len(t, content, 2)
	require.Equal(t, "text", content[0].Get("type").String())
	require.Equal(t, "Before  After", content[0].Get("text").String())
	require.Equal(t, "tool_use", content[1].Get("type").String())
	require.Equal(t, "remote_web_search", content[1].Get("name").String())
	require.Equal(t, "golang concurrency", content[1].Get("input.query").String())
}

func TestParseNonStreamingEventStreamExtractsXMLInvokeToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Before <invoke name=\"Bash\">\n<parameter name=\"command\">cd /tmp\nls -la</parameter>\n<parameter name=\"description\">List files</parameter>\n</invoke> After",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.NotContains(t, string(result.ResponseBody), "<invoke")
	require.NotContains(t, string(result.ResponseBody), "<parameter")

	content := gjson.GetBytes(result.ResponseBody, "content").Array()
	require.Len(t, content, 2)
	require.Equal(t, "text", content[0].Get("type").String())
	require.Equal(t, "Before  After", content[0].Get("text").String())
	require.Equal(t, "tool_use", content[1].Get("type").String())
	require.Equal(t, "Bash", content[1].Get("name").String())
	require.Equal(t, "cd /tmp\nls -la", content[1].Get("input.command").String())
	require.Equal(t, "List files", content[1].Get("input.description").String())
}

func TestParseNonStreamingEventStreamExtractsEscapedXMLInvokeToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `&lt;invoke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;echo &amp;&amp; pwd&lt;/parameter&gt;&lt;/invoke&gt;`,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.NotContains(t, string(result.ResponseBody), "&lt;invoke")

	content := gjson.GetBytes(result.ResponseBody, "content").Array()
	require.Len(t, content, 1)
	require.Equal(t, "tool_use", content[0].Get("type").String())
	require.Equal(t, "Bash", content[0].Get("name").String())
	require.Equal(t, "echo && pwd", content[0].Get("input.command").String())
}

func TestParseNonStreamingEventStreamDeduplicatesToolUsesByContent(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"toolUses": []map[string]any{
				{
					"toolUseId": "toolu_first",
					"name":      "remote_web_search",
					"input": map[string]any{
						"query": "golang",
					},
				},
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_second",
			"name":      "remote_web_search",
			"input": map[string]any{
				"query": "golang",
			},
			"stop": true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)

	content := gjson.GetBytes(result.ResponseBody, "content").Array()
	toolUseCount := 0
	for _, block := range content {
		if block.Get("type").String() == "tool_use" {
			toolUseCount++
		}
	}
	require.Equal(t, 1, toolUseCount)
}

func TestParseNonStreamingEventStreamSkipsTruncatedToolUse(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_truncated",
			"name":      "write_to_file",
			"input":     `{"path":"main.go","content":"package main`,
			"stop":      true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	content := gjson.GetBytes(result.ResponseBody, "content").Array()
	require.Len(t, content, 1)
	require.Equal(t, "text", content[0].Get("type").String())
	require.NotContains(t, string(result.ResponseBody), `"type":"tool_use"`)
}

func TestParseNonStreamingEventStreamNormalizesAskUserQuestionInput(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_question",
			"name":      "AskUserQuestion",
			"input":     `{"questions":"确认是否继续？"}`,
			"stop":      true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.Equal(t, "AskUserQuestion", gjson.GetBytes(result.ResponseBody, "content.0.name").String())
	require.Equal(t, "确认是否继续？", gjson.GetBytes(result.ResponseBody, "content.0.input.questions.0.question").String())
	require.False(t, gjson.GetBytes(result.ResponseBody, "content.0.input.questions").IsObject())
}

func TestParseNonStreamingEventStreamAcceptsKiroToolUseFieldVariants(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"tool_use_id": "toolu_variant",
			"tool_name":   "read_file",
			"input":       `{"path":"/tmp/a.txt"}`,
			"done":        true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.Equal(t, "toolu_variant", gjson.GetBytes(result.ResponseBody, "content.0.id").String())
	require.Equal(t, "read_file", gjson.GetBytes(result.ResponseBody, "content.0.name").String())
	require.Equal(t, "/tmp/a.txt", gjson.GetBytes(result.ResponseBody, "content.0.input.path").String())
}

func TestParseNonStreamingEventStreamDropsIncompleteEmbeddedToolTail(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `Before [Called web_search with args: {"query":"golang`,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)
	require.NotContains(t, string(result.ResponseBody), "[Called")
	require.Equal(t, "Before ", gjson.GetBytes(result.ResponseBody, "content.0.text").String())
}

func TestParseNonStreamingEventStreamThinkingOnlyResponse(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{
			"text": "I should think first.",
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Contains(t, err.Error(), "no deliverable assistant output")
}

func TestStreamEventStreamAsAnthropicThinkingOnlyDoesNotReleasePartialOutput(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{
			"text": "I should think first.",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(
		context.Background(),
		stream,
		&out,
		"claude-sonnet-4-5",
		9,
		KiroRequestContext{ThinkingEnabled: true},
	)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Contains(t, err.Error(), "no deliverable assistant output")
	require.Empty(t, out.String(), "thinking-only empty stream must not leak partial SSE frames")
}

func TestParseNonStreamingEventStreamMergesManyReasoningFragments(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, frag := range []string{"I ", "need ", "to ", "think"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
			"reasoningContentEvent": map[string]any{"text": frag},
		}))
	}
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "answer"},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	// 连续 reasoning 片段合并为单个 thinking 块，且内部不混入字面标签
	require.Equal(t, "thinking", gjson.GetBytes(result.ResponseBody, "content.0.type").String())
	require.Equal(t, "I need to think", gjson.GetBytes(result.ResponseBody, "content.0.thinking").String())
	require.Equal(t, "text", gjson.GetBytes(result.ResponseBody, "content.1.type").String())
	require.Equal(t, "answer", gjson.GetBytes(result.ResponseBody, "content.1.text").String())
	require.False(t, gjson.GetBytes(result.ResponseBody, "content.2").Exists())
}

func TestStreamEventStreamAsAnthropicExtractsEmbeddedToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `Before [Called web_search with args: {"query":"gol`,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": `ang"}] After`,
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)

	output := out.String()
	require.NotContains(t, output, "[Called")
	require.Contains(t, output, `"text":"Before "`)
	require.Contains(t, output, `"text":" After"`)
	require.Contains(t, output, `"name":"remote_web_search"`)
	require.Contains(t, output, `"partial_json":"{\"query\":\"golang\"}"`)
}

func TestStreamEventStreamAsAnthropicExtractsSplitXMLInvokeToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Before <invoke name=\"Bash\">\n<parameter name=\"command\">cd /tmp\n",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "ls -la</parameter>\n<parameter name=\"description\">List files</parameter>\n</invoke> After",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)

	output := out.String()
	require.NotContains(t, output, "<invoke")
	require.NotContains(t, output, "<parameter")
	require.Contains(t, output, `"text":"Before "`)
	require.Contains(t, output, `"text":" After"`)
	require.Contains(t, output, `"name":"Bash"`)
	var partialJSON string
	for _, block := range strings.Split(output, "\n\n") {
		payload := strings.TrimPrefix(strings.TrimSpace(block), "event: content_block_delta\ndata: ")
		if payload == strings.TrimSpace(block) {
			continue
		}
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			partialJSON = gjson.Get(payload, "delta.partial_json").String()
			break
		}
	}
	require.NotEmpty(t, partialJSON)
	require.Equal(t, "cd /tmp\nls -la", gjson.Get(partialJSON, "command").String())
	require.Equal(t, "List files", gjson.Get(partialJSON, "description").String())
}

func TestStreamEventStreamAsAnthropicExtractsSplitEscapedXMLInvokeToolCall(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Before &lt;invoke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;cd /tmp\n",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "ls -la&lt;/parameter&gt;&lt;parameter name=&quot;description&quot;&gt;List files&lt;/parameter&gt;&lt;/invoke&gt; After",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)

	output := out.String()
	require.NotContains(t, output, "&lt;invoke")
	require.NotContains(t, output, "&lt;parameter")
	require.Contains(t, output, `"text":"Before "`)
	require.Contains(t, output, `"text":" After"`)
	require.Contains(t, output, `"name":"Bash"`)
	var partialJSON string
	for _, block := range strings.Split(output, "\n\n") {
		payload := strings.TrimPrefix(strings.TrimSpace(block), "event: content_block_delta\ndata: ")
		if payload == strings.TrimSpace(block) {
			continue
		}
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			partialJSON = gjson.Get(payload, "delta.partial_json").String()
			break
		}
	}
	require.NotEmpty(t, partialJSON)
	require.Equal(t, "cd /tmp\nls -la", gjson.Get(partialJSON, "command").String())
	require.Equal(t, "List files", gjson.Get(partialJSON, "description").String())
}

func TestStreamEventStreamAsAnthropicSkipsLeadingWhitespaceOnlyChunk(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "\n",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Hello from Kiro",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"text":"Hello from Kiro"`)
	require.NotContains(t, output, `"delta":{"text":"\n","type":"text_delta"}`)
	require.NotContains(t, output, `"delta":{"text":"","type":"text_delta"}`)
}

func TestStreamEventStreamAsAnthropicSkipsTrailingWhitespaceOnlyChunk(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Hello from Kiro",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "\n",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "\n\n",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"text":"Hello from Kiro"`)
	require.NotContains(t, output, `"text":"\n"`)
	require.NotContains(t, output, `"text":"\n\n"`)
}

func TestStreamEventStreamAsAnthropicAppliesStopSequencesAcrossChunks(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "hello ST",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "OP hidden",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{
		StopSequences: []string{"STOP"},
	})
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"text":"hello "`)
	require.NotContains(t, output, "hidden")
	require.Contains(t, output, `"stop_reason":"stop_sequence"`)
	require.Contains(t, output, `"stop_sequence":"STOP"`)
}

func TestStreamEventStreamAsAnthropicAllowsImmediateStopSequence(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "STOP hidden",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{
		StopSequences: []string{"STOP"},
	})
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"type":"message_start"`)
	require.Contains(t, output, `"type":"content_block_start"`)
	require.NotContains(t, output, "hidden")
	require.Contains(t, output, `"stop_reason":"stop_sequence"`)
	require.Contains(t, output, `"stop_sequence":"STOP"`)
}

func TestStreamEventStreamAsAnthropicDelaysMessageStartUntilContent(t *testing.T) {
	pr, pw := io.Pipe()
	var out bytes.Buffer
	errCh := make(chan error, 1)

	go func() {
		_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), pr, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
		errCh <- err
	}()

	_, err := pw.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 9,
			},
		},
	}))
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	require.Empty(t, out.String())

	_, err = pw.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_delayed",
			"name":      "remote_web_search",
			"input": map[string]any{
				"query": "golang",
			},
			"stop": true,
		},
	}))
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	require.NoError(t, <-errCh)

	output := out.String()
	require.Contains(t, output, "event: message_start")
	require.Contains(t, output, `"name":"remote_web_search"`)
	require.Contains(t, output, `"partial_json":"{\"query\":\"golang\"}`)
	messageStartIdx := strings.Index(output, "event: message_start")
	toolUseIdx := strings.Index(output, `"name":"remote_web_search"`)
	require.NotEqual(t, -1, messageStartIdx)
	require.NotEqual(t, -1, toolUseIdx)
	require.Less(t, messageStartIdx, toolUseIdx)
}

func TestClaudeCompatibleGeneratedIDs(t *testing.T) {
	requestID := NewClaudeRequestID()
	require.Regexp(t, regexp.MustCompile(`^req_01[0-9A-Za-z]{25}$`), requestID)

	response := buildClaudeResponse(
		"hello",
		nil,
		"claude-sonnet-4-5",
		Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		"end_turn",
		KiroRequestContext{},
	)
	require.Regexp(t, regexp.MustCompile(`^msg_01[0-9A-Za-z]{25}$`), gjson.GetBytes(response, "id").String())

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 1, KiroRequestContext{})
	require.NoError(t, err)

	matches := regexp.MustCompile(`"id":"(msg_01[0-9A-Za-z]{25})"`).FindStringSubmatch(out.String())
	require.Len(t, matches, 2)
}

func TestStreamEventStreamAsAnthropicStreamsToolUseFragments(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_stream",
			"name":      "write_file",
			"input":     `{"path":"/tmp/a.txt",`,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_stream",
			"name":      "write_file",
			"input":     `"content":"hello"}`,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_stream",
			"name":      "write_file",
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)

	output := out.String()
	require.Equal(t, 1, strings.Count(output, `"id":"toolu_stream"`))
	require.Contains(t, output, `"partial_json":"{\"path\":\"/tmp/a.txt\","`)
	require.Contains(t, output, `"partial_json":"\"content\":\"hello\"}"`)
	require.Contains(t, output, `event: content_block_stop`)
}

func TestStreamEventStreamAsAnthropicStreamsIncompleteToolUseFragment(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_incomplete",
			"name":      "write_file",
			"input":     `{"path":`,
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.Contains(t, out.String(), `"partial_json":"{\"path\":"`)
}

func TestStreamEventStreamAsAnthropicStopsPreviousToolWhenIDChanges(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_one",
			"name":      "write_file",
			"input":     `{"path":"a"}`,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_two",
			"name":      "read_file",
			"input":     `{"path":"b"}`,
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)

	output := out.String()
	firstStart := strings.Index(output, `"id":"toolu_one"`)
	firstStop := strings.Index(output[firstStart:], `event: content_block_stop`)
	secondStart := strings.Index(output, `"id":"toolu_two"`)
	require.NotEqual(t, -1, firstStart)
	require.NotEqual(t, -1, firstStop)
	require.NotEqual(t, -1, secondStart)
	require.Less(t, firstStart+firstStop, secondStart)
}

func TestStreamEventStreamAsAnthropicClosesToolBeforeText(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_before_text",
			"name":      "write_file",
			"input":     `{"path":"a"}`,
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "done",
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)

	output := out.String()
	toolStart := strings.Index(output, `"id":"toolu_before_text"`)
	toolStop := strings.Index(output[toolStart:], `event: content_block_stop`)
	textDelta := strings.Index(output, `"text":"done"`)
	require.NotEqual(t, -1, toolStart)
	require.NotEqual(t, -1, toolStop)
	require.NotEqual(t, -1, textDelta)
	require.Less(t, toolStart+toolStop, textDelta)
}

func TestStreamEventStreamAsAnthropicClosesThinkingBeforeTool(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{
			"text": "thinking first",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_after_thinking",
			"name":      "write_file",
			"input":     `{"path":"a"}`,
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)

	output := out.String()
	thinkingDelta := strings.Index(output, `"thinking":"thinking first"`)
	toolStart := strings.Index(output, `"id":"toolu_after_thinking"`)
	require.NotEqual(t, -1, thinkingDelta)
	thinkingStop := strings.Index(output[thinkingDelta:], `event: content_block_stop`)
	require.NotEqual(t, -1, thinkingStop)
	require.NotEqual(t, -1, toolStart)
	require.Less(t, thinkingDelta+thinkingStop, toolStart)
}

func TestStreamEventStreamAsAnthropicClosesOpenToolAtEOF(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_eof",
			"name":      "write_file",
			"input":     `{"path":"a"}`,
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "tool_use", result.StopReason)
	require.Contains(t, out.String(), `event: content_block_stop`)
}

func TestStreamEventStreamAsAnthropicStreamsToolUseMapInput(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_map",
			"name":      "remote_web_search",
			"input": map[string]any{
				"query": "golang",
			},
			"stop": true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Contains(t, out.String(), `"partial_json":"{\"query\":\"golang\"}"`)
}

func TestStreamEventStreamAsAnthropicNormalizesAskUserQuestionInput(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_question",
			"name":      "ask_user_question",
			"input": map[string]any{
				"questions": []any{
					"第一项？",
					map[string]any{"text": "第二项？", "options": []any{"继续", "停止"}},
				},
			},
			"stop": true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)

	var partialJSON string
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			partialJSON = gjson.Get(payload, "delta.partial_json").String()
			break
		}
	}
	require.NotEmpty(t, partialJSON)
	require.Equal(t, "第一项？", gjson.Get(partialJSON, "questions.0.question").String())
	require.Equal(t, "第二项？", gjson.Get(partialJSON, "questions.1.question").String())
	require.True(t, gjson.Get(partialJSON, "questions.1.options").IsArray())
}

func TestStreamEventStreamAsAnthropicNormalizesAskUserQuestionJSONString(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_question",
			"name":      "AskUserQuestion",
			"input":     `{"question":"确认是否继续？"}`,
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)

	var partialJSON string
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			partialJSON = gjson.Get(payload, "delta.partial_json").String()
			break
		}
	}
	require.NotEmpty(t, partialJSON)
	require.Equal(t, "确认是否继续？", gjson.Get(partialJSON, "questions.0.question").String())
}

func TestStreamEventStreamAsAnthropicAcceptsGeneratedToolIDUpgrade(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolName": "read_file",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseID": "toolu_real",
			"toolName":  "read_file",
			"input":     `{"path":"/tmp/a.txt"}`,
			"isStop":    true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(out.String(), `"type":"content_block_start"`))
	require.Contains(t, out.String(), `"id":"toolu_real"`)
	require.Contains(t, out.String(), `"name":"read_file"`)
	require.Contains(t, out.String(), `"partial_json":"{\"path\":\"/tmp/a.txt\"}"`)
}

func TestStreamEventStreamAsAnthropicIgnoresPingFrames(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "ping", map[string]any{}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "Hello after ping",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)
	require.Contains(t, out.String(), `"text":"Hello after ping"`)
}

func TestStreamEventStreamAsAnthropicTreatsKiroContentAsDeltas(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, fragment := range []string{"I'm ", "starting"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{
				"content": fragment,
			},
		}))
	}

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-7", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Equal(t, 1, strings.Count(output, `event: content_block_start`))
	require.Contains(t, output, `"text":"I'm "`)
	require.Contains(t, output, `"text":"starting"`)
	require.NotContains(t, output, `"text":"'m"`)
}

func TestStreamEventStreamAsAnthropicCapturesKiroCredits(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	var out bytes.Buffer

	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello world"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        5,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": 0.12},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": 0.05},
	}))

	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.Contains(t, out.String(), "_sub2api_kiro_credits")

	var delta map[string]any
	for _, line := range strings.Split(out.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || !strings.Contains(data, "_sub2api_kiro_credits") {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(data), &delta))
		break
	}
	require.NotNil(t, delta)
	usageMap, ok := delta["usage"].(map[string]any)
	require.True(t, ok)
	require.InDelta(t, 0.17, usageMap["_sub2api_kiro_credits"].(float64), 0.000001)
}

func TestStreamEventStreamAsAnthropicSkipsConsecutiveDuplicateContent(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, fragment := range []string{"hello", "hello", " world"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{
				"content": fragment,
			},
		}))
	}

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-7", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Equal(t, 1, strings.Count(output, `"text":"hello"`))
	require.Contains(t, output, `"text":" world"`)
}

func TestStreamEventStreamAsAnthropicDoesNotCreateHalfWordFromKiroDelta(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, fragment := range []string{"I", "'m starting"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{
				"content": fragment,
			},
		}))
	}

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-7", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"text":"I"`)
	require.Contains(t, output, `"text":"'m starting"`)
}

func TestStreamEventStreamAsAnthropicSynthesizesTerminalWhenUpstreamOmitsIt(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "partial answer",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 9, KiroRequestContext{
		RequireTerminalEvent: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "end_turn", result.StopReason)
	require.Contains(t, out.String(), "partial answer")
	require.Contains(t, out.String(), "event: message_stop")
	require.NotContains(t, out.String(), "event: sub2api_internal_kiro_ping")
}

func TestStreamEventStreamAsAnthropicAcceptsTerminalEventWhenRequired(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "complete answer",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{
			"stop_reason": "end_turn",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 9, KiroRequestContext{
		RequireTerminalEvent: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "end_turn", result.StopReason)
	require.Contains(t, out.String(), "event: message_stop")
}

func TestStreamEventStreamAsAnthropicThinkingOnlyResponse(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{
			"text": "I should think first.",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "end_turn", result.StopReason)
	require.Contains(t, out.String(), `"type":"thinking_delta"`)
	require.Contains(t, out.String(), `"thinking":"I should think first."`)
	require.Contains(t, out.String(), `"type":"signature_delta"`)
	require.Contains(t, out.String(), "event: message_stop")
}

func TestStreamEventStreamAsAnthropicParsesMultipleReasoningEventsWhenEnabled(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "first thought"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "second thought"},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "final"},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"thinking":"first thought"`)
	require.Contains(t, output, `"thinking":"second thought"`)
	require.Contains(t, output, `"text":"final"`)
	// 连续 reasoning 片段必须合并进同一个 thinking 块，而不是每片一个块
	require.Equal(t, 1, strings.Count(output, `"type":"thinking"`), "consecutive reasoning events should produce exactly one thinking block")
}

func TestStreamEventStreamAsAnthropicMergesManyReasoningFragmentsIntoOneBlock(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, frag := range []string{"I ", "need ", "to ", "think"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
			"reasoningContentEvent": map[string]any{"text": frag},
		}))
	}
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "answer"},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)

	output := out.String()
	require.Equal(t, 1, strings.Count(output, `"type":"thinking"`), "many reasoning fragments must collapse into a single thinking block")
	// 每个片段各自一个 thinking_delta，但同属一个块
	require.Equal(t, 4, strings.Count(output, `"type":"thinking_delta"`))
	require.Contains(t, output, `"text":"answer"`)
}

func TestStreamEventStreamAsAnthropicParsesTaggedThinkingWhenEnabled(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "<thinking>\nreason</thinking>\n\nfinal",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	thinkingDelta := strings.Index(output, `"thinking":"reason"`)
	textDelta := strings.Index(output, `"text":"final"`)
	require.NotEqual(t, -1, thinkingDelta)
	require.NotEqual(t, -1, textDelta)
	require.Less(t, thinkingDelta, textDelta)
	require.Contains(t, output, `"type":"signature_delta"`)
	require.NotContains(t, output, `\u003c/thinking\u003e`)
}

func TestStreamEventStreamAsAnthropicParsesTaggedThinkingWithLeadingApostrophe(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, chunk := range []string{"<thinking>'re working with.", "</thinking>\n\n", "final"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": chunk},
		}))
	}

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-7", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `"type":"thinking_delta"`)
	require.Contains(t, output, `"thinking":"'re "`)
	require.Contains(t, output, `"thinking":"working with."`)
	require.Contains(t, output, `"text":"final"`)
	require.NotContains(t, output, `"text":"\u003cthinking\u003e're working with.\u003c/thinking\u003e`)
	require.NotContains(t, output, `"text":"'re working with."`)
}

func TestStreamEventStreamAsAnthropicBuffersSplitThinkingTags(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	for _, chunk := range []string{"\n\n<think", "ing>\nrea", "son</thinking>", "\n\nfinal"} {
		_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": chunk},
		}))
	}

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{ThinkingEnabled: true})
	require.NoError(t, err)

	output := out.String()
	thinkingStart := strings.Index(output, `"type":"thinking"`)
	textDelta := strings.Index(output, `"text":"final"`)
	require.NotEqual(t, -1, thinkingStart)
	require.NotEqual(t, -1, textDelta)
	require.Less(t, thinkingStart, textDelta)
	require.NotContains(t, output, `\u003cthink`)
	require.NotContains(t, output, `\u003c/thinking\u003e`)
	require.NotContains(t, output, `"text":"\n\n"`)
}

func TestStreamEventStreamAsAnthropicTreatsThinkingTagsAsTextWhenDisabled(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "<thinking>reason</thinking>\n\nfinal",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, "end_turn", result.StopReason)

	output := out.String()
	require.Contains(t, output, `\u003cthinking\u003ereason\u003c/thinking\u003e`)
	require.NotContains(t, output, `"type":"thinking_delta"`)
}

func TestStreamEventStreamAsAnthropicIgnoresReasoningContentWhenThinkingDisabled(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "hidden reasoning"},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 9, KiroRequestContext{})
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.NotContains(t, out.String(), "hidden reasoning")
	require.NotContains(t, out.String(), `"type":"thinking"`)
}

func TestBuildAssistantMessageStructUsesSpacePlaceholderForToolOnly(t *testing.T) {
	msg := gjson.Parse(`{
		"role":"assistant",
		"content":[
			{"type":"tool_use","id":"toolu_01ABC","name":"read_file","input":{"path":"/tmp/test.txt"}}
		]
	}`)

	result := buildAssistantMessageStruct(msg, nil)
	require.Equal(t, " ", result.Content)
	require.Len(t, result.ToolUses, 1)
	require.Equal(t, "readFile", result.ToolUses[0].Name)
	require.Equal(t, "/tmp/test.txt", result.ToolUses[0].Input["path"])
}

func TestBuildAssistantMessageStructPreservesThinkingStartingWithApostrophe(t *testing.T) {
	msg := gjson.Parse(`{
		"role":"assistant",
		"content":[
			{"type":"thinking","thinking":"I should look at the project structure to get a sense of what we're working with."},
			{"type":"text","text":"<thinking>'re working with.</thinking>\n\n"},
			{"type":"tool_use","id":"toolu_01ABC","name":"Bash","input":{"command":"ls"}}
		]
	}`)

	result := buildAssistantMessageStruct(msg, nil)
	require.Contains(t, result.Content, "<thinking>I should look at the project structure to get a sense of what we're working with.")
	require.Contains(t, result.Content, "'re working with.</thinking>")
	require.NotContains(t, result.Content, "\n\n<thinking>'re working with.</thinking>")
	require.Len(t, result.ToolUses, 1)
}

func TestBuildKiroPayloadAddsPlaceholderToolForHistoryToolUse(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"read_file","input":{"path":"/tmp/a.txt"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"},{"type":"text","text":"continue"}]}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	tools := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, tools, 1)
	require.Equal(t, "readFile", tools[0].Get("toolSpecification.name").String())
	require.Equal(t, "Tool used in conversation history", tools[0].Get("toolSpecification.description").String())
	require.Equal(t, "object", tools[0].Get("toolSpecification.inputSchema.json.type").String())
}

func TestBuildKiroPayloadNormalizesToolJSONSchema(t *testing.T) {
	inputSchema := map[string]any{
		"properties":           nil,
		"required":             []any{"", 1, "path"},
		"additionalProperties": "sometimes",
		"items": map[string]any{
			"properties":           nil,
			"required":             []any{1, "ok"},
			"additionalProperties": 7,
		},
	}
	bodyBytes, err := json.Marshal(map[string]any{
		"model":    "claude-sonnet-4-5",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
		"tools": []any{map[string]any{
			"name":         "bad_schema",
			"description":  "bad schema",
			"input_schema": inputSchema,
		}},
	})
	require.NoError(t, err)

	kiroBuildResult, err := BuildKiroPayloadWithContext(bodyBytes, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	schema := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.inputSchema.json")
	require.Equal(t, "object", schema.Get("type").String())
	require.False(t, schema.Get("additionalProperties").Exists())
	require.Equal(t, "path", schema.Get("required.0").String())
	require.False(t, schema.Get("items.additionalProperties").Exists())
	require.Equal(t, "ok", schema.Get("items.required.0").String())
	require.Contains(t, inputSchema, "additionalProperties", "schema sanitizer must not mutate caller input")
}

func TestBuildKiroPayloadDefaultsInvalidToolJSONSchemaToObject(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"bad_schema",
			"description":"bad schema",
			"input_schema":"bad"
		}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	schema := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.inputSchema.json")
	require.Equal(t, "object", schema.Get("type").String())
	require.False(t, schema.Get("required").Exists())
	require.False(t, schema.Get("additionalProperties").Exists())
}

func TestBuildKiroPayloadFiltersCurrentOrphanToolResult(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"missing","content":"orphaned"}]}]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults").Exists())
}

func TestBuildKiroPayloadRemovesHistoryOrphanToolUse(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_orphan","name":"read_file","input":{"path":"/tmp/a.txt"}}]},
			{"role":"user","content":"continue"}
		]
	}`)

	kiroBuildResult, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := kiroBuildResult.Payload
	history := gjson.GetBytes(payload, "conversationState.history").Array()
	for _, msg := range history {
		require.NotEqual(t, " ", msg.Get("assistantResponseMessage.content").String())
		require.False(t, msg.Get("assistantResponseMessage.toolUses").Exists())
	}
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Exists())
}

func TestBuildKiroPayloadFlattensCompletedHistoryToolCycles(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[
			{"role":"user","content":"run the build"},
			{"role":"assistant","content":[
				{"type":"text","text":"running build"},
				{"type":"tool_use","id":"t1","name":"exec_command","input":{"cmd":"make"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"t1","content":"build ok"}
			]},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"t2","name":"exec_command","input":{"cmd":"test"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"t2","content":"tests pass"}
			]},
			{"role":"user","content":"Summarize everything that happened above."}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	for _, msg := range gjson.GetBytes(payload, "conversationState.history").Array() {
		require.Equal(t, int64(0), msg.Get("assistantResponseMessage.toolUses.#").Int())
		require.False(t, msg.Get("userInputMessage.userInputMessageContext.toolResults").Exists())
		require.NotContains(t, msg.Get("assistantResponseMessage.content").String(), "[Called tool")
	}
	require.False(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults").Exists())
	require.Contains(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String(), "Summarize everything")

	var historyText strings.Builder
	for _, msg := range gjson.GetBytes(payload, "conversationState.history").Array() {
		historyText.WriteString(msg.Get("assistantResponseMessage.content").String())
		historyText.WriteString("\n")
		historyText.WriteString(msg.Get("userInputMessage.content").String())
		historyText.WriteString("\n")
	}
	historyText.WriteString(gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	combined := historyText.String()
	require.Contains(t, combined, "[exec_command]")
	require.Contains(t, combined, "tests pass")
}

func TestBuildKiroPayloadKeepsActiveToolTurnStructured(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-8",
		"tools":[{"name":"exec_command","description":"run","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"user","content":"run ls"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"t9","name":"exec_command","input":{"cmd":"ls"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"t9","content":"file1 file2"}
			]}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	history := gjson.GetBytes(payload, "conversationState.history").Array()
	require.NotEmpty(t, history)
	last := history[len(history)-1]
	require.Equal(t, "t9", last.Get("assistantResponseMessage.toolUses.0.toolUseId").String())
	require.Equal(t, "t9", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults.0.toolUseId").String())
}

func TestBuildKiroPayloadCompactsLongSuccessfulToolResult(t *testing.T) {
	longOutput := strings.Repeat("head-", 1200) + strings.Repeat("middle-", 1800) + strings.Repeat("tail-", 800)
	body := []byte(fmt.Sprintf(`{
		"model":"claude-opus-4-8",
		"tools":[{"name":"exec_command","description":"run","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"t_long","name":"exec_command","input":{"cmd":"build"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_long","content":%q}]}
		]
	}`, longOutput))

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	text := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults.0.content.0.text").String()
	require.Less(t, len(text), len(longOutput))
	require.Contains(t, text, "[Output truncated for Kiro context:")
	require.Contains(t, text, "head-head-head")
	require.Contains(t, text, "tail-tail-tail")
	require.NotContains(t, text, "middle-middle-middle-middle")
}

func TestBuildKiroPayloadKeepsLongErrorToolResult(t *testing.T) {
	longOutput := strings.Repeat("error detail ", 1400)
	body := []byte(fmt.Sprintf(`{
		"model":"claude-opus-4-8",
		"tools":[{"name":"exec_command","description":"run","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"t_error","name":"exec_command","input":{"cmd":"build"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_error","is_error":true,"content":%q}]}
		]
	}`, longOutput))

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-4.8", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	text := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults.0.content.0.text").String()
	require.Equal(t, longOutput, text)
	require.NotContains(t, text, "[Output truncated for Kiro context:")
}

func TestMergeAdjacentMessagesUsesDoubleNewline(t *testing.T) {
	messages := gjson.Parse(`[
		{"role":"user","content":"first"},
		{"role":"user","content":"second"}
	]`).Array()

	merged := mergeAdjacentMessages(messages)
	require.Len(t, merged, 1)
	require.Equal(t, "first\n\nsecond", merged[0].Get("content.0.text").String())
}

func TestLongToolNamesUseHashSuffixAndDoNotCollide(t *testing.T) {
	nameA := strings.Repeat("tool_prefix_", 8) + "alpha"
	nameB := strings.Repeat("tool_prefix_", 8) + "bravo"
	shortA := shortenToolNameIfNeeded(nameA)
	shortB := shortenToolNameIfNeeded(nameB)

	require.Len(t, shortA, kiroMaxToolNameLen)
	require.Len(t, shortB, kiroMaxToolNameLen)
	require.NotEqual(t, shortA, shortB)
	require.Regexp(t, `h[0-9a-f]{8}$`, shortA)
	require.Regexp(t, `h[0-9a-f]{8}$`, shortB)
}

func TestBuildKiroPayloadMapsToolNamesToKiroCamelCaseAndRestoresResponses(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"use tool"}],
		"tools":[{"name":"read_file","description":"read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],
		"tool_choice":{"type":"tool","name":"read_file"}
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)

	require.Equal(t, "read_file", result.Context.ToolNameMap["readFile"])
	require.Equal(t, "readFile", gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Contains(t, gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String(), "MUST use the tool named 'readFile'")

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_read",
			"name":      "readFile",
			"input":     `{"path":"/tmp/a.txt"}`,
			"stop":      true,
		},
	}))
	parsed, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", result.Context)
	require.NoError(t, err)
	require.Equal(t, "read_file", gjson.GetBytes(parsed.ResponseBody, "content.0.name").String())
}

func TestBuildKiroPayloadMapsLongToolNameConsistently(t *testing.T) {
	longName := strings.Repeat("mcp__very_long_server__", 4) + "read_file"
	body := []byte(fmt.Sprintf(`{
		"model":"claude-sonnet-4-5",
		"system":"Follow tool choice.",
		"tool_choice":{"type":"tool","name":%q},
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":%q,"input":{"path":"/tmp/a.txt"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"},{"type":"text","text":"continue"}]}
		],
		"tools":[{"name":%q,"description":"read","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`, longName, longName, longName))

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	require.Len(t, result.Context.ToolNameMap, 1)
	var shortName string
	for short, original := range result.Context.ToolNameMap {
		shortName = short
		require.Equal(t, longName, original)
	}
	require.NotEmpty(t, shortName)
	require.Equal(t, shortName, gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.0.toolSpecification.name").String())
	require.Contains(t, gjson.GetBytes(result.Payload, "conversationState.history.0.userInputMessage.content").String(), "MUST use the tool named '"+shortName+"'")

	var historyText strings.Builder
	for _, msg := range gjson.GetBytes(result.Payload, "conversationState.history").Array() {
		require.Equal(t, int64(0), msg.Get("assistantResponseMessage.toolUses.#").Int())
		historyText.WriteString(msg.Get("userInputMessage.content").String())
		historyText.WriteString("\n")
	}
	historyText.WriteString(gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.content").String())
	require.Contains(t, historyText.String(), "["+longName+"] ok")
	require.NotContains(t, historyText.String(), "["+shortName+"] ok")
}

func TestParseNonStreamingEventStreamRestoresShortToolName(t *testing.T) {
	longName := strings.Repeat("long_tool_name_", 6)
	shortName := shortenToolNameIfNeeded(longName)
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_long",
			"name":      shortName,
			"input":     `{"path":"/tmp/a.txt"}`,
			"stop":      true,
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{
		ToolNameMap: map[string]string{shortName: longName},
	})
	require.NoError(t, err)
	require.Equal(t, longName, gjson.GetBytes(result.ResponseBody, "content.0.name").String())
}

func TestStreamEventStreamAsAnthropicRestoresShortToolName(t *testing.T) {
	longName := strings.Repeat("long_tool_name_", 6)
	shortName := shortenToolNameIfNeeded(longName)
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_long",
			"name":      shortName,
			"input":     `{"path":"/tmp/a.txt"}`,
			"stop":      true,
		},
	}))

	var out bytes.Buffer
	_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 1, KiroRequestContext{
		ToolNameMap: map[string]string{shortName: longName},
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), `"name":"`+longName+`"`)
	require.NotContains(t, out.String(), `"name":"`+shortName+`"`)
}

func TestKiroCacheEmulationUsageInjectedIntoNonStreamingResponse(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 120,
				"outputTokens":        7,
			},
		},
	}))
	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{
		CacheEmulationUsage: &Usage{
			InputTokens:                20,
			CacheReadInputTokens:       70,
			CacheCreationInputTokens:   30,
			CacheCreation5mInputTokens: 30,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 70, result.Usage.CacheReadInputTokens)
	require.Equal(t, 30, result.Usage.CacheCreationInputTokens)
	require.Equal(t, 20, int(gjson.GetBytes(result.ResponseBody, "usage.input_tokens").Int()))
	require.Equal(t, 70, int(gjson.GetBytes(result.ResponseBody, "usage.cache_read_input_tokens").Int()))
	require.Equal(t, 30, int(gjson.GetBytes(result.ResponseBody, "usage.cache_creation_input_tokens").Int()))
	require.Equal(t, 30, int(gjson.GetBytes(result.ResponseBody, "usage.cache_creation.ephemeral_5m_input_tokens").Int()))
}

func TestKiroCacheEmulationUsageInjectedIntoStreamAndResult(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 120,
				"outputTokens":        7,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello"},
	}))
	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-sonnet-4-5", 120, KiroRequestContext{
		CacheEmulationUsage: &Usage{
			InputTokens:                20,
			CacheReadInputTokens:       70,
			CacheCreationInputTokens:   30,
			CacheCreation1hInputTokens: 30,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 70, result.Usage.CacheReadInputTokens)
	require.Equal(t, 30, result.Usage.CacheCreationInputTokens)
	output := out.String()
	require.Contains(t, output, `"input_tokens":20`)
	require.Contains(t, output, `"cache_read_input_tokens":70`)
	require.Contains(t, output, `"cache_creation_input_tokens":30`)
	require.Contains(t, output, `"ephemeral_1h_input_tokens":30`)
}

func TestRepairJSONKeepsStringBracesWhileRepairingTrailingComma(t *testing.T) {
	raw := `{"key":"value with {nested}",}`
	repaired := repairJSON(raw)

	var parsed map[string]string
	require.NoError(t, json.Unmarshal([]byte(repaired), &parsed))
	require.Equal(t, "value with {nested}", parsed["key"])
}

func TestMapModel_MatchesKiroReferenceMapping(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-opus-4-8":                     "claude-opus-4.8",
		"claude-opus-4-8-thinking":            "claude-opus-4.8",
		"claude-opus-4.8":                     "claude-opus-4.8",
		"claude-opus-4-9":                     "claude-opus-4.9",
		"claude-opus-4-9-thinking":            "claude-opus-4.9",
		"claude-opus-4-7":                     "claude-opus-4.7",
		"claude-opus-4-7-thinking":            "claude-opus-4.7",
		"claude-opus-4.7":                     "claude-opus-4.7",
		"claude-sonnet-4-20250514":            "claude-sonnet-4",
		"claude-3-5-sonnet-20241022":          "claude-sonnet-4.5",
		"claude-3-opus":                       "claude-sonnet-4.5",
		"claude-3-sonnet":                     "claude-sonnet-4",
		"claude-3-haiku":                      "claude-haiku-4.5",
		"gpt-4-turbo":                         "claude-sonnet-4.5",
		"gpt-4o":                              "claude-sonnet-4.5",
		"gpt-4":                               "claude-sonnet-4.5",
		"gpt-3.5-turbo":                       "claude-sonnet-4.5",
		"claude-sonnet-5":                     "claude-sonnet-5",
		"claude-sonnet-4-6":                   "claude-sonnet-4.6",
		"claude-sonnet-4-6-thinking":          "claude-sonnet-4.6",
		"claude-sonnet-4.6":                   "claude-sonnet-4.6",
		"claude-sonnet-4-5-20250929":          "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250929-thinking": "claude-sonnet-4.5",
		"claude-sonnet-4-5":                   "claude-sonnet-4.5",
		"claude-sonnet-4.5":                   "claude-sonnet-4.5",
		"claude-opus-4-6":                     "claude-opus-4.6",
		"claude-opus-4-6-thinking":            "claude-opus-4.6",
		"claude-opus-4.6":                     "claude-opus-4.6",
		"claude-opus-4-5-20251101":            "claude-opus-4.5",
		"claude-opus-4-5-20251101-thinking":   "claude-opus-4.5",
		"claude-opus-4-5":                     "claude-opus-4.5",
		"claude-opus-4.5":                     "claude-opus-4.5",
		"claude-haiku-4-5-20251001":           "claude-haiku-4.5",
		"claude-haiku-4-5-20251001-thinking":  "claude-haiku-4.5",
		"claude-haiku-4-5":                    "claude-haiku-4.5",
		"claude-haiku-4.5":                    "claude-haiku-4.5",
	}

	for input, want := range cases {
		if got := MapModel(input); got != want {
			t.Fatalf("MapModel(%q) = %q, want %q", input, got, want)
		}
	}

	rejected := []string{
		"claude-sonnet-4-6-chat",
		" claude-sonnet-4-6-thinking-chat ",
		"claude-sonnet-4-6-agentic",
		" claude-sonnet-4-6-thinking-agentic ",
		"claude-opus-4-20250514",
	}
	for _, input := range rejected {
		if got := MapModel(input); got != "" {
			t.Fatalf("MapModel(%q) = %q, want empty", input, got)
		}
	}
}

func TestMapModel_ReturnsEmptyForUnsupportedModels(t *testing.T) {
	t.Parallel()

	cases := []string{
		"auto",
		"deepseek-3-2",
		"minimax-m2-1",
		"qwen3-coder-next",
	}

	for _, input := range cases {
		if got := MapModel(input); got != "" {
			t.Fatalf("MapModel(%q) = %q, want empty string", input, got)
		}
	}
}

func TestParseNonStreamingEventStreamEstimatesOutputTokensWhenMissing(t *testing.T) {
	// Kiro sometimes omits outputTokens; output should be estimated from response text.
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{
			"content": "hello world",
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"totalTokens":         15,
				// outputTokens intentionally absent
			},
		},
	}))

	result, err := ParseNonStreamingEventStreamWithContext(stream, "claude-sonnet-4-5", KiroRequestContext{})
	require.NoError(t, err)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Greater(t, result.Usage.OutputTokens, 0, "should estimate outputTokens from response text")
}

func TestStreamEventStreamAsAnthropicEstimatesOutputTokensWhenMissing(t *testing.T) {
	// Kiro sometimes omits outputTokens; output should be estimated from streamed text.
	pr, pw := io.Pipe()
	var out bytes.Buffer
	errCh := make(chan error, 1)

	go func() {
		_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), pr, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
		errCh <- err
	}()

	_, _ = pw.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello world"},
	}))
	_, _ = pw.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"totalTokens":         16,
				// outputTokens intentionally absent
			},
		},
	}))
	require.NoError(t, pw.Close())
	require.NoError(t, <-errCh)

	output := out.String()
	// message_delta should have output_tokens > 0 (estimated from "hello world")
	require.Contains(t, output, "event: message_delta", "message_delta should be present")
	deltaIdx := strings.Index(output, "event: message_delta")
	deltaSection := output[deltaIdx:]
	require.NotContains(t, deltaSection, `"output_tokens":0`, "message_delta output_tokens should not be 0")
	require.Contains(t, deltaSection, `"output_tokens":`, "output_tokens should be present in message_delta")
}

func TestStreamEventStreamAsAnthropicStreamingToolInputCountsOutputTokens(t *testing.T) {
	// Streaming tool input fragments should be counted toward output_tokens estimation.
	pr, pw := io.Pipe()
	var out bytes.Buffer
	errCh := make(chan error, 1)

	go func() {
		_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), pr, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
		errCh <- err
	}()

	_, _ = pw.Write(buildEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "toolu_01",
			"name":      "bash",
			"input":     `{"command": "echo hello world"}`,
			"stop":      true,
		},
	}))
	// No outputTokens in metadata
	_, _ = pw.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
			},
		},
	}))
	require.NoError(t, pw.Close())
	require.NoError(t, <-errCh)

	output := out.String()
	deltaIdx := strings.Index(output, "event: message_delta")
	require.GreaterOrEqual(t, deltaIdx, 0, "message_delta should be present")
	deltaSection := output[deltaIdx:]
	require.NotContains(t, deltaSection, `"output_tokens":0`, "streaming tool input should contribute to output_tokens")
	require.Contains(t, deltaSection, `"output_tokens":`, "output_tokens should be present in message_delta")
}

func TestStreamEventStreamAsAnthropicUpstreamOutputTokensNotOverridden(t *testing.T) {
	// When upstream provides real outputTokens, estimation must not override it.
	pr, pw := io.Pipe()
	var out bytes.Buffer
	errCh := make(chan error, 1)

	go func() {
		_, err := StreamEventStreamAsAnthropicWithContext(context.Background(), pr, &out, "claude-sonnet-4-5", 10, KiroRequestContext{})
		errCh <- err
	}()

	_, _ = pw.Write(buildEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hi"},
	}))
	_, _ = pw.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        42,
				"totalTokens":         52,
			},
		},
	}))
	require.NoError(t, pw.Close())
	require.NoError(t, <-errCh)

	output := out.String()
	deltaIdx := strings.Index(output, "event: message_delta")
	require.GreaterOrEqual(t, deltaIdx, 0)
	deltaSection := output[deltaIdx:]
	require.Contains(t, deltaSection, `"output_tokens":42`, "upstream outputTokens should not be overridden by estimation")
}

func TestStreamEventStreamAsAnthropicRejectsEmptyKiroStream(t *testing.T) {
	var out bytes.Buffer

	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), strings.NewReader(""), &out, "claude-opus-4-8", 10, KiroRequestContext{})

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Empty(t, out.String())
}

func TestStreamEventStreamAsAnthropicRejectsMetadataOnlyKiroTurn(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        0,
				"totalTokens":         10,
			},
		},
	}))
	_, _ = stream.Write(buildEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{
			"stopReason": "end_turn",
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 10, KiroRequestContext{RequireTerminalEvent: true})

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Contains(t, err.Error(), "metadata-only assistant output")
	require.NotContains(t, out.String(), "event: message_start")
	require.NotContains(t, out.String(), "event: message_delta")
	require.NotContains(t, out.String(), "event: message_stop")
}

func TestStreamEventStreamAsAnthropicRejectsMetadataOnlyKiroTurnWithoutTerminal(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 10,
				"outputTokens":        0,
				"totalTokens":         10,
			},
		},
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 10, KiroRequestContext{RequireTerminalEvent: true})

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty kiro event stream")
	require.Contains(t, err.Error(), "metadata-only assistant output")
	require.NotContains(t, out.String(), "event: message_start")
	require.NotContains(t, out.String(), "event: message_delta")
	require.NotContains(t, out.String(), "event: message_stop")
}

func TestStreamEventStreamAsAnthropicRejectsKiroExceptionFrame(t *testing.T) {
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(buildEventStreamExceptionFrame(t, "ThrottlingException", map[string]any{
		"message": "Too many requests, please wait before trying again.",
		"reason":  nil,
	}))

	var out bytes.Buffer
	result, err := StreamEventStreamAsAnthropicWithContext(context.Background(), stream, &out, "claude-opus-4-8", 10, KiroRequestContext{})

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ThrottlingException")
	require.Contains(t, err.Error(), "Too many requests")
	require.Empty(t, out.String())
}

func buildEventStreamFrame(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	headers := bytes.NewBuffer(nil)
	_ = headers.WriteByte(byte(len(":event-type")))
	_, _ = headers.WriteString(":event-type")
	_ = headers.WriteByte(7)
	require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(eventType))))
	_, _ = headers.WriteString(eventType)

	totalLength := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, totalLength))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}

func buildEventStreamExceptionFrame(t *testing.T, exceptionType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	return buildEventStreamFrameWithHeaders(t, map[string]string{
		":message-type":   "exception",
		":exception-type": exceptionType,
	}, payloadBytes)
}

func buildEventStreamFrameWithHeaders(t *testing.T, headerValues map[string]string, payloadBytes []byte) []byte {
	t.Helper()
	headers := bytes.NewBuffer(nil)
	for name, value := range headerValues {
		_ = headers.WriteByte(byte(len(name)))
		_, _ = headers.WriteString(name)
		_ = headers.WriteByte(7)
		require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(value))))
		_, _ = headers.WriteString(value)
	}

	totalLength := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, totalLength))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}

func TestBuildKiroPayloadTrailingInlineSystemPreservesCurrentUserAndTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"real question"},
			{"role":"system","content":"SKILL LIST REMINDER"}
		],
		"tools":[
			{"name":"read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},
			{"name":"grep","description":"search","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	require.Equal(t, "real question", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	require.Equal(t, int64(2), gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.#").Int())
	require.Contains(t, gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String(), "SKILL LIST REMINDER")
}

func TestBuildKiroPayloadMidConversationSystemMergesAndKeepsAlternation(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"alpha"},
			{"role":"system","content":"MID NOTE"},
			{"role":"user","content":"bravo"}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	// alpha 与 bravo 过滤 system 后相邻，应被合并为当前消息
	current := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String()
	require.Contains(t, current, "alpha")
	require.Contains(t, current, "bravo")
	// MID NOTE 折叠进前置注入
	require.Contains(t, gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String(), "MID NOTE")
	// history 中不应出现裸 system 角色
	for _, msg := range gjson.GetBytes(payload, "conversationState.history").Array() {
		require.NotEqual(t, "system", msg.Get("userInputMessage.role").String())
	}
}

func TestBuildKiroPayloadInlineSystemBlockArrayExtracted(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"system","content":[{"type":"text","text":"BLOCK NOTE"}]}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	require.Equal(t, "hi", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	require.Contains(t, gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String(), "BLOCK NOTE")
}

func TestBuildKiroPayloadTrailingAssistantThenSystemStillAttachesTools(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"user","content":"do something"},
			{"role":"assistant","content":"done"},
			{"role":"system","content":"TRAILING NOTE"}
		],
		"tools":[
			{"name":"read","description":"read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-sonnet-4.5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	payload := result.Payload

	// 末尾过滤后变 assistant，走 Continue 兜底，但 tools 仍应挂载
	require.Equal(t, "Continue", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
	require.Greater(t, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools.#").Int(), int64(0))
	require.Contains(t, gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String(), "TRAILING NOTE")
}
