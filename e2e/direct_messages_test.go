package e2e

import (
	"net/url"

	"context"
	"github.com/tidwall/gjson"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestDirectMessagesOptIn: a private conversation is only bridged when an
// administrator allowed it and everyone in it agreed, and leaving stops it.
func TestDirectMessagesOptIn(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{DirectMessages: true})
	ctx := context.Background()

	carol, carolClient := h.CreateMattermostUser("carol")
	dave, daveClient := h.CreateMattermostUser("dave")
	carolGhost, daveGhost := h.GhostUserID(carol.Id), h.GhostUserID(dave.Id)
	dm, _, err := carolClient.CreateDirectChannel(ctx, carol.Id, dave.Id)
	require.NoError(t, err)

	// Slash commands run in a team channel; direct channels have no team.
	lobby := h.CreateChannel("lobby", "Lobby")
	for _, u := range []string{carol.Id, dave.Id} {
		_, _, err := h.Admin.AddChannelMember(ctx, lobby.Id, u)
		require.NoError(t, err)
	}
	run := func(cl *model.Client4, cmd string) string {
		resp, _, err := cl.ExecuteCommand(ctx, lobby.Id, cmd)
		require.NoError(t, err)
		return resp.Text
	}
	roomWith := func(body string) string {
		for _, r := range joinedRoomsOf(t, h, carolGhost) {
			if _, ok := messagesSeenBy(t, h, carolGhost, r)[body]; ok {
				return r
			}
		}
		return ""
	}
	post := func(msg string) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: dm.Id, Message: msg})
		require.NoError(t, err)
	}

	t.Run("nothing is bridged before anyone agrees", func(t *testing.T) {
		assert.Contains(t, run(carolClient, "/matrix dm status"), "not bridged")
		post("Nobody agreed yet")
		time.Sleep(5 * time.Second)
		assert.Empty(t, roomWith("Nobody agreed yet"))
	})

	t.Run("one person agreeing is not enough", func(t *testing.T) {
		assert.Contains(t, run(carolClient, "/matrix dm on"), "can be bridged")
		post("Only carol agreed")
		time.Sleep(5 * time.Second)
		assert.Empty(t, roomWith("Only carol agreed"))
	})

	var room string
	t.Run("once everyone agrees new messages go to a private room", func(t *testing.T) {
		run(daveClient, "/matrix dm on")
		post("Both of us agreed")
		WaitFor(t, bridgeTimeout, "the message in a Matrix room", func() bool { room = roomWith("Both of us agreed"); return room != "" })

		seen := messagesSeenBy(t, h, carolGhost, room)
		assert.Equal(t, carolGhost, seen["Both of us agreed"])
		members := joinedMembersOf(t, h, room, carolGhost)
		assert.Contains(t, members, carolGhost)
		assert.Contains(t, members, daveGhost)
		assert.Len(t, members, 3, "the two people and the bridge bot, nobody else")
		assert.Empty(t, roomWith("Nobody agreed yet"), "earlier messages are not copied")
		capabilities.Verify(t, "rooms.dm", room != "")
	})

	t.Run("leaving stops the conversation from syncing", func(t *testing.T) {
		assert.Contains(t, run(daveClient, "/matrix dm off"), "no longer bridged")
		post("After dave left")
		time.Sleep(5 * time.Second)
		assert.Empty(t, roomWith("After dave left"))
		assert.NotContains(t, messagesSeenBy(t, h, carolGhost, room), "After dave left")
	})
}

// joinedMembersOf lists the accounts that have joined a room, as seen by one of them.
func joinedMembersOf(t *testing.T, h *Harness, roomID, actAs string) []string {
	t.Helper()
	_, out := asRequest(t, h, "GET", "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/joined_members", actAs, nil)
	var members []string
	out.Get("joined").ForEach(func(k, _ gjson.Result) bool {
		members = append(members, k.Str)
		return true
	})
	return members
}
