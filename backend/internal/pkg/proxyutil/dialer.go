// Package proxyutil 提供统一的代理配置功能
//
// 支持的代理协议：
//   - HTTP/HTTPS: 通过 Transport.Proxy 设置
//   - SOCKS5/SOCKS5H: 通过 Transport.DialContext 设置
//
// 注意：proxyurl.Parse() 会自动将 socks5:// 升级为 socks5h://。
// ConfigureTransportProxy 也会防御性升级 socks5://，确保未来误用时
// 目标域名仍随 SOCKS 握手交给代理端解析，防止 DNS 泄漏。
package proxyutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const (
	// socks5DialTimeout 限制到 SOCKS5 代理自身的 TCP 建连耗时。
	socks5DialTimeout = 10 * time.Second
	// socks5DialKeepAlive 与 Go 默认 keepalive 探测间隔保持一致。
	socks5DialKeepAlive = 30 * time.Second
)

// socks5ForwardDialer 是 SOCKS5 dialer 的底层拨号器。
//
// proxy.FromURL 的默认 forward dialer 是 proxy.Direct（零值 net.Dialer，无超时），
// 代理地址不可达时会一直卡到内核 TCP 重传耗尽（Linux 约 130 秒）。SOCKS5 分支会
// 覆盖 Transport.DialContext，因此调用方在 Transport 上设置的建连超时对这条路径
// 无效，必须在这里补上。
var socks5ForwardDialer = &net.Dialer{
	Timeout:   socks5DialTimeout,
	KeepAlive: socks5DialKeepAlive,
}

// ConfigureTransportProxy 根据代理 URL 配置 Transport
//
// 支持的协议：
//   - http/https: 设置 transport.Proxy
//   - socks5/socks5h: 设置 transport.DialContext
//
// 参数：
//   - transport: 需要配置的 http.Transport
//   - proxyURL: 代理地址，nil 表示直连
//
// 返回：
//   - error: 代理配置错误（协议不支持或 dialer 创建失败）
func ConfigureTransportProxy(transport *http.Transport, proxyURL *url.URL) error {
	if proxyURL == nil {
		return nil
	}

	normalizedProxyURL := *proxyURL
	scheme := strings.ToLower(normalizedProxyURL.Scheme)
	normalizedProxyURL.Scheme = scheme
	if scheme == "socks5" {
		scheme = "socks5h"
		normalizedProxyURL.Scheme = scheme
	}

	switch scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(&normalizedProxyURL)
		return nil

	case "socks5h":
		dialer, err := proxy.FromURL(&normalizedProxyURL, proxy.Direct)
		if err != nil {
			return fmt.Errorf("create socks5 dialer: %w", err)
		}
		// 优先使用支持 context 的 DialContext，以支持请求取消和超时
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			// 回退路径：如果 dialer 不支持 ContextDialer，则包装为简单的 DialContext
			// 注意：此回退不支持请求取消和超时控制
			transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported proxy scheme: %s", scheme)
	}
}
