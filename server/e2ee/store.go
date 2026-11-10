// Package e2ee gives the bridge bot an end-to-end encryption device, using the
// mautrix-go crypto machine. Everything it needs to remember (the Olm account,
// sessions and Megolm keys) is stored in the plugin's key-value store.
package e2ee

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/olm"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

// KV is the slice of the plugin key-value store the crypto store needs.
type KV interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

const (
	pickleKeyKey  = "e2ee_pickle_key"
	snapshotMeta  = "e2ee_snapshot_meta"
	snapshotChunk = "e2ee_snapshot_%d_%d"
	// The plugin KV store limits value sizes; stay well under it.
	chunkSize     = 30 * 1024
	flushInterval = 2 * time.Second
)

// Store is a crypto.Store whose contents survive restarts. It keeps everything
// in memory (crypto.MemoryStore) and writes a compressed snapshot to the
// key-value store shortly after anything changes.
type Store struct {
	*crypto.MemoryStore

	kv        KV
	box       *secretbox.Box // seals the pickle key; nil stores it as is
	pickleKey []byte

	dirty    atomic.Bool
	capture  atomic.Pointer[captureReq]
	persistM sync.Mutex

	stop chan struct{}
	done chan struct{}
}

// NewStore loads the snapshot from kv (or starts empty) and begins flushing
// changes in the background. Call Close to flush and stop.
func NewStore(kv KV, box *secretbox.Box) (*Store, error) {
	s := &Store{kv: kv, box: box, stop: make(chan struct{}), done: make(chan struct{})}
	key, err := s.loadOrCreatePickleKey()
	if err != nil {
		return nil, err
	}
	s.pickleKey = key
	s.MemoryStore = crypto.NewMemoryStore(s.onSave)
	if err := s.load(); err != nil {
		return nil, errors.Wrap(err, "could not load the encryption state")
	}
	go s.flushLoop()
	return s, nil
}

// loadOrCreatePickleKey returns the key that encrypts the pickled Olm state.
// It is stored sealed with a key derived from a secret outside the store, so a
// copy of the store alone cannot open the pickles. A key stored in plain by an
// earlier version is read as is and sealed on the next start.
func (s *Store) loadOrCreatePickleKey() ([]byte, error) {
	stored, err := s.kv.Get(pickleKeyKey)
	if err != nil {
		return nil, errors.Wrap(err, "could not read the pickle key")
	}
	if secretbox.IsSealed(string(stored)) {
		plain, _, err := s.box.Open(string(stored))
		if err != nil {
			return nil, errors.Wrap(err, "could not open the pickle key; the appservice token may have changed")
		}
		return []byte(plain), nil
	}
	key := stored
	if len(key) != 32 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
	}
	value := key
	if s.box != nil {
		sealed, err := s.box.Seal(string(key))
		if err != nil {
			return nil, err
		}
		value = []byte(sealed)
	}
	if err := s.kv.Set(pickleKeyKey, value); err != nil {
		return nil, errors.Wrap(err, "could not save the pickle key")
	}
	return key, nil
}

// onSave is the MemoryStore's save hook; it runs with the store lock held. A
// pending capture request is served here, because this is the only place the
// store's maps can be read safely.
func (s *Store) onSave() error {
	if req := s.capture.Swap(nil); req != nil {
		req.data, req.err = s.encode()
		return nil
	}
	s.dirty.Store(true)
	return nil
}

func (s *Store) flushLoop() {
	defer close(s.done)
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if s.dirty.Load() {
				_ = s.Persist()
			}
		case <-s.stop:
			_ = s.Persist()
			return
		}
	}
}

// Close writes any pending changes and stops the background flusher.
func (s *Store) Close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
}

