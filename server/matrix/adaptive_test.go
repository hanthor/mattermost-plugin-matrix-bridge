package matrix

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetryAfterReadsTheBodyThenTheHeader(t *testing.T) {
	assert.Equal(t, 250*time.Millisecond, retryAfter(http.Header{}, []byte(`{"errcode":"M_LIMIT_EXCEEDED","retry_after_ms":250}`)))
	h := http.Header{}
	h.Set("Retry-After", "3")
	assert.Equal(t, 3*time.Second, retryAfter(h, []byte(`not json`)))
	assert.Equal(t, defaultRetryAfter, retryAfter(http.Header{}, nil))
	assert.Equal(t, maxRetryAfter, retryAfter(http.Header{}, []byte(`{"retry_after_ms":900000}`)), "a huge value is capped")
}

func TestARefusedRequestIsRepeatedWithItsBodyAfterTheStatedWait(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errcode":"M_LIMIT_EXCEEDED","retry_after_ms":60}`))
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"$ok"}`))
	}))
	defer srv.Close()
	adapter := newRateAdapter()
	limiter := NewTokenBucket(TokenBucketConfig{Rate: 10, BurstSize: 10})
	adapter.attach(limiter)
	client := &http.Client{Transport: &rateAwareTransport{base: http.DefaultTransport, adapter: adapter}}

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/send", strings.NewReader(`{"body":"hello"}`))
	require.NoError(t, err)
	start := time.Now()
	resp, err := client.Do(req)
	require.NoError(t, err)
	out, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(out), "$ok")
	assert.Equal(t, int32(3), calls.Load())
	assert.Equal(t, []string{`{"body":"hello"}`, `{"body":"hello"}`, `{"body":"hello"}`}, bodies, "the body is sent again each time")
	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond, "it waited for the time the server named")
	assert.Equal(t, uint64(2), adapter.Hits())
	assert.Less(t, adapter.Factor(), 1.0, "the bridge slowed down")
}

func TestRepeatedRefusalsAreReturnedToTheCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errcode":"M_LIMIT_EXCEEDED","retry_after_ms":1}`))
	}))
	defer srv.Close()
	client := &http.Client{Transport: &rateAwareTransport{base: http.DefaultTransport, adapter: newRateAdapter()}}

	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Contains(t, string(body), "M_LIMIT_EXCEEDED", "the caller still sees the homeserver's answer")
}

func TestTheRateRecoversAfterTheHomeserverStopsComplaining(t *testing.T) {
	adapter := newRateAdapter()
	limiter := NewTokenBucket(TokenBucketConfig{Rate: 10, BurstSize: 10})
	adapter.attach(limiter)

	adapter.penalize(time.Millisecond)
	adapter.penalize(time.Millisecond)
	assert.InDelta(t, 0.25, adapter.Factor(), 0.001)
	assert.InDelta(t, 0.25, limiter.factor, 0.001, "the limiter follows")

	for i := 0; i < recoveryEvery*30; i++ {
		adapter.success()
	}
	assert.InDelta(t, 1.0, adapter.Factor(), 0.001)
	assert.InDelta(t, 1.0, limiter.factor, 0.001)
}

func TestTheRateNeverDropsBelowTheFloor(t *testing.T) {
	adapter := newRateAdapter()
	for i := 0; i < 20; i++ {
		adapter.penalize(time.Millisecond)
	}
	assert.InDelta(t, minRateFactor, adapter.Factor(), 0.0001)
}

func TestASlowedBucketRefillsMoreSlowly(t *testing.T) {
	full := NewTokenBucket(TokenBucketConfig{Rate: 10, BurstSize: 1})
	slow := NewTokenBucket(TokenBucketConfig{Rate: 10, BurstSize: 1})
	slow.SetRateFactor(0.1)
	require.True(t, full.Allow())
	require.True(t, slow.Allow())

	assert.Less(t, full.getWaitTime(), slow.getWaitTime())
	assert.InDelta(t, float64(time.Second), float64(slow.getWaitTime()), float64(150*time.Millisecond))
}
