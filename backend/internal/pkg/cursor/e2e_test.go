//go:build e2e

// 针对真实 Cursor 账号的联机验证。默认不参与任何测试目标，需要显式提供凭证：
//
//	CURSOR_ACCESS_TOKEN=... CURSOR_MACHINE_ID=... CURSOR_MAC_MACHINE_ID=... \
//	  go test -tags=e2e -v ./internal/pkg/cursor/
//
// 这套断言覆盖的正是无法靠单测验证的部分：checksum 是否被上游接受、
// 手写 protobuf 的字段号是否正确、内置 slug 快照是否还与线上一致。
package cursor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func e2eCredentials(t *testing.T) Credentials {
	t.Helper()
	accessToken := strings.TrimSpace(os.Getenv("CURSOR_ACCESS_TOKEN"))
	if accessToken == "" {
		t.Skip("CURSOR_ACCESS_TOKEN not set; skipping Cursor live test")
	}
	machineID := strings.TrimSpace(os.Getenv("CURSOR_MACHINE_ID"))
	macMachineID := strings.TrimSpace(os.Getenv("CURSOR_MAC_MACHINE_ID"))
	if machineID == "" || macMachineID == "" {
		t.Skip("CURSOR_MACHINE_ID / CURSOR_MAC_MACHINE_ID not set; the checksum header needs both")
	}
	return Credentials{
		AccessToken:   normalizeToken(accessToken),
		MachineID:     machineID,
		MacMachineID:  macMachineID,
		ClientVersion: strings.TrimSpace(os.Getenv("CURSOR_CLIENT_VERSION")),
		ClientCommit:  strings.TrimSpace(os.Getenv("CURSOR_CLIENT_COMMIT")),
	}
}

// TestE2EChat 验证整条上行链路：checksum 头被接受、Run 请求的 protobuf 字段
// 号正确、响应帧能解析出文本。任何一环错了这里都会失败。
func TestE2EChat(t *testing.T) {
	creds := e2eCredentials(t)
	client := NewClient(creds)
	client.ProxyURL = os.Getenv("CURSOR_PROXY_URL")

	model := strings.TrimSpace(os.Getenv("CURSOR_E2E_MODEL"))
	if model == "" {
		model = "default"
	}
	resolved := ResolveRunModel(model, RunOpts{}, nil)
	if resolved.RunSlug != "" {
		model = resolved.RunSlug
	}
	t.Logf("requesting model %q", model)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := client.StreamChat(ctx, []ChatMessage{
		{Role: "user", Content: "Reply with the single word OK."},
	}, model)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	defer resp.Body.Close()

	var text, thinking strings.Builder
	usage, connectErr := ConsumeAssistantStream(resp.Body, func(ev StreamEvent) error {
		if ev.Type == "thinking" {
			thinking.WriteString(ev.Text)
		} else {
			text.WriteString(ev.Text)
		}
		return nil
	})
	if connectErr != "" {
		t.Fatalf("upstream error: %s", connectErr)
	}
	if strings.TrimSpace(text.String()) == "" && strings.TrimSpace(thinking.String()) == "" {
		t.Fatal("stream ended without any content; the request was accepted but produced nothing")
	}
	t.Logf("text=%q thinking=%d chars usage=%+v", text.String(), thinking.Len(), usage)

	// usage 全零说明 turn_ended 的字段号对不上，计费会整体归零。
	if usage.Empty() {
		t.Error("turn_ended carried no usage; billing would record zero for a real turn")
	}
}

// TestE2EAvailableModels 验证目录端点与解析。
func TestE2EAvailableModels(t *testing.T) {
	creds := e2eCredentials(t)
	client := NewClient(creds)
	client.ProxyURL = os.Getenv("CURSOR_PROXY_URL")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := client.AvailableModels(ctx)
	if err != nil {
		t.Fatalf("AvailableModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("AvailableModels returned nothing")
	}
	ids := ModelIDs(models)
	t.Logf("live catalog has %d models: %s", len(ids), strings.Join(ids, ", "))
}

// TestE2ERunSlugSnapshotMatchesLive 报告内置快照相对线上目录的漂移。
// Cursor 会增删模型，快照过期会让默认解析退化到裸 picker 名。
func TestE2ERunSlugSnapshotMatchesLive(t *testing.T) {
	creds := e2eCredentials(t)
	client := NewClient(creds)
	client.ProxyURL = os.Getenv("CURSOR_PROXY_URL")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	live, err := client.AvailableModels(ctx)
	if err != nil {
		t.Fatalf("AvailableModels: %v", err)
	}

	snapshot := defaultRunSlugTable()
	liveByPicker := make(map[string][]string, len(live))
	for _, model := range live {
		liveByPicker[model.Name] = model.LegacySlugs
	}

	for picker, slugs := range liveByPicker {
		known, ok := snapshot[picker]
		if !ok {
			t.Errorf("live picker %q is missing from the bundled snapshot (slugs: %v)", picker, slugs)
			continue
		}
		for _, slug := range slugs {
			if !containsFold(known, slug) {
				t.Errorf("live slug %q for picker %q is missing from the snapshot", slug, picker)
			}
		}
	}
	for picker := range snapshot {
		if _, ok := liveByPicker[picker]; !ok {
			t.Logf("snapshot picker %q is no longer offered to this account (plan-dependent, not necessarily stale)", picker)
		}
	}
}

// TestE2ETokenRefresh 验证刷新端点。它会消耗一次刷新，默认不跑。
func TestE2ETokenRefresh(t *testing.T) {
	refreshToken := strings.TrimSpace(os.Getenv("CURSOR_REFRESH_TOKEN"))
	if refreshToken == "" {
		t.Skip("CURSOR_REFRESH_TOKEN not set; skipping refresh test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := RefreshSessionViaProxy(ctx, refreshToken, os.Getenv("CURSOR_PROXY_URL"))
	if err != nil {
		t.Fatalf("RefreshSessionViaProxy: %v", err)
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		t.Fatal("refresh returned an empty access token")
	}
	t.Logf("refreshed; expires at %s", result.ExpiresAt(time.Now()).Format(time.RFC3339))
}
