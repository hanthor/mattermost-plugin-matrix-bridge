package e2e

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// encryptedClient is a Matrix client with its own encryption device, standing
// in for a person using Element in an encrypted room. It syncs in the
// background, decrypts what it receives and encrypts what it sends.
type encryptedClient struct {
	t    *testing.T
	cli  *mautrix.Client
	mach *crypto.OlmMachine
	ctx  context.Context

	mu       sync.Mutex
	messages []*event.Event // decrypted (or plain) m.room.message events
	raw      []*event.Event // everything seen in room timelines, as the server holds it
}

type testStateStore struct{ room id.RoomID }

func (s testStateStore) IsEncrypted(_ context.Context, r id.RoomID) (bool, error) { return true, nil }
func (s testStateStore) GetEncryptionEvent(_ context.Context, r id.RoomID) (*event.EncryptionEventContent, error) {
	return &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}, nil
}
func (s testStateStore) GetHistoryVisibility(context.Context, id.RoomID) (*event.HistoryVisibilityEventContent, error) {
	return &event.HistoryVisibilityEventContent{HistoryVisibility: event.HistoryVisibilityShared}, nil
}
func (s testStateStore) FindSharedRooms(context.Context, id.UserID) ([]id.RoomID, error) {
	return nil, nil
}

func newEncryptedClient(t *testing.T, cs *client.CSAPI) *encryptedClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cli, err := mautrix.NewClient(cs.BaseURL, id.UserID(cs.UserID), cs.AccessToken)
	require.NoError(t, err)
	require.NotEmpty(t, cs.DeviceID, "the test account needs a device")
	cli.DeviceID = id.DeviceID(cs.DeviceID)

	log := zerolog.Nop()
	mach := crypto.NewOlmMachine(cli, &log, crypto.NewMemoryStore(nil), testStateStore{})
	require.NoError(t, mach.Load(ctx))
	require.NoError(t, mach.ShareKeys(ctx, -1))

	ec := &encryptedClient{t: t, cli: cli, mach: mach, ctx: ctx}
	go ec.syncLoop()
	return ec
}

func (ec *encryptedClient) syncLoop() {
	since := ""
	for ec.ctx.Err() == nil {
		resp, err := ec.cli.FullSyncRequest(ec.ctx, mautrix.ReqSync{Timeout: 1000, Since: since})
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		ec.mach.ProcessSyncResponse(ec.ctx, resp, since)
		for roomID, room := range resp.Rooms.Join {
			for _, evt := range room.Timeline.Events {
				evt.RoomID = roomID
				ec.handle(evt)
			}
		}
		since = resp.NextBatch
	}
}

func (ec *encryptedClient) handle(evt *event.Event) {
	_ = evt.Content.ParseRaw(evt.Type)
	ec.mu.Lock()
	ec.raw = append(ec.raw, evt)
	ec.mu.Unlock()
	switch evt.Type {
	case event.EventEncrypted:
		// The room key may arrive a moment after the message.
		var dec *event.Event
		var err error
		for i := 0; i < 20; i++ {
			dec, err = ec.mach.DecryptMegolmEvent(ec.ctx, evt)
			if err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			return
		}
		dec.RoomID = evt.RoomID
		dec.Sender = evt.Sender
		_ = dec.Content.ParseRaw(dec.Type)
		if dec.Type == event.EventMessage {
			ec.mu.Lock()
			ec.messages = append(ec.messages, dec)
			ec.mu.Unlock()
		}
	case event.EventMessage:
		ec.mu.Lock()
		ec.messages = append(ec.messages, evt)
		ec.mu.Unlock()
	}
}

// Send encrypts and sends a text message.
func (ec *encryptedClient) Send(room id.RoomID, body string) {
	ec.t.Helper()
	ec.sendEncrypted(room, map[string]any{"msgtype": "m.text", "body": body})
}

// WaitForMessage waits for a decrypted message with this body.
func (ec *encryptedClient) WaitForMessage(room id.RoomID, body string) *event.Event {
	ec.t.Helper()
	var found *event.Event
	WaitFor(ec.t, 60*time.Second, "an encrypted message "+body, func() bool {
		ec.mu.Lock()
		defer ec.mu.Unlock()
		for _, m := range ec.messages {
			if m.RoomID == room {
				if c := m.Content.AsMessage(); c != nil && c.Body == body {
					found = m
					return true
				}
			}
		}
		return false
	})
	return found
}

// WaitForFile waits for a decrypted file message with this body.
func (ec *encryptedClient) WaitForFile(room id.RoomID, name string) *event.Event {
	ec.t.Helper()
	var found *event.Event
	defer func() {
		if ec.t.Failed() {
			ec.mu.Lock()
			defer ec.mu.Unlock()
			for _, m := range ec.messages {
				raw, _ := json.Marshal(m.Content.Raw)
				ec.t.Logf("saw %s in %s: %s", m.Type.Type, m.RoomID, raw)
			}
		}
	}()
	WaitFor(ec.t, 20*time.Second, "an encrypted file "+name, func() bool {
		ec.mu.Lock()
		defer ec.mu.Unlock()
		for _, m := range ec.messages {
			if c := m.Content.AsMessage(); m.RoomID == room && c != nil && c.Body == name && c.File != nil {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// SendFile encrypts a file, uploads the ciphertext and sends a message that
// points at it.
func (ec *encryptedClient) SendFile(room id.RoomID, cs *client.CSAPI, name string, data []byte) {
	ec.t.Helper()
	file := attachment.NewEncryptedFile()
	mxc := cs.UploadContent(ec.t, file.Encrypt(data), "encrypted", "application/octet-stream")
	ec.sendEncrypted(room, map[string]any{
		"msgtype": "m.file",
		"body":    name,
		"file":    event.EncryptedFileInfo{EncryptedFile: *file, URL: id.ContentURIString(mxc)},
		"info":    map[string]any{"mimetype": "text/plain", "size": len(data)},
	})
}

func (ec *encryptedClient) sendEncrypted(room id.RoomID, content map[string]any) {
	ec.t.Helper()
	members, err := ec.cli.JoinedMembers(ec.ctx, room)
	require.NoError(ec.t, err)
	var users []id.UserID
	for u := range members.Joined {
		users = append(users, u)
	}
	require.NoError(ec.t, ec.mach.ShareGroupSession(ec.ctx, room, users))
	enc, err := ec.mach.EncryptMegolmEvent(ec.ctx, room, event.EventMessage, content)
	require.NoError(ec.t, err)
	_, err = ec.cli.SendMessageEvent(ec.ctx, room, event.EventEncrypted, enc)
	require.NoError(ec.t, err)
}

// ServerCopyContains reports whether any event the server holds for the room
// carries this text in the clear.
func (ec *encryptedClient) ServerCopyContains(room id.RoomID, text string) bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	for _, e := range ec.raw {
		if e.RoomID != room || e.Type == event.EventEncrypted {
			continue
		}
		if c := e.Content.AsMessage(); c != nil && c.Body == text {
			return true
		}
	}
	return false
}
