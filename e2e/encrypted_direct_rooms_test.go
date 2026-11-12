package e2e

import (
	"context"
	"net/url"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestDirectRoomsCanStartEncrypted turns on "Create Direct Message Rooms
// Encrypted": once two people agree to bridge their conversation, the room the
// bridge creates for it has end-to-end encryption from the start, so the
// homeserver never holds the messages in the clear.
func TestDirectRoomsCanStartEncrypted(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{DirectMessages: true, Encryption: true, EncryptDirectRooms: true})
	ctx := context.Background()

	carol, carolClient := h.CreateMattermostUser("carol")
	dave, daveClient := h.CreateMattermostUser("dave")
	carolGhost := h.GhostUserID(carol.Id)
	dm, _, err := carolClient.CreateDirectChannel(ctx, carol.Id, dave.Id)
	require.NoError(t, err)

	lobby := h.CreateChannel("lobby", "Lobby")
	for _, u := range []string{carol.Id, dave.Id} {
		_, _, err := h.Admin.AddChannelMember(ctx, lobby.Id, u)
		require.NoError(t, err)
	}
	for _, cl := range []*model.Client4{carolClient, daveClient} {
		_, _, err := cl.ExecuteCommand(ctx, lobby.Id, "/matrix dm on")
		require.NoError(t, err)
	}

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: dm.Id, Message: "a private hello"})
	require.NoError(t, err)

	var room string
	WaitFor(t, bridgeTimeout, "the conversation's room", func() bool {
		for _, r := range joinedRoomsOf(t, h, carolGhost) {
			_, out := asRequest(t, h, "GET", "/_matrix/client/v3/rooms/"+url.PathEscape(r)+"/messages?dir=b&limit=50", carolGhost, nil)
			found := false
			out.Get("chunk").ForEach(func(_, ev gjson.Result) bool {
				if ev.Get("type").Str == "m.room.encrypted" {
					found = true
				}
				return true
			})
			if found {
				room = r
				return true
			}
		}
		return false
	})

	status, state := asRequest(t, h, "GET", "/_matrix/client/v3/rooms/"+url.PathEscape(room)+"/state/m.room.encryption", carolGhost, nil)
	assert.Equal(t, 200, status, "the room has an encryption setting")
	assert.Equal(t, "m.megolm.v1.aes-sha2", state.Get("algorithm").Str)
	assert.NotContains(t, messagesSeenBy(t, h, carolGhost, room), "a private hello", "no message is stored in the clear")
	capabilities.Verify(t, "rooms.dm.encrypted", status == 200 && state.Get("algorithm").Str != "")
}
