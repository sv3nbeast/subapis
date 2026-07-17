package kiro

import (
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"strings"

	"golang.org/x/image/webp"
)

const (
	kiroImageTokenLongEdge  = 1568
	kiroImageTokenMaxPixels = 1_150_000
	kiroImagePixelsPerToken = 750

	DefaultImageTokenEstimate = 1600
)

// EstimateImageTokens estimates an inline image without performing network I/O.
// The bool is false for remote URLs, malformed data, and unsupported formats.
func EstimateImageTokens(mediaType, source string) (int, bool) {
	mediaType = strings.TrimSpace(mediaType)
	source = strings.TrimSpace(source)
	if source == "" || isRemoteImageURL(source) {
		return DefaultImageTokenEstimate, false
	}
	if dataMediaType, payload, ok := imageDataURLPayload(source); ok {
		if mediaType == "" {
			mediaType = dataMediaType
		}
		source = payload
	}

	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoder := base64.NewDecoder(encoding, strings.NewReader(source))
		if tokens, ok := estimateImageReaderTokens(mediaType, decoder); ok {
			return tokens, true
		}
	}
	return DefaultImageTokenEstimate, false
}

func estimateImageReaderTokens(mediaType string, reader io.Reader) (int, bool) {
	var (
		cfg image.Config
		err error
	)
	format := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(mediaType), "image/")))
	if format == "webp" {
		cfg, err = webp.DecodeConfig(reader)
	} else {
		cfg, _, err = image.DecodeConfig(reader)
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, false
	}
	return imageTokensForDimensions(cfg.Width, cfg.Height), true
}

func imageTokensForDimensions(width, height int) int {
	if width <= 0 || height <= 0 {
		return DefaultImageTokenEstimate
	}
	w, h := float64(width), float64(height)
	scale := math.Min(1, math.Min(float64(kiroImageTokenLongEdge)/w, float64(kiroImageTokenLongEdge)/h))
	if pixels := w * h; pixels*scale*scale > kiroImageTokenMaxPixels {
		scale = math.Min(scale, math.Sqrt(float64(kiroImageTokenMaxPixels)/pixels))
	}
	resizedWidth := math.Max(1, math.Floor(w*scale))
	resizedHeight := math.Max(1, math.Floor(h*scale))
	return max(1, int(math.Ceil(resizedWidth*resizedHeight/kiroImagePixelsPerToken)))
}

func imageDataURLPayload(value string) (mediaType, payload string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", "", false
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 || !strings.Contains(strings.ToLower(value[:comma]), ";base64") {
		return "", "", false
	}
	metadata := strings.TrimSpace(value[len("data:"):comma])
	if semi := strings.IndexByte(metadata, ';'); semi >= 0 {
		mediaType = strings.TrimSpace(metadata[:semi])
	}
	return mediaType, strings.TrimSpace(value[comma+1:]), true
}

func isRemoteImageURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
