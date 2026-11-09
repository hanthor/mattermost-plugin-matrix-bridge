package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestMattermostToMatrix covers everything that flows from Mattermost into a
// mapped Matrix room. The subtests share one deployment and run in order.
func TestMattermostToMatrix(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("mm-to-matrix", "MM to Matrix")
	roomID := h.CreateMappedRoom(alice, channel.Id, "MM to Matrix")
	adminGhost := h.GhostUserID(h.AdminUser.Id)

	var rootPost *model.Post
	var rootEventID string

	t.Run("message is delivered as the ghost of the author", func(t *testing.T) {
		post, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Hello from Mattermost"})
		require.NoError(t, err)
		rootPost = post

		ev := WaitForMatrixMessage(t, alice, roomID, "Hello from Mattermost")
		assert.Equal(t, adminGhost, ev.Get("sender").Str, "message must be sent by the author's ghost")
		assert.Equal(t, "m.text", ev.Get("content.msgtype").Str)
		rootEventID = mustString(t, ev.Get("event_id"), "event_id")
		capabilities.Verify(t, "messages.text", ev.Get("sender").Str == adminGhost && ev.Get("content.msgtype").Str == "m.text")
	})

	t.Run("markdown is converted to formatted HTML", func(t *testing.T) {
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Some **bold** and `code`"})
		require.NoError(t, err)

		ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && strings.Contains(ev.Get("content.body").Str, "bold")
		})
		assert.Equal(t, "org.matrix.custom.html", ev.Get("content.format").Str)
		formatted := ev.Get("content.formatted_body").Str
		assert.Contains(t, formatted, "<strong>bold</strong>")
		assert.Contains(t, formatted, "<code>code</code>")
	})

	t.Run("edit becomes an m.replace event", func(t *testing.T) {
		require.NotNil(t, rootPost)
		_, _, err := h.Admin.PatchPost(ctx, rootPost.Id, &model.PostPatch{Message: model.NewPointer("Hello from Mattermost (edited)")})
		require.NoError(t, err)

		ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("content.m\\.relates_to.rel_type").Str == "m.replace" &&
				ev.Get("content.m\\.relates_to.event_id").Str == rootEventID
		})
		assert.Equal(t, "Hello from Mattermost (edited)", ev.Get("content.m\\.new_content.body").Str)
		assert.Equal(t, adminGhost, ev.Get("sender").Str)
		capabilities.Verify(t, "messages.edit", ev.Get("content.m\\.new_content.body").Str == "Hello from Mattermost (edited)")
	})

	t.Run("thread reply is sent as an m.thread relation", func(t *testing.T) {
		require.NotNil(t, rootPost)
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, RootId: rootPost.Id, Message: "Reply in thread"})
		require.NoError(t, err)

		ev := WaitForMatrixMessage(t, alice, roomID, "Reply in thread")
		assert.Equal(t, "m.thread", ev.Get("content.m\\.relates_to.rel_type").Str)
		assert.Equal(t, rootEventID, ev.Get("content.m\\.relates_to.event_id").Str)
		capabilities.Verify(t, "messages.thread", ev.Get("content.m\\.relates_to.rel_type").Str == "m.thread" && ev.Get("content.m\\.relates_to.event_id").Str == rootEventID)
	})

	t.Run("mention produces m.mentions", func(t *testing.T) {
		bob, _ := h.CreateMattermostUser("bob")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, bob.Id)
		require.NoError(t, err)
		// Bob's ghost is created asynchronously when he joins the channel.
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(h.GhostUserID(bob.Id), roomID))

		_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Ping @bob please"})
		require.NoError(t, err)

		ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && strings.HasPrefix(ev.Get("content.body").Str, "Ping ")
		})
		mentioned := ev.Get("content.m\\.mentions.user_ids").Array()
		var ids []string
		for _, m := range mentioned {
			ids = append(ids, m.Str)
		}
		assert.Contains(t, ids, h.GhostUserID(bob.Id), "mentioned user's ghost must be listed in m.mentions")
		capabilities.Verify(t, "messages.mentions", containsString(ids, h.GhostUserID(bob.Id)))
	})

	t.Run("reaction is added and removed", func(t *testing.T) {
		require.NotNil(t, rootPost)
		reaction := &model.Reaction{UserId: h.AdminUser.Id, PostId: rootPost.Id, EmojiName: "+1"}
		_, _, err := h.Admin.SaveReaction(ctx, reaction)
		require.NoError(t, err)

		var reactionEv gjson.Result
		WaitFor(t, bridgeTimeout, "ghost reaction on the root event", func() bool {
			for _, ann := range Annotations(t, alice, roomID, rootEventID) {
				if ann.Get("sender").Str == adminGhost {
					reactionEv = ann
					return true
				}
			}
			return false
		})
		assert.Equal(t, "m.annotation", reactionEv.Get("content.m\\.relates_to.rel_type").Str)
		assert.Equal(t, "👍", reactionEv.Get("content.m\\.relates_to.key").Str)

		_, err = h.Admin.DeleteReaction(ctx, reaction)
		require.NoError(t, err)

		WaitFor(t, bridgeTimeout, "ghost reaction to be redacted", func() bool {
			for _, ann := range Annotations(t, alice, roomID, rootEventID) {
				if ann.Get("sender").Str == adminGhost {
					return false
				}
			}
			return true
		})
		capabilities.Verify(t, "reactions.add_remove", true) // added as a 👍 annotation and redacted again, both waited for above
	})

	t.Run("file attachment is sent as a file event", func(t *testing.T) {
		upload, _, err := h.Admin.UploadFile(ctx, []byte("attachment body"), channel.Id, "notes.txt")
		require.NoError(t, err)
		require.Len(t, upload.FileInfos, 1)

		_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "See attachment", FileIds: []string{upload.FileInfos[0].Id}})
		require.NoError(t, err)

		fileEv := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && ev.Get("content.msgtype").Str == "m.file" && ev.Get("content.body").Str == "notes.txt"
		})
		mxc := mustString(t, fileEv.Get("content.url"), "content.url")
		assert.True(t, strings.HasPrefix(mxc, "mxc://"), "file must point at the media repo: %s", mxc)
		data, _ := alice.DownloadContentAuthenticated(t, mxc)
		assert.Equal(t, "attachment body", string(data))
		capabilities.Verify(t, "files.attachments", string(data) == "attachment body")
	})

	t.Run("deleted post is redacted", func(t *testing.T) {
		post, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "To be deleted"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, "To be deleted")
		eventID := ev.Get("event_id").Str

		_, err = h.Admin.DeletePost(ctx, post.Id)
		require.NoError(t, err)

		WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.redaction" && ev.Get("redacts").Str == eventID
		})
		capabilities.Verify(t, "messages.delete", true)
	})

	t.Run("channel membership is mirrored to the room", func(t *testing.T) {
		carol, _ := h.CreateMattermostUser("carol")
		carolGhost := h.GhostUserID(carol.Id)

		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(carolGhost, roomID))

		_, err = h.Admin.RemoveUserFromChannel(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(carolGhost, roomID))
		capabilities.Verify(t, "membership.join_leave", true) // joined and left, both waited for above
	})

	t.Run("profile changes update the ghost display name", func(t *testing.T) {
		dave, daveClient := h.CreateMattermostUser("dave")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, dave.Id)
		require.NoError(t, err)
		daveGhost := h.GhostUserID(dave.Id)
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(daveGhost, roomID))

		_, _, err = h.Admin.PatchUser(ctx, dave.Id, &model.UserPatch{FirstName: model.NewPointer("David"), LastName: model.NewPointer("Example")})
		require.NoError(t, err)

		// Mattermost has no profile-change hook: the ghost is refreshed on the
		// user's next activity in a bridged channel (and by a periodic sweep).
		_, _, err = daveClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Hello, I renamed myself"})
		require.NoError(t, err)

		WaitFor(t, bridgeTimeout, "ghost display name to update", func() bool {
			res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "profile", daveGhost, "displayname"})
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != http.StatusOK {
				return false
			}
			body := client.ParseJSON(t, res)
			return strings.Contains(gjson.GetBytes(body, "displayname").Str, "David")
		})
		capabilities.Verify(t, "users.profile", true)
	})

	t.Run("new channel gets a Matrix room automatically", func(t *testing.T) {
		auto := h.CreateChannel("auto-room", "Auto Room")

		// The room carries a com.mattermost.bridge.channel marker; the alias
		// may be missing when the registration reserves no alias namespace.
		bot := h.BotClient()
		var autoRoomID string
		WaitFor(t, bridgeTimeout, "a room for the new channel to be created", func() bool {
			autoRoomID = FindBridgedRoomForChannel(t, bot, auto.Id)
			return autoRoomID != ""
		})

		// The automatically created room is immediately usable: a post in the
		// new channel shows up there once alice joins.
		alice.MustJoinRoom(t, autoRoomID, nil)
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: auto.Id, Message: "First post in auto room"})
		require.NoError(t, err)
		WaitForMatrixMessage(t, alice, autoRoomID, "First post in auto room")
		capabilities.Verify(t, "rooms.create", autoRoomID != "")
	})

	t.Run("no Mattermost post is echoed back", func(t *testing.T) {
		time.Sleep(3 * time.Second)
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, "Reply in thread"), "bridged posts must not be echoed back into Mattermost")
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, "Reply in thread"), "a Mattermost post must appear exactly once in Matrix")
	})
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
