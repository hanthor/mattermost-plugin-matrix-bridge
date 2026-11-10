package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestMatrixHistoryImport maps a room that already has history and copies it
// into the channel.
func TestMatrixHistoryImport(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	roomID := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat", "name": "Old room"})
	before := time.Now().Add(-time.Second)

	first := SendMatrixText(t, alice, roomID, "the first message ever")
	SendMatrixText(t, alice, roomID, "a second message")
	alice.SendEventSynced(t, roomID, b.Event{Type: "m.room.message", Content: map[string]interface{}{
		"msgtype": "m.text", "body": "* the first message, edited",
		"m.new_content": map[string]interface{}{"msgtype": "m.text", "body": "the first message, edited"},
		"m.relates_to":  map[string]interface{}{"rel_type": "m.replace", "event_id": first},
	}})
	alice.SendEventSynced(t, roomID, b.Event{Type: "m.reaction", Content: map[string]interface{}{
		"m.relates_to": map[string]interface{}{"rel_type": "m.annotation", "event_id": first, "key": "👍"},
	}})
	time.Sleep(2 * time.Second) // the messages are now older than the mapping

	channel := h.CreateChannel("old-room", "Old room")
	h.MapChannelToRoom(channel.Id, roomID)
	WaitFor(t, bridgeTimeout, "the bridge to join the room", func() bool {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history from-matrix")
		return err == nil && strings.Contains(resp.Text, "would be imported")
	})

	t.Run("the preview says what would be imported and imports nothing", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history from-matrix")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "2 messages and 1 reactions would be imported")
		assert.Contains(t, resp.Text, "1 edits are applied")
		posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 30, "", false, false)
		require.NoError(t, err)
		for _, id := range posts.Order {
			assert.NotContains(t, posts.Posts[id].Message, "first message")
		}
	})

	t.Run("confirm copies the history with its original times", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history from-matrix confirm")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "History import started")

		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "the first message, edited")
		assert.Less(t, post.CreateAt, before.Add(3*time.Second).UnixMilli(), "the post keeps the time it was written in Matrix")
		WaitForPostWithMessage(t, h.Admin, channel.Id, "a second message")
		WaitFor(t, bridgeTimeout, "the reaction", func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, post.Id)
			return err == nil && len(reactions) == 1
		})
		capabilities.Verify(t, "history.import.in", true)
	})

	t.Run("running it again changes nothing", func(t *testing.T) {
		time.Sleep(3 * time.Second)
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history from-matrix")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Nothing to import")
	})
}
