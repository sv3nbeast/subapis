package cursor

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestEncodeDecodeFrameRoundTrip(t *testing.T) {
	payload := []byte("cursor connect-rpc payload")
	for _, compress := range []bool{false, true} {
		frame, err := EncodeFrame(payload, compress)
		if err != nil {
			t.Fatalf("EncodeFrame(compress=%v): %v", compress, err)
		}
		decoded, err := DecodeFrame(bytes.NewReader(frame))
		if err != nil {
			t.Fatalf("DecodeFrame(compress=%v): %v", compress, err)
		}
		if !bytes.Equal(decoded.Payload, payload) {
			t.Fatalf("compress=%v: payload = %q, want %q", compress, decoded.Payload, payload)
		}
		wantFlag := FrameFlagUncompressed
		if compress {
			wantFlag = FrameFlagGzip
		}
		if decoded.Flags != wantFlag {
			t.Fatalf("compress=%v: flags = %#x, want %#x", compress, decoded.Flags, wantFlag)
		}
	}
}

func TestDecodeFrameReportsEOFWhenDrained(t *testing.T) {
	if _, err := DecodeFrame(bytes.NewReader(nil)); err != io.EOF {
		t.Fatalf("DecodeFrame on empty reader = %v, want io.EOF", err)
	}
}

func TestReadRawFramePreservesWireBytes(t *testing.T) {
	// nalReadCloser replays the first frame to the consumer, so the raw bytes
	// must round-trip even when the payload arrived gzip-compressed.
	frame, err := EncodeFrame([]byte("hello"), true)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	raw, decoded, err := ReadRawFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("ReadRawFrame: %v", err)
	}
	if !bytes.Equal(raw, frame) {
		t.Fatalf("raw bytes = %x, want %x", raw, frame)
	}
	if string(decoded.Payload) != "hello" {
		t.Fatalf("decoded payload = %q, want %q", decoded.Payload, "hello")
	}
}

func TestParseConnectError(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantOK       bool
		wantCode     string
		wantMessage  string
		wantBadModel bool
	}{
		{
			name:     "flat envelope",
			raw:      `{"code":"unauthenticated","message":"token expired"}`,
			wantOK:   true,
			wantCode: "unauthenticated", wantMessage: "token expired",
		},
		{
			name:     "nested error wins",
			raw:      `{"code":"x","error":{"code":"invalid_argument","message":"ERROR_BAD_MODEL_NAME"}}`,
			wantOK:   true,
			wantCode: "invalid_argument", wantMessage: "ERROR_BAD_MODEL_NAME",
			wantBadModel: true,
		},
		{
			name:        "unparsable json falls back to the raw body",
			raw:         `{not json`,
			wantOK:      true,
			wantMessage: `{not json`,
		},
		{
			name:   "non-json is not a connect error",
			raw:    "plain text",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseConnectError(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got.Code, tt.wantCode)
			}
			if got.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", got.Message, tt.wantMessage)
			}
			if got.IsBadModelName() != tt.wantBadModel {
				t.Errorf("IsBadModelName = %v, want %v", got.IsBadModelName(), tt.wantBadModel)
			}
		})
	}
}

func TestConnectErrorJSONOnlyReadsEndStreamFrames(t *testing.T) {
	body := `{"code":"internal"}`
	if got := ConnectErrorJSON(&Frame{Flags: FrameFlagUncompressed, Payload: []byte(body)}); got != "" {
		t.Fatalf("data frame reported as error: %q", got)
	}
	if got := ConnectErrorJSON(&Frame{Flags: FrameFlagEndStream, Payload: []byte(body)}); got != body {
		t.Fatalf("end-stream error = %q, want %q", got, body)
	}
}

func TestProtobufWriterReaderRoundTrip(t *testing.T) {
	var nested ProtobufWriter
	nested.String(1, "inner")

	var w ProtobufWriter
	w.String(1, "text")
	w.Varint(2, 42)
	w.Bool(3, true)
	w.Bytes(4, nested.Result())

	if got := GetString(w.Result(), 1); got != "text" {
		t.Errorf("GetString(1) = %q, want %q", got, "text")
	}
	if got := getVarint(w.Result(), 2); got != 42 {
		t.Errorf("varint(2) = %d, want 42", got)
	}
	if got := getVarint(w.Result(), 3); got != 1 {
		t.Errorf("bool(3) = %d, want 1", got)
	}
	if got := GetString(GetNested(w.Result(), 4), 1); got != "inner" {
		t.Errorf("nested string = %q, want %q", got, "inner")
	}
}

