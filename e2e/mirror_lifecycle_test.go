package e2e

import (
	"context"
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

// TestMirrorAccountLifecycle covers what happens to a person's Matrix account
// when their Mattermost account changes: deactivation, and linking a native
// Matrix account after a mirror account was already in use.
func TestMirrorAccountLifecycle(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{MirrorMode: true})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("lifecycle", "Lifecycle")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Lifecycle")

	eve, _ := h.CreateMattermostUser("eve")
	frank, frankClient := h.CreateMattermostUser("frank")
	gina, ginaClient := h.CreateMattermostUser("gina")
	for _, u := range []string{eve.Id, frank.Id, gina.Id} {
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, u)
		require.NoError(t, err)
	}
	for _, name := range []string{"eve", "frank", "gina"} {
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo("@"+name+":"+h.ServerName, roomID))
	}

	memberState := func(mxid string) string {
		res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", mxid})
		defer func() { _ = res.Body.Close() }()
		return gjson.GetBytes(client.ParseJSON(t, res), "membership").Str
	}

	t.Run("a deactivated person's mirror account leaves and is deactivated", func(t *testing.T) {
		eveMXID := "@eve:" + h.ServerName
		_, err := h.Admin.UpdateUserActive(ctx, eve.Id, false)
		require.NoError(t, err)

		WaitFor(t, bridgeTimeout, "the mirror account to leave", func() bool { return memberState(eveMXID) == "leave" })
		WaitFor(t, bridgeTimeout, "the account to be deactivated on the homeserver", func() bool {
			res := h.MatrixAdmin.Do(t, "GET", []string{"_synapse", "admin", "v2", "users", eveMXID})
			defer func() { _ = res.Body.Close() }()
			return gjson.GetBytes(client.ParseJSON(t, res), "deactivated").Bool()
		})
		capabilities.Verify(t, "users.deactivation", memberState(eveMXID) == "leave")
	})

	t.Run("a claimed account is left alone", func(t *testing.T) {
		_, _, err := frankClient.ExecuteCommand(ctx, channel.Id, "/matrix claim")
		require.NoError(t, err)
		_, err = h.Admin.UpdateUserActive(ctx, frank.Id, false)
		require.NoError(t, err)

		time.Sleep(5 * time.Second)
		assert.Equal(t, "join", memberState("@frank:"+h.ServerName))
		res := h.MatrixAdmin.Do(t, "GET", []string{"_synapse", "admin", "v2", "users", "@frank:" + h.ServerName})
		defer func() { _ = res.Body.Close() }()
		assert.False(t, gjson.GetBytes(client.ParseJSON(t, res), "deactivated").Bool())
	})

	t.Run("linking a native account retires the mirror account", func(t *testing.T) {
		ginaMXID := "@gina:" + h.ServerName
		require.Equal(t, "join", memberState(ginaMXID))

		// Alice is also gina's native Matrix account: she links it and logs in.
		dm := alice.MustCreateRoom(t, map[string]interface{}{"preset": "private_chat", "is_direct": true, "invite": []string{h.BridgeBotUserID()}})
		resp, _, err := ginaClient.ExecuteCommand(ctx, channel.Id, "/matrix link")
		require.NoError(t, err)
		m := linkCodePattern.FindStringSubmatch(resp.Text)
		require.Len(t, m, 2, resp.Text)
		SendMatrixText(t, alice, dm, "!mattermost link "+m[1])
		WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "Linked to Mattermost user")
		})
		SendMatrixText(t, alice, dm, "!mattermost login "+alice.AccessToken)
		WaitForMatrixEvent(t, alice, dm, func(ev gjson.Result) bool {
			return ev.Get("sender").Str == h.BridgeBotUserID() && gjsonContains(ev.Get("content.body"), "Double puppeting enabled")
		})

		WaitFor(t, bridgeTimeout, "the mirror account to leave", func() bool { return memberState(ginaMXID) == "leave" })

		// Gina's posts now come from the native account.
		_, _, err = ginaClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Gina after linking"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, "Gina after linking")
		assert.Equal(t, alice.UserID, ev.Get("sender").Str)
		assert.False(t, strings.HasPrefix(ev.Get("sender").Str, ginaMXID))
		capabilities.Verify(t, "mirror.link_retire", ev.Get("sender").Str == alice.UserID)
	})
}
