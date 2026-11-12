package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createdRoomBody returns the createRoom request body CreateDirectRoom sends.
func createdRoomBody(t *testing.T, encrypt func() bool) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"room_id":"!dm:example.com"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewClientWithLoggerAndRateLimit(srv.URL, "token", "remote", NewTestLogger(t), TestRateLimitConfig())
	client.SetEncryptDirectRooms(encrypt)
	_, err := client.CreateDirectRoom([]string{"@a:example.com", "@b:example.com"}, "")
	require.NoError(t, err)
	return body
}

func hasEncryptionState(body map[string]any) bool {
	states, _ := body["initial_state"].([]any)
	for _, s := range states {
		if m, ok := s.(map[string]any); ok && m["type"] == "m.room.encryption" {
			return true
		}
	}
	return false
}

func TestDirectRoomsStartEncryptedOnlyWhenAsked(t *testing.T) {
	t.Run("by default the room has no encryption setting", func(t *testing.T) {
		assert.False(t, hasEncryptionState(createdRoomBody(t, nil)))
	})
	t.Run("a check that says no leaves the room plain", func(t *testing.T) {
		assert.False(t, hasEncryptionState(createdRoomBody(t, func() bool { return false })))
	})
	t.Run("a check that says yes adds encryption next to the other state", func(t *testing.T) {
		body := createdRoomBody(t, func() bool { return true })
		assert.True(t, hasEncryptionState(body))
		states, _ := body["initial_state"].([]any)
		assert.Len(t, states, 2, "history visibility is kept")
	})
}
