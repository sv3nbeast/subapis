package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsSensitiveKey_TokenBudgetKeysNotRedacted(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"max_tokens",
		"max_output_tokens",
		"max_input_tokens",
		"max_completion_tokens",
		"max_tokens_to_sample",
		"budget_tokens",
		"prompt_tokens",
		"completion_tokens",
		"input_tokens",
		"output_tokens",
		"total_tokens",
		"token_count",
	} {
		if isSensitiveKey(key) {
			t.Fatalf("expected key %q to NOT be treated as sensitive", key)
		}
	}

	for _, key := range []string{
		"authorization",
		"Authorization",
		"access_token",
		"refresh_token",
		"id_token",
		"session_token",
		"token",
		"client_secret",
		"private_key",
		"signature",
	} {
		if !isSensitiveKey(key) {
			t.Fatalf("expected key %q to be treated as sensitive", key)
		}
	}
}

func TestSanitizeAndTrimJSONPayload_PreservesTokenBudgetFields(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"model":"claude-3","max_tokens":123,"thinking":{"type":"enabled","budget_tokens":456},"access_token":"abc","messages":[{"role":"user","content":"hi"}]}`)
	out, _, _ := sanitizeAndTrimJSONPayload(raw, 10*1024)
	if out == "" {
		t.Fatalf("expected non-empty sanitized output")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal sanitized output: %v", err)
	}

	if got, ok := decoded["max_tokens"].(float64); !ok || got != 123 {
		t.Fatalf("expected max_tokens=123, got %#v", decoded["max_tokens"])
	}

	thinking, ok := decoded["thinking"].(map[string]any)
	if !ok || thinking == nil {
		t.Fatalf("expected thinking object to be preserved, got %#v", decoded["thinking"])
	}
	if got, ok := thinking["budget_tokens"].(float64); !ok || got != 456 {
		t.Fatalf("expected thinking.budget_tokens=456, got %#v", thinking["budget_tokens"])
	}

	if got := decoded["access_token"]; got != "[REDACTED]" {
		t.Fatalf("expected access_token to be redacted, got %#v", got)
	}
}

func TestSanitizeErrorBodyForStorage_RedactsInlineImagesAndSecrets(t *testing.T) {
	t.Parallel()

	raw := `{"error":{"message":"bad"},"image_url":"data:image/png;base64,abcdefghijklmnopqrstuvwxyz0123456789","b64_json":"ZmluYWw=","output":[{"type":"image_generation_call","result":"aW1hZ2UtMQ=="}],"authorization":"Bearer secret-token"}`
	out, truncated := sanitizeErrorBodyForStorage(raw, 20*1024)

	if truncated {
		t.Fatalf("did not expect truncation")
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("expected valid JSON, got %s", out)
	}
	for _, leaked := range []string{
		"abcdefghijklmnopqrstuvwxyz0123456789",
		"ZmluYWw=",
		"aW1hZ2UtMQ==",
		"secret-token",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("sanitized output leaked %q: %s", leaked, out)
		}
	}
	for _, expected := range []string{"[REDACTED_BASE64", "[REDACTED]"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in sanitized output: %s", expected, out)
		}
	}
}

func TestSanitizeErrorBodyForStorage_RedactsNonJSONBodyBeforeTruncation(t *testing.T) {
	t.Parallel()

	raw := `upstream failed access_token=secret-value body={"b64_json":"abcdefghijklmnopqrstuvwxyz0123456789","url":"data:image/jpeg;base64,abcdefghijklmnopqrstuvwxyz0123456789"}`
	out, truncated := sanitizeErrorBodyForStorage(raw, 90)

	if !truncated {
		t.Fatalf("expected truncation")
	}
	for _, leaked := range []string{
		"secret-value",
		"abcdefghijklmnopqrstuvwxyz0123456789",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("sanitized output leaked %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "access_token=[REDACTED]") {
		t.Fatalf("expected access_token redaction, got %s", out)
	}
	if !strings.Contains(out, "[REDACTED_BASE64") {
		t.Fatalf("expected base64 redaction, got %s", out)
	}
}

func TestSanitizeOpsUpstreamErrors_RedactsRequestAndResponseBodies(t *testing.T) {
	t.Parallel()

	entry := &OpsInsertErrorLogInput{
		UpstreamErrors: []*OpsUpstreamErrorEvent{
			{
				UpstreamStatusCode:   429,
				Message:              "quota",
				Detail:               `{"refresh_token":"rt-secret","url":"data:image/png;base64,abcdefghijklmnopqrstuvwxyz0123456789"}`,
				UpstreamRequestBody:  `{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,abcdefghijklmnopqrstuvwxyz0123456789"}}]}],"api_key":"sk-secret"}`,
				UpstreamResponseBody: `{"data":[{"b64_json":"ZmluYWw="}],"access_token":"at-secret"}`,
			},
		},
	}

	if err := sanitizeOpsUpstreamErrors(entry); err != nil {
		t.Fatalf("sanitize upstream errors: %v", err)
	}
	if entry.UpstreamErrorsJSON == nil {
		t.Fatalf("expected upstream errors JSON")
	}
	out := *entry.UpstreamErrorsJSON
	for _, leaked := range []string{
		"rt-secret",
		"sk-secret",
		"at-secret",
		"abcdefghijklmnopqrstuvwxyz0123456789",
		"ZmluYWw=",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("sanitized upstream errors leaked %q: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "[REDACTED_BASE64") {
		t.Fatalf("expected base64 redaction in upstream errors: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected credential redaction in upstream errors: %s", out)
	}
}

func TestShrinkToEssentials_IncludesThinking(t *testing.T) {
	t.Parallel()

	root := map[string]any{
		"model":      "claude-3",
		"max_tokens": 100,
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 200,
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "first"},
			map[string]any{"role": "user", "content": "last"},
		},
	}

	out := shrinkToEssentials(root)
	if _, ok := out["thinking"]; !ok {
		t.Fatalf("expected thinking to be included in essentials: %#v", out)
	}
}