func TestGenerateChecksumShape(t *testing.T) {
	machineID := strings.Repeat("a", 64)
	macMachineID := strings.Repeat("b", 64)
	at := time.Unix(1_700_000_000, 0)

	withMac := GenerateChecksumAt(machineID, macMachineID, at)
	if !strings.HasSuffix(withMac, machineID+"/"+macMachineID) {
		t.Fatalf("checksum = %q, want suffix %q", withMac, machineID+"/"+macMachineID)
	}
	// The encoded prefix is a fixed-width base64url block over 6 timestamp bytes.
	prefix := strings.TrimSuffix(withMac, machineID+"/"+macMachineID)
	if len(prefix) != 8 {
		t.Fatalf("encoded prefix = %q (len %d), want len 8", prefix, len(prefix))
	}
	for _, r := range prefix {
		if !strings.ContainsRune(checksumAlphabet, r) {
			t.Fatalf("prefix %q contains %q, outside the checksum alphabet", prefix, r)
		}
	}

	// Same coarse timestamp must produce the same checksum: it is a stable
	// device fingerprint within its ~1000-second bucket, not a nonce.
	if again := GenerateChecksumAt(machineID, macMachineID, at.Add(time.Second)); again != withMac {
		t.Fatalf("checksum drifted within the same bucket: %q vs %q", again, withMac)
	}

	// Without a mac id Cursor omits the trailing separator entirely.
	if withoutMac := GenerateChecksumAt(machineID, "", at); !strings.HasSuffix(withoutMac, machineID) ||
		strings.Contains(withoutMac, "/") {
		t.Fatalf("checksum without mac id = %q, want plain machine-id suffix", withoutMac)
	}
}

func TestBuildAgentClientMessageCarriesAskModeTurn(t *testing.T) {
	payload, conversationID, runID := BuildAgentClientMessage([]ChatMessage{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "latest"},
	}, "cursor-grok-4.6-medium")

	if conversationID == "" || runID == "" {
		t.Fatalf("conversationID=%q runID=%q, both must be set", conversationID, runID)
	}

	run := GetNested(payload, fieldAgentClientRunRequest)
	if run == nil {
		t.Fatal("run_request field missing")
	}
	if got := GetString(run, fieldRunConversationID); got != conversationID {
		t.Errorf("conversation id = %q, want %q", got, conversationID)
	}
	if got := GetString(run, fieldRunID); got != runID {
		t.Errorf("run id = %q, want %q", got, runID)
	}
	// System turns become the custom system prompt rather than a user message.
	if got := GetString(run, fieldRunCustomSystem); got != "be terse" {
		t.Errorf("custom system = %q, want %q", got, "be terse")
	}
	if got := GetString(GetNested(run, fieldRunModelDetails), fieldModelID); got != "cursor-grok-4.6-medium" {
		t.Errorf("model id = %q, want %q", got, "cursor-grok-4.6-medium")
	}
	if got := getVarint(GetNested(run, fieldRunConversationState), fieldConvStateMode); got != AgentModeAsk {
		t.Errorf("conversation mode = %d, want %d (ask)", got, AgentModeAsk)
	}

	// The trailing user turn is the live message; earlier turns are prepended
	// with a role prefix so the model still sees the history.
	userAction := GetNested(GetNested(run, fieldRunAction), fieldActionUserMessage)
	userMsg := GetNested(userAction, fieldUserMsgActionMessage)
	if got := GetString(userMsg, fieldUserMsgText); got != "latest" {
		t.Errorf("user text = %q, want %q", got, "latest")
	}
	if got := getVarint(userMsg, fieldUserMsgMode); got != AgentModeAsk {
		t.Errorf("user message mode = %d, want %d (ask)", got, AgentModeAsk)
	}
	if got := GetString(userAction, fieldUserMsgActionPrepend); got == "" {
		t.Error("history was dropped: expected the earlier turns as prepends")
	}
}

