package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestIntegrationPostsKeepTheirContent sends a post whose content is in
// attachments with an empty message, as a CI or alerting integration does.
func TestIntegrationPostsKeepTheirContent(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("ci", "CI")
	roomID := h.CreateMappedRoom(alice, channel.Id, "CI")

	post := &model.Post{ChannelId: channel.Id}
	post.AddProp("attachments", []any{map[string]any{
		"title": "Build 42 passed", "title_link": "https://ci.example.com/42",
		"text": "All 318 tests passed", "footer": "Jenkins",
		"fields": []any{map[string]any{"title": "Branch", "value": "main"}},
	}})
	_, _, err := h.Admin.CreatePost(ctx, post)
	require.NoError(t, err)

	ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
		return ev.Get("type").Str == "m.room.message" && strings.Contains(ev.Get("content.body").Str, "All 318 tests passed")
	})
	body := ev.Get("content.body").Str
	require.Contains(t, body, "Build 42 passed")
	require.Contains(t, body, "Branch")
	capabilities.Verify(t, "messages.integrations", true)
}
