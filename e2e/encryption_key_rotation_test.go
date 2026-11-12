package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"maunium.net/go/mautrix/id"
)

// megolmSessions lists the distinct room-key session IDs of the encrypted
// events in a room, as the homeserver stores them.
func megolmSessions(t *testing.T, cs *client.CSAPI, room string) map[string]bool {
	t.Helper()
	res := cs.MustDo(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", room, "messages"},
		client.WithQueries(map[string][]string{"dir": {"b"}, "limit": {"100"}}))
	sessions := map[string]bool{}
	gjson.GetBytes(client.ParseJSON(t, res), "chunk").ForEach(func(_, ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.encrypted" {
			if sid := ev.Get("content.session_id").Str; sid != "" {
				sessions[sid] = true
			}
		}
		return true
	})
	return sessions
}

// TestRoomKeyChangesWhenAMemberLeaves checks that a person who leaves an
// encrypted bridged room cannot read what the bridge sends afterwards: the
// bridge must start a new room key (session) once the member is gone.
func TestRoomKeyChangesWhenAMemberLeaves(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true})
	ctx := t.Context()
	alice := h.NewMatrixUser("alice")
	bob := h.NewMatrixUser("bob")
	carol, carolClient := h.CreateMattermostUser("carol")

	channel := h.CreateChannel("rotate", "Rotate")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	h.WaitForChannelRoom(channel)
	encryption := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
	room := h.CreateMappedRoomWith(alice, channel.Id, map[string]any{"name": "Rotate", "initial_state": encryption})
	roomID := id.RoomID(room)
	bob.MustJoinRoom(t, room, []spec.ServerName{spec.ServerName(HSName)})
	ec := newEncryptedClient(t, alice)

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "while bob is here"})
	require.NoError(t, err)
	ec.WaitForMessage(roomID, "while bob is here")
	before := megolmSessions(t, alice, room)
	require.NotEmpty(t, before, "the first message was encrypted with a room key")

	// Control: while nobody has left, the next message reuses the same key.
	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "still with bob"})
	require.NoError(t, err)
	ec.WaitForMessage(roomID, "still with bob")
	assert.Equal(t, before, megolmSessions(t, alice, room), "no one left, so the room key is unchanged")

	bob.MustLeaveRoom(t, room)

	// The bridge learns of the leave through the homeserver; keep posting until
	// a message uses a key bob never had.
	i := 0
	WaitFor(t, bridgeTimeout, "a message encrypted with a new room key after bob left", func() bool {
		i++
		msg := fmt.Sprintf("after bob left %d", i)
		if _, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: msg}); err != nil {
			return false
		}
		ec.WaitForMessage(roomID, msg)
		now := megolmSessions(t, alice, room)
		for sid := range now {
			if !before[sid] {
				return true
			}
		}
		return false
	})
	assert.Greater(t, len(megolmSessions(t, alice, room)), len(before))
	capabilities.Verify(t, "rooms.encryption.key_rotation", len(megolmSessions(t, alice, room)) > len(before))
}