func TestSplitAskMessages(t *testing.T) {
	tests := []struct {
		name         string
		messages     []ChatMessage
		wantSystem   string
		wantUserText string
		wantPrior    []string
	}{
		{
			name:         "single user turn has no prepends",
			messages:     []ChatMessage{{Role: "user", Content: "hi"}},
			wantUserText: "hi",
		},
		{
			name: "multiple systems are joined",
			messages: []ChatMessage{
				{Role: "system", Content: "a"},
				{Role: "system", Content: "b"},
				{Role: "user", Content: "hi"},
			},
			wantSystem:   "a\n\nb",
			wantUserText: "hi",
		},
		{
			name: "history is prefixed by role",
			messages: []ChatMessage{
				{Role: "user", Content: "one"},
				{Role: "assistant", Content: "two"},
				{Role: "user", Content: "three"},
			},
			wantUserText: "three",
			wantPrior:    []string{"User: one", "Assistant: two"},
		},
		{
			name: "a trailing assistant turn collapses the whole transcript",
			messages: []ChatMessage{
				{Role: "user", Content: "one"},
				{Role: "assistant", Content: "two"},
			},
			wantUserText: "User: one\n\nAssistant: two",
		},
		{
			name:       "system-only input yields an empty turn",
			messages:   []ChatMessage{{Role: "system", Content: "only"}},
			wantSystem: "only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system, userText, prior := splitAskMessages(tt.messages)
			if system != tt.wantSystem {
				t.Errorf("system = %q, want %q", system, tt.wantSystem)
			}
			if userText != tt.wantUserText {
				t.Errorf("user text = %q, want %q", userText, tt.wantUserText)
			}
			if len(prior) != len(tt.wantPrior) {
				t.Fatalf("prior = %v, want %v", prior, tt.wantPrior)
			}
			for i := range prior {
				if prior[i] != tt.wantPrior[i] {
					t.Errorf("prior[%d] = %q, want %q", i, prior[i], tt.wantPrior[i])
				}
			}
		})
	}
}

// agentFrame builds one AgentServerMessage{interaction} Connect frame.
func agentFrame(t *testing.T, buildInteraction func(*ProtobufWriter)) []byte {
	t.Helper()
	var interaction ProtobufWriter
	buildInteraction(&interaction)
	var server ProtobufWriter
	server.Bytes(fieldAgentServerInteraction, interaction.Result())
	frame, err := EncodeFrame(server.Result(), false)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	return frame
}

func textDeltaFrame(t *testing.T, text string, serverNotice bool) []byte {
	t.Helper()
	return agentFrame(t, func(w *ProtobufWriter) {
		var delta ProtobufWriter
		delta.String(fieldTextDeltaText, text)
		if serverNotice {
			delta.Bool(fieldTextDeltaServerNotice, true)
		}
		w.Bytes(fieldInteractionTextDelta, delta.Result())
	})
}

func thinkingDeltaFrame(t *testing.T, text string) []byte {
	t.Helper()
	return agentFrame(t, func(w *ProtobufWriter) {
		var delta ProtobufWriter
		delta.String(fieldThinkingDeltaText, text)
		w.Bytes(fieldInteractionThinkingDelta, delta.Result())
	})
}

func tokenDeltaFrame(t *testing.T, tokens int) []byte {
	t.Helper()
	return agentFrame(t, func(w *ProtobufWriter) {
		var delta ProtobufWriter
		delta.Varint(fieldTokenDeltaTokens, tokens)
		w.Bytes(fieldInteractionTokenDelta, delta.Result())
	})
}

func turnEndedFrame(t *testing.T, u TokenUsage) []byte {
	t.Helper()
	return agentFrame(t, func(w *ProtobufWriter) {
		var ended ProtobufWriter
		ended.Varint(fieldTurnEndedInputTokens, u.InputTokens)
		ended.Varint(fieldTurnEndedOutputTokens, u.OutputTokens)
		ended.Varint(fieldTurnEndedCacheReadTokens, u.CacheReadTokens)
		ended.Varint(fieldTurnEndedCacheWriteTokens, u.CacheWriteTokens)
		ended.Varint(fieldTurnEndedReasoningTokens, u.ReasoningTokens)
		w.Bytes(fieldInteractionTurnEnded, ended.Result())
	})
}

