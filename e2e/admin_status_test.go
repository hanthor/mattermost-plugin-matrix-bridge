package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestAdminStatusPage checks the document behind the System Console page: it
// is for system administrators only, and says what is bridged and how the
// bridge is doing.
func TestAdminStatusPage(t *testing.T) {
	h := NewHarness(t)

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("adminstatus", "Admin Status")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Admin Status")
	_, carolClient := h.CreateMattermostUser("carol")

	t.Run("an ordinary person is refused", func(t *testing.T) {
		status, _ := pluginAPI(t, h, carolClient, http.MethodGet, "/admin/status")
		assert.Equal(t, http.StatusForbidden, status)
	})

	t.Run("an administrator sees health and what is bridged", func(t *testing.T) {
		status, body := pluginAPI(t, h, h.Admin, http.MethodGet, "/admin/status")
		require.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, true, body["enabled"])
		assert.NotEmpty(t, body["health"])
		assert.Contains(t, []any{"ok", "warn"}, body["worst"], "%v", body["health"])
		mappings, _ := body["mappings"].([]any)
		found := false
		for _, m := range mappings {
			mm, _ := m.(map[string]any)
			if mm["channel_id"] == channel.Id {
				found = true
				assert.Equal(t, "Admin Status", mm["channel_name"])
				assert.Equal(t, roomID, mm["room"])
			}
		}
		assert.True(t, found, "the mapped channel is listed: %v", mappings)
		capabilities.Verify(t, "integration.admin_page", found)
	})
}
