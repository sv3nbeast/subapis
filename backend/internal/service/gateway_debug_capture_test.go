package service

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseDebugGatewayUserID(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"1", 1},
		{" 42 ", 42},
		{"-3", 0},
		{"abc", 0},
		{"1.5", 0},
	}
	for _, tc := range cases {
		if got := parseDebugGatewayUserID(tc.in); got != tc.want {
			t.Errorf("parseDebugGatewayUserID(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func newDebugTestContext(userID int64) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if userID > 0 {
		c.Set(ginContextKeyAPIKey, &APIKey{UserID: userID})
	}
	return c
}

func TestGinUserIDForDebug(t *testing.T) {
	s := &GatewayService{}
	if got := s.ginUserIDForDebug(nil); got != 0 {
		t.Errorf("nil context: got %d, want 0", got)
	}
	if got := s.ginUserIDForDebug(newDebugTestContext(0)); got != 0 {
		t.Errorf("no api_key: got %d, want 0", got)
	}
	if got := s.ginUserIDForDebug(newDebugTestContext(9)); got != 9 {
		t.Errorf("api_key user 9: got %d, want 9", got)
	}
}

func TestDebugCaptureGates(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()

	// 文件未开启：两个门都关。
	s := &GatewayService{}
	if s.debugBodyCaptureEnabled(newDebugTestContext(1)) || s.debugCaptureEnabledForUser(newDebugTestContext(1)) {
		t.Fatal("file disabled: both gates must be closed")
	}

	// 文件开启 + 无用户目标(target=0)：body 走旧的全局落盘；响应抓取保持关闭。
	s = &GatewayService{}
	s.debugGatewayBodyFile.Store(devnull)
	if !s.debugBodyCaptureEnabled(newDebugTestContext(0)) {
		t.Error("target=0: body capture must be global-on")
	}
	if s.debugCaptureEnabledForUser(newDebugTestContext(0)) {
		t.Error("target=0: response capture must stay off")
	}

	// 文件开启 + 用户目标=5：仅命中用户落盘，其余用户全关。
	s = &GatewayService{}
	s.debugGatewayBodyFile.Store(devnull)
	s.debugGatewayBodyUserID.Store(5)
	match := newDebugTestContext(5)
	other := newDebugTestContext(6)
	if !s.debugBodyCaptureEnabled(match) || !s.debugCaptureEnabledForUser(match) {
		t.Error("target=5, user=5: both gates must be open")
	}
	if s.debugBodyCaptureEnabled(other) || s.debugCaptureEnabledForUser(other) {
		t.Error("target=5, user=6: both gates must be closed")
	}
}