func TestConsumeAssistantStreamEmitsDeltasAndPrefersTurnEndedUsage(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(thinkingDeltaFrame(t, "pondering"))
	stream.Write(textDeltaFrame(t, "hello ", false))
	stream.Write(textDeltaFrame(t, "ignored", true)) // server notice, not model output
	stream.Write(textDeltaFrame(t, "world", false))
	stream.Write(tokenDeltaFrame(t, 7))
	stream.Write(turnEndedFrame(t, TokenUsage{
		InputTokens: 11, OutputTokens: 22, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 5,
	}))

	var text, thinking strings.Builder
	usage, connectErr := ConsumeAssistantStream(&stream, func(ev StreamEvent) error {
		if ev.Type == "thinking" {
			thinking.WriteString(ev.Text)
		} else {
			text.WriteString(ev.Text)
		}
		return nil
	})

	if connectErr != "" {
		t.Fatalf("connect error = %q, want none", connectErr)
	}
	if text.String() != "hello world" {
		t.Errorf("text = %q, want %q", text.String(), "hello world")
	}
	if thinking.String() != "pondering" {
		t.Errorf("thinking = %q, want %q", thinking.String(), "pondering")
	}
	want := TokenUsage{InputTokens: 11, OutputTokens: 22, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 5}
	if usage != want {
		t.Errorf("usage = %+v, want %+v", usage, want)
	}
}

func TestConsumeAssistantStreamFallsBackToTokenDeltas(t *testing.T) {
	// A turn that ends without totals must still bill the streamed output.
	var stream bytes.Buffer
	stream.Write(textDeltaFrame(t, "hi", false))
	stream.Write(tokenDeltaFrame(t, 4))
	stream.Write(tokenDeltaFrame(t, 6))
	stream.Write(turnEndedFrame(t, TokenUsage{}))

	usage, connectErr := ConsumeAssistantStream(&stream, nil)
	if connectErr != "" {
		t.Fatalf("connect error = %q, want none", connectErr)
	}
	if usage.OutputTokens != 10 {
		t.Errorf("output tokens = %d, want 10 (summed deltas)", usage.OutputTokens)
	}
}

func TestConsumeAssistantStreamSurfacesEndStreamError(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(textDeltaFrame(t, "partial", false))
	errFrame, err := EncodeFrame([]byte(`{"code":"internal","message":"upstream exploded"}`), false)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	errFrame[0] |= FrameFlagEndStream
	stream.Write(errFrame)

	var text strings.Builder
	_, connectErr := ConsumeAssistantStream(&stream, func(ev StreamEvent) error {
		text.WriteString(ev.Text)
		return nil
	})

	if text.String() != "partial" {
		t.Errorf("text = %q, want %q", text.String(), "partial")
	}
	if connectErr == "" {
		t.Fatal("connect error was swallowed; a truncated turn would look clean")
	}
	parsed, ok := ParseConnectError(connectErr)
	if !ok || parsed.Message != "upstream exploded" {
		t.Errorf("parsed error = %+v (ok=%v), want message %q", parsed, ok, "upstream exploded")
	}
}

