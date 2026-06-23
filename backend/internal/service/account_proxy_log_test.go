package service

import "testing"

func TestBuildAccountProxyLogInfo_RedactsProxyEndpoint(t *testing.T) {
	proxyID := int64(42)
	account := &Account{
		ProxyID: &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Name:     "sensitive-proxy-name",
			Protocol: "socks5",
			Host:     "proxy.internal.example",
			Port:     1080,
			Username: "user",
			Password: "secret",
		},
	}

	info := buildAccountProxyLogInfo(account)
	if !info.Enabled {
		t.Fatal("expected proxy to be marked enabled")
	}
	if info.ID != proxyID {
		t.Fatalf("proxy id = %d, want %d", info.ID, proxyID)
	}
	if info.Protocol != "socks5h" {
		t.Fatalf("proxy protocol = %q, want socks5h", info.Protocol)
	}
}
