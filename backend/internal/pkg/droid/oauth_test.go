package droid

import (
	"net/http"
	"testing"
)

func TestNewHTTPClient_SOCKS5UsesDialContext(t *testing.T) {
	client, err := newHTTPClient("socks5://proxy.example.com:1080")
	if err != nil {
		t.Fatalf("newHTTPClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("socks5 proxy should not use Transport.Proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("socks5 proxy should configure DialContext")
	}
}