func TestNormalizeEffort(t *testing.T) {
	tests := map[string]string{
		"":           "",
		"Medium":     "medium",
		"extra-high": "xhigh",
		"extra_high": "xhigh",
		"XHIGH":      "xhigh",
		" max ":      "max",
		"none":       "none",
		"bogus":      "",
	}
	for in, want := range tests {
		if got := NormalizeEffort(in); got != want {
			t.Errorf("NormalizeEffort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRunModel(t *testing.T) {
	tests := []struct {
		name          string
		requested     string
		opts          RunOpts
		wantSlug      string
		wantPicker    string
		wantAlias     bool
		wantVariant   bool
		wantIsRawSlug bool
	}{
		{
			name:        "picker family resolves to a parameterized slug",
			requested:   "grok-4.6",
			wantSlug:    "cursor-grok-4.6-medium",
			wantPicker:  "grok-4.6",
			wantVariant: true,
		},
		{
			name:          "an exact run slug passes through untouched",
			requested:     "cursor-grok-4.6-high",
			wantSlug:      "cursor-grok-4.6-high",
			wantPicker:    "grok-4.6",
			wantIsRawSlug: true,
		},
		{
			name:        "effort selects the matching variant",
			requested:   "grok-4.6",
			opts:        RunOpts{Effort: "high"},
			wantSlug:    "cursor-grok-4.6-high",
			wantPicker:  "grok-4.6",
			wantVariant: true,
		},
		{
			// A family whose only variant is "-fast" keeps the bare picker id:
			// fast is a separately priced tier, so it is never applied on the
			// client's behalf.
			name:       "an alias maps onto its picker id without opting into fast",
			requested:  "composer",
			wantSlug:   "composer-2.5",
			wantPicker: "composer-2.5",
			wantAlias:  true,
		},
		{
			name:        "fast is applied only when the client asks for it",
			requested:   "composer-2.5",
			opts:        RunOpts{Fast: true},
			wantSlug:    "composer-2.5-fast",
			wantPicker:  "composer-2.5",
			wantVariant: true,
		},
		{
			name:      "an unknown model is left for the upstream to reject",
			requested: "not-a-cursor-model",
			wantSlug:  "not-a-cursor-model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveRunModel(tt.requested, tt.opts, nil)
			if got.RunSlug != tt.wantSlug {
				t.Errorf("RunSlug = %q, want %q", got.RunSlug, tt.wantSlug)
			}
			if tt.wantPicker != "" && got.PickerID != tt.wantPicker {
				t.Errorf("PickerID = %q, want %q", got.PickerID, tt.wantPicker)
			}
			if got.AliasFallback != tt.wantAlias {
				t.Errorf("AliasFallback = %v, want %v", got.AliasFallback, tt.wantAlias)
			}
			if got.VariantApplied != tt.wantVariant {
				t.Errorf("VariantApplied = %v, want %v", got.VariantApplied, tt.wantVariant)
			}
			if got.RequestedIsSlug != tt.wantIsRawSlug {
				t.Errorf("RequestedIsSlug = %v, want %v", got.RequestedIsSlug, tt.wantIsRawSlug)
			}
		})
	}
}

func TestResolveRunModelPrefersLiveCatalog(t *testing.T) {
	// A live catalog entry must win over the bundled snapshot: Cursor adds and
	// renames slugs between client releases.
	catalog := []AvailableModel{{
		Name:        "grok-4.6",
		LegacySlugs: []string{"cursor-grok-4.6-brandnew"},
	}}
	got := ResolveRunModel("grok-4.6", RunOpts{}, catalog)
	if got.RunSlug != "cursor-grok-4.6-brandnew" {
		t.Fatalf("RunSlug = %q, want the live catalog slug", got.RunSlug)
	}
}

func TestTokenRefreshResultExpiresAt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	if got := (TokenRefreshResult{ExpiresIn: 3600}).ExpiresAt(now); !got.Equal(now.Add(time.Hour)) {
		t.Errorf("expires_in honored = %v, want %v", got, now.Add(time.Hour))
	}
	// Without expires_in and without a decodable JWT the result must still be
	// bounded, so a stale token cannot be cached forever.
	if got := (TokenRefreshResult{AccessToken: "not-a-jwt"}).ExpiresAt(now); !got.After(now) {
		t.Errorf("fallback expiry = %v, want a time after %v", got, now)
	}
}

func TestDefaultModelIDsMatchSnapshot(t *testing.T) {
	ids := DefaultModelIDs()
	if len(ids) != len(DefaultModels) {
		t.Fatalf("DefaultModelIDs len = %d, want %d", len(ids), len(DefaultModels))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatal("DefaultModelIDs contains an empty id")
		}
		if seen[id] {
			t.Fatalf("DefaultModelIDs contains duplicate %q", id)
		}
		seen[id] = true
	}
	if !seen["default"] {
		t.Error(`DefaultModelIDs is missing "default" (Cursor's Auto picker)`)
	}
}
