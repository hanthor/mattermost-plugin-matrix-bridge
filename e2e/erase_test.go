package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestEraseAPerson covers erasing a person from the Matrix side: their bridged
// messages are redacted, other people's are not, and their mirror account is
// deactivated and erased on the homeserver.
func TestEraseAPerson(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{MirrorMode: true})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("erase", "Erase")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Erase")
	dave, daveClient := h.CreateMattermostUser("dave")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, dave.Id)
	require.NoError(t, err)
	daveMXID := "@dave:" + h.ServerName
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(daveMXID, roomID))

	for _, text := range []string{"Dave says one", "Dave says two"} {
		_, _, err = daveClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: text})
		require.NoError(t, err)
	}
	_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Admin keeps this"})
	require.NoError(t, err)
	first := WaitForMatrixMessage(t, alice, roomID, "Dave says one").Get("event_id").Str
	second := WaitForMatrixMessage(t, alice, roomID, "Dave says two").Get("event_id").Str
	kept := WaitForMatrixMessage(t, alice, roomID, "Admin keeps this").Get("event_id").Str

	body := func(eventID string) string {
		res := alice.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "event", eventID})
		defer func() { _ = res.Body.Close() }()
		return gjson.GetBytes(client.ParseJSON(t, res), "content.body").Str
	}

	t.Run("the preview changes nothing", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix erase dave")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "2 messages", resp.Text)
		assert.Equal(t, "Dave says one", body(first))
	})

	t.Run("confirming redacts their messages, not other people's, and erases the account", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix erase dave confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "Erased", resp.Text)

		WaitFor(t, bridgeTimeout, "the first message to be redacted", func() bool { return body(first) == "" })
		WaitFor(t, bridgeTimeout, "the second message to be redacted", func() bool { return body(second) == "" })
		assert.Equal(t, "Admin keeps this", body(kept))

		WaitFor(t, bridgeTimeout, "the account to be erased on the homeserver", func() bool {
			res := h.MatrixAdmin.Do(t, "GET", []string{"_synapse", "admin", "v2", "users", daveMXID})
			defer func() { _ = res.Body.Close() }()
			parsed := gjson.ParseBytes(client.ParseJSON(t, res))
			return parsed.Get("deactivated").Bool() && parsed.Get("erased").Bool()
		})
		capabilities.Verify(t, "privacy.erase", true)
	})

	t.Run("ordinary people cannot run it", func(t *testing.T) {
		carol, carolClient := h.CreateMattermostUser("carol")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix erase alice")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Only system administrators")
	})
}
