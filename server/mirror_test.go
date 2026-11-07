package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
)

// fakeSynapse implements the few homeserver endpoints mirror mode uses.
type fakeSynapse struct {
	mu        sync.Mutex
	existing  map[string]bool // accounts that already exist (natives)
	created   []string
	passwords map[string]string
	inhibited map[string]bool
	server    *httptest.Server
}

func newFakeSynapse(t *testing.T, natives ...string) *fakeSynapse {
	t.Helper()
	f := &fakeSynapse{existing: map[string]bool{}, passwords: map[string]string{}, inhibited: map[string]bool{}}
	for _, n := range natives {
		f.existing[n] = true
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/_matrix/client/v3/register":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			username, _ := req["username"].(string)
			if f.existing[username] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"errcode":"M_USER_IN_USE","error":"User ID already taken."}`))
				return
			}
			f.existing[username] = true
			f.created = append(f.created, username)
			f.inhibited[username], _ = req["inhibit_login"].(bool)
			if pw, ok := req["password"].(string); ok {
				f.passwords[username] = pw
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "@" + username + ":example.com"})
		case strings.HasPrefix(r.URL.Path, "/_synapse/admin/v2/users/"):
			if r.Header.Get("Authorization") != "Bearer admin-token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.passwords[strings.TrimPrefix(r.URL.Path, "/_synapse/admin/v2/users/")], _ = req["password"].(string)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func newMirrorTestBridge(t *testing.T, hs *fakeSynapse, users ...*model.User) (*Plugin, *plugintest.API) {
	t.Helper()
	p, api := newAppServiceTestPlugin(t, true)
	p.configuration.EnableMirrorMode = true
	p.configuration.MatrixServerURL = hs.server.URL
	p.configuration.MatrixServerDomain = "example.com"
	p.configuration.MatrixAdminToken = "admin-token"
	p.matrixClient = matrix.NewClientWithLoggerAndRateLimit(hs.server.URL, "as-token", "remote", matrix.NewTestLogger(t), matrix.TestRateLimitConfig())
	p.matrixClient.SetServerDomain("example.com")
	p.metrics = NewMetrics()
	for _, u := range users {
		api.On("GetUser", u.Id).Return(u, nil).Maybe()
		api.On("GetUserByUsername", u.Username).Return(u, nil).Maybe()
	}
	p.initBridges()
	return p, api
}

func TestMirrorLocalpart(t *testing.T) {
	assert.Equal(t, "ada", mirrorLocalpart("Ada"))
	assert.Equal(t, "ada.admin-1", mirrorLocalpart("ada.admin-1"))
	assert.Equal(t, "a_b", mirrorLocalpart("a b"))
	assert.Equal(t, "x", mirrorLocalpart("__x"))
	assert.Equal(t, "user", mirrorLocalpart("___"))
	assert.Equal(t, "ada_mm", localpartCandidate("ada", 1))
	assert.Equal(t, "ada_mm3", localpartCandidate("ada", 3))
}

func TestMirrorAccountIsCreatedWithoutCredentials(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)

	got, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada:example.com", got)

	assert.Equal(t, []string{"ada"}, hs.created)
	assert.True(t, hs.inhibited["ada"], "mirrored accounts must be created without credentials")
	assert.Empty(t, hs.passwords)

	// Idempotent and resolvable both ways.
	again, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, got, again)
	assert.Len(t, hs.created, 1)
	acct, ok := newMirrorStore(p.kvstore).AccountForMatrixUser("@ada:example.com")
	require.True(t, ok)
	assert.Equal(t, "mm-ada", acct.MattermostUserID)
	assert.Equal(t, accountModeMirror, effectiveAccountMode(p.kvstore))

	id, found := p.getGhostUser("mm-ada")
	assert.True(t, found)
	assert.Equal(t, "@ada:example.com", id)
}

func TestMirrorAccountCollisionsGetASuffixedName(t *testing.T) {
	// A native account already uses the natural name.
	hs := newFakeSynapse(t, "ada")
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)

	got, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada_mm:example.com", got)
	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.True(t, acct.Collision)
	assert.Equal(t, mirrorForeign, newMirrorStore(p.kvstore).LocalpartOwner("ada"), "the foreign account is remembered, not retried")

	assert.Equal(t, 1, newMirrorStore(p.kvstore).Stats().Collisions)
}

func TestMirrorLocalpartsAreNeverShared(t *testing.T) {
	hs := newFakeSynapse(t)
	// Two users whose names normalise to the same localpart.
	a := &model.User{Id: "mm-a", Username: "a.b"}
	b := &model.User{Id: "mm-b", Username: "a.b"}
	p, _ := newMirrorTestBridge(t, hs, a, b)

	first, err := p.mattermostToMatrixBridge.ensureAccount("mm-a")
	require.NoError(t, err)
	second, err := p.mattermostToMatrixBridge.ensureAccount("mm-b")
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

func TestMirrorRegistrationRecoversAfterACrash(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)

	// The bridge registered the account, then died before recording it.
	store := newMirrorStore(p.kvstore)
	store.setPending("ada", "mm-ada")
	hs.existing["ada"] = true

	got, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada:example.com", got, "an account the bridge itself registered is adopted, not treated as foreign")
}

func TestMirrorLegacyRecordsAreUpgraded(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(legacyMirrorUserPrefix+"mm-old", []byte("@old:example.com")))

	acct, ok := newMirrorStore(p.kvstore).Account("mm-old")
	require.True(t, ok)
	assert.Equal(t, "old", acct.Localpart)
	_, ok = newMirrorStore(p.kvstore).AccountForMatrixUser("@old:example.com")
	assert.True(t, ok)
}

func TestAccountModeIsPinnedByExistingAccounts(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	recordAccountMode(p.kvstore, accountModeGhost)

	cfg := &configuration{EnableMirrorMode: true}
	p.API.(*plugintest.API).On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	p.applyAccountModeLock(cfg)
	assert.False(t, cfg.EnableMirrorMode, "existing ghosts keep the mode ghost")
	assert.True(t, cfg.accountModePinned)

	// Recording is first-come: later modes do not overwrite it.
	recordAccountMode(p.kvstore, accountModeMirror)
	assert.Equal(t, accountModeGhost, effectiveAccountMode(p.kvstore))
}

func TestClaimWithAdminTokenSetsAOneTimePassword(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)

	matrixUserID, password, err := p.ClaimMirrorAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada:example.com", matrixUserID)
	assert.Len(t, password, 20)
	assert.Equal(t, password, hs.passwords["@ada:example.com"])

	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.True(t, acct.Claimed)
	link, ok := p.identity().Link("mm-ada")
	require.True(t, ok, "a claim links the account so the person's own events post as them")
	assert.Equal(t, "@ada:example.com", link.MatrixUserID)
	assert.False(t, link.Puppeted(), "the bridge keeps sending through the appservice, no token is stored")

	st := p.MirrorStatus()
	assert.Equal(t, "mirror", st.Mode)
	assert.Equal(t, 1, st.Claimed)
	assert.True(t, st.ClaimSupported)
}

func TestClaimWithoutAdminTokenHasNoPassword(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)
	p.configuration.MatrixAdminToken = ""

	_, password, err := p.ClaimMirrorAccount("mm-ada")
	require.NoError(t, err)
	assert.Empty(t, password)
	assert.Empty(t, hs.passwords)
}

func TestBridgeAccountsAndClaimedAccountsAreTreatedDifferently(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)
	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)

	assert.True(t, p.isBridgeAccount("@ada:example.com"), "an unclaimed mirror account is driven only by the bridge")
	assert.True(t, p.mattermostToMatrixBridge.ownsAccount("@ada:example.com"))
	assert.False(t, p.isBridgeAccount("@someone:example.com"))

	_, _, err = p.ClaimMirrorAccount("mm-ada")
	require.NoError(t, err)
	assert.False(t, p.isBridgeAccount("@ada:example.com"), "events from a claimed account are the person's")
	assert.False(t, p.mattermostToMatrixBridge.ownsAccount("@ada:example.com"), "the bridge stops managing a claimed profile")
}

func TestFirstOwnMessageClaimsTheAccount(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)
	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)

	// The bridge's own message (with its metadata) does not claim.
	p.claimOnFirstOwnMessage(MatrixEvent{Type: "m.room.message", Sender: "@ada:example.com", Content: map[string]any{"body": "hi", "mattermost_post_id": "p1"}})
	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.False(t, acct.Claimed)

	// Other event types do not claim either.
	p.claimOnFirstOwnMessage(MatrixEvent{Type: "m.reaction", Sender: "@ada:example.com", Content: map[string]any{}})
	acct, _ = newMirrorStore(p.kvstore).Account("mm-ada")
	assert.False(t, acct.Claimed)

	// A plain message from the account is the person at their own client.
	p.claimOnFirstOwnMessage(MatrixEvent{Type: "m.room.message", Sender: "@ada:example.com", Content: map[string]any{"body": "hello from Element"}})
	acct, _ = newMirrorStore(p.kvstore).Account("mm-ada")
	assert.True(t, acct.Claimed)
}

func TestUserQueryProvisionsMirrorAccounts(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	api.On("GetUserByUsername", "nobody").Return(nil, &model.AppError{Message: "not found"}).Maybe()

	assert.Equal(t, "mm-ada", p.mirrorUserForMatrixID("@ada:example.com"))
	assert.Equal(t, "", p.mirrorUserForMatrixID("@_mattermost_x:example.com"))
	assert.Equal(t, "", p.mirrorUserForMatrixID("@nobody:example.com"), "unknown localparts are not provisioned")
}

func TestSentEventsAreRecognisedAsEchoes(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	assert.False(t, p.matrixClient.SentByBridge("$event"))
	p.matrixClient.RecordSentEvent("$event")
	assert.True(t, p.matrixClient.SentByBridge("$event"))
	assert.False(t, p.matrixClient.SentByBridge(""))
}
