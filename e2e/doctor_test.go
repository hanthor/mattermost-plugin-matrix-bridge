package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestDoctor runs /matrix doctor against a working stack, then breaks a
// setting and checks that the report names it.
func TestDoctor(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("doctor", "Doctor")
	h.CreateMappedRoom(alice, channel.Id, "Doctor")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	run := func(cmd string) string {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, cmd)
		require.NoError(t, err)
		return resp.Text
	}

	t.Run("a working setup passes, including the homeserver calling the plugin back", func(t *testing.T) {
		report := run("/matrix doctor")
		assert.Contains(t, report, "✅ **Settings**")
		assert.Contains(t, report, "✅ **Homeserver reachable**")
		assert.Contains(t, report, "✅ **Appservice token and server name**")
		assert.Contains(t, report, "✅ **Homeserver can reach the plugin**")
		assert.Contains(t, report, "✅ **Bridge bot in the room**")
		assert.NotContains(t, report, "❌", report)
		capabilities.Verify(t, "ops.doctor", !containsFail(report))
	})

	t.Run("a wrong server name is named, with what to do", func(t *testing.T) {
		h.SetBridgeSetting("matrix_server_domain", "not-the-server.example")
		defer h.SetBridgeSetting("matrix_server_domain", HSName)

		var report string
		WaitFor(t, bridgeTimeout, "the report to name the server name", func() bool {
			report = run("/matrix doctor")
			return strings.Contains(report, "❌ **Matrix server name**")
		})
		assert.Contains(t, report, "server_name")
	})

	t.Run("only system administrators can run it", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix doctor")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Only system administrators")
	})
}

func containsFail(report string) bool {
	for i := 0; i+len("❌") <= len(report); i++ {
		if report[i:i+len("❌")] == "❌" {
			return true
		}
	}
	return false
}
