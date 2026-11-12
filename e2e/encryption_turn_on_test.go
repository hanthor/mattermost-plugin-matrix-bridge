package e2e

import (
	"net/http"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/id"
)

// TestEncryptionTurnedOnInABridgedRoom starts with a plain bridged room and
// turns end-to-end encryption on while it is already mapped. The bridge must
// notice, stop sending plaintext, and keep reading what arrives.
func TestEncryptionTurnedOnInABridgedRoom(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true})
	ctx := t.Context()
	alice := h.NewMatrixUser("alice")
	carol, carolClient := h.CreateMattermostUser("carol")

	channel := h.CreateChannel("later-secret", "Later secret")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	h.WaitForChannelRoom(channel)
	room := h.CreateMappedRoomWith(alice, channel.Id, map[string]any{"name": "Later secret"})
	roomID := id.RoomID(room)

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "sent before encryption"})
	require.NoError(t, err)
	WaitForMatrixMessage(t, alice, room, "sent before encryption")

	alice.MustDo(t, http.MethodPut, []string{"_matrix", "client", "v3", "rooms", room, "state", "m.room.encryption"},
		client.WithJSONBody(t, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}))
	ec := newEncryptedClient(t, alice)

	t.Run("a Mattermost post after that is encrypted, not plaintext", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "sent after encryption"})
		require.NoError(t, err)
		ec.WaitForMessage(roomID, "sent after encryption")
		assert.False(t, ec.ServerCopyContains(roomID, "sent after encryption"), "no plaintext on the homeserver")
		assert.Equal(t, 0, CountMatrixMessages(t, alice, room, "sent after encryption"))
		capabilities.Verify(t, "rooms.encryption.turned_on_later", CountMatrixMessages(t, alice, room, "sent after encryption") == 0)
	})

	t.Run("an encrypted Matrix message arrives in Mattermost", func(t *testing.T) {
		ec.Send(roomID, "encrypted from alice, later")
		post := WaitForPostWithMessage(t, carolClient, channel.Id, "encrypted from alice, later")
		assert.NotEmpty(t, post.Id)
	})
}
