package proxyurl

import (
	"strings"
	"testing"
)

func TestParse_空字符串直连(t *testing.T) {
	trimmed, parsed, err := Parse("")
	if err != nil {
		t.Fatalf("空字符串应直连: %v", err)
	}
	if trimmed != "" {
		t.Errorf("trimmed 应为空: got %q", trimmed)
	}
	if parsed != nil {
		t.Errorf("parsed 应为 nil: got %v", parsed)
	}
}

func TestParse_空白字符串直连(t *testing.T) {
	trimmed, parsed, err := Parse("   ")
	if err != nil {
		t.Fatalf("空白字符串应直连: %v", err)
	}
	if trimmed != "" {
		t.Errorf("trimmed 应为空: got %q", trimmed)
	}
	if parsed != nil {
		t.Errorf("parsed 应为 nil: got %v", parsed)
	}
}

func TestParse_有效HTTP代理(t *testing.T) {
	trimmed, parsed, err := Parse("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("有效 HTTP 代理应成功: %v", err)
	}
	if trimmed != "http://proxy.example.com:8080" {
		t.Errorf("trimmed 不匹配: got %q", trimmed)
	}
	if parsed == nil {
		t.Fatal("parsed 不应为 nil")
	}
	if parsed.Host != "proxy.example.com:8080" {
		t.Errorf("Host 不匹配: got %q", parsed.Host)
	}
}

func TestParse_有效HTTPS代理(t *testing.T) {
	_, parsed, err := Parse("https://proxy.example.com:443")
	if err != nil {
		t.Fatalf("有效 HTTPS 代理应成功: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("Scheme 不匹配: got %q", parsed.Scheme)
	}
}

func TestParse_有效SOCKS5代理_自动升级为SOCKS5H(t *testing.T) {
	trimmed, parsed, err := Parse("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("有效 SOCKS5 代理应成功: %v", err)
	}
	// socks5 自动升级为 socks5h，确保 DNS 由代理端解析
	if trimmed != "socks5h://127.0.0.1:1080" {
		t.Errorf("trimmed 应升级为 socks5h: got %q", trimmed)
	}
	if parsed.Scheme != "socks5h" {
		t.Errorf("Scheme 应升级为 socks5h: got %q", parsed.Scheme)
	}
}

func TestParse_无效URL(t *testing.T) {
	_, _, err := Parse("://invalid")
	if err == nil {
		t.Fatal("无效 URL 应返回错误")
	}
	if !strings.Contains(err.Error(), "invalid proxy URL") {
		t.Errorf("错误信息应包含 'invalid proxy URL': got %s", err.Error())
	}
}

func TestParse_缺少Host(t *testing.T) {
	_, _, err := Parse("http://")
	if err == nil {
		t.Fatal("缺少 host 应返回错误")
	}
	if !strings.Contains(err.Error(), "missing host") {
		t.Errorf("错误信息应包含 'missing host': got %s", err.Error())
	}
}

func TestParse_不支持的Scheme(t *testing.T) {
	_, _, err := Parse("ftp://proxy.example.com:21")
	if err == nil {
		t.Fatal("不支持的 scheme 应返回错误")
	}
	if !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Errorf("错误信息应包含 'unsupported proxy scheme': got %s", err.Error())
	}
}

func TestParse_含密码URL脱敏(t *testing.T) {
	// 场景 1: 带密码的 socks5 URL 应成功解析并升级为 socks5h
	trimmed, parsed, err := Parse("socks5://user:secret_password@proxy.local:1080")
	if err != nil {
		t.Fatalf("含密码的有效 URL 应成功: %v", err)
	}
	if trimmed == "" || parsed == nil {
		t.Fatal("应返回非空结果")
	}
	if parsed.Scheme != "socks5h" {
		t.Errorf("Scheme 应升级为 socks5h: got %q", parsed.Scheme)
	}
	if !strings.HasPrefix(trimmed, "socks5h://") {
		t.Errorf("trimmed 应以 socks5h:// 开头: got %q", trimmed)
	}
	if parsed.User == nil {
		t.Error("升级后应保留 UserInfo")
	}

	// 场景 2: 带密码但缺少 host（触发 Redacted 脱敏路径）
	_, _, err = Parse("http://user:secret_password@:0/")
	if err == nil {
		t.Fatal("缺少 host 应返回错误")
	}
	if strings.Contains(err.Error(), "secret_password") {
		t.Error("错误信息不应包含明文密码")
	}
	if !strings.Contains(err.Error(), "missing host") {
		t.Errorf("错误信息应包含 'missing host': got %s", err.Error())
	}
}

func TestParse_带空白的有效URL(t *testing.T) {
	trimmed, parsed, err := Parse("  http://proxy.example.com:8080  ")
	if err != nil {
		t.Fatalf("带空白的有效 URL 应成功: %v", err)
	}
	if trimmed != "http://proxy.example.com:8080" {
		t.Errorf("trimmed 应去除空白: got %q", trimmed)
	}
	if parsed == nil {
		t.Fatal("parsed 不应为 nil")
	}
}

