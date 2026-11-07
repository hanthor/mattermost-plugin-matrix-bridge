package e2e

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

var oneTimePasswordPattern = regexp.MustCompile("One-time password[^`]*`([^`]+)`")

// TestMirrorMode runs the bridge in mirror mode against a real Synapse and
// checks what a migration depends on: people get real accounts with no
// credentials, a name collision with a native account is handled, claiming
// hands the account over, and the person's own Matrix client then posts as
// them rather than as a lookalike.
func TestMirrorMode(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{MirrorMode: true})
	ctx := context.Background()

	// A native account already uses the name "dave".
	unauth := h.Deployment.UnauthenticatedClient(t, HSName)
	unauth.MustDo(t, "POST", []string{"_matrix", "client", "v3", "register"}, client.WithJSONBody(t, map[string]any{
		"username": "dave", "password": "native-password-1", "auth": map[string]any{"type": "m.login.dummy"},
	}))

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("mirror", "Mirror")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Mirror")

	carol, carolClient := h.CreateMattermostUser("carol")
	dave, _ := h.CreateMattermostUser("dave")
	for _, u := range []string{carol.Id, dave.Id} {
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, u)
		require.NoError(t, err)
	}
	carolMXID := "@carol:" + h.ServerName

	t.Run("users appear as real accounts, not ghosts", func(t *testing.T) {
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(carolMXID, roomID))

		text := "Posted from Mattermost as a mirrored account"
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: text})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, text)
		assert.Equal(t, carolMXID, ev.Get("sender").Str, "the sender is @carol, not an @_mattermost_ ghost")
		capabilities.Verify(t, "mirror.accounts", ev.Get("sender").Str == carolMXID)
	})

	t.Run("a name taken by a native account gets a suffixed one", func(t *testing.T) {
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo("@dave_mm:"+h.ServerName, roomID))
		capabilities.Verify(t, "mirror.collisions", true)
	})

	t.Run("accounts are created without credentials", func(t *testing.T) {
		res := unauth.Do(t, "POST", []string{"_matrix", "client", "v3", "login"}, client.WithJSONBody(t, map[string]any{
			"type":       "m.login.password",
			"identifier": map[string]any{"type": "m.id.user", "user": "carol"},
			"password":   "anything-at-all",
		}))
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, res.StatusCode, "no password works before the account is claimed")
	})

	t.Run("claiming hands the account over and its own client posts as the person", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix claim")
		require.NoError(t, err)
		m := oneTimePasswordPattern.FindStringSubmatch(resp.Text)
		require.NotNil(t, m, "claim should show a one-time password: %s", resp.Text)

		res := unauth.MustDo(t, "POST", []string{"_matrix", "client", "v3", "login"}, client.WithJSONBody(t, map[string]any{
			"type":       "m.login.password",
			"identifier": map[string]any{"type": "m.id.user", "user": "carol"},
			"password":   m[1],
		}))
		login := gjson.ParseBytes(client.ParseJSON(t, res))
		carolMatrix := h.Deployment.UnauthenticatedClient(t, HSName)
		carolMatrix.UserID = login.Get("user_id").Str
		carolMatrix.AccessToken = login.Get("access_token").Str
		carolMatrix.DeviceID = login.Get("device_id").Str
		require.Equal(t, carolMXID, carolMatrix.UserID)

		text := "Typed in Matrix by Carol herself"
		SendMatrixText(t, carolMatrix, roomID, text)
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, text)
		assert.Equal(t, carol.Id, post.UserId, "the message is posted as the real Mattermost user, not a remote lookalike")
		capabilities.Verify(t, "mirror.claim", post.UserId == carol.Id)

		// The bridge keeps posting for her through the appservice and does not echo.
		back := "Posted from Mattermost after claiming"
		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: back})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, alice, roomID, back)
		assert.Equal(t, carolMXID, ev.Get("sender").Str)
		assert.Equal(t, 1, CountMatrixMessages(t, alice, roomID, back), "the message must not loop back")
		assert.Equal(t, 1, CountPostsWithMessage(t, h.Admin, channel.Id, back), "no duplicate post in Mattermost")
	})

	t.Run("status reports the mode", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "mirror mode")
		assert.Contains(t, resp.Text, "claimed")
		assert.Contains(t, resp.Text, "suffixed name")
	})
}
