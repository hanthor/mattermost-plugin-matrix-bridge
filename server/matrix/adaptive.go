package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// A homeserver that answers 429 M_LIMIT_EXCEEDED says how long to wait
// (retry_after_ms). The fixed rate limiting modes are a guess at the
// homeserver's limits; this adapts to what it actually says: every request
// pauses until the stated time, the bridge's own limiters slow down while the
// homeserver keeps complaining and recover as it stops, and the request that
// was refused is repeated once the wait is over.

const (
	defaultRetryAfter   = time.Second
	maxRetryAfter       = 30 * time.Second
	maxRateLimitRetries = 3
	minRateFactor       = 0.1
	rateBackoff         = 0.5  // factor multiplier on each 429
	rateRecovery        = 0.05 // factor added per recoveryEvery successes
	recoveryEvery       = 20
)

// rateAdapter is shared by every request of one client.
type rateAdapter struct {
	mu         sync.Mutex
	pauseUntil time.Time
	factor     float64
	successes  int
	limiters   []*TokenBucket

	hits atomic.Uint64
}

func newRateAdapter() *rateAdapter { return &rateAdapter{factor: 1} }

// attach registers the limiters whose rate follows the factor.
func (a *rateAdapter) attach(limiters ...*TokenBucket) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, l := range limiters {
		if l != nil {
			a.limiters = append(a.limiters, l)
			l.SetRateFactor(a.factor)
		}
	}
}

// Factor is the share of the configured rate in use now.
func (a *rateAdapter) Factor() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.factor
}

// Hits counts the 429 responses seen.
func (a *rateAdapter) Hits() uint64 { return a.hits.Load() }

func (a *rateAdapter) applyFactor() {
	for _, l := range a.limiters {
		l.SetRateFactor(a.factor)
	}
}

// penalize is called on a 429: nothing is sent before the stated time and the
// limiters slow down.
func (a *rateAdapter) penalize(wait time.Duration) {
	a.hits.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	if until := time.Now().Add(wait); until.After(a.pauseUntil) {
		a.pauseUntil = until
	}
	a.factor *= rateBackoff
	if a.factor < minRateFactor {
		a.factor = minRateFactor
	}
	a.successes = 0
	a.applyFactor()
}

// success is called for each answer that is not a 429; after a run of them the
// limiters speed back up.
func (a *rateAdapter) success() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.factor >= 1 {
		return
	}
	a.successes++
	if a.successes >= recoveryEvery {
		a.successes = 0
		a.factor += rateRecovery
		if a.factor > 1 {
			a.factor = 1
		}
		a.applyFactor()
	}
}

// pause waits out a stated retry-after, if one is running.
func (a *rateAdapter) pause(ctx context.Context) error {
	a.mu.Lock()
	wait := time.Until(a.pauseUntil)
	a.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// retryAfter reads how long the homeserver asked to wait: retry_after_ms in
// the body, or a Retry-After header in seconds.
func retryAfter(h http.Header, body []byte) time.Duration {
	var parsed struct {
		RetryAfterMs int64 `json:"retry_after_ms"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.RetryAfterMs > 0 {
		return clampRetryAfter(time.Duration(parsed.RetryAfterMs) * time.Millisecond)
	}
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 {
		return clampRetryAfter(time.Duration(secs) * time.Second)
	}
	return defaultRetryAfter
}

func clampRetryAfter(d time.Duration) time.Duration {
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

// rateAwareTransport applies the adapter to every request.
type rateAwareTransport struct {
	base    http.RoundTripper
	adapter *rateAdapter
	logger  Logger
}

func (t *rateAwareTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := t.adapter.pause(req.Context()); err != nil {
			return nil, err
		}
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return resp, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			t.adapter.success()
			return resp, nil
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		wait := retryAfter(resp.Header, body)
		t.adapter.penalize(wait)
		if t.logger != nil {
			t.logger.LogWarn("Homeserver rate limited a request; slowing down", "retry_after_ms", wait.Milliseconds(), "attempt", attempt+1, "method", req.Method, "path", req.URL.Path)
		}

		// Repeat the request when its body can be sent again.
		if attempt+1 >= maxRateLimitRetries || (req.Body != nil && req.GetBody == nil) {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return resp, nil
		}
		if req.GetBody != nil {
			fresh, err := req.GetBody()
			if err != nil {
				resp.Body = io.NopCloser(bytes.NewReader(body))
				return resp, nil
			}
			req = req.Clone(req.Context())
			req.Body = fresh
		}
	}
}

// RateLimitFactor is the share of the configured rate the bridge currently
// uses: 1 normally, lower after the homeserver answered 429.
func (c *Client) RateLimitFactor() float64 {
	if c.adapter == nil {
		return 1
	}
	return c.adapter.Factor()
}

// RateLimitHits counts the 429 responses the homeserver has sent.
func (c *Client) RateLimitHits() uint64 {
	if c.adapter == nil {
		return 0
	}
	return c.adapter.Hits()
}
