package e2ee

import (
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/crypto"
	"maunium.net/go/mautrix/crypto/backup"
	"maunium.net/go/mautrix/crypto/olm"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
)

func backupManager(kv KV, box *secretbox.Box, supplied string) *Manager {
	return &Manager{opts: Options{KV: kv, BackupBox: box, BackupKey: supplied, Log: zerolog.Nop()}}
}

func TestBackupKeyIsMadeSealedAndReused(t *testing.T) {
	kv := newMemKV()
	box := secretbox.New("a server secret", "e2ee-backup-key")

	first, err := backupManager(kv, box, "").loadBackupKey()
	require.NoError(t, err)

	stored, _ := kv.Get(backupKeyKV)
	require.NotEmpty(t, stored)
	assert.True(t, secretbox.IsSealed(string(stored)), "the key is not stored in the clear")

	second, err := backupManager(kv, box, "").loadBackupKey()
	require.NoError(t, err)
	assert.Equal(t, first.Bytes(), second.Bytes(), "the same key every time")

	other := secretbox.New("another secret", "e2ee-backup-key")
	_, err = backupManager(kv, other, "").loadBackupKey()
	assert.Error(t, err, "a different server secret cannot open it")
}

func TestASuppliedKeyReplacesAStoredKeyThatCanNoLongerBeOpened(t *testing.T) {
	kv := newMemKV()
	box := secretbox.New("the old appservice token", "e2ee-backup-key")
	original, err := backupManager(kv, box, "").loadBackupKey()
	require.NoError(t, err)
	supplied := backupManager(newMemKV(), box, "").encodeForTest(original)

	// The appservice token was rotated, so the sealed key cannot be opened.
	rotated := secretbox.New("the new appservice token", "e2ee-backup-key")
	key, err := backupManager(kv, rotated, supplied).loadBackupKey()
	require.NoError(t, err)
	assert.Equal(t, original.Bytes(), key.Bytes(), "the administrator's copy brings the same key back")

	// It is sealed again under the new secret, so the next start needs no setting.
	again, err := backupManager(kv, rotated, "").loadBackupKey()
	require.NoError(t, err)
	assert.Equal(t, original.Bytes(), again.Bytes())
}

func TestASuppliedBackupKeyIsUsedOnlyWhenNoneIsStored(t *testing.T) {
	box := secretbox.New("a server secret", "e2ee-backup-key")
	made, err := backup.NewMegolmBackupKey()
	require.NoError(t, err)
	supplied := backupManager(newMemKV(), box, "").encodeForTest(made)

	t.Run("after losing the database, the supplied key is taken and sealed", func(t *testing.T) {
		kv := newMemKV()
		key, err := backupManager(kv, box, supplied).loadBackupKey()
		require.NoError(t, err)
		assert.Equal(t, made.Bytes(), key.Bytes())
		stored, _ := kv.Get(backupKeyKV)
		assert.True(t, secretbox.IsSealed(string(stored)))
	})
	t.Run("a stored key wins over the setting", func(t *testing.T) {
		kv := newMemKV()
		stored, err := backupManager(kv, box, "").loadBackupKey()
		require.NoError(t, err)
		key, err := backupManager(kv, box, supplied).loadBackupKey()
		require.NoError(t, err)
		assert.Equal(t, stored.Bytes(), key.Bytes())
		assert.NotEqual(t, made.Bytes(), key.Bytes())
	})
	t.Run("a key that is not a key is refused", func(t *testing.T) {
		_, err := backupManager(newMemKV(), box, "not a key!").loadBackupKey()
		assert.Error(t, err)
	})
}

func (m *Manager) encodeForTest(k *backup.MegolmBackupKey) string {
	m.backup = &backupState{key: k}
	s, _ := m.BackupKey()
	return s
}

func TestARoomKeyRoundTripsThroughTheBackup(t *testing.T) {
	ogs, err := crypto.NewOutboundGroupSession("!room:hs", nil, nil)
	require.NoError(t, err)
	igs, err := crypto.NewInboundGroupSession("sender-key", "signing-key", "!room:hs", ogs.Internal.Key(), 0, 0, nil, false)
	require.NoError(t, err)

	key, err := backup.NewMegolmBackupKey()
	require.NoError(t, err)
	data, err := sessionBackupData(key, igs)
	require.NoError(t, err)
	assert.Equal(t, 0, data.FirstMessageIndex)

	var encrypted backup.EncryptedSessionData[backup.MegolmSessionData]
	require.NoError(t, json.Unmarshal(data.SessionData, &encrypted))
	plain, err := encrypted.Decrypt(key)
	require.NoError(t, err)
	assert.Equal(t, id.AlgorithmMegolmV1, plain.Algorithm)
	assert.Equal(t, id.SenderKey("sender-key"), plain.SenderKey)
	assert.Equal(t, id.Ed25519("signing-key"), plain.SenderClaimedKeys.Ed25519)

	// The exported session key opens the same session.
	// (An exported key is imported, as mautrix does when it restores a backup.)
	restored, err := olm.InboundGroupSessionImport([]byte(plain.SessionKey))
	require.NoError(t, err)
	assert.Equal(t, igs.ID(), restored.ID())

	other, err := backup.NewMegolmBackupKey()
	require.NoError(t, err)
	_, err = encrypted.Decrypt(other)
	assert.Error(t, err, "another key cannot read the backup")
}
