package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestGhostsMoveToMirrorAccounts starts in ghost mode, posts as ghosts, turns
// mirror mode on and runs the migration against a real homeserver.
func TestGhostsMoveToMirrorAccounts(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	carol, carolClient := h.CreateMattermostUser("carol")
	channel := h.CreateChannel("migrating", "Migrating")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	roomID := h.CreateMappedRoom(alice, channel.Id, "Migrating")
	ghost := h.GhostUserID(carol.Id)
	mirror := "@carol:" + HSName

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "written as a ghost"})
	require.NoError(t, err)
	before := WaitForMatrixMessage(t, alice, roomID, "written as a ghost")
	assert.Equal(t, ghost, before.Get("sender").Str)

	membership := func(mxid string) string {
		res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", mxid})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			return ""
		}
		return gjson.GetBytes(client.ParseJSON(t, res), "membership").Str
	}

	t.Run("without the setting the migration says what to do first", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix migrate ghosts-to-mirror")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "turn on Mirror Mode")
	})

	h.SetBridgeSetting("enable_mirror_mode", true)

	t.Run("the dry run reports and changes nothing", func(t *testing.T) {
		var out string
		WaitFor(t, bridgeTimeout, "the setting to apply", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix migrate ghosts-to-mirror")
			require.NoError(t, err)
			out = resp.Text
			return strings.Contains(out, "Dry run")
		})
		assert.Contains(t, out, "would move to mirror accounts")
		assert.Equal(t, "join", membership(ghost), "the ghost is still in the room")
	})

	t.Run("confirm moves memberships and switches the mode", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix migrate ghosts-to-mirror confirm")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Migration finished")
		assert.Contains(t, resp.Text, "now in mirror mode")
		WaitFor(t, bridgeTimeout, "the memberships to move", func() bool {
			return membership(mirror) == "join" && membership(ghost) == "leave"
		})
	})

	t.Run("history stays attributed and new posts come from the mirror account", func(t *testing.T) {
		still := WaitForMatrixMessage(t, alice, roomID, "written as a ghost")
		assert.Equal(t, ghost, still.Get("sender").Str, "old messages keep their author")

		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "written as an account"})
		require.NoError(t, err)
		after := WaitForMatrixMessage(t, alice, roomID, "written as an account")
		assert.Equal(t, mirror, after.Get("sender").Str)
	})

	t.Run("status reports mirror mode", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
		require.NoError(t, err)
		assert.Contains(t, strings.ToLower(resp.Text), "mirror")
	})
}
