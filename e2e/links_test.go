package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestLinksPointAtTheReadersOwnSide checks that message links are rewritten in
// both directions.
func TestLinksPointAtTheReadersOwnSide(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("links", "Links")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Links")

	cfg, _, err := h.Admin.GetConfig(ctx)
	require.NoError(t, err)
	site := strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")

	// A post that exists on both sides.
	original, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "the original message"})
	require.NoError(t, err)
	originalEvent := WaitForMatrixMessage(t, alice, roomID, "the original message").Get("event_id").Str
	require.NotEmpty(t, originalEvent)

	t.Run("a Mattermost permalink arrives as a link to the Matrix event", func(t *testing.T) {
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "as discussed in " + site + "/" + h.Team.Name + "/pl/" + original.Id})
		require.NoError(t, err)
		ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && strings.Contains(ev.Get("content.body").Str, "as discussed in")
		})
		body := ev.Get("content.body").Str
		assert.Contains(t, body, roomID+"/"+originalEvent)
		assert.NotContains(t, body, "/pl/")
		capabilities.Verify(t, "messages.links", strings.Contains(body, roomID+"/"+originalEvent))
	})

	t.Run("a Matrix event link arrives as a Mattermost permalink", func(t *testing.T) {
		SendMatrixText(t, alice, roomID, "see https://matrix.to/#/"+roomID+"/"+originalEvent+" and the room https://matrix.to/#/"+roomID)
		var message string
		WaitFor(t, bridgeTimeout, "the rewritten message", func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 30, "", false, false)
			require.NoError(t, err)
			for _, id := range posts.Order {
				if m := posts.Posts[id].Message; strings.HasPrefix(m, "see ") {
					message = m
					return true
				}
			}
			return false
		})
		assert.Contains(t, message, site+"/"+h.Team.Name+"/pl/"+original.Id)
		assert.Contains(t, message, "~"+channel.Name)
		assert.NotContains(t, message, "matrix.to")
		capabilities.Verify(t, "messages.links.in", strings.Contains(message, "/pl/"+original.Id))
	})
}
