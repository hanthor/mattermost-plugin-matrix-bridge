package e2ee

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

const (
	deviceIDKey       = "e2ee_device_id"
	deviceDisplayName = "Mattermost bridge"
	// How long an inbound message waits for the sender's room key to arrive.
	keyWaitTimeout = 10 * time.Second
)

// Options configures a Manager.
type Options struct {
	ServerURL string
	ASToken   string
	BotUserID id.UserID
	KV        KV
	Log       zerolog.Logger
	// MSC4190 creates the bot's device with the appservice device API instead
	// of logging in. It needs "io.element.msc4190" in the registration.
	MSC4190 bool
	// IsManaged reports whether a user is one of the bridge's own accounts
	// (ghosts), which never have devices and need no room keys.
	IsManaged func(id.UserID) bool
	// Box seals the key that protects the stored Olm state; nil leaves it plain.
	Box *secretbox.Box
}

// Manager is the bridge bot's encryption device.
type Manager struct {
	opts   Options
	client *mautrix.Client
	store  *Store
	mach   *crypto.OlmMachine
	state  *stateStore

	mu       sync.Mutex
	shareMu  sync.Mutex
	closed   bool
	deviceID id.DeviceID
}

// New creates (or restores) the bot's device and uploads its keys.
func New(ctx context.Context, opts Options) (*Manager, error) {
	if opts.ServerURL == "" || opts.ASToken == "" || opts.BotUserID == "" {
		return nil, errors.New("homeserver URL, appservice token and bot user are required")
	}
	store, err := NewStore(opts.KV, opts.Box)
	if err != nil {
		return nil, err
	}
	client, err := mautrix.NewClient(opts.ServerURL, opts.BotUserID, opts.ASToken)
	if err != nil {
		store.Close()
		return nil, errors.Wrap(err, "invalid homeserver URL")
	}
	client.SetAppServiceUserID = true
	client.Log = opts.Log

	m := &Manager{opts: opts, client: client, store: store}
	m.state = newStateStore(client)
	if err := m.start(ctx); err != nil {
		store.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) start(ctx context.Context) error {
	// A stored device ID is only useful together with the keys that belong to
	// it. Without them, the bot has to become a new device.
	deviceID := id.DeviceID("")
	if raw, err := m.opts.KV.Get(deviceIDKey); err == nil && len(raw) > 0 {
		if acct, _ := m.store.GetAccount(ctx); acct != nil {
			deviceID = id.DeviceID(raw)
		}
	}
	isExisting := deviceID != ""

	if m.opts.MSC4190 {
		if err := m.client.CreateDeviceMSC4190(ctx, deviceID, deviceDisplayName); err != nil {
			return errors.Wrap(err, "could not create the bridge bot's device")
		}
	} else {
		resp, err := m.client.Login(ctx, &mautrix.ReqLogin{
			Type:                     mautrix.AuthTypeAppservice,
			Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: string(m.opts.BotUserID)},
			DeviceID:                 deviceID,
			InitialDeviceDisplayName: deviceDisplayName,
		})
		if err != nil {
			return errors.Wrap(err, "could not log in the bridge bot")
		}
		m.client.DeviceID = resp.DeviceID
		m.client.SetAppServiceDeviceID = true
	}
	m.deviceID = m.client.DeviceID
	if err := m.opts.KV.Set(deviceIDKey, []byte(m.deviceID)); err != nil {
		return errors.Wrap(err, "could not save the bot's device ID")
	}

	m.mach = crypto.NewOlmMachine(m.client, &m.opts.Log, m.store, m.state)
	m.mach.DisableSharedGroupSessionTracking = true
	if err := m.mach.Load(ctx); err != nil {
		return errors.Wrap(err, "could not load the encryption account")
	}

	if isExisting && m.keysAreOnServer(ctx) {
		return nil
	}
	if err := m.mach.ShareKeys(ctx, -1); err != nil {
		return errors.Wrap(err, "could not upload the bot's encryption keys")
	}
	// Make sure the account is stored before anything depends on its keys.
	return m.store.Persist()
}

func (m *Manager) keysAreOnServer(ctx context.Context) bool {
	resp, err := m.client.QueryKeys(ctx, &mautrix.ReqQueryKeys{
		DeviceKeys: map[id.UserID]mautrix.DeviceIDList{m.client.UserID: {m.client.DeviceID}},
	})
	if err != nil {
		// Could not ask; assume they are, the next transaction's key count
		// tells the machine to upload more if not.
		return true
	}
	dev, ok := resp.DeviceKeys[m.client.UserID][m.client.DeviceID]
	return ok && len(dev.Keys) > 0
}

// DeviceID is the bridge bot's device.
func (m *Manager) DeviceID() id.DeviceID { return m.deviceID }

// Close stops background work and saves the encryption state.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.store.Close()
}

// Extras is the encryption data a homeserver puts in an appservice
// transaction when the registration asks for it (MSC3202).
type Extras struct {
	ToDevice []*event.Event `json:"to_device,omitempty"`

	MSC2409ToDevice []*event.Event `json:"de.sorunome.msc2409.to_device,omitempty"`

	DeviceLists    *mautrix.DeviceLists                           `json:"device_lists,omitempty"`
	DeviceOTKCount map[id.UserID]map[id.DeviceID]mautrix.OTKCount `json:"device_one_time_keys_count,omitempty"`

	MSC3202DeviceLists *mautrix.DeviceLists                           `json:"org.matrix.msc3202.device_lists,omitempty"`
	MSC3202OTKCount    map[id.UserID]map[id.DeviceID]mautrix.OTKCount `json:"org.matrix.msc3202.device_one_time_keys_count,omitempty"`
}

