package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsAreServedInPrometheusFormatToTheHomeserverToken(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	p.configuration.EnableSync = true
	p.configuration.MatrixHSToken = "hs-secret"
	p.metrics.RecordMessageToMatrix()
	p.metrics.RecordMessageToMatrix()
	p.metrics.RecordError("send")

	get := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		p.ServeHTTP(nil, rec, req)
		return rec
	}

	assert.Equal(t, http.StatusUnauthorized, get("").Code, "no credentials")
	assert.Equal(t, http.StatusForbidden, get("wrong").Code)

	rec := get("hs-secret")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4"))
	body := rec.Body.String()
	assert.Contains(t, body, "# TYPE matrix_bridge_messages_to_matrix_total counter\nmatrix_bridge_messages_to_matrix_total 2\n")
	assert.Contains(t, body, `matrix_bridge_errors_total{type="send"} 1`)
	assert.Contains(t, body, "# TYPE matrix_bridge_uptime_seconds gauge")
	assert.Contains(t, body, "matrix_bridge_encryption_ready 0")
	assert.Contains(t, body, "matrix_bridge_rate_limit_factor 1")
	assert.Contains(t, body, "matrix_bridge_rate_limit_responses_total 0")
}

func TestPrometheusSamplesAreGroupedUnderOneHeader(t *testing.T) {
	out := renderPrometheus([]promSample{
		{name: "x_total", help: "h", kind: "counter", value: 1, labels: map[string]string{"a": "1"}},
		{name: "x_total", help: "h", kind: "counter", value: 2, labels: map[string]string{"a": "2"}},
	})
	assert.Equal(t, 1, strings.Count(out, "# TYPE x_total"))
	assert.Contains(t, out, `x_total{a="1"} 1`)
	assert.Contains(t, out, `x_total{a="2"} 2`)
}

func TestADedicatedMetricsTokenReplacesTheHomeserverToken(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	p.configuration.EnableSync = true
	p.configuration.MatrixHSToken = "hs-secret"
	p.configuration.MetricsToken = "metrics-secret"

	status := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		p.ServeHTTP(nil, rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, status("metrics-secret"))
	assert.Equal(t, http.StatusForbidden, status("hs-secret"), "whoever scrapes metrics must not hold the homeserver token")

	// The metrics token does not open the appservice routes either.
	req := httptest.NewRequest(http.MethodPost, "/_matrix/app/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer metrics-secret")
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