// Persist writes a snapshot now.
func (s *Store) Persist() error {
	s.persistM.Lock()
	defer s.persistM.Unlock()
	s.dirty.Store(false)

	req := &captureReq{}
	s.capture.Store(req)
	// Flush takes the store lock and calls the save hook, which serves req.
	if err := s.Flush(context.Background()); err != nil {
		s.dirty.Store(true)
		return err
	}
	if req.err != nil {
		s.dirty.Store(true)
		return req.err
	}
	return s.writeBlob(req.data)
}

type captureReq struct {
	data []byte
	err  error
}

func (s *Store) writeBlob(data []byte) error {
	gen := time.Now().UnixNano()
	n := (len(data) + chunkSize - 1) / chunkSize
	for i := 0; i < n; i++ {
		end := (i + 1) * chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := s.kv.Set(fmt.Sprintf(snapshotChunk, gen, i), data[i*chunkSize:end]); err != nil {
			s.dirty.Store(true)
			return errors.Wrap(err, "could not save the encryption state")
		}
	}
	prev, _ := s.kv.Get(snapshotMeta)
	meta, _ := json.Marshal(blobMeta{Gen: gen, Chunks: n})
	if err := s.kv.Set(snapshotMeta, meta); err != nil {
		s.dirty.Store(true)
		return errors.Wrap(err, "could not save the encryption state")
	}
	var old blobMeta
	if json.Unmarshal(prev, &old) == nil {
		for i := 0; i < old.Chunks; i++ {
			_ = s.kv.Delete(fmt.Sprintf(snapshotChunk, old.Gen, i))
		}
	}
	return nil
}

type blobMeta struct {
	Gen    int64 `json:"gen"`
	Chunks int   `json:"chunks"`
}

func (s *Store) readBlob() ([]byte, error) {
	raw, err := s.kv.Get(snapshotMeta)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var meta blobMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, errors.Wrap(err, "corrupt snapshot index")
	}
	var buf bytes.Buffer
	for i := 0; i < meta.Chunks; i++ {
		part, err := s.kv.Get(fmt.Sprintf(snapshotChunk, meta.Gen, i))
		if err != nil {
			return nil, err
		}
		if part == nil {
			return nil, errors.Errorf("snapshot chunk %d is missing", i)
		}
		buf.Write(part)
	}
	return buf.Bytes(), nil
}

// record is the serialisable form of the store. Olm and Megolm objects are
// kept as encrypted pickles; the rest is plain JSON.
type record struct {
	Account *accountRec `json:"account,omitempty"`

	Sessions       map[id.SenderKey][]sessionRec                                    `json:"sessions,omitempty"`
	GroupSessions  []groupSessionRec                                                `json:"group_sessions,omitempty"`
	Withheld       []*event.RoomKeyWithheldEventContent                             `json:"withheld,omitempty"`
	OutSessions    []outSessionRec                                                  `json:"out_sessions,omitempty"`
	SharedSessions []sharedRec                                                      `json:"shared,omitempty"`
	Devices        map[id.UserID]map[id.DeviceID]*id.Device                         `json:"devices,omitempty"`
	CrossSigning   map[id.UserID]map[id.CrossSigningUsage]id.CrossSigningKey        `json:"cross_signing,omitempty"`
	Signatures     map[id.UserID]map[id.Ed25519]map[id.UserID]map[id.Ed25519]string `json:"signatures,omitempty"`
	Outdated       []id.UserID                                                      `json:"outdated,omitempty"`
	Secrets        map[id.Secret]string                                             `json:"secrets,omitempty"`
}

type accountRec struct {
	Pickle           []byte              `json:"pickle"`
	Shared           bool                `json:"shared"`
	KeyBackupVersion id.KeyBackupVersion `json:"key_backup_version,omitempty"`
}

type sessionRec struct {
	Pickle []byte                 `json:"pickle"`
	Meta   crypto.ExpirationMixin `json:"meta"`
}

type groupSessionRec struct {
	Pickle []byte                     `json:"pickle"`
	Meta   crypto.InboundGroupSession `json:"meta"`
}

