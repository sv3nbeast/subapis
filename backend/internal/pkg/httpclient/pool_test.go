package httpclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestValidatedTransport_CacheHostValidation(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	var validateCalls int32
	validateResolvedIP = func(host string) error {
		atomic.AddInt32(&validateCalls, 1)
		require.Equal(t, "api.openai.com", host)
		return nil
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730000000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time { return now }

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&validateCalls))
	require.Equal(t, int32(2), atomic.LoadInt32(&baseCalls))
}

func TestValidatedTransport_ExpiredCacheTriggersRevalidation(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	var validateCalls int32
	validateResolvedIP = func(_ string) error {
		atomic.AddInt32(&validateCalls, 1)
		return nil
	}

	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730001000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time { return now }

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	now = now.Add(validatedHostTTL + time.Second)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(2), atomic.LoadInt32(&validateCalls))
}

func TestValidatedTransport_ValidationErrorStopsRoundTrip(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	expectedErr := errors.New("dns rebinding rejected")
	validateResolvedIP = func(_ string) error {
		return expectedErr
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	transport := newValidatedTransport(base)
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, int32(0), atomic.LoadInt32(&baseCalls))
}

func TestBuildClient_WithProxySkipsValidatedTransport(t *testing.T) {
	originalValidate := validateResolvedIP
	defer func() { validateResolvedIP = originalValidate }()

	var validateCalls int32
	validateResolvedIP = func(host string) error {
		atomic.AddInt32(&validateCalls, 1)
		return errors.New("must not locally resolve proxied host: " + host)
	}

	client, err := buildClient(Options{
		ProxyURL:           "socks5://proxy.local:1080",
		ValidateResolvedIP: true,
		AllowPrivateHosts:  false,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	_, isValidated := client.Transport.(*validatedTransport)
	require.False(t, isValidated, "proxied clients must not wrap transport with local DNS validation")
	require.Equal(t, int32(0), atomic.LoadInt32(&validateCalls))
}

func TestGetClient_NormalizesSOCKS5ProxyCacheKey(t *testing.T) {
	sharedClients = sync.Map{}
	t.Cleanup(func() { sharedClients = sync.Map{} })

	first, err := GetClient(Options{ProxyURL: "socks5://proxy.local:1080", Timeout: time.Second})
	require.NoError(t, err)
	second, err := GetClient(Options{ProxyURL: "socks5h://proxy.local:1080", Timeout: time.Second})
	require.NoError(t, err)

	require.Same(t, first, second)

	var count int
	sharedClients.Range(func(_, _ any) bool {
		count++
		return true
	})
	require.Equal(t, 1, count)
}

func TestGetClient_NormalizesHTTPProxyCacheKey(t *testing.T) {
	sharedClients = sync.Map{}
	t.Cleanup(func() { sharedClients = sync.Map{} })

	first, err := GetClient(Options{ProxyURL: "HTTP://PROXY.LOCAL:80/path?x=1#frag", Timeout: time.Second})
	require.NoError(t, err)
	second, err := GetClient(Options{ProxyURL: "http://proxy.local", Timeout: time.Second})
	require.NoError(t, err)

	require.Same(t, first, second)

	var count int
	sharedClients.Range(func(_, _ any) bool {
		count++
		return true
	})
	require.Equal(t, 1, count)
}
