package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// The Matrix-side tests assert through the Matrix client API, the way
// Complement does. These assert from where a Mattermost user stands: what
// their client receives over the real-time websocket, and the unread and
// mention counts that drive notifications. A bridged event that exists in the
// database but never reaches a connected client would pass the other suites.

// mmClient is a Mattermost client connected to the websocket. It keeps every
// event, so a wait never misses one that arrived earlier.
type mmClient struct {
	ws *model.WebSocketClient

	mu       sync.Mutex
	events   []*model.WebSocketEvent
	consumed map[int]bool
}

func newMMClient(t *testing.T, mmURL, token string) *mmClient {
	t.Helper()
	ws, err := model.NewWebSocketClient4("ws"+strings.TrimPrefix(mmURL, "http"), token)
	require.NoError(t, err, "websocket connect")
	c := &mmClient{ws: ws, consumed: map[int]bool{}}
	ws.Listen()
	go func() {
		for ev := range ws.EventChannel {
			c.mu.Lock()
			c.events = append(c.events, ev)
			c.mu.Unlock()
		}
	}()
	t.Cleanup(ws.Close)
	return c
}

// wait returns the first event not used by an earlier wait that satisfies pred.
func (c *mmClient) wait(t *testing.T, what string, pred func(*model.WebSocketEvent) bool) *model.WebSocketEvent {
	t.Helper()
	var found *model.WebSocketEvent
	defer func() {
		if found == nil {
			c.mu.Lock()
			defer c.mu.Unlock()
			var seen []string
			for _, ev := range c.events {
				seen = append(seen, string(ev.EventType()))
			}
			t.Logf("events the client received while waiting for %s: %v", what, seen)
		}
	}()
	WaitFor(t, bridgeTimeout, "the client to receive "+what, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, ev := range c.events {
			if !c.consumed[i] && pred(ev) {
				c.consumed[i] = true
				found = ev
				return true
			}
		}
		return false
	})
	return found
}

func eventPost(t *testing.T, ev *model.WebSocketEvent) *model.Post {
	t.Helper()
	raw, _ := ev.GetData()["post"].(string)
	require.NotEmpty(t, raw, "event %s carries a post", ev.EventType())
	var p model.Post
	require.NoError(t, json.Unmarshal([]byte(raw), &p))
	return &p
}

func postedWith(channelID, message string) func(*model.WebSocketEvent) bool {
	return func(ev *model.WebSocketEvent) bool {
		if ev.EventType() != model.WebsocketEventPosted {
			return false
		}
		raw, _ := ev.GetData()["post"].(string)
		var p model.Post
		return json.Unmarshal([]byte(raw), &p) == nil && p.ChannelId == channelID && p.Message == message
	}
}