type userDeviceState struct {
	UserID   id.UserID       `json:"user_id"`
	DeviceID id.DeviceID     `json:"device_id"`
	State    crypto.OGSState `json:"state"`
}

type outSessionRec struct {
	Pickle        []byte                 `json:"pickle"`
	Expiration    crypto.ExpirationMixin `json:"expiration"`
	MaxMessages   int                    `json:"max_messages"`
	MessageCount  int                    `json:"message_count"`
	SharedHistory *bool                  `json:"shared_history,omitempty"`
	Users         []userDeviceState      `json:"users,omitempty"`
	RoomID        id.RoomID              `json:"room_id"`
	Shared        bool                   `json:"shared"`
}

type sharedRec struct {
	UserID      id.UserID      `json:"user_id"`
	IdentityKey id.IdentityKey `json:"identity_key"`
	SessionID   id.SessionID   `json:"session_id"`
}

// encode serialises the store. The caller holds the MemoryStore lock.
func (s *Store) encode() ([]byte, error) {
	m := s.MemoryStore
	rec := record{
		Devices:      m.Devices,
		CrossSigning: m.CrossSigningKeys,
		Signatures:   m.KeySignatures,
		Secrets:      m.Secrets,
	}
	if m.Account != nil {
		pickle, err := m.Account.Internal.Pickle(s.pickleKey)
		if err != nil {
			return nil, errors.Wrap(err, "pickle account")
		}
		rec.Account = &accountRec{Pickle: pickle, Shared: m.Account.Shared, KeyBackupVersion: m.Account.KeyBackupVersion}
	}
	rec.Sessions = map[id.SenderKey][]sessionRec{}
	for key, list := range m.Sessions {
		for _, sess := range list {
			pickle, err := sess.Internal.Pickle(s.pickleKey)
			if err != nil {
				return nil, errors.Wrap(err, "pickle olm session")
			}
			rec.Sessions[key] = append(rec.Sessions[key], sessionRec{Pickle: pickle, Meta: sess.ExpirationMixin})
		}
	}
	for _, bySession := range m.GroupSessions {
		for _, igs := range bySession {
			pickle, err := igs.Internal.Pickle(s.pickleKey)
			if err != nil {
				return nil, errors.Wrap(err, "pickle group session")
			}
			meta := *igs
			meta.Internal = nil
			rec.GroupSessions = append(rec.GroupSessions, groupSessionRec{Pickle: pickle, Meta: meta})
		}
	}
	for _, byID := range m.WithheldGroupSessions {
		for _, w := range byID {
			rec.Withheld = append(rec.Withheld, w)
		}
	}
	for _, ogs := range m.OutGroupSessions {
		pickle, err := ogs.Internal.Pickle(s.pickleKey)
		if err != nil {
			return nil, errors.Wrap(err, "pickle outbound group session")
		}
		o := outSessionRec{
			Pickle: pickle, Expiration: ogs.ExpirationMixin, MaxMessages: ogs.MaxMessages,
			MessageCount: ogs.MessageCount, SharedHistory: ogs.SharedHistory, RoomID: ogs.RoomID, Shared: ogs.Shared,
		}
		for ud, st := range ogs.Users {
			o.Users = append(o.Users, userDeviceState{UserID: ud.UserID, DeviceID: ud.DeviceID, State: st})
		}
		rec.OutSessions = append(rec.OutSessions, o)
	}
	for user, byKey := range m.SharedGroupSessions {
		for key, sessions := range byKey {
			for sid := range sessions {
				rec.SharedSessions = append(rec.SharedSessions, sharedRec{UserID: user, IdentityKey: key, SessionID: sid})
			}
		}
	}
	for u := range m.OutdatedUsers {
		rec.Outdated = append(rec.Outdated, u)
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// load restores the snapshot, if there is one.
func (s *Store) load() error {
	blob, err := s.readBlob()
	if err != nil {
		return err
	}
	if len(blob) == 0 {
		return nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return errors.Wrap(err, "snapshot is not valid")
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return errors.Wrap(err, "snapshot is not valid")
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return errors.Wrap(err, "snapshot is not valid")
	}

	m := s.MemoryStore
	if rec.Account != nil {
		acct, err := olm.AccountFromPickled(rec.Account.Pickle, s.pickleKey)
		if err != nil {
			return errors.Wrap(err, "restore account")
		}
		m.Account = &crypto.OlmAccount{Internal: acct, Shared: rec.Account.Shared, KeyBackupVersion: rec.Account.KeyBackupVersion}
	}
	for key, list := range rec.Sessions {
		for _, sr := range list {
			sess, err := olm.SessionFromPickled(sr.Pickle, s.pickleKey)
			if err != nil {
				return errors.Wrap(err, "restore olm session")
			}
			m.Sessions[key] = append(m.Sessions[key], &crypto.OlmSession{Internal: sess, ExpirationMixin: sr.Meta})
		}
	}
	for i := range rec.GroupSessions {
		gr := rec.GroupSessions[i]
		internal, err := olm.InboundGroupSessionFromPickled(gr.Pickle, s.pickleKey)
		if err != nil {
			return errors.Wrap(err, "restore group session")
		}
		igs := gr.Meta
		igs.Internal = internal
		if m.GroupSessions[igs.RoomID] == nil {
			m.GroupSessions[igs.RoomID] = map[id.SessionID]*crypto.InboundGroupSession{}
		}
		m.GroupSessions[igs.RoomID][igs.ID()] = &igs
	}
	for _, w := range rec.Withheld {
		if m.WithheldGroupSessions[w.RoomID] == nil {
			m.WithheldGroupSessions[w.RoomID] = map[id.SessionID]*event.RoomKeyWithheldEventContent{}
		}
		m.WithheldGroupSessions[w.RoomID][w.SessionID] = w
	}
	for _, o := range rec.OutSessions {
		internal, err := olm.OutboundGroupSessionFromPickled(o.Pickle, s.pickleKey)
		if err != nil {
			return errors.Wrap(err, "restore outbound group session")
		}
		ogs := &crypto.OutboundGroupSession{
			Internal: internal, ExpirationMixin: o.Expiration, MaxMessages: o.MaxMessages,
			MessageCount: o.MessageCount, SharedHistory: o.SharedHistory, RoomID: o.RoomID, Shared: o.Shared,
			Users: map[crypto.UserDevice]crypto.OGSState{},
		}
		for _, u := range o.Users {
			ogs.Users[crypto.UserDevice{UserID: u.UserID, DeviceID: u.DeviceID}] = u.State
		}
		m.OutGroupSessions[o.RoomID] = ogs
	}
	for _, sh := range rec.SharedSessions {
		if m.SharedGroupSessions[sh.UserID] == nil {
			m.SharedGroupSessions[sh.UserID] = map[id.IdentityKey]map[id.SessionID]struct{}{}
		}
		if m.SharedGroupSessions[sh.UserID][sh.IdentityKey] == nil {
			m.SharedGroupSessions[sh.UserID][sh.IdentityKey] = map[id.SessionID]struct{}{}
		}
		m.SharedGroupSessions[sh.UserID][sh.IdentityKey][sh.SessionID] = struct{}{}
	}
	if rec.Devices != nil {
		m.Devices = rec.Devices
	}
	if rec.CrossSigning != nil {
		m.CrossSigningKeys = rec.CrossSigning
	}
	if rec.Signatures != nil {
		m.KeySignatures = rec.Signatures
	}
	if rec.Secrets != nil {
		m.Secrets = rec.Secrets
	}
	for _, u := range rec.Outdated {
		m.OutdatedUsers[u] = struct{}{}
	}
	return nil
}
