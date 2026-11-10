package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestMembershipRepair changes a channel's members while the homeserver is
// down, so the room never hears about it, then repairs the room.
func TestMembershipRepair(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("repair", "Repair")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Repair")
	carol, _ := h.CreateMattermostUser("carol")
	dave, _ := h.CreateMattermostUser("dave")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(h.GhostUserID(carol.Id), roomID))
	aliceID, aliceToken := alice.UserID, alice.AccessToken

	h.Deployment.StopServer(t, HSName)
	_, err = h.Admin.RemoveUserFromChannel(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	_, _, err = h.Admin.AddChannelMember(ctx, channel.Id, dave.Id)
	require.NoError(t, err)
	time.Sleep(5 * time.Second)
	h.Deployment.StartServer(t, HSName)

	back := h.Deployment.UnauthenticatedClient(t, HSName)
	back.UserID, back.AccessToken = aliceID, aliceToken
	membership := func(mxid string) string {
		res := back.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", mxid})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return ""
		}
		return gjson.GetBytes(client.ParseJSON(t, res), "membership").Str
	}

	t.Run("the room still shows the old members", func(t *testing.T) {
		WaitFor(t, 2*time.Minute, "the homeserver", func() bool { return membership(h.GhostUserID(carol.Id)) != "" })
		assert.Equal(t, "join", membership(h.GhostUserID(carol.Id)), "Carol is gone from the channel but the room does not know")
		assert.NotEqual(t, "join", membership(h.GhostUserID(dave.Id)))
	})

	t.Run("repair makes the room match the channel", func(t *testing.T) {
		var out string
		WaitFor(t, bridgeTimeout, "the bridge to reach the homeserver again", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix repair")
			require.NoError(t, err)
			out = resp.Text
			return resp.Text != "" && !strings.Contains(resp.Text, "❌")
		})
		assert.Contains(t, out, "Membership repaired")
		WaitFor(t, bridgeTimeout, "the room to match", func() bool {
			return membership(h.GhostUserID(carol.Id)) == "leave" && membership(h.GhostUserID(dave.Id)) == "join"
		})
		capabilities.Verify(t, "membership.reconcile", membership(h.GhostUserID(dave.Id)) == "join")

		// A second run has nothing to do.
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix repair")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "same people")
	})
}
