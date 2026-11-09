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
		// The panel follows the same rule as /matrix create, map and unmap: whoever
		// may edit the channel's properties. Mattermost gives that to members of a
		// public channel by default.
		assert.Equal(t, true, ch["can_manage"])
		assert.Equal(t, false, ch["can_create"], "an already bridged channel cannot be bridged again")

		_, adminView := pluginAPI(t, h, h.Admin, http.MethodGet, "/channels/"+channel.Id)
		assert.Equal(t, true, adminView["can_manage"])
		capabilities.Verify(t, "ui.panel", ch["room_id"] == roomID)
		capabilities.Verify(t, "integration.channel_header", ch["room_id"] == roomID && ch["element_url"] != "")
	})

	t.Run("a private channel is closed to people who are not in it", func(t *testing.T) {
		private, _, err := h.Admin.CreateChannel(ctx, &model.Channel{TeamId: h.Team.Id, Name: "secret-panel", DisplayName: "Secret", Type: model.ChannelTypePrivate})
		require.NoError(t, err)
		status, _ := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+private.Id)
		assert.Equal(t, http.StatusForbidden, status)
	})

	t.Run("an unbridged channel can be bridged from the panel", func(t *testing.T) {
		scratch := h.CreateChannel("panel-scratch", "Panel scratch")
		h.WaitForChannelRoom(scratch)
		resp, _, err := h.Admin.ExecuteCommand(ctx, scratch.Id, "/matrix unmap")
		require.NoError(t, err)
		require.NotContains(t, resp.Text, "❌", resp.Text)

		_, before := pluginAPI(t, h, h.Admin, http.MethodGet, "/channels/"+scratch.Id)
		assert.Equal(t, false, before["mapped"])
		assert.Equal(t, true, before["can_create"])

		status, out := pluginAPI(t, h, h.Admin, http.MethodPost, "/channels/"+scratch.Id+"/bridge")
		require.Equal(t, http.StatusOK, status, out)

		_, after := pluginAPI(t, h, h.Admin, http.MethodGet, "/channels/"+scratch.Id)
		assert.Equal(t, true, after["mapped"])
		assert.NotEmpty(t, after["room_id"])
		assert.Equal(t, false, after["can_create"])

		status, _ = pluginAPI(t, h, h.Admin, http.MethodPost, "/channels/"+scratch.Id+"/bridge")
		assert.Equal(t, http.StatusConflict, status, "bridging twice is refused")
	})

	t.Run("linking gives a code the bridge bot understands", func(t *testing.T) {
		status, link := pluginAPI(t, h, carolClient, http.MethodPost, "/me/link")
		require.Equal(t, http.StatusOK, status, link)
		code, _ := link["code"].(string)
		assert.Len(t, code, 8)
		assert.Equal(t, "!mattermost link "+code, link["command"])
	})
}
