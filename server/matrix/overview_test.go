package matrix

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRoomOverview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer as_token", r.Header.Get("Authorization"))
		switch r.URL.EscapedPath() {
		case "/_matrix/client/v3/rooms/%21room:hs/joined_members":
			_, _ = w.Write([]byte(`{"joined":{"@a:hs":{},"@b:hs":{},"@c:hs":{}}}`))
		case "/_matrix/client/v3/rooms/%21room:hs/state/m.room.name/":
			_, _ = w.Write([]byte(`{"name":"Bridge Demo"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClientWithLoggerAndRateLimit(srv.URL, "as_token", "remote", NewTestLogger(t), TestRateLimitConfig())
	got, err := c.GetRoomOverview("!room:hs")
	require.NoError(t, err)
	assert.Equal(t, RoomOverview{Name: "Bridge Demo", Members: 3}, got)
}

func TestGetRoomOverviewWithoutAName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_matrix/client/v3/rooms/!room:hs/joined_members" {
			_, _ = w.Write([]byte(`{"joined":{"@a:hs":{}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClientWithLoggerAndRateLimit(srv.URL, "as_token", "remote", NewTestLogger(t), TestRateLimitConfig())
	got, err := c.GetRoomOverview("!room:hs")
	require.NoError(t, err)
	assert.Equal(t, 1, got.Members)
	assert.Empty(t, got.Name)
}

func TestGetRoomOverviewFailsWhenMembersCannotBeListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	c := NewClientWithLoggerAndRateLimit(srv.URL, "as_token", "remote", NewTestLogger(t), TestRateLimitConfig())
	_, err := c.GetRoomOverview("!room:hs")
	assert.Error(t, err)
}

func TestMessagesCanCarryAnOriginalTimestamp(t *testing.T) {
	var got struct{ ts, userID string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.ts = r.URL.Query().Get("ts")
		got.userID = r.URL.Query().Get("user_id")
		_, _ = w.Write([]byte(`{"event_id":"$e1"}`))
	}))
	defer srv.Close()

	c := NewClientWithLoggerAndRateLimit(srv.URL, "as_token", "remote", NewTestLogger(t), TestRateLimitConfig())
	_, err := c.SendMessage(MessageRequest{RoomID: "!r:hs", GhostUserID: "@ada:hs", Message: "from last year", Timestamp: 1700000000123})
	require.NoError(t, err)
	assert.Equal(t, "1700000000123", got.ts)
	assert.Equal(t, "@ada:hs", got.userID)

	got.ts = "unset"
	_, err = c.SendMessage(MessageRequest{RoomID: "!r:hs", GhostUserID: "@ada:hs", Message: "now"})
	require.NoError(t, err)
	assert.Empty(t, got.ts, "no timestamp parameter unless one is asked for")
}
