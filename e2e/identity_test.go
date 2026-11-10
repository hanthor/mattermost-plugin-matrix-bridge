package e2e

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

var linkCodePattern = regexp.MustCompile(`!mattermost link ([A-Z0-9]{8})`)

// TestIdentityLinking covers "one identity on both sides": the bridge bot in
// Matrix, self-service account linking and double puppeting.
func TestIdentityLinking(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("identity", "Identity")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Identity")

	// Carol is the Mattermost user who will link her account to alice.
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	// A DM between alice and the bridge bot.
	dm := alice.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat", "is_direct": true, "invite": []string{h.BridgeBotUserID()}})

	t.Run("the bot accepts the invite and answers help", func(t *testing.T) {
		SendMatrixText(t, alice, dm, "!mattermost help")
		reply := WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && ev.Get("content.msgtype").Str == "m.notice" &&
				strings.Contains(ev.Get("content.body").Str, "!mattermost link")
		})
		assert.Contains(t, reply.Get("content.body").Str, "!mattermost whoami")
		capabilities.Verify(t, "integration.bot_commands.in", true)
	})

	var code string
	t.Run("/matrix link issues a code", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix link")
		require.NoError(t, err)
		m := linkCodePattern.FindStringSubmatch(resp.Text)
		require.Len(t, m, 2, "link command must contain the code: %s", resp.Text)
		code = m[1]
	})

	t.Run("redeeming the code links the accounts", func(t *testing.T) {
		SendMatrixText(t, alice, dm, "!mattermost link "+code)
		reply := WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "Linked to Mattermost user")
		})
		assert.Contains(t, reply.Get("content.body").Str, "@carol")

		SendMatrixText(t, alice, dm, "!mattermost whoami")
		WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "linked to Mattermost user @carol")
		})
		capabilities.Verify(t, "identity.link", true)
	})

	t.Run("linked Matrix messages post as the real Mattermost user", func(t *testing.T) {
		SendMatrixText(t, alice, roomID, "Hello from linked alice")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Hello from linked alice")
		assert.Equal(t, carol.Id, post.UserId, "the post must belong to carol, not a remote user")
		assert.NotNil(t, post.GetProp("from_matrix"))

		// Carol's own profile is left alone.
		user, _, err := h.Admin.GetUser(ctx, carol.Id, "")
		require.NoError(t, err)
		assert.Equal(t, "carol", user.Username)
		assert.False(t, user.IsRemote())
	})

	t.Run("without a token Mattermost messages still come from the ghost", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Carol via ghost"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, "Carol via ghost")
		assert.Equal(t, h.GhostUserID(carol.Id), ev.Get("sender").Str)
	})

	t.Run("login enables double puppeting", func(t *testing.T) {
		SendMatrixText(t, alice, dm, "!mattermost login "+alice.AccessToken)
		WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "Double puppeting enabled")
		})

		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Carol as alice"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, "Carol as alice")
		assert.Equal(t, alice.UserID, ev.Get("sender").Str, "the message must be sent by alice's own account")
		capabilities.Verify(t, "users.puppeting", true)
	})

	t.Run("after login the ghost has left and no shadow stays active", func(t *testing.T) {
		require.Eventually(t, func() bool {
			return !strings.Contains(h.JoinedMembers(roomID), h.GhostUserID(carol.Id))
		}, 30*time.Second, time.Second, "carol's ghost must leave once alice sends as herself")
		assert.Contains(t, h.JoinedMembers(roomID), alice.UserID)
		capabilities.Verify(t, "identity.link_retire_ghost", true)
	})

	t.Run("puppeted messages are not echoed back", func(t *testing.T) {
		time.Sleep(3 * time.Second)
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, "Carol as alice"))
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, "Carol as alice"))
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, "Hello from linked alice"))
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, "Hello from linked alice"))
	})

	t.Run("a stolen token is refused", func(t *testing.T) {
		bob := h.NewMatrixUser("bob")
		SendMatrixText(t, alice, dm, "!mattermost login "+bob.AccessToken)
		WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "belongs to "+bob.UserID)
		})
	})

	t.Run("unlink restores the ghost", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix unlink")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "unlinked")

		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Carol unlinked"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, "Carol unlinked")
		assert.Equal(t, h.GhostUserID(carol.Id), ev.Get("sender").Str)
	})
}

func gjsonContains(r gjson.Result, substr string) bool {
	return r.Exists() && len(r.Str) > 0 && contains(r.Str, substr)
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
