package service

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"testing"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/stretchr/testify/require"
)

// nianzsKiroImageHistoryBody builds an alternating user/assistant conversation
// where withImage decides which user turns carry a screenshot.
func nianzsKiroImageHistoryBody(t *testing.T, model string, turns int, withImage func(turn int) bool, imageData string) []byte {
	t.Helper()
	parts := make([]string, 0, turns*2)
	for i := 0; i < turns; i++ {
		// One ephemeral breakpoint mid-history, the way Claude Code marks a
		// stable prefix. Without it nianzsBuildKiroCacheProfile has nothing to
		// cache and reports no profile at all.
		cacheControl := ""
		if i == turns/2 {
			cacheControl = `,"cache_control":{"type":"ephemeral"}`
		}
		content := fmt.Sprintf(`{"type":"text","text":"turn %d: %s"%s}`, i, strings.Repeat("analyse the attached screenshot carefully ", 80), cacheControl)
		if withImage(i) {
			content += fmt.Sprintf(`,{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}`, imageData)
		}
		parts = append(parts, fmt.Sprintf(`{"role":"user","content":[%s]}`, content))
		if i < turns-1 {
			parts = append(parts, fmt.Sprintf(`{"role":"assistant","content":"reply %d"}`, i))
		}
	}
	return []byte(fmt.Sprintf(`{"model":%q,"messages":[%s]}`, model, strings.Join(parts, ",")))
}

func nianzsKiroTestImageData(t *testing.T) string {
	t.Helper()
	return strings.TrimPrefix(
		nianzsTestKiroPNGDataURL(t, 512, 512, color.RGBA{R: 37, G: 89, B: 151, A: 255}),
		"data:image/png;base64,",
	)
}

// The translator replaces older history images with a text placeholder, so the
// provider never receives them. Charging for them billed an image-heavy session
// several times what the model actually read.
func TestNianzsKiroInputTokenEstimateSkipsDroppedHistoryImages(t *testing.T) {
	ctx := context.Background()
	data := nianzsKiroTestImageData(t)
	singleImage := nianzskiro.EstimateImageTokens(ctx, "image/png", data)
	require.Greater(t, singleImage, 0)

	const turns = 20
	oldImages := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", turns, func(i int) bool { return i < turns-8 }, data)
	noImages := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", turns, func(int) bool { return false }, data)

	delta := nianzsEstimateKiroInputTokens(ctx, oldImages) - nianzsEstimateKiroInputTokens(ctx, noImages)
	require.Less(t, delta, singleImage,
		"12 trimmed history images must together cost less than one image: none of them reach Kiro")
}

func TestNianzsKiroInputTokenEstimateSkipsDroppedHistoryImagesOnLegacyAccounting(t *testing.T) {
	ctx := context.Background()
	data := nianzsKiroTestImageData(t)
	singleImage := nianzskiro.EstimateImageTokens(ctx, "image/png", data)

	const turns = 20
	oldImages := nianzsKiroImageHistoryBody(t, "claude-sonnet-4-6", turns, func(i int) bool { return i < turns-8 }, data)
	noImages := nianzsKiroImageHistoryBody(t, "claude-sonnet-4-6", turns, func(int) bool { return false }, data)

	require.False(t, nianzsUsesModernClaudeInputAccounting("claude-sonnet-4-6"),
		"fixture must exercise the legacy estimator")
	delta := nianzsEstimateKiroInputTokens(ctx, oldImages) - nianzsEstimateKiroInputTokens(ctx, noImages)
	require.Less(t, delta, singleImage)
}

// Guard against over-correcting: images inside the window the payload still
// carries must stay billable, otherwise the platform eats provider cost.
func TestNianzsKiroInputTokenEstimateStillChargesSurvivingImages(t *testing.T) {
	ctx := context.Background()
	data := nianzsKiroTestImageData(t)
	singleImage := nianzskiro.EstimateImageTokens(ctx, "image/png", data)

	const turns = 20
	currentImage := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", turns, func(i int) bool { return i == turns-1 }, data)
	noImages := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", turns, func(int) bool { return false }, data)

	delta := nianzsEstimateKiroInputTokens(ctx, currentImage) - nianzsEstimateKiroInputTokens(ctx, noImages)
	require.GreaterOrEqual(t, delta, singleImage*9/10,
		"the current turn's image still reaches Kiro and must stay billable")
}

// Conversations without images must be byte-for-byte unaffected by trimming.
// This is the blast-radius guarantee for every other user on the platform.
func TestNianzsKiroHistoryImageTrimIsNoOpWithoutImages(t *testing.T) {
	ctx := context.Background()
	body := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", 40, func(int) bool { return false }, "")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	messages, _ := payload["messages"].([]any)
	require.NotEmpty(t, messages)

	require.Equal(t,
		nianzsCountModernClaudeMessagesTokens(ctx, messages, nil),
		nianzsCountModernClaudeMessagesTokens(ctx, messages, nianzsKiroHistoryImageKeep(messages)),
		"an image-free conversation must estimate identically with and without history trimming")
}

// The cache profile must be driven by the same corrected total, otherwise the
// cached-prefix split would still be computed on image bytes that never shipped.
func TestNianzsKiroCacheProfileFollowsTrimmedEstimate(t *testing.T) {
	ctx := context.Background()
	data := nianzsKiroTestImageData(t)

	const turns = 20
	body := nianzsKiroImageHistoryBody(t, "claude-opus-4-8", turns, func(i int) bool { return i < turns-8 }, data)
	inputTokens := nianzsEstimateKiroInputTokens(ctx, body)
	require.Greater(t, inputTokens, 0)

	profile, ok := nianzsBuildKiroCacheProfile(ctx, body, "claude-opus-4-8", inputTokens)
	require.True(t, ok)
	require.NotNil(t, profile)
	require.Equal(t, inputTokens, profile.totalInputTokens,
		"the cache profile must be sized by the trimmed estimate, not by images that never shipped")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	messages, _ := payload["messages"].([]any)
	keep := nianzsKiroHistoryImageKeep(messages)
	require.NotEmpty(t, keep)
	require.False(t, keep[0], "the oldest turn's image must be reported as dropped")
	require.True(t, keep[len(keep)-1], "the current turn keeps its images")
}
