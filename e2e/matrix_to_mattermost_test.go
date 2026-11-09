package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestMatrixToMattermost covers everything that flows from a mapped Matrix room
// into Mattermost. The subtests share one deployment and run in order.
func TestMatrixToMattermost(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("matrix-to-mm", "Matrix to MM")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Matrix to MM")

	var aliceMM *model.User
	var rootPost *model.Post
	var rootEventID string

	t.Run("message creates a bridged user and a post", func(t *testing.T) {
		rootEventID = SendMatrixText(t, alice, roomID, "Hello from Matrix")

		rootPost = WaitForPostWithMessage(t, h.Admin, channel.Id, "Hello from Matrix")
		author, _, err := h.Admin.GetUser(ctx, rootPost.UserId, "")
		require.NoError(t, err)
		assert.True(t, author.IsRemote(), "Matrix users are represented as remote users")
		assert.Contains(t, author.Username, "alice")
		assert.NotNil(t, rootPost.GetProp("from_matrix"))
		aliceMM = author
		capabilities.Verify(t, "messages.text.in", author.IsRemote() && rootPost.GetProp("from_matrix") != nil)
	})

	t.Run("HTML is converted to markdown", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype":        "m.text",
				"body":           "rich text",
				"format":         "org.matrix.custom.html",
				"formatted_body": "<strong>rich</strong> <em>text</em>",
			},
		})
		WaitForPostWithMessage(t, h.Admin, channel.Id, "**rich** *text*")
	})

	t.Run("edit updates the post", func(t *testing.T) {
		require.NotNil(t, rootPost)
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "* Hello from Matrix (edited)",
				"m.new_content": map[string]interface{}{
					"msgtype": "m.text",
					"body":    "Hello from Matrix (edited)",
				},
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.replace",
					"event_id": rootEventID,
				},
			},
		})
		WaitFor(t, bridgeTimeout, "post to be edited", func() bool {
			post, _, err := h.Admin.GetPost(ctx, rootPost.Id, "")
			return err == nil && post.Message == "Hello from Matrix (edited)"
		})
		capabilities.Verify(t, "messages.edit.in", true)
	})

	t.Run("thread reply lands in the post's thread", func(t *testing.T) {
		require.NotNil(t, rootPost)
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text",
				"body":    "Threaded reply",
				"m.relates_to": map[string]interface{}{
					"rel_type":        "m.thread",
					"event_id":        rootEventID,
					"is_falling_back": true,
					"m.in_reply_to":   map[string]interface{}{"event_id": rootEventID},
				},
			},
		})
		reply := WaitForPostWithMessage(t, h.Admin, channel.Id, "Threaded reply")
		assert.Equal(t, rootPost.Id, reply.RootId)
		capabilities.Verify(t, "messages.thread.in", reply.RootId == rootPost.Id)
	})

	t.Run("reaction is added and removed", func(t *testing.T) {
		require.NotNil(t, rootPost)
		reactionEventID := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.reaction",
			Content: map[string]interface{}{
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.annotation",
					"event_id": rootEventID,
					"key":      "👍",
				},
			},
		})
		WaitFor(t, bridgeTimeout, "reaction to appear on post", func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, rootPost.Id)
			return err == nil && len(reactions) == 1 && reactions[0].UserId == aliceMM.Id
		})

		alice.MustSendRedaction(t, roomID, map[string]interface{}{"reason": "changed my mind"}, reactionEventID)
		WaitFor(t, bridgeTimeout, "reaction to be removed", func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, rootPost.Id)
			return err == nil && len(reactions) == 0
		})
		capabilities.Verify(t, "reactions.add_remove.in", true) // added then removed, both waited for above
	})

	t.Run("image upload becomes a file attachment", func(t *testing.T) {
		png := []byte("\x89PNG\r\n\x1a\nnot really a png but enough for a bridge")
		mxc := alice.UploadContent(t, png, "pic.png", "image/png")
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.image",
				"body":    "pic.png",
				"url":     mxc,
				"info":    map[string]interface{}{"mimetype": "image/png", "size": len(png)},
			},
		})
		post := WaitForPost(t, h.Admin, channel.Id, "post with attachment", func(p *model.Post) bool {
			return len(p.FileIds) == 1
		})
		info, _, err := h.Admin.GetFileInfo(ctx, post.FileIds[0])
		require.NoError(t, err)
		assert.Equal(t, "pic.png", info.Name)
		capabilities.Verify(t, "files.attachments.in", info.Name == "pic.png")
	})

	t.Run("redacted message is deleted", func(t *testing.T) {
		eventID := SendMatrixText(t, alice, roomID, "Delete me")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Delete me")

		alice.MustSendRedaction(t, roomID, map[string]interface{}{}, eventID)
		WaitFor(t, bridgeTimeout, "post to be deleted", func() bool {
			_, resp, err := h.Admin.GetPost(ctx, post.Id, "")
			return err != nil && resp != nil && resp.StatusCode == 404
		})
		capabilities.Verify(t, "messages.delete.in", true)
	})

	t.Run("joining the room adds the user to the channel and leaving removes them", func(t *testing.T) {
		bob := h.NewMatrixUser("bob")
		bob.MustJoinRoom(t, roomID, nil)
		bobMM := WaitForBridgedUserInChannel(t, h, channel.Id, "bob")

		bob.MustLeaveRoom(t, roomID)
		WaitFor(t, bridgeTimeout, "bob to be removed from channel", func() bool {
			return !IsUserInChannel(t, h.Admin, channel.Id, bobMM.Id)
		})
		capabilities.Verify(t, "membership.join_leave.in", true) // joined and left, both waited for above
	})

	t.Run("display name change is synced to the user profile", func(t *testing.T) {
		require.NotNil(t, aliceMM)
		alice.MustSetDisplayName(t, "Alice Liddell")
		WaitFor(t, bridgeTimeout, "display name to sync", func() bool {
			user, _, err := h.Admin.GetUser(ctx, aliceMM.Id, "")
			if err != nil {
				return false
			}
			return user.Nickname == "Alice Liddell" || user.FirstName == "Alice"
		})
		capabilities.Verify(t, "users.profile.in", true)
	})

	t.Run("room name and topic update the channel", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.name",
			StateKey: model.NewPointer(""),
			Content:  map[string]interface{}{"name": "Renamed from Matrix"},
		})
		alice.SendEventSynced(t, roomID, b.Event{
			Type:     "m.room.topic",
			StateKey: model.NewPointer(""),
			Content:  map[string]interface{}{"topic": "Topic from Matrix"},
		})
		WaitFor(t, bridgeTimeout, "channel name and header to update", func() bool {
			ch, _, err := h.Admin.GetChannel(ctx, channel.Id)
			return err == nil && ch.DisplayName == "Renamed from Matrix" && ch.Header == "Topic from Matrix"
		})
		capabilities.Verify(t, "rooms.name_topic.in", true)
	})

	t.Run("typing notifications reach Mattermost", func(t *testing.T) {
		require.NotNil(t, aliceMM)
		done := make(chan bool, 1)
		go func() {
			done <- WaitForTypingEvent(t, h.Mattermost.URL, h.Admin.AuthToken, channel.Id, aliceMM.Id, bridgeTimeout)
		}()
		// Give the websocket a moment to connect, then type a few times.
		time.Sleep(2 * time.Second)
		for i := 0; i < 5; i++ {
			alice.MustSendTyping(t, roomID, true, 30000)
			time.Sleep(time.Second)
		}
		typed := <-done
		assert.True(t, typed, "expected a typing websocket event for the bridged user")
		capabilities.Verify(t, "ephemeral.typing.in", typed)
	})

	t.Run("no Matrix message is echoed back", func(t *testing.T) {
		time.Sleep(3 * time.Second)
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, "Threaded reply"), "Matrix messages must not be echoed back into the room")
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, "Threaded reply"), "a Matrix message must create exactly one post")
	})
}
