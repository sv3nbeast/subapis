package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"golang.org/x/net/http2"
)

const (
	antigravityWorkerTransportIdleConnTimeout    = 90 * time.Second
	antigravityWorkerTransportResponseHeaderWait = 300 * time.Second
	antigravityWorkerTransportDialTimeout        = 5 * time.Second
	antigravityWorkerTransportTLSHandshakeWait   = 5 * time.Second
)

type antigravityWorkerHTTPExecutor struct {
	mu sync.Mutex

	client *http.Client

	proxyURL              string
	tlsProfileKey         string
	concurrency           int
	responseHeaderTimeout time.Duration
}

func (e *antigravityWorkerHTTPExecutor) do(req *http.Request, proxyURL string, account *Account, profile *tlsfingerprint.Profile, responseHeaderTimeout time.Duration) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	client, err := e.ensureClient(proxyURL, account, profile, responseHeaderTimeout)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func (e *antigravityWorkerHTTPExecutor) ensureClient(proxyURL string, account *Account, profile *tlsfingerprint.Profile, responseHeaderTimeout time.Duration) (*http.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	proxyURL = strings.TrimSpace(proxyURL)
	tlsProfileKey := antigravityWorkerTLSProfileKey(profile)
	concurrency := 1
	if account != nil && account.Concurrency > 0 {
		concurrency = account.Concurrency
	}
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = antigravityWorkerTransportResponseHeaderWait
	}

	if e.client != nil &&
		e.proxyURL == proxyURL &&
		e.tlsProfileKey == tlsProfileKey &&
		e.concurrency == concurrency &&
		e.responseHeaderTimeout == responseHeaderTimeout {
		return e.client, nil
	}

	client, err := buildAntigravityWorkerHTTPClient(proxyURL, concurrency, profile, responseHeaderTimeout)
	if err != nil {
		return nil, err
	}
	if e.client != nil {
		e.client.CloseIdleConnections()
	}
	e.client = client
	e.proxyURL = proxyURL
	e.tlsProfileKey = tlsProfileKey
	e.concurrency = concurrency
	e.responseHeaderTimeout = responseHeaderTimeout
	return e.client, nil
}

func antigravityWorkerTLSProfileKey(profile *tlsfingerprint.Profile) string {
	if profile == nil {
		return "no_tls"
	}
	body, err := json.Marshal(profile)
	if err != nil {
		return profile.Name
	}
	return string(body)
}

func buildAntigravityWorkerHTTPClient(proxy string, concurrency int, profile *tlsfingerprint.Profile, responseHeaderTimeout time.Duration) (*http.Client, error) {
	if concurrency <= 0 {
		concurrency = 1
	}

	_, parsedProxy, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		MaxIdleConns:          concurrency,
		MaxIdleConnsPerHost:   concurrency,
		MaxConnsPerHost:       concurrency,
		IdleConnTimeout:       antigravityWorkerTransportIdleConnTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ForceAttemptHTTP2:     true,
	}

	if profile != nil {
		return buildAntigravityWorkerHTTP2Client(proxy, profile)
	}

	if profile == nil {
		transport.DialContext = (&net.Dialer{Timeout: antigravityWorkerTransportDialTimeout}).DialContext
		transport.TLSHandshakeTimeout = antigravityWorkerTransportTLSHandshakeWait
		if err := proxyutil.ConfigureTransportProxy(transport, parsedProxy); err != nil {
			return nil, fmt.Errorf("configure antigravity worker proxy: %w", err)
		}
	}

	if err := http2.ConfigureTransport(transport); err != nil {
		return nil, fmt.Errorf("configure antigravity worker http2 transport: %w", err)
	}

	return &http.Client{Transport: transport}, nil
}

func buildAntigravityWorkerHTTP2Client(proxy string, profile *tlsfingerprint.Profile) (*http.Client, error) {
	_, parsedProxy, err := proxyurl.Parse(proxy)
	if err != nil {
		return nil, err
	}

	transport := &http2.Transport{
		DialTLSContext:             antigravityWorkerDialTLSContext(profile, parsedProxy),
		DisableCompression:         true,
		StrictMaxConcurrentStreams: false,
		PingTimeout:                15 * time.Second,
		ReadIdleTimeout:            antigravityWorkerTransportIdleConnTimeout,
	}
	return &http.Client{Transport: transport}, nil
}

func antigravityWorkerDialTLSContext(profile *tlsfingerprint.Profile, proxyURL *url.URL) func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
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
			return nil, fmt.Errorf("unsupported antigravity worker proxy scheme: %s", proxyURL.Scheme)
		}
	}
}

func (w *antigravityWorkerState) requestExecutor() *antigravityWorkerHTTPExecutor {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.requestExecutorImpl == nil {
		w.requestExecutorImpl = &antigravityWorkerHTTPExecutor{}
	}
	return w.requestExecutorImpl
}

func (w *antigravityWorkerState) externalWorkerExecutor() *antigravityExternalWorkerExecutor {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.externalExecutor == nil {
		w.externalExecutor = &antigravityExternalWorkerExecutor{}
	}
	return w.externalExecutor
}

func (s *AntigravityGatewayService) antigravityWorkerResponseHeaderTimeout() time.Duration {
	if s != nil && s.settingService != nil && s.settingService.cfg != nil && s.settingService.cfg.Gateway.ResponseHeaderTimeout > 0 {
		return time.Duration(s.settingService.cfg.Gateway.ResponseHeaderTimeout) * time.Second
	}
	return antigravityWorkerTransportResponseHeaderWait
}

func (s *AntigravityGatewayService) doAntigravityUpstreamRequest(req *http.Request, proxyURL string, account *Account, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if s == nil {
		return nil, fmt.Errorf("nil antigravity gateway service")
	}
	if s.useAntigravityWorkerTransport && account != nil && account.Platform == PlatformAntigravity {
		if worker := s.antigravityWorker(account); worker != nil {
			if antigravityExternalWorkerEnabled() {
				if externalExecutor := worker.externalWorkerExecutor(); externalExecutor != nil {
					return externalExecutor.do(req, proxyURL, account, profile, s.antigravityWorkerResponseHeaderTimeout())
				}
			}
			if executor := worker.requestExecutor(); executor != nil {
				return executor.do(req, proxyURL, account, profile, s.antigravityWorkerResponseHeaderTimeout())
			}
		}
	}
	if s.httpUpstream == nil {
		return nil, fmt.Errorf("http upstream not configured")
	}
	return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, profile)
}

func (w *antigravityWorkerState) resetTransport() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.requestExecutorImpl != nil && w.requestExecutorImpl.client != nil {
		w.requestExecutorImpl.client.CloseIdleConnections()
	}
	w.requestExecutorImpl = nil
}

func (s *AntigravityGatewayService) resetAntigravityWorkerSession(account *Account, conversationKey string) antigravityRequestIdentity {
	identity := s.buildFreshSessionRecoveryIdentity(account, conversationKey)
	worker := s.antigravityWorker(account)
	if worker != nil {
		worker.setSessionID(identity.SessionID)
		worker.resetTransport()
		if externalExecutor := worker.externalWorkerExecutor(); externalExecutor != nil {
			externalExecutor.reset()
		}
	}
	if account != nil {
		s.clearClaudeToolUseSignatures(account.ID, conversationKey)
	}
	return identity
}
