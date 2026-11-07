package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppServiceEndpoint exercises the Application Service API the homeserver
// talks to, from the outside of Mattermost, and the end-to-end setup commands.
func TestAppServiceEndpoint(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	base := h.Mattermost.URL + "/plugins/" + PluginID

	do := func(method, path, token string, body string) *http.Response {
		req, err := http.NewRequest(method, base+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return res
	}

	t.Run("requests without the hs_token are rejected", func(t *testing.T) {
		res := do(http.MethodPut, "/_matrix/app/v1/transactions/unauth", "", `{"events":[]}`)
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	t.Run("requests with a wrong hs_token are forbidden", func(t *testing.T) {
		res := do(http.MethodPut, "/_matrix/app/v1/transactions/wrong", "not-the-token", `{"events":[]}`)
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("the homeserver token is accepted", func(t *testing.T) {
		res := do(http.MethodPut, "/_matrix/app/v1/transactions/ok", h.HSToken, `{"events":[]}`)
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusOK, res.StatusCode)

		res = do(http.MethodPost, "/_matrix/app/v1/ping", h.HSToken, `{}`)
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("user query provisions a ghost for an existing Mattermost user", func(t *testing.T) {
		ghost := h.GhostUserID(h.AdminUser.Id)
		res := do(http.MethodGet, "/_matrix/app/v1/users/"+ghost, h.HSToken, "")
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusOK, res.StatusCode)

		// The ghost now exists on the homeserver and has a profile.
		alice := h.NewMatrixUser("alice")
		profile := alice.MustDo(t, http.MethodGet, []string{"_matrix", "client", "v3", "profile", ghost})
		defer func() { _ = profile.Body.Close() }()
		assert.Equal(t, http.StatusOK, profile.StatusCode)
	})

	t.Run("user query for an unknown user is not found", func(t *testing.T) {
		res := do(http.MethodGet, "/_matrix/app/v1/users/"+h.GhostUserID("doesnotexist"), h.HSToken, "")
		defer func() { _ = res.Body.Close() }()
		assert.Equal(t, http.StatusNotFound, res.StatusCode)
	})

	t.Run("/matrix test reports a healthy connection", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, h.TownSquare().Id, "/matrix test")
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "❌", "/matrix test should pass: %s", resp.Text)
	})

	t.Run("/matrix create makes a room and /matrix list shows it", func(t *testing.T) {
		channel := h.CreateChannel("created-room", "Created Room")
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix create")
		require.NoError(t, err)
		assert.NotContains(t, resp.Text, "❌", "/matrix create failed: %s", resp.Text)

		list, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix list")
		require.NoError(t, err)
		assert.True(t, strings.Contains(list.Text, channel.Name) || strings.Contains(list.Text, channel.DisplayName) || strings.Contains(list.Text, channel.Id),
			"expected the new mapping in /matrix list output: %s", list.Text)

		unmap, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
		require.NoError(t, err)
		assert.NotContains(t, unmap.Text, "❌", "/matrix unmap failed: %s", unmap.Text)
	})

	t.Run("regular users cannot run administrative commands", func(t *testing.T) {
		_, cl := h.CreateMattermostUser("eve")
		channel := h.TownSquare()
		resp, _, err := cl.ExecuteCommand(ctx, channel.Id, fmt.Sprintf("/matrix map_user eve @eve:%s", h.ServerName))
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "system administrators")
	})
}
