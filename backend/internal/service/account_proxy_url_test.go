package service

import "testing"

func TestAccountProxyURLHelpersNormalizeSOCKS5(t *testing.T) {
	proxyID := int64(42)
	account := &Account{
		ProxyID: &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Protocol: "SOCKS5",
			Host:     "PROXY.EXAMPLE.COM",
			Port:     1080,
			Username: "user",
			Password: "pass",
		},
	}
	want := "socks5h://user:pass@proxy.example.com:1080"

	helpers := []struct {
		name string
		fn   func(*Account) string
	}{
		{name: "accountProxyURL", fn: accountProxyURL},
		{name: "kiroProxyURL", fn: kiroProxyURL},
		{name: "resolveAccountProxyURL", fn: resolveAccountProxyURL},
		{name: "upstreamModelsProxyURL", fn: upstreamModelsProxyURL},
		{name: "vertexServiceAccountProxyURL", fn: vertexServiceAccountProxyURL},
	}

	for _, helper := range helpers {
		helper := helper
		t.Run(helper.name, func(t *testing.T) {
			if got := helper.fn(account); got != want {
				t.Fatalf("%s() = %q, want %q", helper.name, got, want)
			}
		})
	}
}
