package e2e

import (
	"context"
	"testing"

	"github.com/matrix-org/complement/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestAutoLinkByVerifiedEmail covers a Matrix person and a Mattermost person
// who are the same person, known to both systems by one verified address: the
// first message links them, so nothing is posted by a separate remote account.
func TestAutoLinkByVerifiedEmail(t *testing.T) {
	// Mirror mode is only used to get a Synapse admin token into the bridge
	// configuration; the Matrix → Mattermost path under test is the same.
	h := NewHarnessWithOptions(t, HarnessOptions{MirrorMode: true})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("autolink", "Autolink")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Autolink")

	// Carol on Mattermost has a verified address; alice on Matrix has the same
	// one, validated by the homeserver.
	carol, _ := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.VerifyUserEmailWithoutToken(ctx, carol.Id)
	require.NoError(t, err)
	_, _, err = h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	res := h.MatrixAdmin.Do(t, "PUT", []string{"_synapse", "admin", "v2", "users", alice.UserID},
		client.WithJSONBody(t, map[string]any{"threepids": []map[string]any{{"medium": "email", "address": carol.Email}}}))
	require.Less(t, res.StatusCode, 300, "set the Matrix account's verified email")

	t.Run("nothing is linked while the setting is off", func(t *testing.T) {
		SendMatrixText(t, alice, roomID, "Before linking by email")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Before linking by email")
		assert.NotEqual(t, carol.Id, post.UserId)
		user, _, err := h.Admin.GetUser(ctx, post.UserId, "")
		require.NoError(t, err)
		assert.True(t, user.IsRemote(), "without the setting alice is a remote user")
	})

	t.Run("a person seen for the first time after switching it on posts as the real account", func(t *testing.T) {
		// A person who already has a remote account stays as they are; the
		// link is made for people the bridge has not seen yet.
		bob := h.NewMatrixUser("bob")
		dave, _ := h.CreateMattermostUser("dave")
		_, _, err := h.Admin.VerifyUserEmailWithoutToken(ctx, dave.Id)
		require.NoError(t, err)
		_, _, err = h.Admin.AddChannelMember(ctx, channel.Id, dave.Id)
		require.NoError(t, err)
		res := h.MatrixAdmin.Do(t, "PUT", []string{"_synapse", "admin", "v2", "users", bob.UserID},
			client.WithJSONBody(t, map[string]any{"threepids": []map[string]any{{"medium": "email", "address": dave.Email}}}))
		require.Less(t, res.StatusCode, 300)

		// The link is made when the person first appears, joining or writing.
		h.SetBridgeSetting("auto_link_by_email", true)
		bob.MustJoinRoom(t, roomID, nil)
		SendMatrixText(t, bob, roomID, "Hello from a linked bob")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Hello from a linked bob")
		assert.Equal(t, dave.Id, post.UserId, "bob must post as dave, not as a separate remote user")
		user, _, err := h.Admin.GetUser(ctx, post.UserId, "")
		require.NoError(t, err)
		assert.False(t, user.IsRemote())
		capabilities.Verify(t, "identity.auto_link_email", true)
	})

}
