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

// TestLongMessages sends messages longer than the receiving side allows.
func TestLongMessages(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("long", "Long")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Long")

	paragraph := strings.TrimSpace(strings.Repeat("lorem ipsum dolor sit amet ", 40))
	var paragraphs []string
	for i := 0; i < 25; i++ {
		paragraphs = append(paragraphs, paragraph)
	}
	long := strings.Join(paragraphs, "\n\n") // about 27,000 characters, over Mattermost's 16,383
	require.Greater(t, len(long), 20000)

	t.Run("a Matrix message over Mattermost's limit becomes several posts", func(t *testing.T) {
		SendMatrixText(t, alice, roomID, long)

		var messages []string
		WaitFor(t, bridgeTimeout, "the parts of the long message", func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
			require.NoError(t, err)
			messages = nil
			for _, id := range posts.Order {
				if m := posts.Posts[id].Message; strings.Contains(m, "lorem ipsum") {
					messages = append([]string{m}, messages...)
				}
			}
			return len(messages) >= 2
		})
		total := 0
		for _, m := range messages {
			assert.LessOrEqual(t, len([]rune(m)), model.PostMessageMaxRunesV2)
			total += strings.Count(m, "lorem")
		}
		assert.Equal(t, 25*40, total, "every paragraph survives the split")
		capabilities.Verify(t, "messages.long.in", len(messages) >= 2 && total == 25*40)
	})

	t.Run("a long Mattermost post arrives whole", func(t *testing.T) {
		text := long[:16000]
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: text})
		require.NoError(t, err)
		ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && len(ev.Get("content.body").Str) >= 15900 && ev.Get("sender").Str != alice.UserID
		})
		assert.GreaterOrEqual(t, len(ev.Get("content.body").Str), 15900)
		capabilities.Verify(t, "messages.long", len(ev.Get("content.body").Str) >= 15900)
	})
}
