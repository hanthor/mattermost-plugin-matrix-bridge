package e2e

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestBridgeInformation checks that a mapped room says where it leads, and
// stops saying so once the channel is unmapped.
func TestBridgeInformation(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("bridgeinfo", "Bridge Info")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Bridge Info")

	info := func() gjson.Result {
		res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
		defer func() { _ = res.Body.Close() }()
		var found gjson.Result
		gjson.ParseBytes(client.ParseJSON(t, res)).ForEach(func(_, ev gjson.Result) bool {
			if ev.Get("type").Str == "uk.half-shot.bridge" {
				found = ev.Get("content")
				return false
			}
			return true
		})
		return found
	}

	widget := func() gjson.Result {
		res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state"})
		defer func() { _ = res.Body.Close() }()
		var found gjson.Result
		gjson.ParseBytes(client.ParseJSON(t, res)).ForEach(func(_, ev gjson.Result) bool {
			if ev.Get("type").Str == "im.vector.modular.widgets" && ev.Get("state_key").Str == "com.mattermost.channel" {
				found = ev.Get("content")
				return false
			}
			return true
		})
		return found
	}

	// A room mapped without power for the bot keeps no event (documented);
	// once an admin gives the bot power, mapping again sets it.
	h.GrantBridgePower(alice, roomID, 50)
	_, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
	require.NoError(t, err)
	h.MapChannelToRoom(channel.Id, roomID)

	WaitFor(t, bridgeTimeout, "the bridge information", func() bool { return info().Get("protocol.id").Str == "mattermost" })
	got := info()
	assert.Equal(t, channel.Id, got.Get("channel.id").Str)
	assert.Equal(t, "Bridge Info", got.Get("channel.displayname").Str)
	capabilities.Verify(t, "integration.bridge_info", got.Get("protocol.id").Str == "mattermost")

	// The room also carries a widget that shows the channel; its address is
	// the plugin's, signed for this channel, and only a signed address works.
	WaitFor(t, bridgeTimeout, "the widget", func() bool { return widget().Get("url").Str != "" })
	u, err := url.Parse(widget().Get("url").Str)
	require.NoError(t, err)
	assert.Equal(t, "customwidget", widget().Get("type").Str)
	page := func(rawQuery string) (int, string) {
		resp, err := http.Get(h.Mattermost.URL + u.Path + "?" + rawQuery)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, body := page(u.RawQuery)
	assert.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Bridge Info")
	status, _ = page("channel=" + channel.Id + "&sig=wrong")
	assert.Equal(t, http.StatusNotFound, status, "an unsigned address must not show the channel")
	capabilities.Verify(t, "integration.widget", status == http.StatusNotFound && widget().Get("url").Str != "")

	resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
	require.NoError(t, err)
	t.Log(resp.Text)
	WaitFor(t, bridgeTimeout, "the information to be removed", func() bool { return info().Get("protocol.id").Str == "" })
	WaitFor(t, bridgeTimeout, "the widget to be removed", func() bool { return widget().Get("url").Str == "" })
}
