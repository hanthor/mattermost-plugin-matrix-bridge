package e2e

import (
	"context"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestModerationCrossesTheBridge checks removals in both directions.
func TestModerationCrossesTheBridge(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	carol, _ := h.CreateMattermostUser("carol")
	channel := h.CreateChannel("moderated", "Moderated")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	roomID := h.CreateMappedRoom(alice, channel.Id, "Moderated")
	carolGhost := h.GhostUserID(carol.Id)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(carolGhost, roomID))

	member := func(mxid string) gjson.Result {
		res := alice.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", mxid})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			return gjson.Result{}
		}
		return gjson.ParseBytes(client.ParseJSON(t, res))
	}
	inChannel := func(userID string) bool {
		_, _, err := h.Admin.GetChannelMember(ctx, channel.Id, userID, "")
		return err == nil
	}

	t.Run("removing someone from a channel kicks their account from the room", func(t *testing.T) {
		_, err := h.Admin.RemoveUserFromChannel(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		WaitFor(t, bridgeTimeout, "the account to leave the room", func() bool {
			return member(carolGhost).Get("membership").Str == "leave"
		})
		// When Mattermost says who removed them the room sees a kick with the
		// reason; otherwise it sees the account leave.
		t.Logf("membership event reason: %q", member(carolGhost).Get("reason").Str)
	})

	t.Run("a Matrix moderator kicking a Mattermost person's account removes them from the channel", func(t *testing.T) {
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		WaitFor(t, bridgeTimeout, "the account to rejoin the room", func() bool {
			return member(carolGhost).Get("membership").Str == "join"
		})
		require.True(t, inChannel(carol.Id))

		alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"}, client.WithJSONBody(t, map[string]any{"user_id": carolGhost, "reason": "off topic"}))
		WaitFor(t, bridgeTimeout, "Carol to be removed from the channel", func() bool { return !inChannel(carol.Id) })
	})

	t.Run("a Matrix moderator kicking a Matrix person removes them from the channel", func(t *testing.T) {
		bob := h.NewMatrixUser("bob")
		bob.MustJoinRoom(t, roomID, nil)
		bobMM := WaitForBridgedUserInChannel(t, h, channel.Id, "bob")
		require.True(t, inChannel(bobMM.Id))

		alice.MustDo(t, "POST", []string{"_matrix", "client", "v3", "rooms", roomID, "kick"}, client.WithJSONBody(t, map[string]any{"user_id": bob.UserID, "reason": "spam"}))
		WaitFor(t, bridgeTimeout, "Bob to be removed from the channel", func() bool { return !inChannel(bobMM.Id) })
		capabilities.Verify(t, "moderation.kick", true)
	})
}
