package service

import (
	"net/url"
	"testing"
)

func TestProxyURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proxy Proxy
		want  string
	}{
		{
			name: "without auth",
			proxy: Proxy{
				Protocol: "http",
				Host:     "proxy.example.com",
				Port:     8080,
			},
			want: "http://proxy.example.com:8080",
		},
		{
			name: "socks5 with auth uses remote DNS scheme",
			proxy: Proxy{
				Protocol: "socks5",
				Host:     "socks.example.com",
				Port:     1080,
				Username: "user",
				Password: "pass",
			},
			want: "socks5h://user:pass@socks.example.com:1080",
		},
		{
			name: "uppercase socks5 normalizes to socks5h",
			proxy: Proxy{
				Protocol: "SOCKS5",
				Host:     "socks.example.com",
				Port:     1080,
			},
			want: "socks5h://socks.example.com:1080",
		},
		{
			name: "uppercase host and http default port normalize",
			proxy: Proxy{
				Protocol: "HTTP",
				Host:     "PROXY.EXAMPLE.COM",
				Port:     80,
			},
			want: "http://proxy.example.com",
		},
		{
			name: "uppercase host and https default port normalize",
			proxy: Proxy{
				Protocol: "HTTPS",
				Host:     "PROXY.EXAMPLE.COM",
				Port:     443,
			},
			want: "https://proxy.example.com",
		},
		{
			name: "ipv6 host without default http port keeps brackets",
			proxy: Proxy{
				Protocol: "http",
				Host:     "::1",
				Port:     80,
			},
			want: "http://[::1]",
		},
		{
			name: "bracketed ipv6 host with port does not double bracket",
			proxy: Proxy{
				Protocol: "http",
				Host:     "[::1]",
				Port:     8080,
			},
			want: "http://[::1]:8080",
		},
		{
			name: "bracketed ipv6 host after default https port keeps brackets",
			proxy: Proxy{
				Protocol: "https",
				Host:     "[2001:db8::1]",
				Port:     443,
			},
			want: "https://[2001:db8::1]",
		},
		{
			name: "username only keeps no auth for compatibility",
			proxy: Proxy{
				Protocol: "http",
				Host:     "proxy.example.com",
				Port:     8080,
				Username: "user-only",
			},
			want: "http://proxy.example.com:8080",
		},
		{
			name: "with special characters in credentials",
			proxy: Proxy{
				Protocol: "http",
				Host:     "proxy.example.com",
				Port:     3128,
				Username: "first last@corp",
				Password: "p@ ss:#word",
			},
			want: "http://first%20last%40corp:p%40%20ss%3A%23word@proxy.example.com:3128",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.proxy.URL(); got != tc.want {
				t.Fatalf("Proxy.URL() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestProxyURL_SpecialCharactersRoundTrip(t *testing.T) {
	t.Parallel()

	proxy := Proxy{
		Protocol: "http",
		Host:     "proxy.example.com",
		Port:     3128,
		Username: "first last@corp",
		Password: "p@ ss:#word",
	}

	parsed, err := url.Parse(proxy.URL())
	if err != nil {
		t.Fatalf("parse proxy URL failed: %v", err)
	}
	if got := parsed.User.Username(); got != proxy.Username {
		t.Fatalf("username mismatch after parse: got=%q want=%q", got, proxy.Username)
	}
	pass, ok := parsed.User.Password()
	if !ok {
		t.Fatal("password missing after parse")
	}
	if pass != proxy.Password {
		t.Fatalf("password mismatch after parse: got=%q want=%q", pass, proxy.Password)
	}
}
