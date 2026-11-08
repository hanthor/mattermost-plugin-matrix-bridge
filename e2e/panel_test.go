package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// pluginAPI calls the plugin's HTTP API as the client's user.
func pluginAPI(t *testing.T, h *Harness, cl *model.Client4, method, path string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, h.Mattermost.URL+"/plugins/"+PluginID+"/api/v1"+path, strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+cl.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return res.StatusCode, body
}

// TestBridgePanelAPI checks the routes behind the Mattermost side panel
// against a real server: who is asking decides what they see.
func TestBridgePanelAPI(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("panel", "Panel")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Panel")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	t.Run("requests without a session are refused", func(t *testing.T) {
		res, err := http.Get(h.Mattermost.URL + "/plugins/" + PluginID + "/api/v1/me")
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("a person sees their own account", func(t *testing.T) {
		status, me := pluginAPI(t, h, carolClient, http.MethodGet, "/me")
		require.Equal(t, http.StatusOK, status, me)
		assert.Equal(t, "ghost", me["mode"])
		assert.Equal(t, true, me["enabled"])
		assert.Equal(t, h.BridgeBotUserID(), me["bot_user"])
		assert.Equal(t, false, me["is_admin"])
		assert.Equal(t, h.GhostUserID(carol.Id), me["matrix_user_id"])
	})

	t.Run("a mapped channel shows its room, its people and who may manage it", func(t *testing.T) {
		status, ch := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+channel.Id)
		require.Equal(t, http.StatusOK, status, ch)
		assert.Equal(t, true, ch["mapped"])
		assert.Equal(t, roomID, ch["room_id"])
		assert.Equal(t, "Panel", ch["room_name"])
		assert.GreaterOrEqual(t, ch["participants"], float64(2), "the bridge bot and alice are in the room")
		assert.Equal(t, false, ch["can_manage"], "a plain member cannot bridge or unbridge")
		assert.Equal(t, false, ch["can_create"])

		_, adminView := pluginAPI(t, h, h.Admin, http.MethodGet, "/channels/"+channel.Id)
		assert.Equal(t, true, adminView["can_manage"])
		capabilities.Verify(t, "ui.panel", ch["room_id"] == roomID)
	})

	t.Run("a private channel is closed to people who are not in it", func(t *testing.T) {
		private, _, err := h.Admin.CreateChannel(ctx, &model.Channel{TeamId: h.Team.Id, Name: "secret-panel", DisplayName: "Secret", Type: model.ChannelTypePrivate})
		require.NoError(t, err)
		status, _ := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+private.Id)
		assert.Equal(t, http.StatusForbidden, status)
	})

	t.Run("linking gives a code the bridge bot understands", func(t *testing.T) {
		status, link := pluginAPI(t, h, carolClient, http.MethodPost, "/me/link")
		require.Equal(t, http.StatusOK, status, link)
		code, _ := link["code"].(string)
		assert.Len(t, code, 8)
		assert.Equal(t, "!mattermost link "+code, link["command"])
	})
}
