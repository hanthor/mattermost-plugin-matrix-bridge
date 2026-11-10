package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

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

	t.Run("an event left in the journal by a crashed run is delivered after a restart", func(t *testing.T) {
		// The state a crash leaves: acknowledged by the bridge, written to the
		// journal, never handled. Put it there directly, then restart the plugin.
		entry := fmt.Sprintf(`{"event":{"type":"m.room.message","event_id":"$left-behind","sender":%q,"room_id":%q,"origin_server_ts":%d,"content":{"msgtype":"m.text","body":"Survived the crash"}},"received":%d}`,
			alice.UserID, roomID, time.Now().UnixMilli(), time.Now().Add(-time.Minute).Unix())
		h.Mattermost.ExecSQL(t, fmt.Sprintf(`INSERT INTO pluginkeyvaluestore (pluginid, pkey, pvalue, expireat) VALUES ('%s', 'inbox_$left-behind', convert_to('%s', 'UTF8'), 0)`, PluginID, entry))

		_, err := h.Admin.DisablePlugin(ctx, PluginID)
		require.NoError(t, err)
		_, err = h.Admin.EnablePlugin(ctx, PluginID)
		require.NoError(t, err)

		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Survived the crash")
		assert.NotEmpty(t, post.Id)
		capabilities.Verify(t, "ops.inbound_journal", post.Id != "")
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
	t.Run("a dedicated metrics token replaces the homeserver token", func(t *testing.T) {
		h.SetBridgeSetting("metrics_token", "metrics-only-token")
		WaitFor(t, bridgeTimeout, "the metrics token to apply", func() bool {
			status, _ := call(http.MethodGet, "/metrics", "metrics-only-token", "")
			return status == http.StatusOK
		})
		status, _ := call(http.MethodGet, "/metrics", h.HSToken, "")
		assert.Equal(t, http.StatusForbidden, status, "whoever scrapes metrics does not need the homeserver token")
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

// TestMattermostChangesSurviveAHomeserverOutage stops the homeserver, posts in
// Mattermost, starts it again, and expects the posts to arrive in order
// without anyone running a command.
func TestMattermostChangesSurviveAHomeserverOutage(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("outage", "Outage")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Outage")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	aliceID, aliceToken := alice.UserID, alice.AccessToken

	h.Deployment.StopServer(t, HSName)
	for _, text := range []string{"first post during the outage", "second post during the outage"} {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: text})
		require.NoError(t, err, "Mattermost itself keeps working while Matrix is down")
		time.Sleep(time.Second)
	}
	time.Sleep(5 * time.Second)
	h.Deployment.StartServer(t, HSName)

	// The homeserver is on a new port now; Alice's session is the same.
	back := h.Deployment.UnauthenticatedClient(t, HSName)
	back.UserID, back.AccessToken = aliceID, aliceToken

	var order []string
	WaitFor(t, 3*time.Minute, "both posts to reach the room", func() bool {
		res := back.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "messages"},
			client.WithQueries(url.Values{"dir": {"f"}, "limit": {"100"}, "from": {"s0"}}))
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return false
		}
		order = order[:0]
		gjson.GetBytes(client.ParseJSON(t, res), "chunk").ForEach(func(_, ev gjson.Result) bool {
			if ev.Get("type").Str == "m.room.message" {
				order = append(order, ev.Get("content.body").Str)
			}
			return true
		})
		return len(order) >= 2
	})
	assert.Equal(t, []string{"first post during the outage", "second post during the outage"}, order)
	capabilities.Verify(t, "ops.outbox", len(order) == 2)
}
