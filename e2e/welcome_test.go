package e2e

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestWelcomeAccounts covers handing people their Matrix account: they are told
// their Matrix ID, and what they had favourited in Mattermost is a favourite in
// the Matrix account.
func TestWelcomeAccounts(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{MirrorMode: true})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("welcome", "Welcome")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Welcome")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	carolMXID := "@carol:" + h.ServerName
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(carolMXID, roomID))

	_, err = carolClient.UpdatePreferences(ctx, carol.Id, model.Preferences{{
		UserId: carol.Id, Category: model.PreferenceCategoryFavoriteChannel, Name: channel.Id, Value: "true",
	}})
	require.NoError(t, err)

	t.Run("the channel is told once that it is bridged", func(t *testing.T) {
		post := WaitForPost(t, h.Admin, channel.Id, "the bridging notice", func(p *model.Post) bool {
			return strings.Contains(p.Message, "now bridged to a Matrix room")
		})
		assert.Equal(t, "custom_matrix_notice", post.Type)
	})

	t.Run("the preview says what would happen and changes nothing", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix welcome")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "would be welcomed", resp.Text)
	})

	tags := func() gjson.Result {
		res := h.BotClient().Do(t, "GET", []string{"_matrix", "client", "v3", "user", carolMXID, "rooms", roomID, "tags"},
			client.WithQueries(url.Values{"user_id": {carolMXID}}))
		defer func() { _ = res.Body.Close() }()
		return gjson.ParseBytes(client.ParseJSON(t, res)).Get("tags")
	}

	t.Run("confirming tells the person and brings their favourites over", func(t *testing.T) {
		require.False(t, tags().Get(`m\.favourite`).Exists(), "no favourite before the welcome")
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix welcome confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "Welcomed", resp.Text)

		assert.True(t, tags().Get(`m\.favourite`).Exists(), "the favourite channel is a favourite room")

		bot, _, err := carolClient.GetUserByUsername(ctx, "matrixbridge", "")
		require.NoError(t, err)
		dm, _, err := carolClient.CreateDirectChannel(ctx, carol.Id, bot.Id)
		require.NoError(t, err)
		post := WaitForPost(t, carolClient, dm.Id, "the welcome message", func(p *model.Post) bool {
			return strings.Contains(p.Message, carolMXID)
		})
		assert.Contains(t, post.Message, "/matrix claim")
		capabilities.Verify(t, "mirror.welcome", true)
	})

	t.Run("repeating it does not message the person again", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix welcome confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "0 told", resp.Text)
	})
}
