package kiro

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageTokensForDimensions(t *testing.T) {
	require.Equal(t, 54, imageTokensForDimensions(200, 200))
	require.Equal(t, 1334, imageTokensForDimensions(1000, 1000))
	require.Equal(t, 1533, imageTokensForDimensions(2000, 1000))
	require.Equal(t, DefaultImageTokenEstimate, imageTokensForDimensions(0, 100))
}

func TestEstimateImageTokensUsesDimensionsNotEncodedLength(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 512, 512))
	var flatPNG bytes.Buffer
	require.NoError(t, png.Encode(&flatPNG, flat))

	noisy := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			noisy.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var noisyPNG bytes.Buffer
	require.NoError(t, png.Encode(&noisyPNG, noisy))
	require.Greater(t, noisyPNG.Len(), flatPNG.Len())

	flatTokens, ok := EstimateImageTokens("image/png", base64.StdEncoding.EncodeToString(flatPNG.Bytes()))
	require.True(t, ok)
	noisyTokens, ok := EstimateImageTokens("", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(noisyPNG.Bytes()))
	require.True(t, ok)
	require.Equal(t, 350, flatTokens)
	require.Equal(t, flatTokens, noisyTokens)
}

func TestEstimateImageTokensNeverFetchesRemoteURL(t *testing.T) {
	tokens, ok := EstimateImageTokens("image/png", "https://example.com/image.png")
	require.False(t, ok)
	require.Equal(t, DefaultImageTokenEstimate, tokens)
}
