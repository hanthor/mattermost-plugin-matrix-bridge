package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/e2ee"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

// encryptionState holds the bridge bot's encryption device while encryption
// is switched on.
type encryptionState struct {
	mu      sync.Mutex
	mgr     *e2ee.Manager
	cancel  context.CancelFunc
	started bool
}

func (p *Plugin) cryptoManager() *e2ee.Manager {
	p.encryption.mu.Lock()
	defer p.encryption.mu.Unlock()
	return p.encryption.mgr
}

// EncryptionSupported implements command.PluginAccessor: rooms with
// end-to-end encryption can be bridged while the bot has a working device.
func (p *Plugin) EncryptionSupported() bool { return p.cryptoManager() != nil }

// CrossSignBridgeDevice implements command.PluginAccessor: it verifies the bot's
// device with a new cross-signing identity and returns the recovery key to show
// once. Only system administrators may ask.
func (p *Plugin) CrossSignBridgeDevice(userID string) (string, error) {
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		return "", errors.New("only system administrators can do this")
	}
	mgr := p.cryptoManager()
	if mgr == nil {
		return "", errors.New("encryption is not running; turn on Enable end-to-end encryption first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return mgr.CrossSign(ctx)
}

// stopEncryption shuts the bot's device down.
func (p *Plugin) stopEncryption() {
	p.encryption.mu.Lock()
	mgr, cancel := p.encryption.mgr, p.encryption.cancel
	p.encryption.mgr, p.encryption.cancel, p.encryption.started = nil, nil, false
	p.encryption.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if p.matrixClient != nil {
		p.matrixClient.SetRoomCrypto(nil)
	}
	if mgr != nil {
		mgr.Close()
	}
}

// startEncryption (re)starts the bot's device according to the settings. It
// returns at once; the homeserver may not be reachable yet, so setup retries
// in the background.
func (p *Plugin) startEncryption() {
	p.stopEncryption()
	cfg := p.getConfiguration()
	if !cfg.EnableEncryption || !cfg.EnableSync || cfg.MatrixServerURL == "" || cfg.MatrixASToken == "" || p.kvstore == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.encryption.mu.Lock()
	p.encryption.cancel, p.encryption.started = cancel, true
	p.encryption.mu.Unlock()

	go func() {
		backoff := 2 * time.Second
		for {
			mgr, err := e2ee.New(ctx, e2ee.Options{
				ServerURL: cfg.MatrixServerURL,
				ASToken:   cfg.MatrixASToken,
				BotUserID: id.UserID(p.BridgeBotUserID()),
				KV:        p.kvstore,
				Box:       secretbox.New(secretFor(p.API, cfg), "e2ee-pickle-key"),
				Log:       zerolog.New(pluginLogWriter{p.logger}).Level(zerolog.InfoLevel),
				IsManaged: func(u id.UserID) bool { return p.isBridgeAccount(string(u)) },
			})
			if err == nil {
				p.encryption.mu.Lock()
				if ctx.Err() != nil {
					p.encryption.mu.Unlock()
					mgr.Close()
					return
				}
				p.encryption.mgr = mgr
				p.encryption.mu.Unlock()
				if p.matrixClient != nil {
					p.matrixClient.SetRoomCrypto(mgr)
				}
				p.logger.LogInfo("End-to-end encryption is on", "device_id", mgr.DeviceID().String())
				return
			}
			p.logger.LogWarn("Could not start end-to-end encryption; will retry", "error", err, "retry_in", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
		}
	}()
}

// pluginLogWriter forwards the crypto library's log lines to the plugin log.
type pluginLogWriter struct{ logger Logger }

func (w pluginLogWriter) Write(b []byte) (int, error) { return len(b), nil }

func (w pluginLogWriter) WriteLevel(level zerolog.Level, b []byte) (int, error) {
	msg := "encryption: " + string(b)
	switch {
	case level >= zerolog.ErrorLevel:
		w.logger.LogError(msg)
	case level == zerolog.WarnLevel:
		w.logger.LogWarn(msg)
	default:
		w.logger.LogDebug(msg)
	}
	return len(b), nil
}

// handleEncryptionExtras passes the encryption data of a transaction (room
// keys, device lists, key counts) to the bot's device.
func (p *Plugin) handleEncryptionExtras(body []byte) {
	mgr := p.cryptoManager()
	if mgr == nil {
		return
	}
	extras, err := e2ee.ParseExtras(body)
	if err != nil || extras.Empty() {
		return
	}
	mgr.HandleExtras(context.Background(), extras)
}

// toMautrixEvent converts a webhook event for the crypto library.
func toMautrixEvent(ev MatrixEvent) (*event.Event, error) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	var out event.Event
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// decryptMatrixEvent turns an m.room.encrypted event into the event it holds.
// It reports false when the event cannot be read, in which case it should be
// dropped.
func (p *Plugin) decryptMatrixEvent(ev MatrixEvent) (MatrixEvent, bool) {
	mgr := p.cryptoManager()
	if mgr == nil {
		p.logger.LogWarn("Ignoring an encrypted event: end-to-end encryption is off", "event_id", ev.EventID, "room_id", ev.RoomID)
		return ev, false
	}
	in, err := toMautrixEvent(ev)
	if err != nil {
		p.logger.LogWarn("Could not read an encrypted event", "error", err, "event_id", ev.EventID)
		return ev, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dec, err := mgr.Decrypt(ctx, in)
	if err != nil {
		p.logger.LogWarn("Could not decrypt a Matrix event; it was not bridged", "error", err, "event_id", ev.EventID, "room_id", ev.RoomID)
		p.noteUndecryptable(ev)
		return ev, false
	}
	content := dec.Content.VeryRaw
	if len(content) == 0 {
		var err error
		if content, err = json.Marshal(&dec.Content); err != nil {
			return ev, false
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		return ev, false
	}
	p.logger.LogDebug("Decrypted a Matrix event", "event_id", ev.EventID, "room_id", ev.RoomID, "type", dec.Type.Type)
	out := ev
	out.Type = dec.Type.Type
	out.Content = parsed
	return out, true
}

// noteMember lets the bot's device rotate room keys when someone leaves.
func (p *Plugin) noteMember(ev MatrixEvent) {
	mgr := p.cryptoManager()
	if mgr == nil {
		return
	}
	// Only rooms the bridge is in can be asked about; for any other room the
	// homeserver would refuse.
	if channelID, _ := p.getChannelIDFromMatrixRoom(ev.RoomID); channelID == "" {
		return
	}
	in, err := toMautrixEvent(ev)
	if err != nil {
		return
	}
	in.Type.Class = event.StateEventType
	if err := in.Content.ParseRaw(in.Type); err != nil {
		return
	}
	mgr.HandleMember(context.Background(), in)
}

// uploadAttachment uploads a file for a room. In an encrypted room the file
// is encrypted first and the returned map describes how to read it; the
// homeserver never sees the contents.
func (p *Plugin) uploadAttachment(roomIdentifier string, data []byte, name, mimeType string) (string, map[string]any, error) {
	if p.matrixClient == nil {
		return "", nil, errors.New("matrix client not initialized")
	}
	roomID := roomIdentifier
	if strings.HasPrefix(roomID, "#") {
		if resolved, err := p.matrixClient.ResolveRoomAlias(roomID); err == nil {
			roomID = resolved
		}
	}
	encrypted := false
	if strings.HasPrefix(roomID, "!") {
		encrypted, _ = p.matrixClient.IsRoomEncrypted(roomID)
	}
	if !encrypted {
		mxc, err := p.matrixClient.UploadMedia(data, name, mimeType)
		return mxc, nil, err
	}
	if p.cryptoManager() == nil {
		return "", nil, errors.New("the room is end-to-end encrypted and encryption is off")
	}
	file := attachment.NewEncryptedFile()
	// The caller's copy of the file is not used afterwards, so encrypt in place.
	file.EncryptInPlace(data)
	mxc, err := p.matrixClient.UploadMedia(data, "encrypted-file", "application/octet-stream")
	if err != nil {
		return "", nil, err
	}
	raw, err := json.Marshal(event.EncryptedFileInfo{EncryptedFile: *file, URL: id.ContentURIString(mxc)})
	if err != nil {
		return "", nil, err
	}
	var described map[string]any
	if err := json.Unmarshal(raw, &described); err != nil {
		return "", nil, err
	}
	return mxc, described, nil
}

// decryptAttachment reads a file from an encrypted room using the key in the
// message's "file" field.
func decryptAttachment(file map[string]any, data []byte) ([]byte, error) {
	raw, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	var info event.EncryptedFileInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	if err := info.PrepareForDecryption(); err != nil {
		return nil, err
	}
	if err := info.DecryptInPlace(data); err != nil {
		return nil, err
	}
	return data, nil
}

const undecryptableKeyPrefix = "undecryptable_"

// noteUndecryptable tells the channel that something was said which the bridge
// could not read, so people know a message is missing instead of seeing
// nothing. It is posted once per event, as the person who sent it.
func (p *Plugin) noteUndecryptable(ev MatrixEvent) {
	if p.kvstore == nil || p.matrixToMattermostBridge == nil || ev.EventID == "" {
		return
	}
	key := undecryptableKeyPrefix + ev.EventID
	if data, _ := p.kvstore.Get(key); len(data) > 0 {
		return
	}
	channelID, err := p.getChannelIDFromMatrixRoom(ev.RoomID)
	if err != nil || channelID == "" || p.isBridgeAccount(ev.Sender) {
		return
	}
	userID, err := p.matrixToMattermostBridge.getOrCreateMattermostUser(ev.Sender, channelID)
	if err != nil {
		p.logger.LogDebug("Could not find who sent an undecryptable message", "error", err, "event_id", ev.EventID)
		return
	}
	post := &model.Post{
		UserId: userID, ChannelId: channelID,
		Message:  "🔒 _A message was sent here that the bridge could not decrypt, so it is not shown. If this keeps happening, see the encryption notes in the plugin documentation._",
		CreateAt: ev.Timestamp,
		Props:    map[string]any{"from_matrix": true},
	}
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		p.logger.LogWarn("Could not post the notice about an undecryptable message", "error", appErr)
		return
	}
	_ = p.kvstore.Set(key, []byte("1"))
}
