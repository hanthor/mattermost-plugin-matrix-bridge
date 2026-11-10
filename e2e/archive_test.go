package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestArchivingAChannelTellsTheRoom archives a mapped channel and expects a
// notice and the bridge to leave the room.
func TestArchivingAChannelTellsTheRoom(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("archive-me", "Archive Me")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Archive Me")

	_, err := h.Admin.DeleteChannel(ctx, channel.Id)
	require.NoError(t, err)
	WaitFor(t, bridgeTimeout, "the archive notice", func() bool {
		return timelineHas(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && strings.Contains(ev.Get("content.body").Str, "archived")
		})
	})
	WaitFor(t, bridgeTimeout, "the bridge to leave", func() bool {
		state, ok := roomState(t, alice, roomID, "m.room.member", h.BridgeBotUserID())
		return ok && state.Get("membership").Str == "leave"
	})
}
