package matrix

import (
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/pkg/errors"
)

// RetryConfig configures retry behavior for HTTP requests
type RetryConfig struct {
	MaxRetries     int           // Maximum number of retry attempts
	InitialBackoff time.Duration // Initial backoff duration
	MaxBackoff     time.Duration // Maximum backoff duration
	Multiplier     float64       // Backoff multiplier
	Jitter         bool          // Add randomization to backoff
}

// DefaultRetryConfig returns sensible defaults for retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
		Jitter:         true,
	}
}

// DisabledRetryConfig returns a config with retries disabled
func DisabledRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 0,
	}
}

// isRetriableStatusCode determines if an HTTP status code warrants a retry
func isRetriableStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests:      // 429 - Rate limit
		return true
	case http.StatusRequestTimeout:       // 408
		return true
	case http.StatusServiceUnavailable:   // 503 - Service temporarily unavailable
		return true
	case http.StatusGatewayTimeout:       // 504
		return true
	case http.StatusBadGateway:           // 502 - May be transient
		return true
	case http.StatusInternalServerError:  // 500 - May be transient
		return true
	default:
		return false
	}
}

// isRetriableError determines if a non-HTTP error warrants a retry
func isRetriableError(err error) bool {
	if err == nil {
		return false
	}

	// Network errors, timeouts, connection refused are typically retriable
	// This is a simplified check - could be expanded with more specific error types
	return true // For now, treat all non-HTTP errors as potentially retriable
}

// calculateBackoff calculates the backoff duration for a given attempt
func calculateBackoff(config RetryConfig, attempt int) time.Duration {
	if attempt <= 0 {
		return config.InitialBackoff
	}

	// Exponential backoff: initialBackoff * multiplier^attempt
	backoff := float64(config.InitialBackoff) * math.Pow(config.Multiplier, float64(attempt))

	// Cap at max backoff
	if backoff > float64(config.MaxBackoff) {
		backoff = float64(config.MaxBackoff)
	}

	duration := time.Duration(backoff)

	// Add jitter if enabled (±25% randomization)
	if config.Jitter {
		jitterRange := float64(duration) * 0.25
		jitter := (rand.Float64() * 2 * jitterRange) - jitterRange
		duration = time.Duration(float64(duration) + jitter)
	}

	return duration
}

// shouldRetry determines if a request should be retried based on error and config
func shouldRetry(err error, statusCode int, attempt int, config RetryConfig) (bool, time.Duration) {
	// Check if we've exceeded max retries
	if attempt >= config.MaxRetries {
		return false, 0
	}

	// Determine if the error/status is retriable
	retriable := false
	if statusCode > 0 {
		retriable = isRetriableStatusCode(statusCode)
	} else if err != nil {
		retriable = isRetriableError(err)
	}

	if !retriable {
		return false, 0
	}

	// Calculate backoff
	backoff := calculateBackoff(config, attempt)
	return true, backoff
}

// withRetry wraps an HTTP request with retry logic
func (c *Client) withRetry(requestFunc func() (*http.Response, error), operationName string) (*http.Response, error) {
	config := c.getRetryConfig()
	var lastErr error
	var lastStatusCode int

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// Execute the request
		resp, err := requestFunc()

		// Success case - return immediately
		if err == nil && resp != nil && resp.StatusCode < 400 {
			return resp, nil
		}

		// Capture error and status code for retry decision
		lastErr = err
		if resp != nil {
			lastStatusCode = resp.StatusCode
			// Close response body to avoid connection leak
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}

		// Determine if we should retry
		retry, backoff := shouldRetry(lastErr, lastStatusCode, attempt, config)
		if !retry {
			// Not retriable or max attempts reached
			if attempt >= config.MaxRetries && (lastErr != nil || lastStatusCode >= 400) {
				// We exhausted retries - return error
				if lastErr != nil {
					return nil, errors.Wrapf(lastErr, "request failed after %d retries", config.MaxRetries)
				}
				return nil, errors.Errorf("request failed with status %d after %d retries", lastStatusCode, config.MaxRetries)
			}
			
			// Not retriable but not max retries - return as-is
			if lastErr != nil {
				return resp, lastErr
			}
			return resp, nil
		}

		// Log retry attempt
		c.logger.LogWarn("Request failed, retrying",
			"operation", operationName,
			"attempt", attempt+1,
			"max_retries", config.MaxRetries,
			"backoff_ms", backoff.Milliseconds(),
			"status_code", lastStatusCode,
			"error", lastErr)

		// Wait before retrying
		time.Sleep(backoff)
	}

	// All retries exhausted
	if lastErr != nil {
		return nil, errors.Wrapf(lastErr, "request failed after %d retries", config.MaxRetries)
	}
	return nil, errors.Errorf("request failed with status %d after %d retries", lastStatusCode, config.MaxRetries)
}

// getRetryConfig returns the retry configuration for this client
// For now, returns a default config - could be made configurable via Client struct
func (c *Client) getRetryConfig() RetryConfig {
	// TODO: Make this configurable via Client initialization
	return DefaultRetryConfig()
}
