package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestIdentityStoreLinkLifecycle(t *testing.T) {
	store := newIdentityStore(NewMemoryKVStore())

	code, err := store.CreateCode("mm-user-1")
	require.NoError(t, err)
	assert.Len(t, code, identityCodeLength)

	_, ok := store.Link("mm-user-1")
	assert.False(t, ok, "no link before the code is redeemed")

	link, err := store.Redeem(strings.ToLower(code), "@alice:example.com")
	require.NoError(t, err, "codes are case-insensitive")
	assert.Equal(t, "mm-user-1", link.MattermostUserID)
	assert.Equal(t, "@alice:example.com", link.MatrixUserID)
	assert.False(t, link.Puppeted())

	// The inbound bridge resolves the Matrix user to the real account.
	mapped, err := store.kv.Get(kvstore.BuildMatrixUserKey("@alice:example.com"))
	require.NoError(t, err)
	assert.Equal(t, "mm-user-1", string(mapped))

	byMatrix, ok := store.LinkForMatrixUser("@alice:example.com")
	require.True(t, ok)
	assert.Equal(t, "mm-user-1", byMatrix.MattermostUserID)

	// A code can only be used once.
	_, err = store.Redeem(code, "@bob:example.com")
	assert.Error(t, err)

	// Tokens enable puppeting.
	withToken, err := store.SetToken("@alice:example.com", "syt_secret")
	require.NoError(t, err)
	assert.True(t, withToken.Puppeted())

	// Re-linking the Matrix account to another Mattermost user moves it.
	code2, err := store.CreateCode("mm-user-2")
	require.NoError(t, err)
	_, err = store.Redeem(code2, "@alice:example.com")
	require.NoError(t, err)
	_, ok = store.Link("mm-user-1")
	assert.False(t, ok, "previous Mattermost user is unlinked")
	byMatrix, ok = store.LinkForMatrixUser("@alice:example.com")
	require.True(t, ok)
	assert.Equal(t, "mm-user-2", byMatrix.MattermostUserID)

	require.NoError(t, store.Unlink("mm-user-2"))
	_, ok = store.LinkForMatrixUser("@alice:example.com")
	assert.False(t, ok)
	_, err = store.kv.Get(kvstore.BuildMatrixUserKey("@alice:example.com"))
	assert.Error(t, err, "mapping removed with the link")
}

func TestIdentityStoreExpiredCode(t *testing.T) {
	store := newIdentityStore(NewMemoryKVStore())
	expired, _ := json.Marshal(identityCode{MattermostUserID: "mm", ExpiresAt: 1})
	require.NoError(t, store.kv.Set(identityCodeKeyPrefix+"OLDCODE1", expired))
	_, err := store.Redeem("OLDCODE1", "@alice:example.com")
	assert.Error(t, err)
}

func TestBotCommands(t *testing.T) {
	// A fake homeserver that answers whoami for one token.
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_matrix/client/v3/account/whoami" {
			switch r.Header.Get("Authorization") {
			case "Bearer alice-token":
				_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "@alice:example.com"})
			case "Bearer bob-token":
				_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "@bob:example.com"})
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN_TOKEN"}`))
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hs.Close()

	p, api := newAppServiceTestPlugin(t, true)
	p.configuration.MatrixServerURL = hs.URL
	p.matrixClient = matrix.NewClientWithLoggerAndRateLimit(hs.URL, "as-token", "remote", matrix.NewTestLogger(t), matrix.TestRateLimitConfig())
	api.On("GetUser", "mm-user-1").Return(&model.User{Id: "mm-user-1", Username: "alice.mm"}, nil).Maybe()

	alice := "@alice:example.com"

	assert.Contains(t, p.executeBotCommand(alice, "!mattermost"), "Mattermost bridge commands")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost help"), "!mattermost link")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost whoami"), "not linked")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost link NOPE1234"), "unknown or expired")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost login alice-token"), "not linked")

	code, err := p.identity().CreateCode("mm-user-1")
	require.NoError(t, err)
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost link "+code), "Linked to Mattermost user @alice.mm")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost whoami"), "@alice.mm")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost whoami"), "ghost user")

	// Tokens must belong to the sender and be accepted by the homeserver.
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost login bob-token"), "belongs to @bob:example.com")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost login garbage"), "rejected")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost login alice-token"), "Double puppeting enabled")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost whoami"), "sent as this Matrix account")

	matrixUserID, puppeted, ok := p.GetIdentityLink("mm-user-1")
	require.True(t, ok)
	assert.Equal(t, alice, matrixUserID)
	assert.True(t, puppeted)

	assert.Contains(t, p.executeBotCommand(alice, "!mattermost logout"), "Token removed")
	assert.Contains(t, p.executeBotCommand(alice, "!mattermost unlink"), "Unlinked")
	_, _, ok = p.GetIdentityLink("mm-user-1")
	assert.False(t, ok)

	assert.Contains(t, p.executeBotCommand(alice, "!mattermost dance"), "Unknown command")
}

func TestIsBotCommand(t *testing.T) {
	assert.True(t, isBotCommand(MatrixEvent{Type: "m.room.message", Content: map[string]any{"body": "!mattermost help"}}))
	assert.True(t, isBotCommand(MatrixEvent{Type: "m.room.message", Content: map[string]any{"body": "  !Mattermost whoami"}}))
	assert.False(t, isBotCommand(MatrixEvent{Type: "m.room.message", Content: map[string]any{"body": "hello !mattermost"}}))
	assert.False(t, isBotCommand(MatrixEvent{Type: "m.reaction", Content: map[string]any{"body": "!mattermost"}}))
}

func TestPuppetClientAuthorization(t *testing.T) {
	var gotAuth, gotQuery string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$e"})
	}))
	defer hs.Close()

	client := matrix.NewClientWithLoggerAndRateLimit(hs.URL, "as-token", "remote", matrix.NewTestLogger(t), matrix.TestRateLimitConfig())

	_, err := client.SendReactionAsGhost("!room:example.com", "$target", "👍", "@_mattermost_x:example.com")
	require.NoError(t, err)
	assert.Equal(t, "Bearer as-token", gotAuth)
	assert.Contains(t, gotQuery, "user_id=")

	puppet := client.AsPuppet("alice-token")
	assert.True(t, puppet.IsPuppet())
	assert.False(t, client.IsPuppet(), "the original client is unchanged")
	_, err = puppet.SendReactionAsGhost("!room:example.com", "$target", "👍", "@alice:example.com")
	require.NoError(t, err)
	assert.Equal(t, "Bearer alice-token", gotAuth)
	assert.NotContains(t, gotQuery, "user_id=", "puppets act as themselves")
}
