package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestReplyFallbacks checks that context survives in both directions: a
// thread message sent to Matrix also says which message it answers, for
// clients without thread support, and a Matrix reply to a message in the middle
// of a thread keeps what it answered in Mattermost.
func TestReplyFallbacks(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("replies", "Replies")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Replies")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	root, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Thread root"})
	require.NoError(t, err)
	rootEvent := WaitForMatrixMessage(t, alice, roomID, "Thread root").Get("event_id").Str
	reply, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "First reply in the thread", RootId: root.Id})
	require.NoError(t, err)
	_ = reply
	replyEvent := WaitForMatrixMessage(t, alice, roomID, "First reply in the thread")

	t.Run("a thread message also says it answers the thread's root", func(t *testing.T) {
		relates := replyEvent.Get("content.m\\.relates_to")
		assert.Equal(t, "m.thread", relates.Get("rel_type").Str)
		assert.Equal(t, rootEvent, relates.Get("event_id").Str)
		assert.True(t, relates.Get("is_falling_back").Bool())
		assert.Equal(t, rootEvent, relates.Get("m\\.in_reply_to.event_id").Str)
		capabilities.Verify(t, "messages.reply_fallback", relates.Get("m\\.in_reply_to.event_id").Str == rootEvent)
	})

	t.Run("a Matrix reply to a message inside a thread quotes it in Mattermost", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "I agree with the first reply",
				"m.relates_to": map[string]interface{}{
					"rel_type":        "m.thread",
					"event_id":        rootEvent,
					"is_falling_back": false,
					"m.in_reply_to":   map[string]interface{}{"event_id": replyEvent.Get("event_id").Str},
				},
			},
		})
		post := WaitForPost(t, h.Admin, channel.Id, "the reply", func(p *model.Post) bool {
			return strings.Contains(p.Message, "I agree with the first reply")
		})
		assert.Equal(t, root.Id, post.RootId, "it lands in the thread")
		assert.Contains(t, post.Message, "> **@carol:** First reply in the thread")
		capabilities.Verify(t, "messages.reply_fallback.in", strings.Contains(post.Message, "> **@carol:** First reply in the thread"))
	})

	t.Run("a reply that only falls back to the root is not quoted", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Plain thread message",
				"m.relates_to": map[string]interface{}{
					"rel_type":        "m.thread",
					"event_id":        rootEvent,
					"is_falling_back": true,
					"m.in_reply_to":   map[string]interface{}{"event_id": rootEvent},
				},
			},
		})
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Plain thread message")
		assert.Equal(t, root.Id, post.RootId)
	})
}
