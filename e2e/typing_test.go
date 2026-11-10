package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestTypingReachesMatrix checks that a person typing in Mattermost shows as
// typing in the room.
func TestTypingReachesMatrix(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	h.SetBridgeSetting("realtime_poll_seconds", 2)
	h.SetBridgeSetting("enable_realtime", true)

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("typing", "Typing")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Typing")
	ghost := h.GhostUserID(h.AdminUser.Id)

	works := TryFor(40*time.Second, func() bool {
		_, _ = h.Admin.PublishUserTyping(ctx, h.AdminUser.Id, model.TypingRequest{ChannelId: channel.Id})
		return ephemeralHas(t, alice, roomID, func(ev gjson.Result) bool {
			if ev.Get("type").Str != "m.typing" {
				return false
			}
			for _, u := range ev.Get("content.user_ids").Array() {
				if u.Str == ghost {
					return true
				}
			}
			return false
		})
	})
	capabilities.Verify(t, "ephemeral.typing", works)
}
