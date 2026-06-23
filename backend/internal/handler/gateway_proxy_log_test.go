package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestGatewayProxyLogFields_RedactsProxyEndpoint(t *testing.T) {
	proxyID := int64(42)
	account := &service.Account{
		ProxyID: &proxyID,
		Proxy: &service.Proxy{
			ID:       proxyID,
			Name:     "sensitive-proxy-name",
			Protocol: "socks5",
			Host:     "proxy.internal.example",
			Port:     1080,
			Username: "user",
			Password: "secret",
		},
	}

	fields := gatewayProxyLogFields(account)
	keys := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		keys[field.Key] = struct{}{}
	}

	for _, key := range []string{"proxy_enabled", "proxy_id", "proxy_protocol"} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("expected proxy log field %q", key)
		}
	}
	for _, field := range fields {
		if field.Key == "proxy_protocol" && field.String != "socks5h" {
			t.Fatalf("proxy_protocol = %q, want socks5h", field.String)
		}
	}
	for _, key := range []string{"proxy_name", "proxy_host", "proxy_port", "proxy_username", "proxy_password"} {
		if _, ok := keys[key]; ok {
			t.Fatalf("proxy log field %q must not be emitted", key)
		}
	}
}
