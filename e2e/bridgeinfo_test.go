package e2e

import (
	"context"
	"net/http"
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

	resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
	require.NoError(t, err)
	t.Log(resp.Text)
	WaitFor(t, bridgeTimeout, "the information to be removed", func() bool { return info().Get("protocol.id").Str == "" })
}
