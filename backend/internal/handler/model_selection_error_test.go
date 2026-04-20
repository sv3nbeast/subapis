package handler

import "testing"

func TestClassifySelectionError_ChannelPricingRestriction(t *testing.T) {
	err := errorString("no available accounts supporting model: gpt-4o (channel pricing restriction)")
	got := classifySelectionError(err)
	if !got.Handled {
		t.Fatalf("expected handled classification")
	}
	if got.StatusCode != 400 || got.ErrorType != "invalid_request_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if !got.SkipMonitoring {
		t.Fatalf("expected skip monitoring")
	}
}

func TestClassifySelectionError_PureUnsupportedModelSummary(t *testing.T) {
	err := errorString("no available accounts supporting model: gpt-4o (total=197 eligible=0 excluded=0 unschedulable=0 platform_filtered=0 model_unsupported=197 model_rate_limited=0 model_capacity_cooling=0)")
	got := classifySelectionError(err)
	if !got.Handled {
		t.Fatalf("expected handled classification")
	}
	if got.StatusCode != 400 || got.ErrorType != "invalid_request_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
}

func TestClassifySelectionError_RateLimitedSummaryNotTreatedAsUnsupported(t *testing.T) {
	err := errorString("no available accounts supporting model: claude-sonnet-4-6 (total=197 eligible=0 excluded=0 unschedulable=0 platform_filtered=0 model_unsupported=0 model_rate_limited=197 model_capacity_cooling=0)")
	got := classifySelectionError(err)
	if got.Handled {
		t.Fatalf("expected rate-limited summary to remain unhandled, got %#v", got)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
