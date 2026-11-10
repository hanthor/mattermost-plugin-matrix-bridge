package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestRoomManagementFromMatrix bridges and unbridges a room with the bridge
// bot's commands, as a linked person.
func TestRoomManagementFromMatrix(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	// The Mattermost side of alice: an administrator, so Mattermost lets them
	// bridge any channel.
	channel := h.CreateChannel("from-matrix", "From Matrix")

	// seen remembers the bot's earlier answers, so each call waits for a new one.
	seen := map[string]bool{}
	ask := func(room, command string, want string) gjson.Result {
		SendMatrixText(t, alice, room, command)
		return WaitForMatrixEvent(t, alice, room, func(ev gjson.Result) bool {
			if ev.Get("sender").Str != h.BridgeBotUserID() || ev.Get("content.msgtype").Str != "m.notice" || seen[ev.Get("event_id").Str] {
				return false
			}
			if !strings.Contains(ev.Get("content.body").Str, want) {
				return false
			}
			seen[ev.Get("event_id").Str] = true
			return true
		})
	}

	// A DM with the bot to link, and a room alice moderates.
	dm := alice.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat", "is_direct": true, "invite": []string{h.BridgeBotUserID()}})
	room := alice.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat", "name": "Unbridged room", "invite": []string{h.BridgeBotUserID()}})

	t.Run("room commands need a linked account", func(t *testing.T) {
		ask(room, "!mattermost status", "not linked")
	})

	t.Run("after linking, status says the room is not bridged yet", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix link")
		require.NoError(t, err)
		m := linkCodePattern.FindStringSubmatch(resp.Text)
		require.Len(t, m, 2, resp.Text)
		ask(dm, "!mattermost link "+m[1], "Linked to Mattermost user")
		ask(room, "!mattermost status", "not bridged")
	})

	t.Run("link-room bridges the room to a channel", func(t *testing.T) {
		ask(room, "!mattermost link-room "+h.Team.Name+"/"+channel.Name, "Mapping Saved")
		SendMatrixText(t, alice, room, "Bridged from Matrix")
		WaitForPostWithMessage(t, h.Admin, channel.Id, "Bridged from Matrix")
		ask(room, "!mattermost status", "bridged to")
		ask(room, "!mattermost rooms", channel.Name)
		capabilities.Verify(t, "integration.bot_commands.in", true)
	})

	t.Run("unlink-room stops bridging", func(t *testing.T) {
		ask(room, "!mattermost unlink-room", "Mapping")
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "after unlinking"})
		require.NoError(t, err)
		assert.False(t, TryFor(8*1000*1000*1000, func() bool {
			return timelineHas(t, alice, room, func(ev gjson.Result) bool {
				return strings.Contains(ev.Get("content.body").Str, "after unlinking")
			})
		}), "nothing crosses once the room is unlinked")
	})
}
