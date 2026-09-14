package kiro

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// mergedMessageIndexes reproduces mergeAdjacentMessages from the role sequence
// alone so that billing estimation can predict history trimming. If the two
// ever disagree, estimation charges for images the payload dropped (or drops
// images the payload kept), so pin them together here.
func TestMergedMessageIndexesMatchesMergeAdjacentMessages(t *testing.T) {
	cases := [][]string{
		{},
		{"user"},
		{"user", "assistant"},
		{"user", "user"},
		{"user", "user", "user"},
		{"user", "assistant", "assistant", "user"},
		{"tool", "tool"},
		{"user", "tool", "tool", "user", "user"},
		{"assistant", "assistant", "tool", "assistant", "assistant"},
		{"user", "assistant", "user", "assistant", "user", "assistant", "user"},
	}
	for _, roles := range cases {
		messages := make([]gjson.Result, 0, len(roles))
		for i, role := range roles {
			messages = append(messages, gjson.Parse(fmt.Sprintf(`{"role":%q,"content":"m%d"}`, role, i)))
		}
		merged := mergeAdjacentMessages(messages)
		indexes := mergedMessageIndexes(roles)
		if len(roles) == 0 {
			if len(merged) != 0 {
				t.Fatalf("empty roles produced %d merged messages", len(merged))
			}
			continue
		}
		if got, want := indexes[len(indexes)-1]+1, len(merged); got != want {
			t.Fatalf("roles=%v merged count %d, want %d", roles, got, want)
		}
		for i := 1; i < len(indexes); i++ {
			if step := indexes[i] - indexes[i-1]; step != 0 && step != 1 {
				t.Fatalf("roles=%v index step %d at %d is neither same-merge nor next-merge", roles, step, i)
			}
		}
	}
}

func TestHistoryImageKeptByMessageIndexKeepsOnlyTheRecentWindow(t *testing.T) {
	roles := make([]string, 0, 40)
	for i := 0; i < 20; i++ {
		roles = append(roles, "user", "assistant")
	}
	kept := HistoryImageKeptByMessageIndex(roles)
	if len(kept) != len(roles) {
		t.Fatalf("kept length %d, want %d", len(kept), len(roles))
	}
	for i, role := range roles {
		want := role == "user" && len(roles)-1-i <= kiroHistoryImageKeepCount
		if kept[i] != want {
			t.Fatalf("message %d (role %s) kept=%v, want %v", i, role, kept[i], want)
		}
	}
	if kept[0] {
		t.Fatal("the oldest message must not keep its images")
	}
}

// buildAssistantMessageStruct has no image branch, so an assistant turn never
// ships images no matter how recent it is. Charging for them was silent loss.
func TestHistoryImageKeptByMessageIndexNeverKeepsNonUserRoles(t *testing.T) {
	roles := []string{"user", "assistant", "user", "assistant"}
	kept := HistoryImageKeptByMessageIndex(roles)
	for i, role := range roles {
		if role == "user" {
			continue
		}
		if kept[i] {
			t.Fatalf("role %s at %d must never keep images", role, i)
		}
	}
	if !kept[len(kept)-2] {
		t.Fatal("the most recent user turn must keep its images")
	}
}

// The built payload is the only authority on which images reach Kiro. Assert
// the exported rule and the reported drop count both agree with it.
func TestBuiltPayloadDropsExactlyTheImagesTheRulePredicts(t *testing.T) {
	const turns = 20
	imageData := testKiroPNGBase64(t, 512, 512)
	var messages []string
	var roles []string
	for i := 0; i < turns; i++ {
		messages = append(messages, fmt.Sprintf(
			`{"role":"user","content":[{"type":"text","text":"turn %d"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}`,
			i, imageData))
		roles = append(roles, "user")
		if i < turns-1 {
			messages = append(messages, fmt.Sprintf(`{"role":"assistant","content":"reply %d"}`, i))
			roles = append(roles, "assistant")
		}
	}
	body := []byte(`{"model":"claude-opus-4-8","messages":[` + strings.Join(messages, ",") + `]}`)

	result, err := BuildKiroPayloadWithOptions(body, "claude-opus-4-8", "", nil, KiroPayloadOptions{Origin: "AI_EDITOR"})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	carried := len(gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.images").Array())
	gjson.GetBytes(result.Payload, "conversationState.history").ForEach(func(_, value gjson.Result) bool {
		carried += len(value.Get("userInputMessage.images").Array())
		return true
	})

	if carried+result.Context.DroppedHistoryImages != turns {
		t.Fatalf("carried %d + dropped %d != %d images sent by the client",
			carried, result.Context.DroppedHistoryImages, turns)
	}
	if result.Context.DroppedHistoryImages == 0 {
		t.Fatal("a 20-turn image history must drop something, otherwise the fixture no longer exercises trimming")
	}

	predicted := 0
	for _, kept := range HistoryImageKeptByMessageIndex(roles) {
		if kept {
			predicted++
		}
	}
	if predicted != carried {
		t.Fatalf("rule predicts %d surviving images, payload carries %d", predicted, carried)
	}
}

func testKiroPNGBase64(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: 151, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
