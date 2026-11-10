package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestBackfillStructure checks that /matrix backfill structure restores what
// the bridge keeps in a room (its name and its widget) when the room has
// drifted, for example because someone changed it on the Matrix side.
func TestBackfillStructure(t *testing.T) {
	h := NewHarness(t)

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("structure", "Structure")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Something else")
	h.GrantBridgePower(alice, roomID, 50)

	state := func(eventType, key string) gjson.Result {
		res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, key})
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return gjson.Result{}
		}
		return gjson.ParseBytes(client.ParseJSON(t, res))
	}

	// The room was mapped before the bridge had power in it, so it carries a
	// name nobody chose for it and, possibly, no widget.
	require.Equal(t, "Something else", state("m.room.name", "").Get("name").Str)

	// A custom emoji exists in Mattermost.
	_, _, err := h.Admin.CreateEmoji(t.Context(), &model.Emoji{CreatorId: h.AdminUser.Id, Name: "backfillparrot"}, onePixelPNG, "parrot.png")
	require.NoError(t, err)

	resp, _, err := h.Admin.ExecuteCommand(t.Context(), channel.Id, "/matrix backfill structure")
	require.NoError(t, err)
	t.Log(resp.Text)

	WaitFor(t, bridgeTimeout, "the room to take the channel's name", func() bool {
		return state("m.room.name", "").Get("name").Str == "Structure"
	})
	WaitFor(t, bridgeTimeout, "the widget", func() bool {
		return state("im.vector.modular.widgets", "com.mattermost.channel").Get("url").Str != ""
	})
	assert.Equal(t, "Structure", state("m.room.name", "").Get("name").Str)

	// The custom emoji are offered as the room's emoji pack (MSC2545).
	WaitFor(t, bridgeTimeout, "the emoji pack", func() bool {
		return state("im.ponies.room_emotes", "com.mattermost.emoji").Get("images.backfillparrot.url").Str != ""
	})
	mxc := state("im.ponies.room_emotes", "com.mattermost.emoji").Get("images.backfillparrot.url").Str
	require.True(t, strings.HasPrefix(mxc, "mxc://"), mxc)
	capabilities.Verify(t, "integration.backfill", true)
}