// ParseExtras reads the encryption data out of a raw transaction body.
func ParseExtras(body []byte) (*Extras, error) {
	var x Extras
	if err := json.Unmarshal(body, &x); err != nil {
		return nil, err
	}
	return &x, nil
}

// Empty reports whether the transaction carried any encryption data.
func (x *Extras) Empty() bool {
	return len(x.ToDevice)+len(x.MSC2409ToDevice) == 0 && x.deviceLists() == nil && len(x.otkCounts()) == 0
}

func (x *Extras) deviceLists() *mautrix.DeviceLists {
	if x.DeviceLists != nil {
		return x.DeviceLists
	}
	return x.MSC3202DeviceLists
}

func (x *Extras) otkCounts() map[id.UserID]map[id.DeviceID]mautrix.OTKCount {
	if len(x.DeviceOTKCount) > 0 {
		return x.DeviceOTKCount
	}
	return x.MSC3202OTKCount
}

// HandleExtras feeds the transaction's encryption data to the crypto machine:
// room keys arriving by to-device message, device list changes, and the
// count of one-time keys left on the server.
func (m *Manager) HandleExtras(ctx context.Context, x *Extras) {
	toDevice := x.ToDevice
	if len(toDevice) == 0 {
		toDevice = x.MSC2409ToDevice
	}
	for _, evt := range toDevice {
		if evt == nil {
			continue
		}
		evt.Type.Class = event.ToDeviceEventType
		if err := evt.Content.ParseRaw(evt.Type); err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
			m.opts.Log.Warn().Err(err).Str("type", evt.Type.Type).Msg("Could not parse a to-device event")
			continue
		}
		m.mach.HandleToDeviceEvent(ctx, evt)
	}
	if dl := x.deviceLists(); dl != nil {
		m.mach.HandleDeviceLists(ctx, dl, "")
	}
	for user, devices := range x.otkCounts() {
		if user != m.client.UserID {
			continue
		}
		if count, ok := devices[m.client.DeviceID]; ok {
			count.UserID, count.DeviceID = user, m.client.DeviceID
			m.mach.HandleOTKCounts(ctx, &count)
		}
	}
}

// NoteEncryption tells the manager a room turned on encryption.
func (m *Manager) NoteEncryption(roomID id.RoomID) { m.state.forget(roomID) }

// HandleMember passes membership changes on, so keys are rotated when someone
// who could read a room leaves it.
func (m *Manager) HandleMember(ctx context.Context, evt *event.Event) {
	m.mach.HandleMemberEvent(ctx, evt)
}

// Decrypt reads an m.room.encrypted event. When the room key has not arrived
// yet it waits a little for it.
func (m *Manager) Decrypt(ctx context.Context, evt *event.Event) (*event.Event, error) {
	evt.Type.Class = event.MessageEventType
	if err := evt.Content.ParseRaw(evt.Type); err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
		return nil, errors.Wrap(err, "could not parse the encrypted event")
	}
	out, err := m.mach.DecryptMegolmEvent(ctx, evt)
	if err == nil {
		return out, nil
	}
	enc, ok := evt.Content.Parsed.(*event.EncryptedEventContent)
	if !ok || !isMissingKey(err) {
		return nil, err
	}
	if !m.mach.WaitForSession(ctx, evt.RoomID, enc.SenderKey, enc.SessionID, keyWaitTimeout) {
		return nil, err
	}
	return m.mach.DecryptMegolmEvent(ctx, evt)
}

func isMissingKey(err error) bool {
	return errors.Is(err, crypto.ErrNoSessionFound) || errors.Is(err, crypto.ErrGroupSessionWithheld)
}

// Prepare implements matrix.RoomCrypto: it encrypts an outgoing event when
// the room is encrypted and leaves it alone otherwise.
func (m *Manager) Prepare(ctx context.Context, roomID, eventType string, content any) (string, any, error) {
	room := id.RoomID(roomID)
	encrypted, err := m.state.IsEncrypted(ctx, room)
	if err != nil {
		return eventType, content, err
	}
	if !encrypted {
		return eventType, content, nil
	}
	evtType := event.Type{Type: eventType, Class: event.MessageEventType}
	enc, err := m.mach.EncryptMegolmEvent(ctx, room, evtType, content)
	if err != nil && (errors.Is(err, crypto.ErrSessionExpired) || errors.Is(err, crypto.ErrSessionNotShared) || errors.Is(err, crypto.ErrNoGroupSession)) {
		if err = m.shareSession(ctx, room); err != nil {
			return eventType, content, errors.Wrap(err, "could not share the room key")
		}
		enc, err = m.mach.EncryptMegolmEvent(ctx, room, evtType, content)
	}
	if err != nil {
		return eventType, content, errors.Wrap(err, "could not encrypt the event")
	}
	return event.EventEncrypted.Type, enc, nil
}

// shareSession sends the room key to every device in the room.
func (m *Manager) shareSession(ctx context.Context, room id.RoomID) error {
	m.shareMu.Lock()
	defer m.shareMu.Unlock()
	resp, err := m.client.JoinedMembers(ctx, room)
	if err != nil {
		return errors.Wrap(err, "could not list the room's members")
	}
	var users []id.UserID
	for u := range resp.Joined {
		if m.opts.IsManaged != nil && m.opts.IsManaged(u) {
			continue
		}
		users = append(users, u)
	}
	return m.mach.ShareGroupSession(ctx, room, users)
}
