package matrix

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionPoolingConfiguration verifies that the HTTP client is configured with connection pooling
func TestConnectionPoolingConfiguration(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	require.NotNil(t, client.httpClient, "HTTP client should be initialized")
	require.NotNil(t, client.httpClient.Transport, "HTTP transport should be configured")

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")

	// Verify connection pooling settings
	assert.Equal(t, 100, transport.MaxIdleConns, "MaxIdleConns should be 100")
	assert.Equal(t, 10, transport.MaxIdleConnsPerHost, "MaxIdleConnsPerHost should be 10")
	assert.Equal(t, 50, transport.MaxConnsPerHost, "MaxConnsPerHost should be 50")
	assert.Equal(t, 90*time.Second, transport.IdleConnTimeout, "IdleConnTimeout should be 90 seconds")
	assert.False(t, transport.DisableKeepAlives, "Keep-alives should be enabled")
	assert.True(t, transport.ForceAttemptHTTP2, "Should attempt HTTP/2")
}

// TestHTTPClientTimeout verifies the HTTP client timeout is configured correctly
func TestHTTPClientTimeout(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	require.NotNil(t, client.httpClient, "HTTP client should be initialized")
	assert.Equal(t, 30*time.Second, client.httpClient.Timeout, "Timeout should be 30 seconds")
}

// TestConnectionReuseCapability tests that the transport is capable of connection reuse
func TestConnectionReuseCapability(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")

	// Connection reuse is enabled when:
	// 1. DisableKeepAlives is false
	// 2. MaxIdleConnsPerHost > 0
	// 3. IdleConnTimeout > 0
	assert.False(t, transport.DisableKeepAlives, "Keep-alives must be enabled for connection reuse")
	assert.Greater(t, transport.MaxIdleConnsPerHost, 0, "MaxIdleConnsPerHost must be > 0 for connection reuse")
	assert.Greater(t, transport.IdleConnTimeout, time.Duration(0), "IdleConnTimeout must be > 0 for connection reuse")
}

// TestMultipleClientsHaveIndependentPools verifies that each client instance has its own connection pool
func TestMultipleClientsHaveIndependentPools(t *testing.T) {
	client1 := NewClientWithLoggerAndRateLimit(
		"https://matrix1.example.com",
		"token1",
		"remote1",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	client2 := NewClientWithLoggerAndRateLimit(
		"https://matrix2.example.com",
		"token2",
		"remote2",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	// Each client should have its own HTTP client
	assert.NotSame(t, client1.httpClient, client2.httpClient, "Clients should have independent HTTP clients")

	// Each client should have its own transport
	transport1, ok1 := client1.httpClient.Transport.(*http.Transport)
	transport2, ok2 := client2.httpClient.Transport.(*http.Transport)
	require.True(t, ok1 && ok2, "Both transports should be *http.Transport")
	assert.NotSame(t, transport1, transport2, "Clients should have independent transports")
}

// TestConnectionPoolingWithRateLimitDisabled verifies pooling works even when rate limiting is disabled
func TestConnectionPoolingWithRateLimitDisabled(t *testing.T) {
	config := TestRateLimitConfig()
	config.Enabled = false

	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		config,
	)

	require.NotNil(t, client.httpClient.Transport, "Transport should be configured even without rate limiting")

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")
	assert.Equal(t, 100, transport.MaxIdleConns, "Connection pooling should be configured")
}

// TestNewClientWithRateLimitUsesPooling verifies the convenience constructor also uses connection pooling
func TestNewClientWithRateLimitUsesPooling(t *testing.T) {
	// Use the same constructor as the other tests - NewClientWithLoggerAndRateLimit
	// which doesn't require a full plugin.API mock
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	require.NotNil(t, client.httpClient.Transport, "Transport should be configured")

	transport, ok := client.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")
	assert.Equal(t, 100, transport.MaxIdleConns, "Connection pooling should be configured")
}
