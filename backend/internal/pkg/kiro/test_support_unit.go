//go:build unit

package kiro

func SetOIDCEndpointOverrideForTest(endpoint string) string {
	previous := oidcEndpointOverride
	oidcEndpointOverride = endpoint
	return previous
}
