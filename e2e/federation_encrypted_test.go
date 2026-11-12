package e2e

import (
	"testing"

	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestEncryptedRoomWithAMemberFromAnotherHomeserver checks key sharing across
// federation: the bridge's room is encrypted, and one participant is on a
// different homeserver, so room keys and device lists must travel between
// servers for either side to read the other.
func TestEncryptedRoomWithAMemberFromAnotherHomeserver(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true, Federation: true})
	ctx := t.Context()

	lena := h.NewMatrixUser("lena")
	dora := h.NewRemoteMatrixUser("dora")
	carol, carolClient := h.CreateMattermostUser("carol")
	channel := h.CreateChannel("fed-secret", "Fed secret")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	h.WaitForChannelRoom(channel)

	encryption := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
	room := h.CreateMappedRoomWith(lena, channel.Id, map[string]any{"name": "Fed secret", "initial_state": encryption})
	roomID := id.RoomID(room)
	dora.MustJoinRoom(t, room, []spec.ServerName{spec.ServerName(HSName)})
	ec := newEncryptedClient(t, dora)

	t.Run("a Mattermost post is readable by a member on the other homeserver", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "for hs2, encrypted"})
		require.NoError(t, err)
		ec.WaitForMessage(roomID, "for hs2, encrypted")
		assert.False(t, ec.ServerCopyContains(roomID, "for hs2, encrypted"), "no plaintext on the homeserver")
	})

	t.Run("an encrypted message from the other homeserver arrives in Mattermost", func(t *testing.T) {
		ec.Send(roomID, "from hs2, encrypted")
		post := WaitForPostWithMessage(t, carolClient, channel.Id, "from hs2, encrypted")
		author, _, err := h.Admin.GetUser(ctx, post.UserId, "")
		require.NoError(t, err)
		assert.Contains(t, author.Username, "dora")
		capabilities.Verify(t, "federation.encrypted_remote_member", post.Id != "")
	})
}
