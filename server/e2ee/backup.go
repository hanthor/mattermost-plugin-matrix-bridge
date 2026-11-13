package e2ee

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/id"
)

// Server-side key backup. The bridge bot's Megolm room keys exist only in the
// bot's own store; if that store is lost, so is the ability to read what was
// sent before. With backup on, the bot keeps a copy of every room key on the
// homeserver, encrypted with a backup key that only the bridge (and whoever
// was shown it) holds. The key is kept sealed in the plugin's key-value store,
// which is what lets the bridge back up and restore without anyone present.
//
// The sealed key lives next to the state it protects, so it covers losing the
// encryption state (a failed save, a partial restore of the database), not
// losing the whole key-value store: for that an administrator keeps a copy of
// the key (`/matrix encryption-backup-key`) and supplies it in the settings.

const (
	backupKeyKV    = "e2ee_backup_key"
	backupInterval = 2 * time.Minute
	backupBatch    = 100
)

// backupAlgorithm is the only algorithm the Matrix spec defines for backups.
const backupAlgorithm = id.KeyBackupAlgorithmMegolmBackupV1

// backupState is the running backup of one Manager.
type backupState struct {
	mu      sync.Mutex // serialises uploads
	key     *backup.MegolmBackupKey
	version id.KeyBackupVersion
	kick    chan struct{}
	cancel  context.CancelFunc
}

// BackupEnabled reports whether the bot is backing its keys up.
func (m *Manager) BackupEnabled() bool { return m.backup != nil }

// BackupKey returns the backup key for an administrator to keep elsewhere. It
// is the secret that protects the backup: whoever holds it can read the room
// keys stored on the homeserver.
func (m *Manager) BackupKey() (string, error) {
	if m.backup == nil {
		return "", errors.New("key backup is not on")
	}
	return base64.RawStdEncoding.EncodeToString(m.backup.key.Bytes()), nil
}

// loadBackupKey returns the sealed key if there is one, else the key supplied
// in the settings, else a new one, and seals whichever it uses.
func (m *Manager) loadBackupKey() (*backup.MegolmBackupKey, error) {
	if raw, err := m.opts.KV.Get(backupKeyKV); err == nil && len(raw) > 0 {
		plain := string(raw)
		opened := true
		if m.opts.BackupBox != nil {
			var err error
			plain, _, err = m.opts.BackupBox.Open(plain)
			if err != nil {
				// Sealed under a secret that has changed (the appservice token was
				// rotated). The administrator's copy of the key, if supplied, takes
				// its place; without one there is nothing to do but say so.
				if m.opts.BackupKey == "" {
					return nil, errors.New("the stored backup key cannot be opened (the appservice token changed?); supply the backup key in the settings to recover")
				}
				opened = false
			}
		}
		if opened {
			return decodeBackupKey(plain)
		}
	}
	var key *backup.MegolmBackupKey
	if m.opts.BackupKey != "" {
		k, err := decodeBackupKey(m.opts.BackupKey)
		if err != nil {
			return nil, err
		}
		key = k
	} else {
		k, err := backup.NewMegolmBackupKey()
		if err != nil {
			return nil, err
		}
		key = k
	}
	value := base64.RawStdEncoding.EncodeToString(key.Bytes())
	if m.opts.BackupBox != nil {
		sealed, err := m.opts.BackupBox.Seal(value)
		if err != nil {
			return nil, err
		}
		value = sealed
	}
	if err := m.opts.KV.Set(backupKeyKV, []byte(value)); err != nil {
		return nil, err
	}
	return key, nil
}

func decodeBackupKey(s string) (*backup.MegolmBackupKey, error) {
	raw, err := base64.RawStdEncoding.DecodeString(trimPadding(s))
	if err != nil {
		return nil, errors.New("the backup key is not valid base64")
	}
	return backup.MegolmBackupKeyFromBytes(raw)
}

func trimPadding(s string) string {
	for len(s) > 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return s
}

// derivedPublicKey is how a backup version names the key that encrypts it.
func derivedPublicKey(key *backup.MegolmBackupKey) id.Ed25519 {
	return id.Ed25519(base64.RawStdEncoding.EncodeToString(key.PublicKey().Bytes()))
}

// ensureBackupVersion finds the bot's backup that this key belongs to, or
// creates one. It never deletes or changes a backup made with another key.
func (m *Manager) ensureBackupVersion(ctx context.Context, key *backup.MegolmBackupKey) (id.KeyBackupVersion, error) {
	latest, err := m.client.GetKeyBackupLatestVersion(ctx)
	if err == nil && latest != nil && latest.Algorithm == backupAlgorithm && latest.AuthData.PublicKey == derivedPublicKey(key) {
		return latest.Version, nil
	}
	var httpErr mautrix.HTTPError
	if err != nil && !(errors.As(err, &httpErr) && httpErr.RespError != nil && httpErr.RespError.ErrCode == "M_NOT_FOUND") {
		return "", err
	}
	created, err := m.client.CreateKeyBackupVersion(ctx, &mautrix.ReqRoomKeysVersionCreate[backup.MegolmAuthData]{
		Algorithm: backupAlgorithm,
		AuthData:  backup.MegolmAuthData{PublicKey: derivedPublicKey(key)},
	})
	if err != nil {
		return "", err
	}
	return created.Version, nil
}

