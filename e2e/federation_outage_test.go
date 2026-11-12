package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestFederationSurvivesAnUnreachableRemoteServer takes the other homeserver
// away while a room that lives on the bridge's own server has a member from it.
// The bridge only ever talks to its own homeserver, so Mattermost must keep
// working at normal speed, people on the bridge's server must keep receiving,
// and the remote person's messages must flow again once their server is back.
func TestFederationSurvivesAnUnreachableRemoteServer(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Federation: true})
	ctx := context.Background()

	local := h.NewMatrixUser("lena")
	remote := h.NewRemoteMatrixUser("remy")
	channel := h.CreateChannel("outage-remote", "Outage remote")
	roomID := h.CreateMappedRoomWith(local, channel.Id, map[string]any{
		"name": "Outage remote", "preset": "public_chat",
	})
	remote.MustJoinRoom(t, roomID, []spec.ServerName{spec.ServerName(HSName)})

	SendMatrixText(t, remote, roomID, "remy before the outage")
	WaitForPostWithMessage(t, h.Admin, channel.Id, "remy before the outage")

	h.Deployment.PauseServer(t, RemoteHSName)
	paused := true
	defer func() {
		if paused {
			h.Deployment.UnpauseServer(t, RemoteHSName)
		}
	}()

	t.Run("Mattermost keeps working while the remote server is down", func(t *testing.T) {
		start := time.Now()
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "posted while hs2 is down"})
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 10*time.Second, "posting does not wait for the remote server")
		// Someone on the bridge's own server still gets it.
		WaitForMatrixMessage(t, local, roomID, "posted while hs2 is down")
	})

	t.Run("the remote person's messages flow again once their server returns", func(t *testing.T) {
		h.Deployment.UnpauseServer(t, RemoteHSName)
		paused = false
		WaitFor(t, 2*bridgeTimeout, "a message from the remote person to arrive after the outage", func() bool {
			SendMatrixText(t, remote, roomID, "remy after the outage")
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 60, "", false, false)
			if err != nil {
				return false
			}
			for _, id := range posts.Order {
				if posts.Posts[id].Message == "remy after the outage" {
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "federation.remote_outage", true)
	})
}
