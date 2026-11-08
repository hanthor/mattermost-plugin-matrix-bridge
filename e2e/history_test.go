package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestHistoryImport checks that a channel's past messages can be brought into
// its room the way a migration needs: as their authors, in order, keeping the
// time they were written, threads intact, and safe to repeat.
func TestHistoryImport(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("history", "History")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	// The channel has history before it is bridged: drop the automatic room,
	// write the past, then bridge.
	h.WaitForChannelRoom(channel)
	resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
	require.NoError(t, err)
	require.NotContains(t, resp.Text, "❌", resp.Text)

	yearAgo := time.Now().AddDate(-1, 0, 0).UnixMilli()
	first, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "A message from last year", CreateAt: yearAgo})
	require.NoError(t, err)
	reply, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "And a reply in its thread", RootId: first.Id, CreateAt: yearAgo + 60_000})
	require.NoError(t, err)
	assert.Less(t, first.CreateAt, time.Now().AddDate(0, -6, 0).UnixMilli(), "the post really is old (the server kept create_at)")

	roomID := h.CreateMappedRoom(alice, channel.Id, "History")

	t.Run("a preview says what would be imported and sends nothing", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "2 messages would be imported", resp.Text)
		assert.Equal(t, 0, CountMatrixMessages(t, alice, roomID, "A message from last year"))
	})

	t.Run("only system admins can import", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix history confirm")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Only system administrators", resp.Text)
	})

	t.Run("history arrives as its authors, with the original times, in its thread", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "History import started", resp.Text)

		root := WaitForMatrixMessage(t, alice, roomID, "A message from last year")
		assert.Equal(t, h.GhostUserID(carol.Id), root.Get("sender").Str, "sent by the author, not by the bridge bot")
		assert.Equal(t, first.CreateAt, root.Get("origin_server_ts").Int(), "the event keeps the time the post was written")

		threaded := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("content.body").Str == "And a reply in its thread"
		})
		assert.Equal(t, reply.CreateAt, threaded.Get("origin_server_ts").Int())
		assert.Equal(t, "m.thread", threaded.Get("content.m\\.relates_to.rel_type").Str)
		assert.Equal(t, root.Get("event_id").Str, threaded.Get("content.m\\.relates_to.event_id").Str)
		capabilities.Verify(t, "history.import", root.Get("origin_server_ts").Int() == first.CreateAt)
	})

	t.Run("running it again changes nothing", func(t *testing.T) {
		WaitFor(t, bridgeTimeout, "the import to finish", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history")
			return err == nil && !containsFold(resp.Text, "is running")
		})
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history confirm")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Nothing to import", resp.Text)
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, "A message from last year"), "no duplicate in the room")
	})
}
