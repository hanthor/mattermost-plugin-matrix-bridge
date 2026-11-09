package matrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// encryptedHS answers room encryption state for one room and counts sends.
func encryptedHS(t *testing.T, encryptedRoom string) (*httptest.Server, *int32) {
	t.Helper()
	var sends int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.EscapedPath(), "/state/m.room.encryption/"):
			if strings.Contains(r.URL.EscapedPath(), encryptedRoom) {
				_, _ = w.Write([]byte(`{"algorithm":"m.megolm.v1.aes-sha2"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/"):
			atomic.AddInt32(&sends, 1)
			_, _ = w.Write([]byte(`{"event_id":"$e"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &sends
}

func TestMessagesAreNotSentIntoEncryptedRoomsUnlessTheBridgeCanEncrypt(t *testing.T) {
	srv, sends := encryptedHS(t, "secret")
	c := NewClientWithLoggerAndRateLimit(srv.URL, "as", "remote", NewTestLogger(t), TestRateLimitConfig())

	_, err := c.SendMessage(MessageRequest{RoomID: "!secret:hs", GhostUserID: "@g:hs", Message: "hello"})
	require.ErrorIs(t, err, ErrEncryptedRoom)
	assert.Zero(t, atomic.LoadInt32(sends), "nothing was sent")

	_, err = c.SendMessage(MessageRequest{RoomID: "!open:hs", GhostUserID: "@g:hs", Message: "hello"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(sends))
}

func TestRedactionsAndStateStillGoIntoEncryptedRooms(t *testing.T) {
	srv, _ := encryptedHS(t, "secret")
	c := NewClientWithLoggerAndRateLimit(srv.URL, "as", "remote", NewTestLogger(t), TestRateLimitConfig())
	_, err := c.sendEventAsUser("!secret:hs", "m.room.redaction", map[string]any{}, "@g:hs")
	assert.NoError(t, err, "redactions are not encrypted")
}

func TestARoomThatTurnsEncryptionOnIsNoticedWithoutAskingAgain(t *testing.T) {
	srv, sends := encryptedHS(t, "nothing")
	c := NewClientWithLoggerAndRateLimit(srv.URL, "as", "remote", NewTestLogger(t), TestRateLimitConfig())

	enc, err := c.IsRoomEncrypted("!later:hs")
	require.NoError(t, err)
	assert.False(t, enc)

	c.NoteRoomEncrypted("!later:hs")
	_, err = c.SendMessage(MessageRequest{RoomID: "!later:hs", GhostUserID: "@g:hs", Message: "hi"})
	require.ErrorIs(t, err, ErrEncryptedRoom)
	assert.Zero(t, atomic.LoadInt32(sends))
}

type fakeCrypto struct{ calls int }

func (f *fakeCrypto) Prepare(_ context.Context, _, eventType string, content any) (string, any, error) {
	f.calls++
	return "m.room.encrypted", map[string]any{"algorithm": "m.megolm.v1.aes-sha2", "wrapped": eventType}, nil
}

func TestAnInstalledEncryptorHandlesTheEvent(t *testing.T) {
	srv, sends := encryptedHS(t, "secret")
	c := NewClientWithLoggerAndRateLimit(srv.URL, "as", "remote", NewTestLogger(t), TestRateLimitConfig())
	fc := &fakeCrypto{}
	c.SetRoomCrypto(fc)

	_, err := c.SendMessage(MessageRequest{RoomID: "!secret:hs", GhostUserID: "@g:hs", Message: "hello"})
	require.NoError(t, err)
	assert.Equal(t, 1, fc.calls)
	assert.EqualValues(t, 1, atomic.LoadInt32(sends))
}