// setupBackup starts the backup: the key, the server-side version, a restore
// when this is a fresh device, and the loop that uploads new keys.
func (m *Manager) setupBackup(ctx context.Context, freshDevice bool) error {
	key, err := m.loadBackupKey()
	if err != nil {
		return err
	}
	version, err := m.ensureBackupVersion(ctx, key)
	if err != nil {
		return err
	}
	bctx, cancel := context.WithCancel(context.Background())
	st := &backupState{key: key, version: version, kick: make(chan struct{}, 1), cancel: cancel}
	m.backup = st

	if freshDevice {
		// The keys the previous device held come back from the backup.
		if err := m.mach.GetAndStoreKeyBackup(m.opts.Log.WithContext(ctx), version, key); err != nil {
			m.opts.Log.Warn().Err(err).Msg("could not restore room keys from the backup")
		} else {
			m.opts.Log.Info().Str("version", string(version)).Msg("restored room keys from the backup")
		}
	}
	go m.backupLoop(bctx, st)
	st.kickNow()
	return nil
}

func (st *backupState) kickNow() {
	select {
	case st.kick <- struct{}{}:
	default:
	}
}

// KickBackup asks for an upload soon, after new room keys arrived.
func (m *Manager) KickBackup() {
	if m.backup != nil {
		m.backup.kickNow()
	}
}

func (m *Manager) backupLoop(ctx context.Context, st *backupState) {
	t := time.NewTicker(backupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-st.kick:
		case <-t.C:
		}
		if err := m.uploadBackup(ctx, st); err != nil && ctx.Err() == nil {
			m.opts.Log.Warn().Err(err).Msg("could not back up room keys")
		}
	}
}

// uploadBackup sends every room key the backup does not have yet.
func (m *Manager) uploadBackup(ctx context.Context, st *backupState) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		var batch []*crypto.InboundGroupSession
		err := m.store.GetGroupSessionsWithoutKeyBackupVersion(ctx, st.version).Iter(func(igs *crypto.InboundGroupSession) (bool, error) {
			batch = append(batch, igs)
			return len(batch) < backupBatch, nil
		})
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		req := &mautrix.ReqKeyBackup{Rooms: map[id.RoomID]mautrix.ReqRoomKeyBackup{}}
		var uploaded []*crypto.InboundGroupSession
		for _, igs := range batch {
			data, err := sessionBackupData(st.key, igs)
			if err != nil {
				m.opts.Log.Warn().Err(err).Str("session_id", string(igs.ID())).Msg("could not prepare a room key for the backup")
				continue
			}
			room := req.Rooms[igs.RoomID]
			if room.Sessions == nil {
				room.Sessions = map[id.SessionID]mautrix.ReqKeyBackupData{}
			}
			room.Sessions[igs.ID()] = *data
			req.Rooms[igs.RoomID] = room
			uploaded = append(uploaded, igs)
		}
		if len(uploaded) == 0 {
			return errors.New("no room key could be prepared for the backup")
		}
		if _, err := m.client.PutKeysInBackup(ctx, st.version, req); err != nil {
			return err
		}
		for _, igs := range uploaded {
			igs.KeyBackupVersion = st.version
			if err := m.store.PutGroupSession(ctx, igs); err != nil {
				return err
			}
		}
		if len(batch) < backupBatch {
			return nil
		}
	}
}

// sessionBackupData is one room key in the form the backup stores it.
func sessionBackupData(key *backup.MegolmBackupKey, igs *crypto.InboundGroupSession) (*mautrix.ReqKeyBackupData, error) {
	first := igs.Internal.FirstKnownIndex()
	exported, err := igs.Internal.Export(first)
	if err != nil {
		return nil, err
	}
	chain := igs.ForwardingChains
	if chain == nil {
		chain = []string{}
	}
	encrypted, err := backup.EncryptSessionData(key, backup.MegolmSessionData{
		Algorithm:          id.AlgorithmMegolmV1,
		ForwardingKeyChain: chain,
		SenderClaimedKeys:  backup.SenderClaimedKeys{Ed25519: igs.SigningKey},
		SenderKey:          igs.SenderKey,
		SessionKey:         string(exported),
		SharedHistory:      igs.SharedHistory,
	})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(encrypted)
	if err != nil {
		return nil, err
	}
	return &mautrix.ReqKeyBackupData{
		FirstMessageIndex: int(first),
		ForwardedCount:    len(chain),
		IsVerified:        false,
		SessionData:       raw,
	}, nil
}

// stopBackup ends the upload loop.
func (m *Manager) stopBackup() {
	if m.backup != nil {
		m.backup.cancel()
	}
}
