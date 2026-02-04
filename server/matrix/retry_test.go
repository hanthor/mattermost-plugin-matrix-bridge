package matrix

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultRetryConfig verifies default retry configuration
func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	assert.Equal(t, 3, config.MaxRetries, "Should have 3 max retries")
	assert.Equal(t, 100*time.Millisecond, config.InitialBackoff, "Initial backoff should be 100ms")
	assert.Equal(t, 30*time.Second, config.MaxBackoff, "Max backoff should be 30s")
	assert.Equal(t, 2.0, config.Multiplier, "Multiplier should be 2.0")
	assert.True(t, config.Jitter, "Jitter should be enabled")
}

// TestDisabledRetryConfig verifies disabled retry configuration
func TestDisabledRetryConfig(t *testing.T) {
	config := DisabledRetryConfig()
	assert.Equal(t, 0, config.MaxRetries, "Disabled config should have 0 max retries")
}

// TestIsRetriableStatusCode tests status code retry logic
func TestIsRetriableStatusCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		retriable  bool
	}{
		{"429 Too Many Requests", http.StatusTooManyRequests, true},
		{"408 Request Timeout", http.StatusRequestTimeout, true},
		{"503 Service Unavailable", http.StatusServiceUnavailable, true},
		{"504 Gateway Timeout", http.StatusGatewayTimeout, true},
		{"502 Bad Gateway", http.StatusBadGateway, true},
		{"500 Internal Server Error", http.StatusInternalServerError, true},
		{"200 OK", http.StatusOK, false},
		{"400 Bad Request", http.StatusBadRequest, false},
		{"401 Unauthorized", http.StatusUnauthorized, false},
		{"403 Forbidden", http.StatusForbidden, false},
		{"404 Not Found", http.StatusNotFound, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetriableStatusCode(tt.statusCode)
			assert.Equal(t, tt.retriable, result, "Status code %d should be retriable=%v", tt.statusCode, tt.retriable)
		})
	}
}

// TestIsRetriableError tests error retry logic
func TestIsRetriableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retriable bool
	}{
		{"nil error", nil, false},
		{"generic error", errors.New("test error"), true},
		{"connection error", errors.New("connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetriableError(tt.err)
			assert.Equal(t, tt.retriable, result)
		})
	}
}

// TestCalculateBackoff tests exponential backoff calculation
func TestCalculateBackoff(t *testing.T) {
	config := RetryConfig{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		Multiplier:     2.0,
		Jitter:         false, // Disable jitter for predictable tests
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},   // 100ms * 2^0 = 100ms
		{1, 200 * time.Millisecond},   // 100ms * 2^1 = 200ms
		{2, 400 * time.Millisecond},   // 100ms * 2^2 = 400ms
		{3, 800 * time.Millisecond},   // 100ms * 2^3 = 800ms
		{4, 1600 * time.Millisecond},  // 100ms * 2^4 = 1600ms
		{10, 10 * time.Second},        // Should be capped at MaxBackoff
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			backoff := calculateBackoff(config, tt.attempt)
			assert.Equal(t, tt.expected, backoff, "Attempt %d should have backoff %v", tt.attempt, tt.expected)
		})
	}
}

// TestCalculateBackoffWithJitter tests that jitter adds randomization
func TestCalculateBackoffWithJitter(t *testing.T) {
	config := RetryConfig{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     10 * time.Second,
		Multiplier:     2.0,
		Jitter:         true,
	}

	// Run multiple times to verify jitter adds variation
	backoffs := make(map[time.Duration]bool)
	for i := 0; i < 10; i++ {
		backoff := calculateBackoff(config, 1)
		backoffs[backoff] = true

		// Should be within ±25% of 2 seconds
		assert.Greater(t, backoff, 1500*time.Millisecond, "Backoff with jitter should be >= 1.5s")
		assert.Less(t, backoff, 2500*time.Millisecond, "Backoff with jitter should be <= 2.5s")
	}

	// With jitter, we should get different values (though this could theoretically fail due to randomness)
	assert.Greater(t, len(backoffs), 1, "Jitter should produce different backoff values")
}

