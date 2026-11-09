package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/mas"
)

// TestMASWithMattermostAsProvider runs the recipe in docs/MAS_SETUP.md for
// organisations whose people sign in to Mattermost: a real Mattermost is the
// identity provider (as an OAuth 2.0 provider, which MAS reads through the
// userinfo endpoint), and a person signs in to Matrix with their Mattermost
// account and lands on the mirror account the bridge made for them.
func TestMASWithMattermostAsProvider(t *testing.T) {
	stack := startMASStackWith(t, masStackOptions{Mattermost: true})
	client := stack.Client()
	ctx := context.Background()
	masAdmin := mas.New("http://"+stack.hosts["mas:8080"], masAdminID, masAdminPass)

	ada, _, err := stack.MMAdmin.CreateUser(ctx, &model.User{Username: "ada", Email: "ada@example.com", Password: dexUserPass})
	require.NoError(t, err)

	t.Run("the bridge makes the account and tells MAS who owns it", func(t *testing.T) {
		status, body := registerAsAppservice(t, client, stack.SynapseBase, "ada")
		require.Equal(t, http.StatusOK, status, body)

		masID, created, err := masAdmin.EnsureUser(ctx, "ada", "Ada")
		require.NoError(t, err)
		assert.True(t, created)
		// The subject is the person's Mattermost user ID: the id the userinfo endpoint returns.
		require.NoError(t, masAdmin.LinkUpstream(ctx, masID, masProviderID, ada.Id, "ada@example.com"))
	})

	t.Run("signing in with a Mattermost account lands on the mirror account", func(t *testing.T) {
		loginToken := walkMatrixSSO(t, stack, "ada@example.com")

		body, _ := json.Marshal(map[string]any{"type": "m.login.token", "token": loginToken})
		res, err := client.Post(stack.MASBase+"/_matrix/client/v3/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		login := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, login)
		assert.Equal(t, "@ada:"+masServerName, gjson.Get(login, "user_id").Str)
		capabilities.Verify(t, "mirror.sso_mattermost", gjson.Get(login, "user_id").Str == "@ada:"+masServerName)
	})
}