// TestMattermostClientView checks Matrix-originated activity as a connected
// Mattermost client experiences it.
func TestMattermostClientView(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("client-view", "Client view")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Client view")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	// Carol is looking at another channel, so what Alice writes is unread.
	other := h.CreateChannel("elsewhere", "Elsewhere")
	_, _, err = h.Admin.AddChannelMember(ctx, other.Id, carol.Id)
	require.NoError(t, err)
	_, _, err = carolClient.ViewChannel(ctx, carol.Id, &model.ChannelView{ChannelId: other.Id})
	require.NoError(t, err)

	carolWS := newMMClient(t, h.Mattermost.URL, carolClient.AuthToken)

	var root *model.Post
	var rootEventID string

	t.Run("a Matrix message reaches a connected client as a post event", func(t *testing.T) {
		rootEventID = SendMatrixText(t, alice, roomID, "Hello Mattermost clients")
		ev := carolWS.wait(t, "the new post", postedWith(channel.Id, "Hello Mattermost clients"))

		root = eventPost(t, ev)
		assert.Equal(t, channel.Id, ev.GetBroadcast().ChannelId, "broadcast to the channel's members")
		assert.Equal(t, model.ChannelTypeOpen, model.ChannelType(ev.GetData()["channel_type"].(string)))
		assert.Equal(t, h.Team.Id, ev.GetData()["team_id"])
		assert.NotEmpty(t, ev.GetData()["sender_name"], "clients show who wrote it")
		assert.NotNil(t, root.GetProp("from_matrix"))
		author, _, err := h.Admin.GetUser(ctx, root.UserId, "")
		require.NoError(t, err)
		assert.True(t, author.IsRemote())
		capabilities.Verify(t, "client.realtime", root.Message == "Hello Mattermost clients")
	})

	t.Run("unread messages are counted for people who are not looking", func(t *testing.T) {
		for _, text := range []string{"second", "third"} {
			SendMatrixText(t, alice, roomID, text)
		}
		WaitForPostWithMessage(t, h.Admin, channel.Id, "third")
		WaitFor(t, bridgeTimeout, "three unread messages", func() bool {
			unread, _, err := carolClient.GetChannelUnread(ctx, channel.Id, carol.Id)
			return err == nil && unread.MsgCount >= 3
		})
		unread, _, err := carolClient.GetChannelUnread(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		t.Logf("unread after three posts: %+v", unread)

		_, _, err = carolClient.ViewChannel(ctx, carol.Id, &model.ChannelView{ChannelId: channel.Id})
		require.NoError(t, err)
		unread, _, err = carolClient.GetChannelUnread(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		assert.Zero(t, unread.MsgCount, "viewing the channel clears the count")
	})

	t.Run("a mention from Matrix notifies the person it names", func(t *testing.T) {
		_, _, err := carolClient.ViewChannel(ctx, carol.Id, &model.ChannelView{ChannelId: other.Id})
		require.NoError(t, err)
		// Mattermost's own "you were added" notice already counts as a mention,
		// so measure the change.
		before, _, err := carolClient.GetChannelUnread(ctx, channel.Id, carol.Id)
		require.NoError(t, err)

		ghost := h.GhostUserID(carol.Id)
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype":        "m.text",
				"body":           "Carol Carter: are you there?",
				"format":         "org.matrix.custom.html",
				"formatted_body": `<a href="https://matrix.to/#/` + ghost + `">Carol Carter</a>: are you there?`,
				"m.mentions":     map[string]interface{}{"user_ids": []string{ghost}},
			},
		})
		ev := carolWS.wait(t, "a post that mentions her", func(ev *model.WebSocketEvent) bool {
			if ev.EventType() != model.WebsocketEventPosted {
				return false
			}
			mentions, _ := ev.GetData()["mentions"].(string)
			return strings.Contains(mentions, carol.Id) && strings.Contains(eventPost(t, ev).Message, "are you there")
		})
		assert.Contains(t, eventPost(t, ev).Message, "@"+carol.Username, "the mention is a real @mention in the text")
		WaitFor(t, bridgeTimeout, "the mention to be counted", func() bool {
			unread, _, err := carolClient.GetChannelUnread(ctx, channel.Id, carol.Id)
			return err == nil && unread.MentionCount > before.MentionCount
		})
		capabilities.Verify(t, "client.mentions", true)
	})

	t.Run("an edit reaches clients as post_edited", func(t *testing.T) {
		require.NotNil(t, root)
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype":       "m.text",
				"body":          "* Hello Mattermost clients, edited",
				"m.new_content": map[string]interface{}{"msgtype": "m.text", "body": "Hello Mattermost clients, edited"},
				"m.relates_to":  map[string]interface{}{"rel_type": "m.replace", "event_id": rootEventID},
			},
		})
		ev := carolWS.wait(t, "the edit", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventPostEdited && eventPost(t, ev).Id == root.Id
		})
		assert.Equal(t, "Hello Mattermost clients, edited", eventPost(t, ev).Message)
		assert.NotZero(t, eventPost(t, ev).EditAt, "clients mark it as edited")
	})

	t.Run("a threaded reply arrives in its thread", func(t *testing.T) {
		require.NotNil(t, root)
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.text", "body": "A reply in the thread",
				"m.relates_to": map[string]interface{}{
					"rel_type": "m.thread", "event_id": rootEventID, "is_falling_back": true,
					"m.in_reply_to": map[string]interface{}{"event_id": rootEventID},
				},
			},
		})
		ev := carolWS.wait(t, "the reply", postedWith(channel.Id, "A reply in the thread"))
		assert.Equal(t, root.Id, eventPost(t, ev).RootId)

		thread, _, err := carolClient.GetPostThread(ctx, root.Id, "", false)
		require.NoError(t, err)
		assert.Len(t, thread.Posts, 2, "the thread has the root and the reply")
	})

	t.Run("reactions arrive and leave", func(t *testing.T) {
		require.NotNil(t, root)
		reaction := alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.reaction",
			Content: map[string]interface{}{"m.relates_to": map[string]interface{}{
				"rel_type": "m.annotation", "event_id": rootEventID, "key": "👍",
			}},
		})
		reactionOn := func(eventType model.WebsocketEventType) func(*model.WebSocketEvent) bool {
			return func(ev *model.WebSocketEvent) bool {
				if ev.EventType() != eventType {
					return false
				}
				raw, _ := ev.GetData()["reaction"].(string)
				var r model.Reaction
				return json.Unmarshal([]byte(raw), &r) == nil && r.PostId == root.Id
			}
		}
		ev := carolWS.wait(t, "reaction_added on the post", reactionOn(model.WebsocketEventReactionAdded))
		var added model.Reaction
		require.NoError(t, json.Unmarshal([]byte(ev.GetData()["reaction"].(string)), &added))
		assert.Equal(t, "+1", added.EmojiName, "👍 is the +1 emoji")

		alice.MustSendRedaction(t, roomID, map[string]interface{}{}, reaction)
		carolWS.wait(t, "reaction_removed from the post", reactionOn(model.WebsocketEventReactionRemoved))
	})

	t.Run("a typing Matrix user shows as typing", func(t *testing.T) {
		require.NotNil(t, root)
		go func() {
			for i := 0; i < 5; i++ {
				alice.MustSendTyping(t, roomID, true, 30000)
				time.Sleep(time.Second)
			}
		}()
		ev := carolWS.wait(t, "the typing indicator", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventTyping && ev.GetBroadcast().ChannelId == channel.Id && ev.GetData()["user_id"] == root.UserId
		})
		assert.NotNil(t, ev)
	})

	t.Run("a deleted message disappears from clients", func(t *testing.T) {
		eventID := SendMatrixText(t, alice, roomID, "Gone soon")
		ev := carolWS.wait(t, "the post to delete", postedWith(channel.Id, "Gone soon"))
		doomed := eventPost(t, ev)

		alice.MustSendRedaction(t, roomID, map[string]interface{}{}, eventID)
		carolWS.wait(t, "post_deleted", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventPostDeleted && eventPost(t, ev).Id == doomed.Id
		})
	})

	t.Run("an image arrives as a file clients can download", func(t *testing.T) {
		png := []byte("\x89PNG\r\n\x1a\nthe bytes of a picture")
		mxc := alice.UploadContent(t, png, "photo.png", "image/png")
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.image", "body": "photo.png", "url": mxc,
				"info": map[string]interface{}{"mimetype": "image/png", "size": len(png)},
			},
		})
		ev := carolWS.wait(t, "the post with the picture", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventPosted && len(eventPost(t, ev).FileIds) == 1
		})
		fileID := eventPost(t, ev).FileIds[0]
		data, _, err := carolClient.GetFile(ctx, fileID)
		require.NoError(t, err, "a channel member can download it")
		assert.Equal(t, png, data, "the file is the one Alice sent")
		info, _, err := carolClient.GetFileInfo(ctx, fileID)
		require.NoError(t, err)
		assert.Equal(t, "image/png", info.MimeType)
	})

	t.Run("the room's topic becomes the channel header on connected clients", func(t *testing.T) {
		alice.SendEventSynced(t, roomID, b.Event{
			Type: "m.room.topic", StateKey: model.NewPointer(""),
			Content: map[string]interface{}{"topic": "Set from Element"},
		})
		ev := carolWS.wait(t, "the channel update", func(ev *model.WebSocketEvent) bool {
			if ev.EventType() != model.WebsocketEventChannelUpdated {
				return false
			}
			raw, _ := ev.GetData()["channel"].(string)
			return strings.Contains(raw, "Set from Element")
		})
		assert.NotNil(t, ev)
	})

	t.Run("people joining and leaving from Matrix show up as members", func(t *testing.T) {
		bob := h.NewMatrixUser("bob")
		bob.MustJoinRoom(t, roomID, nil)
		bobMM := WaitForBridgedUserInChannel(t, h, channel.Id, "bob")
		carolWS.wait(t, "user_added", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventUserAdded && ev.GetData()["user_id"] == bobMM.Id
		})

		bob.MustLeaveRoom(t, roomID)
		carolWS.wait(t, "user_removed", func(ev *model.WebSocketEvent) bool {
			return ev.EventType() == model.WebsocketEventUserRemoved && ev.GetData()["user_id"] == bobMM.Id
		})
	})

	t.Run("a Matrix display name change reaches clients as a user update", func(t *testing.T) {
		require.NotNil(t, root)
		alice.MustSetDisplayName(t, "Alice Liddell")
		carolWS.wait(t, "user_updated", func(ev *model.WebSocketEvent) bool {
			if ev.EventType() != model.WebsocketEventUserUpdated {
				return false
			}
			// The client decodes the user into a struct; look at it as JSON.
			raw, err := json.Marshal(ev.GetData()["user"])
			if err != nil {
				return false
			}
			return strings.Contains(string(raw), root.UserId) && strings.Contains(string(raw), "Liddell")
		})
	})
}
