package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/net/http2"
)

type workerAnnouncement struct {
	Addr string `json:"addr"`
}

type executeRequest struct {
	Method                       string                  `json:"method"`
	URL                          string                  `json:"url"`
	Headers                      http.Header             `json:"headers,omitempty"`
	BodyBase64                   string                  `json:"body_base64,omitempty"`
	ProxyURL                     string                  `json:"proxy_url,omitempty"`
	AccountConcurrency           int                     `json:"account_concurrency,omitempty"`
	ResponseHeaderTimeoutSeconds int                     `json:"response_header_timeout_seconds,omitempty"`
	TLSProfile                   *tlsfingerprint.Profile `json:"tls_profile,omitempty"`
}

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:0", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", handleExecute)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}

	announcement, _ := json.Marshal(workerAnnouncement{Addr: ln.Addr().String()})
	fmt.Println(string(announcement))

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		os.Exit(1)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload executeRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	body, err := base64.StdEncoding.DecodeString(payload.BodyBase64)
	if err != nil {
		http.Error(w, "invalid body_base64", http.StatusBadRequest)
		return
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), payload.Method, payload.URL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return
	}
	for key, values := range payload.Headers {
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	client, err := buildWorkerHTTPClient(payload.ProxyURL, payload.AccountConcurrency, payload.TLSProfile, time.Duration(payload.ResponseHeaderTimeoutSeconds)*time.Second)
	if err != nil {
		w.Header().Set("X-Antigravity-Worker-Error", "1")
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	resp, err := client.Do(upstreamReq)
	if err != nil {
		w.Header().Set("X-Antigravity-Worker-Error", "1")
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func buildWorkerHTTPClient(proxy string, concurrency int, profile *tlsfingerprint.Profile, responseHeaderTimeout time.Duration) (*http.Client, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = 300 * time.Second
	}

	_, parsedProxy, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		MaxIdleConns:          concurrency,
		MaxIdleConnsPerHost:   concurrency,
		MaxConnsPerHost:       concurrency,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ForceAttemptHTTP2:     true,
	}

	if profile != nil {
		return buildWorkerHTTP2Client(proxy, profile)
	}

	if profile == nil {
		transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
		transport.TLSHandshakeTimeout = 5 * time.Second
		if err := proxyutil.ConfigureTransportProxy(transport, parsedProxy); err != nil {
			return nil, err
		}
	}

	if err := http2.ConfigureTransport(transport); err != nil {
		return nil, err
	}

	return &http.Client{Transport: transport}, nil
}

func buildWorkerHTTP2Client(proxy string, profile *tlsfingerprint.Profile) (*http.Client, error) {
	_, parsedProxy, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, err
	}

	transport := &http2.Transport{
		DialTLSContext:             workerDialTLSContext(profile, parsedProxy),
		DisableCompression:         true,
		StrictMaxConcurrentStreams: false,
		PingTimeout:                15 * time.Second,
		ReadIdleTimeout:            90 * time.Second,
	}
	return &http.Client{Transport: transport}, nil
}

func workerDialTLSContext(profile *tlsfingerprint.Profile, proxyURL *url.URL) func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	return func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		if proxyURL == nil {
			dialer := tlsfingerprint.NewDialer(profile, nil)
			return dialer.DialTLSContext(ctx, network, addr)
		}

		switch strings.ToLower(proxyURL.Scheme) {
		case "socks5", "socks5h":
			dialer := tlsfingerprint.NewSOCKS5ProxyDialer(profile, proxyURL)
			return dialer.DialTLSContext(ctx, network, addr)
		case "http", "https":
			dialer := tlsfingerprint.NewHTTPProxyDialer(profile, proxyURL)
			return dialer.DialTLSContext(ctx, network, addr)
		default:
			return nil, fmt.Errorf("unsupported worker proxy scheme: %s", proxyURL.Scheme)
		}
	}
}

func copyHeader(dst, src http.Header) {
	for key := range dst {
		dst.Del(key)
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
