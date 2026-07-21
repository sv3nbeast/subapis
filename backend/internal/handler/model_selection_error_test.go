package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestClassifySelectionError_ChannelPricingRestriction(t *testing.T) {
	err := errorString("no available accounts supporting model: gpt-4o (channel pricing restriction)")
	got := classifySelectionError(err)
	if !got.Handled {
		t.Fatalf("expected handled classification")
	}
	if got.StatusCode != 400 || got.ErrorType != "invalid_request_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.SkipMonitoring || !got.BusinessLimited {
		t.Fatalf("expected auditable business-limited classification: %#v", got)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	applySelectionErrorMonitoringClassification(c, got)
	if !service.HasOpsClientBusinessLimitedReason(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied) {
		t.Fatalf("expected local-policy-denied ops exclusion")
	}
	if _, exists := c.Get(service.OpsSkipPassthroughKey); exists {
		t.Fatalf("channel rejection must remain visible in the excluded view")
	}
	phase, limited, owner, source := classifyOpsErrorLog(c, got.ErrorType, got.Message, "", got.StatusCode)
	if phase != "auth" || !limited || owner != "client" || source != "client_request" {
		t.Fatalf("unexpected ops classification: phase=%s limited=%v owner=%s source=%s", phase, limited, owner, source)
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

func TestClassifySelectionError_AmbiguousOpenAIEmptyPoolIsNotModelUnsupported(t *testing.T) {
	err := errorString("no available OpenAI accounts supporting model: grok-4.5")
	got := classifySelectionError(err)
	if got.Handled {
		t.Fatalf("ambiguous empty-pool error must use model availability diagnosis: %#v", got)
	}
}

func TestClassifySelectionError_PureRateLimitedSummary(t *testing.T) {
	err := errorString("no available accounts supporting model: claude-sonnet-4-6 (total=197 eligible=0 excluded=0 unschedulable=0 platform_filtered=0 model_unsupported=0 model_rate_limited=197 model_capacity_cooling=0)")
	got := classifySelectionError(err)
	if !got.Handled {
		t.Fatalf("expected handled classification")
	}
	if got.StatusCode != 429 || got.ErrorType != "rate_limit_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.SkipMonitoring {
		t.Fatalf("did not expect skip monitoring")
	}
}

func TestClassifySelectionError_PureModelCapacityCoolingSummary(t *testing.T) {
	err := errorString("no available accounts supporting model: claude-opus-4-6-thinking (total=197 eligible=0 excluded=0 unschedulable=0 platform_filtered=0 model_unsupported=0 model_rate_limited=0 model_capacity_cooling=197)")
	got := classifySelectionError(err)
	if !got.Handled {
		t.Fatalf("expected handled classification")
	}
	if got.StatusCode != 503 || got.ErrorType != "upstream_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
}

func TestClassifySelectionError_KiroCooldownCarriesRetryAfter(t *testing.T) {
	err := &service.KiroCooldownExhaustedError{StatusCode: 429, RetryAfter: 1250 * time.Millisecond}
	got := classifySelectionError(err)
	if !got.Handled || got.StatusCode != 429 || got.ErrorType != "rate_limit_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.Message != clientUpstreamTemporarilyRateLimitedMessage {
		t.Fatalf("client message = %q, want %q", got.Message, clientUpstreamTemporarilyRateLimitedMessage)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	applySelectionErrorMonitoringClassification(c, got)
	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "2" {
		t.Fatalf("Retry-After = %q, want 2", retryAfter)
	}
}

func TestClassifySelectionError_KiroUnavailableUsesGenericClientMessage(t *testing.T) {
	err := &service.KiroCooldownExhaustedError{StatusCode: 503, RetryAfter: time.Second}
	got := classifySelectionError(err)
	if !got.Handled || got.StatusCode != 503 || got.ErrorType != "upstream_error" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.Message != clientUpstreamTemporarilyUnavailableMessage {
		t.Fatalf("client message = %q, want %q", got.Message, clientUpstreamTemporarilyUnavailableMessage)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