func TestParse_规范化缓存键相关字段(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "lowercase host and drop http default port",
			raw:  "HTTP://PROXY.EXAMPLE.COM:80/path?x=1#frag",
			want: "http://proxy.example.com",
		},
		{
			name: "lowercase host and drop https default port",
			raw:  "HTTPS://PROXY.EXAMPLE.COM:443/path?x=1#frag",
			want: "https://proxy.example.com",
		},
		{
			name: "preserve non-default port",
			raw:  "https://PROXY.EXAMPLE.COM:8443/path?x=1#frag",
			want: "https://proxy.example.com:8443",
		},
		{
			name: "normalize socks5 host and path",
			raw:  "socks5://PROXY.EXAMPLE.COM:1080/path?x=1#frag",
			want: "socks5h://proxy.example.com:1080",
		},
		{
			name: "preserve ipv6 bracket formatting",
			raw:  "http://[::1]:8080/path?x=1#frag",
			want: "http://[::1]:8080",
		},
		{
			name: "preserve ipv6 brackets after dropping default http port",
			raw:  "http://[::1]:80/path?x=1#frag",
			want: "http://[::1]",
		},
		{
			name: "preserve ipv6 brackets after dropping default https port",
			raw:  "https://[2001:db8::1]:443/path?x=1#frag",
			want: "https://[2001:db8::1]",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			trimmed, parsed, err := Parse(tc.raw)
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", tc.raw, err)
			}
			if trimmed != tc.want {
				t.Fatalf("trimmed = %q, want %q", trimmed, tc.want)
			}
			if got := parsed.String(); got != tc.want {
				t.Fatalf("parsed.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParse_Scheme大小写不敏感(t *testing.T) {
	// 大写 SOCKS5 应被接受并升级为 socks5h
	trimmed, parsed, err := Parse("SOCKS5://proxy.example.com:1080")
	if err != nil {
		t.Fatalf("大写 SOCKS5 应被接受: %v", err)
	}
	if parsed.Scheme != "socks5h" {
		t.Errorf("大写 SOCKS5 Scheme 应升级为 socks5h: got %q", parsed.Scheme)
	}
	if !strings.HasPrefix(trimmed, "socks5h://") {
		t.Errorf("大写 SOCKS5 trimmed 应升级为 socks5h://: got %q", trimmed)
	}

	// 大写 HTTP 应被接受并规范化为小写，避免缓存 key 分裂。
	trimmed, parsed, err = Parse("HTTP://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("大写 HTTP 应被接受: %v", err)
	}
	if parsed.Scheme != "http" {
		t.Errorf("大写 HTTP Scheme 应规范化为 http: got %q", parsed.Scheme)
	}
	if trimmed != "http://proxy.example.com:8080" {
		t.Errorf("大写 HTTP trimmed 应规范化: got %q", trimmed)
	}

	trimmed, parsed, err = Parse("HTTPS://proxy.example.com:8443")
	if err != nil {
		t.Fatalf("大写 HTTPS 应被接受: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("大写 HTTPS Scheme 应规范化为 https: got %q", parsed.Scheme)
	}
	if trimmed != "https://proxy.example.com:8443" {
		t.Errorf("大写 HTTPS trimmed 应规范化: got %q", trimmed)
	}
}

func TestParse_带认证的有效代理(t *testing.T) {
	trimmed, parsed, err := Parse("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("带认证的代理 URL 应成功: %v", err)
	}
	if parsed.User == nil {
		t.Error("应保留 UserInfo")
	}
	if trimmed != "http://user:pass@proxy.example.com:8080" {
		t.Errorf("trimmed 不匹配: got %q", trimmed)
	}
}

func TestParse_IPv6地址(t *testing.T) {
	trimmed, parsed, err := Parse("http://[::1]:8080")
	if err != nil {
		t.Fatalf("IPv6 代理 URL 应成功: %v", err)
	}
	if parsed.Hostname() != "::1" {
		t.Errorf("Hostname 不匹配: got %q", parsed.Hostname())
	}
	if trimmed != "http://[::1]:8080" {
		t.Errorf("trimmed 不匹配: got %q", trimmed)
	}
}

func TestParse_SOCKS5H保持不变(t *testing.T) {
	trimmed, parsed, err := Parse("socks5h://proxy.local:1080")
	if err != nil {
		t.Fatalf("有效 SOCKS5H 代理应成功: %v", err)
	}
	// socks5h 不需要升级，应保持原样
	if trimmed != "socks5h://proxy.local:1080" {
		t.Errorf("trimmed 不应变化: got %q", trimmed)
	}
	if parsed.Scheme != "socks5h" {
		t.Errorf("Scheme 应保持 socks5h: got %q", parsed.Scheme)
	}
}

func TestParse_无Scheme裸地址(t *testing.T) {
	// 无 scheme 的裸地址，Go url.Parse 将其视为 path，Host 为空
	_, _, err := Parse("proxy.example.com:8080")
	if err == nil {
		t.Fatal("无 scheme 的裸地址应返回错误")
	}
}
