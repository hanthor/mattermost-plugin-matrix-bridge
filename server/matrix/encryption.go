package matrix

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// ErrEncryptedRoom is returned instead of sending a plain event into a room that
// has end-to-end encryption turned on. A plain message in an encrypted room is
// shown with a warning shield and defeats what the room is for, so the bridge
// refuses unless it can encrypt.
var ErrEncryptedRoom = errors.New("the room is end-to-end encrypted and the bridge is not set up to send into it")

// RoomCrypto prepares an outgoing event for a room. A real implementation
// encrypts it when the room is encrypted; it returns the (possibly changed)
// event type and content. Returning ErrEncryptedRoom refuses the send.
type RoomCrypto interface {
	Prepare(ctx context.Context, roomID, eventType string, content any) (string, any, error)
}

// clientCrypto is the encryption state of a client, shared between copies.
type clientCrypto struct {
	mu         sync.RWMutex
	roomCrypto RoomCrypto
	rooms      roomEncryptionCache
}

// cryptoState returns the client's encryption state, creating it on first use
// for clients built without a constructor.
func (c *Client) cryptoState() *clientCrypto {
	if c.crypto == nil {
		c.crypto = &clientCrypto{}
	}
	return c.crypto
}

// SetRoomCrypto installs the encryptor used for outgoing events. Without one,
// the client refuses to send messages and reactions into encrypted rooms.
func (c *Client) SetRoomCrypto(rc RoomCrypto) {
	st := c.cryptoState()
	st.mu.Lock()
	st.roomCrypto = rc
	st.mu.Unlock()
}

func (c *Client) currentRoomCrypto() RoomCrypto {
	st := c.cryptoState()
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.roomCrypto
}

// encryptedEventTypes are the event types Matrix clients encrypt.
func encryptedEventTypes(eventType string) bool {
	switch eventType {
	case "m.room.message", "m.reaction", "m.sticker":
		return true
	}
	return false
}

// roomEncryptionCache remembers what is known about rooms. A room that is
// encrypted stays encrypted, so that answer is kept; "not encrypted" is
// rechecked after a while in case it changed without the bridge noticing.
type roomEncryptionCache struct {
	mu      sync.Mutex
	entries map[string]roomEncryption
}

type roomEncryption struct {
	encrypted bool
	checked   time.Time
}

const plainRoomRecheck = 10 * time.Minute

func (r *roomEncryptionCache) get(roomID string) (encrypted, known bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[roomID]
	if !ok {
		return false, false
	}
	if !e.encrypted && time.Since(e.checked) > plainRoomRecheck {
		return false, false
	}
	return e.encrypted, true
}

func (r *roomEncryptionCache) set(roomID string, encrypted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]roomEncryption{}
	}
	r.entries[roomID] = roomEncryption{encrypted: encrypted, checked: time.Now()}
}

// NoteRoomEncrypted records that a room has turned encryption on, as seen in
// an m.room.encryption event.
func (c *Client) NoteRoomEncrypted(roomID string) {
	c.cryptoState().rooms.set(roomID, true)
}

// IsRoomEncrypted reports whether a room has end-to-end encryption. When the
// homeserver cannot say (the bot is not in the room), it reports false and an
// error, so callers that must not guess can tell.
func (c *Client) IsRoomEncrypted(roomID string) (bool, error) {
	if encrypted, known := c.cryptoState().rooms.get(roomID); known {
		return encrypted, nil
	}
	if c.serverURL == "" || c.asToken == "" {
		return false, errors.New("matrix client not configured")
	}
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "state", "m.room.encryption", "")
	if err != nil {
		return false, errors.Wrap(err, "invalid room ID")
	}
	req, err := http.NewRequest(http.MethodGet, c.serverURL+endpoint, nil)
	if err != nil {
		return false, errors.Wrap(err, "failed to create room encryption request")
	}
	req.Header.Set("Authorization", "Bearer "+c.asToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, errors.Wrap(err, "failed to read room encryption state")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	switch resp.StatusCode {
	case http.StatusOK:
		// The state event always carries the algorithm in use.
		var state struct {
			Algorithm string `json:"algorithm"`
		}
		encrypted := json.Unmarshal(body, &state) == nil && state.Algorithm != ""
		c.cryptoState().rooms.set(roomID, encrypted)
		return encrypted, nil
	case http.StatusNotFound:
		c.cryptoState().rooms.set(roomID, false)
		return false, nil
	default:
		return false, errors.Errorf("could not read the room's encryption state: status %d", resp.StatusCode)
	}
}

// prepareEvent applies the room's encryption policy to an outgoing event.
func (c *Client) prepareEvent(roomID, eventType string, content any) (string, any, error) {
	if !encryptedEventTypes(eventType) {
		return eventType, content, nil
	}
	if rc := c.currentRoomCrypto(); rc != nil {
		return rc.Prepare(context.Background(), roomID, eventType, content)
	}
	encrypted, err := c.IsRoomEncrypted(roomID)
	if err != nil {
		// Not knowing is not a reason to stop a room that has always worked.
		return eventType, content, nil
	}
	if encrypted {
		return eventType, content, ErrEncryptedRoom
	}
	return eventType, content, nil
}
