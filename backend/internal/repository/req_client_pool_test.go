package repository

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func forceHTTPVersion(t *testing.T, client *req.Client) string {
	t.Helper()
	transport := client.GetTransport()
	field := reflect.ValueOf(transport).Elem().FieldByName("forceHttpVersion")
	require.True(t, field.IsValid(), "forceHttpVersion field not found")
	require.True(t, field.CanAddr(), "forceHttpVersion field not addressable")
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().String()
}

func TestGetSharedReqClient_ForceHTTP2SeparatesCache(t *testing.T) {
	sharedReqClients = sync.Map{}
	base := reqClientOptions{
		ProxyURL: "http://proxy.local:8080",
		Timeout:  time.Second,
	}
	clientDefault, err := getSharedReqClient(base)
	require.NoError(t, err)

	force := base
	force.ForceHTTP2 = true
	clientForce, err := getSharedReqClient(force)
	require.NoError(t, err)

	require.NotSame(t, clientDefault, clientForce)
	require.NotEqual(t, buildReqClientKey(base), buildReqClientKey(force))
}

func TestGetSharedReqClient_ReuseCachedClient(t *testing.T) {
	sharedReqClients = sync.Map{}
	opts := reqClientOptions{
		ProxyURL: "http://proxy.local:8080",
		Timeout:  2 * time.Second,
	}
	first, err := getSharedReqClient(opts)
	require.NoError(t, err)
	second, err := getSharedReqClient(opts)
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestGetSharedReqClient_IgnoresNonClientCache(t *testing.T) {
	sharedReqClients = sync.Map{}
	opts := reqClientOptions{
		ProxyURL: " http://proxy.local:8080 ",
		Timeout:  3 * time.Second,
	}
	key := buildReqClientKey(opts)
	sharedReqClients.Store(key, "invalid")

	client, err := getSharedReqClient(opts)
	require.NoError(t, err)

	require.NotNil(t, client)
	loaded, ok := sharedReqClients.Load(key)
	require.True(t, ok)
	require.IsType(t, "invalid", loaded)
}

func TestGetSharedReqClient_ImpersonateAndProxy(t *testing.T) {
	sharedReqClients = sync.Map{}
	opts := reqClientOptions{
		ProxyURL:    "  http://proxy.local:8080  ",
		Timeout:     4 * time.Second,
		Impersonate: true,
	}
	client, err := getSharedReqClient(opts)
	require.NoError(t, err)

	require.NotNil(t, client)
	require.Equal(t, "http://proxy.local:8080|4s|true|false", buildReqClientKey(opts))
}

func TestGetSharedReqClient_NormalizesSOCKS5CacheKey(t *testing.T) {
	sharedReqClients = sync.Map{}
	socks5 := reqClientOptions{ProxyURL: "socks5://proxy.local:1080", Timeout: time.Second}
	socks5h := reqClientOptions{ProxyURL: "socks5h://proxy.local:1080", Timeout: time.Second}

	first, err := getSharedReqClient(socks5)
	require.NoError(t, err)
	second, err := getSharedReqClient(socks5h)
	require.NoError(t, err)

	require.Same(t, first, second)
}

func TestGetSharedReqClient_NormalizesHTTPProxyCacheKey(t *testing.T) {
	sharedReqClients = sync.Map{}

	first, err := getSharedReqClient(reqClientOptions{ProxyURL: "HTTP://PROXY.LOCAL:80/path?x=1#frag", Timeout: time.Second})
	require.NoError(t, err)
	second, err := getSharedReqClient(reqClientOptions{ProxyURL: "http://proxy.local", Timeout: time.Second})
	require.NoError(t, err)

	require.Same(t, first, second)
}

func TestGetSharedReqClient_SOCKS5HRemoteDNSHandshake(t *testing.T) {
	sharedReqClients = sync.Map{}
	capturedHost := make(chan string, 1)
	capturedATYP := make(chan byte, 1)
	proxyAddr := startReqSOCKS5CaptureServer(t, capturedHost, capturedATYP)

	client, err := getSharedReqClient(reqClientOptions{
		ProxyURL: "socks5://" + proxyAddr,
		Timeout:  2 * time.Second,
	})
	require.NoError(t, err)

	resp, err := client.R().Get("https://dns-only.invalid/")
	if resp != nil && resp.Response != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)

	require.Equal(t, byte(0x03), <-capturedATYP, "req SOCKS CONNECT target must use FQDN address type")
	require.Equal(t, "dns-only.invalid", <-capturedHost, "req client must send target host to proxy without local DNS resolution")
}

func TestGetSharedReqClient_InvalidProxyURL(t *testing.T) {
	sharedReqClients = sync.Map{}
	opts := reqClientOptions{
		ProxyURL: "://missing-scheme",
		Timeout:  time.Second,
	}
	_, err := getSharedReqClient(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid proxy URL")
}

func startReqSOCKS5CaptureServer(t *testing.T, hostCh chan<- string, atypCh chan<- byte) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := handleReqSOCKS5Capture(conn, hostCh, atypCh); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Logf("SOCKS5 capture server error: %v", err)
		}
	}()

	return listener.Addr().String()
}

func handleReqSOCKS5Capture(conn net.Conn, hostCh chan<- string, atypCh chan<- byte) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}

	reqHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHeader); err != nil {
		return err
	}
	atyp := reqHeader[3]
	atypCh <- atyp

	var host string
	switch atyp {
	case 0x01:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return err
		}
		host = net.IP(ip).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return err
		}
		host = string(name)
	case 0x04:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return err
		}
		host = net.IP(ip).String()
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return err
	}
	_ = binary.BigEndian.Uint16(port)
	hostCh <- host

	_, _ = conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return nil
}

func TestGetSharedReqClient_ProxyURLMissingHost(t *testing.T) {
	sharedReqClients = sync.Map{}
	opts := reqClientOptions{
		ProxyURL: "http://",
		Timeout:  time.Second,
	}
	_, err := getSharedReqClient(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy URL missing host")
}

func TestCreateOpenAIReqClient_Timeout120Seconds(t *testing.T) {
	sharedReqClients = sync.Map{}
	client, err := createOpenAIReqClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, 120*time.Second, client.GetClient().Timeout)
}

func TestCreateReqClient_NormalizesSOCKS5CacheKey(t *testing.T) {
	sharedReqClients = sync.Map{}
	first, err := createReqClient("socks5://proxy.local:1080")
	require.NoError(t, err)
	second, err := createReqClient("socks5h://proxy.local:1080")
	require.NoError(t, err)
	require.Same(t, first, second)
}

func TestCreateGeminiReqClient_ForceHTTP2Disabled(t *testing.T) {
	sharedReqClients = sync.Map{}
	client, err := createGeminiReqClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, "", forceHTTPVersion(t, client))
}
