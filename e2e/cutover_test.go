package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestTeamCutover walks the last steps of moving a team to Matrix: the report,
// read-only channels with a pinned link, and archiving.
func TestTeamCutover(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("cutover", "Cutover")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Cutover")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Before the cut-over"})
	require.NoError(t, err)
	WaitForMatrixMessage(t, alice, roomID, "Before the cut-over")

	run := func(cmd string) string {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, cmd)
		require.NoError(t, err)
		return resp.Text
	}

	t.Run("the report compares both sides", func(t *testing.T) {
		text := run("/matrix migrate-team report")
		assert.Contains(t, text, "~cutover")
		assert.Contains(t, text, "Cut-over report")
	})

	t.Run("nothing changes without confirm", func(t *testing.T) {
		assert.Contains(t, run("/matrix migrate-team readonly"), "would become read-only")
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Still allowed"})
		require.NoError(t, err)
	})

	t.Run("read-only channels refuse people, pin the link and still take Matrix messages", func(t *testing.T) {
		assert.Contains(t, run("/matrix migrate-team readonly confirm"), "now read-only")

		_, resp, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Refused"})
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.GreaterOrEqual(t, resp.StatusCode, 400)

		pinned, _, err := h.Admin.GetPinnedPosts(ctx, channel.Id, "")
		require.NoError(t, err)
		var notice *model.Post
		for _, p := range pinned.Posts {
			if strings.Contains(p.Message, "moved to Matrix") {
				notice = p
			}
		}
		require.NotNil(t, notice, "a pinned message points at the room")
		assert.Contains(t, notice.Message, roomID)

		SendMatrixText(t, alice, roomID, "Said in Matrix after the cut-over")
		WaitForPostWithMessage(t, h.Admin, channel.Id, "Said in Matrix after the cut-over")
		capabilities.Verify(t, "mirror.team_cutover", true)
	})

	t.Run("writable undoes it", func(t *testing.T) {
		assert.Contains(t, run("/matrix migrate-team writable confirm"), "accept messages again")
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Allowed again"})
		require.NoError(t, err)
	})

	t.Run("archive needs read-only and confirm", func(t *testing.T) {
		assert.Contains(t, run("/matrix migrate-team archive confirm"), "Nothing to archive")
		run("/matrix migrate-team readonly confirm")
		assert.Contains(t, run("/matrix migrate-team archive"), "would be archived")
		assert.Contains(t, run("/matrix migrate-team archive confirm"), "1 channels archived")
		ch, _, err := h.Admin.GetChannel(ctx, channel.Id)
		require.NoError(t, err)
		assert.NotZero(t, ch.DeleteAt, "the channel is archived")
	})
}
