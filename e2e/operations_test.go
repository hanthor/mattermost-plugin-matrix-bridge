package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
)

// TestOperations covers what an operator relies on: metrics for Prometheus
// and homeserver retries that are never handled twice, even across a restart
// of the plugin.
func TestOperations(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("operations", "Operations")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Operations")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	pluginURL := h.Mattermost.URL + "/plugins/" + PluginID
	call := func(method, path, token, body string) (int, string) {
		req, err := http.NewRequest(method, pluginURL+path, strings.NewReader(body))
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}

	t.Run("metrics are served to Prometheus with the homeserver token", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Count me"})
		require.NoError(t, err)
		WaitForMatrixMessage(t, alice, roomID, "Count me")

		status, _ := call(http.MethodGet, "/metrics", "", "")
		assert.Equal(t, http.StatusUnauthorized, status)
		status, _ = call(http.MethodGet, "/metrics", "not-the-token", "")
		assert.Equal(t, http.StatusForbidden, status)

		status, body := call(http.MethodGet, "/metrics", h.HSToken, "")
		require.Equal(t, http.StatusOK, status, body)
		assert.Contains(t, body, "# TYPE matrix_bridge_messages_to_matrix_total counter")
		assert.Regexp(t, `matrix_bridge_messages_to_matrix_total [1-9]`, body)
		assert.Contains(t, body, "matrix_bridge_rate_limit_factor 1")
		capabilities.Verify(t, "ops.metrics", strings.Contains(body, "matrix_bridge_messages_to_matrix_total"))
	})

	t.Run("a retried transaction is handled once, also after the plugin restarts", func(t *testing.T) {
		txn := fmt.Sprintf(`{"events":[{"type":"m.room.message","event_id":"$replayed-event","sender":%q,"room_id":%q,"origin_server_ts":%d,"content":{"msgtype":"m.text","body":"Delivered twice"}}]}`,
			alice.UserID, roomID, time.Now().UnixMilli())
		send := func() {
			status, body := call(http.MethodPut, "/_matrix/app/v1/transactions/replay-1", h.HSToken, txn)
			require.Equal(t, http.StatusOK, status, body)
		}

		send()
		WaitForPostWithMessage(t, h.Admin, channel.Id, "Delivered twice")
		send()

		// The plugin restarts and the homeserver retries once more.
		_, err := h.Admin.DisablePlugin(ctx, PluginID)
		require.NoError(t, err)
		_, err = h.Admin.EnablePlugin(ctx, PluginID)
		require.NoError(t, err)
		WaitFor(t, bridgeTimeout, "the plugin to answer again", func() bool {
			status, _ := call(http.MethodGet, "/metrics", h.HSToken, "")
			return status == http.StatusOK
		})
		send()

		time.Sleep(3 * time.Second)
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, "Delivered twice"))
		capabilities.Verify(t, "ops.txn_dedup", CountPostsWithMessage(t, h.Admin, channel.Id, "Delivered twice") == 1)
	})
}

// TestHomeserverRateLimitIsHonoured points the bridge's Matrix client at a
// homeserver that answers 429 with a retry time, and checks the message still
// gets through once the time has passed. (A stand-in server: the Complement
// Synapse is configured with limits too high to reach.)
func TestHomeserverRateLimitIsHonoured(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errcode":"M_LIMIT_EXCEEDED","error":"Too Many Requests","retry_after_ms":300}`))
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"$delivered"}`))
	}))
	defer srv.Close()

	client := matrix.NewClientWithLoggerAndRateLimit(srv.URL, "as-token", "remote", matrix.NewTestLogger(t), matrix.TestRateLimitConfig())
	client.SetServerDomain("hs1")

	start := time.Now()
	resp, err := client.SendMessage(matrix.MessageRequest{RoomID: "!room:hs1", GhostUserID: "@_mattermost_x:hs1", Message: "through the limit"})
	require.NoError(t, err)
	assert.Equal(t, "$delivered", resp.EventID)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond, "it waited the time the homeserver named")
	assert.Equal(t, uint64(1), client.RateLimitHits())
	assert.Less(t, client.RateLimitFactor(), 1.0, "and slowed down")
	capabilities.Verify(t, "ops.rate_limit_adapt", resp.EventID == "$delivered")
}