// TestShouldRetry tests the retry decision logic
func TestShouldRetry(t *testing.T) {
	config := DefaultRetryConfig()

	tests := []struct {
		name         string
		err          error
		statusCode   int
		attempt      int
		shouldRetry  bool
		hasBackoff   bool
	}{
		{"429 on first attempt", nil, 429, 0, true, true},
		{"500 on first attempt", nil, 500, 0, true, true},
		{"503 on first attempt", nil, 503, 0, true, true},
		{"400 on first attempt", nil, 400, 0, false, false},
		{"404 on first attempt", nil, 404, 0, false, false},
		{"Max retries exceeded", nil, 429, 3, false, false},
		{"Network error on first attempt", errors.New("connection refused"), 0, 0, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry, backoff := shouldRetry(tt.err, tt.statusCode, tt.attempt, config)
			assert.Equal(t, tt.shouldRetry, retry, "Retry decision mismatch")
			if tt.hasBackoff {
				assert.Greater(t, backoff, time.Duration(0), "Should have non-zero backoff")
			} else {
				assert.Equal(t, time.Duration(0), backoff, "Should have zero backoff")
			}
		})
	}
}

// TestWithRetrySuccess tests that successful requests don't retry
func TestWithRetrySuccess(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	attempts := 0
	requestFunc := func() (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK,
		}, nil
	}

	resp, err := client.withRetry(requestFunc, "test_operation")

	require.NoError(t, err, "Should not error on success")
	require.NotNil(t, resp, "Should return response")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return OK status")
	assert.Equal(t, 1, attempts, "Should only make one attempt on success")
}

// TestWithRetryRetriableError tests retry behavior with retriable errors
func TestWithRetryRetriableError(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	attempts := 0
	requestFunc := func() (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable}, nil
		}
		return &http.Response{StatusCode: http.StatusOK}, nil
	}

	startTime := time.Now()
	resp, err := client.withRetry(requestFunc, "test_operation")
	duration := time.Since(startTime)

	require.NoError(t, err, "Should succeed after retries")
	require.NotNil(t, resp, "Should return response")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should eventually succeed")
	assert.Equal(t, 3, attempts, "Should retry twice before success")
	
	// Should have taken at least the sum of backoffs (100ms + 200ms = 300ms approximately)
	assert.Greater(t, duration, 200*time.Millisecond, "Should have waited during retries")
}

// TestWithRetryNonRetriableError tests that non-retriable errors don't retry
func TestWithRetryNonRetriableError(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	attempts := 0
	requestFunc := func() (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusBadRequest}, nil
	}

	resp, err := client.withRetry(requestFunc, "test_operation")

	require.NoError(t, err, "Should not error (just return bad response)")
	require.NotNil(t, resp, "Should return response")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Should return original status")
	assert.Equal(t, 1, attempts, "Should not retry non-retriable status")
}

// TestWithRetryMaxRetriesExhausted tests behavior when all retries are exhausted
func TestWithRetryMaxRetriesExhausted(t *testing.T) {
	client := NewClientWithLoggerAndRateLimit(
		"https://matrix.example.com",
		"test_token",
		"test_remote",
		NewTestLogger(t),
		TestRateLimitConfig(),
	)

	attempts := 0
	requestFunc := func() (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusServiceUnavailable}, nil
	}

	resp, err := client.withRetry(requestFunc, "test_operation")

	require.Error(t, err, "Should error when retries exhausted")
	assert.Contains(t, err.Error(), "after 3 retries", "Error should mention retry count")
	assert.Nil(t, resp, "Should not return response")
	assert.Equal(t, 4, attempts, "Should attempt original + 3 retries = 4 total")
}
