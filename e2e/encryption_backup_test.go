package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// keyLoss is how a scenario loses the bridge's encryption state and what it
// has to get the room keys back.
type keyLoss int

const (
	// lostStateKeptSealedKey: the encryption state is gone but the bridge's
	// sealed backup key survives in Mattermost's database.
	lostStateKeptSealedKey keyLoss = iota
	// lostEverythingKeySupplied: the database is gone too; an administrator
	// supplies the backup key they saved in the Encryption Backup Key setting.
	lostEverythingKeySupplied
	// lostStateNoBackup is the control: no backup, so nothing to restore from.
	lostStateNoBackup
)

// The message the bridge must read after the loss is sent while the bridge is
// down, in a room-key session its new device was never given. Only a restored
// key can open it.

func TestEncryptionKeyBackupRestoresTheRoomKeys(t *testing.T) {
	loseTheEncryptionStateAndImport(t, lostStateKeptSealedKey)
}

func TestEncryptionKeyBackupRestoresWithTheSuppliedKeyAfterLosingTheDatabase(t *testing.T) {
	loseTheEncryptionStateAndImport(t, lostEverythingKeySupplied)
}

// TestWithoutAKeyBackupLostRoomKeysStayLost is the control: the same loss with
// backup off leaves the message unreadable, so the restores above are what made
// it readable.
func TestWithoutAKeyBackupLostRoomKeysStayLost(t *testing.T) {
	loseTheEncryptionStateAndImport(t, lostStateNoBackup)
}

func loseTheEncryptionStateAndImport(t *testing.T, loss keyLoss) {
	withBackup := loss != lostStateNoBackup
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true, EncryptionKeyBackup: withBackup})
	ctx := context.Background()
	alice := h.NewMatrixUser("alice")
	carol, carolClient := h.CreateMattermostUser("carol")

	channel := h.CreateChannel("backed-up", "Backed up")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	h.WaitForChannelRoom(channel)
	encryption := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
	room := h.CreateMappedRoomWith(alice, channel.Id, map[string]any{"name": "Backed up", "initial_state": encryption})
	roomID := id.RoomID(room)
	ec := newEncryptedClient(t, alice)

	// The bridge learns alice's room key from this message.
	ec.Send(roomID, "read while the bridge is healthy")
	WaitForPostWithMessage(t, carolClient, channel.Id, "read while the bridge is healthy")

	var backupKey string
	if withBackup {
		backupCount := func() int64 {
			status, out := asRequest(t, h, http.MethodGet, "/_matrix/client/v3/room_keys/version", h.BridgeBotUserID(), nil)
			if status != http.StatusOK {
				return 0
			}
			return out.Get("count").Int()
		}
		WaitFor(t, bridgeTimeout, "the bridge to back its room keys up to the homeserver", func() bool { return backupCount() >= 1 })

		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix encryption-backup-key")
		require.NoError(t, err)
		parts := strings.Split(resp.Text, "```")
		require.Len(t, parts, 3, "the key is shown in a code block: %s", resp.Text)
		backupKey = strings.TrimSpace(parts[1])
		require.NotEmpty(t, backupKey)

		denied, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix encryption-backup-key")
		require.NoError(t, err)
		assert.NotContains(t, denied.Text, backupKey, "a member is not shown the key")
	}

	// Lose what the bridge knew.
	_, err = h.Admin.DisablePlugin(ctx, PluginID)
	require.NoError(t, err)
	keep := " AND pkey <> 'e2ee_backup_key'"
	if loss == lostEverythingKeySupplied {
		keep = "" // the sealed key goes too
	}
	h.Mattermost.ExecSQL(t, "DELETE FROM pluginkeyvaluestore WHERE pluginid = '"+PluginID+"' AND pkey LIKE 'e2ee\\_%'"+keep)
	if loss == lostEverythingKeySupplied {
		h.SetBridgeSetting("encryption_backup_key", backupKey)
	}

	// Sent while the bridge is down, in the session the bridge already had a
	// copy of in its (now lost) state.
	ec.Send(roomID, "sent while the bridge was down")

	_, err = h.Admin.EnablePlugin(ctx, PluginID)
	require.NoError(t, err)
	h.waitForBridgeCommands(ctx)
	WaitFor(t, bridgeTimeout, "the bridge to be running with encryption again", func() bool {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
		return err == nil && resp != nil && strings.Contains(resp.Text, "encrypts and decrypts")
	})

	resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history from-matrix confirm")
	require.NoError(t, err)
	if !withBackup {
		assert.Contains(t, resp.Text, "cannot be read by the bridge", "without the backup the room key is gone")
		assert.NotContains(t, resp.Text, "History import started")
		return
	}
	require.Contains(t, resp.Text, "History import started", resp.Text)
	post := WaitForPostWithMessage(t, h.Admin, channel.Id, "sent while the bridge was down")
	assert.NotEmpty(t, post.Id)
	if loss == lostStateKeptSealedKey {
		capabilities.Verify(t, "rooms.encryption.key_backup", post.Id != "")
	}
}
