package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestInvitingTheBotCreatesAChannel makes a channel for a new Matrix room.
func TestInvitingTheBotCreatesAChannel(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	h.SetBridgeSetting("invite_creates_channels_team", h.Team.Name)

	alice := h.NewMatrixUser("alice")
	room := alice.MustCreateRoom(t, map[string]interface{}{"preset": "public_chat", "name": "Born in Matrix", "invite": []string{h.BridgeBotUserID()}})

	var channelID string
	WaitFor(t, bridgeTimeout, "the channel for the room", func() bool {
		channels, _, err := h.Admin.GetPublicChannelsForTeam(ctx, h.Team.Id, 0, 100, "")
		if err != nil {
			return false
		}
		for _, c := range channels {
			if strings.Contains(c.Purpose, room) {
				channelID = c.Id
				return true
			}
		}
		return false
	})
	require.NotEmpty(t, channelID)
	SendMatrixText(t, alice, room, "First message in the new channel")
	WaitForPostWithMessage(t, h.Admin, channelID, "First message in the new channel")
	capabilities.Verify(t, "rooms.create.in", true)
}
