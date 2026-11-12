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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMASServer records what the bridge asks Matrix Authentication Service to do.
type fakeMASServer struct {
	mu      sync.Mutex
	users   map[string]map[string]any
	links   []map[string]any
	healthy bool
	server  *httptest.Server
}

func newFakeMASServer(t *testing.T) *fakeMASServer {
	t.Helper()
	f := &fakeMASServer{users: map[string]map[string]any{}, healthy: true}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/oauth2/token" {
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":300}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/v1/users":
			name, _ := req["username"].(string)
			f.users[name] = req
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"01` + name + `"}}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/admin/v1/users/by-username/"):
			_, _ = w.Write([]byte(`{"data":{"id":"01` + strings.TrimPrefix(r.URL.Path, "/api/admin/v1/users/by-username/") + `"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/v1/personal-sessions":
			_, _ = w.Write([]byte(`{"data":{"attributes":{"access_token":"mat_personal"}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/v1/upstream-oauth-links":
			f.links = append(f.links, req)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"01link"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeMASServer) setHealthy(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healthy = v
}

func (f *fakeMASServer) snapshot() (users map[string]map[string]any, links []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	users = map[string]map[string]any{}
	for k, v := range f.users {
		users[k] = v
	}
	return users, append([]map[string]any(nil), f.links...)
}

func withMAS(p *Plugin, f *fakeMASServer) {
	p.configuration.MASURL = f.server.URL
	p.configuration.MASAdminClientID = "client"
	p.configuration.MASAdminClientSecret = "secret"
	p.configuration.MASUpstreamProviderID = "01PROVIDER"
}

func oidcUser(id, username, sub string) *model.User {
	return &model.User{Id: id, Username: username, Email: username + "@example.com", AuthService: openIDService, AuthData: &sub}
}

func TestMirrorAccountIsCreatedInMASAndLinkedToTheIdentityProvider(t *testing.T) {
	hs := newFakeSynapse(t)
	masServer := newFakeMASServer(t)
	ada := oidcUser("mm-ada", "ada", "idp-subject-ada")
	p, _ := newMirrorTestBridge(t, hs, ada)
	withMAS(p, masServer)

	matrixID, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada:example.com", matrixID)
	assert.Equal(t, []string{"ada"}, hs.created, "Synapse still gets the account through the appservice")

	users, links := masServer.snapshot()
	require.Contains(t, users, "ada")
	assert.Equal(t, true, users["ada"]["skip_homeserver_check"], "the account already exists in Synapse")
	require.Len(t, links, 1)
	assert.Equal(t, "01ada", links[0]["user_id"])
	assert.Equal(t, "01PROVIDER", links[0]["provider_id"])
	assert.Equal(t, "idp-subject-ada", links[0]["subject"], "the person's OpenID Connect subject is what ties the account to their login")

	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.Equal(t, "01ada", acct.MASUserID)
	assert.True(t, acct.MASLinked)
}

func TestPeopleWhoDoNotSignInWithOpenIDAreCreatedButNotLinked(t *testing.T) {
	hs := newFakeSynapse(t)
	masServer := newFakeMASServer(t)
	bob := &model.User{Id: "mm-bob", Username: "bob", AuthService: ""}
	p, _ := newMirrorTestBridge(t, hs, bob)
	withMAS(p, masServer)

	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-bob")
	require.NoError(t, err)
	users, links := masServer.snapshot()
	assert.Contains(t, users, "bob")
	assert.Empty(t, links, "there is no identity-provider subject to link")
	acct, _ := newMirrorStore(p.kvstore).Account("mm-bob")
	assert.NotEmpty(t, acct.MASUserID)
	assert.False(t, acct.MASLinked)
}

func TestMASBeingDownDoesNotStopAccountCreationAndIsRetried(t *testing.T) {
	hs := newFakeSynapse(t)
	masServer := newFakeMASServer(t)
	masServer.setHealthy(false)
	ada := oidcUser("mm-ada", "ada", "idp-subject-ada")
	p, _ := newMirrorTestBridge(t, hs, ada)
	withMAS(p, masServer)

	matrixID, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err, "the bridge keeps working when MAS is unreachable")
	assert.Equal(t, "@ada:example.com", matrixID)
	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.Empty(t, acct.MASUserID)

	masServer.setHealthy(true)
	_, err = p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	acct, _ = newMirrorStore(p.kvstore).Account("mm-ada")
	assert.Equal(t, "01ada", acct.MASUserID, "the next use of the account provisions it")
	assert.True(t, acct.MASLinked)
}

func TestWithoutMASNothingIsSentToIt(t *testing.T) {
	hs := newFakeSynapse(t)
	masServer := newFakeMASServer(t)
	ada := oidcUser("mm-ada", "ada", "idp-subject-ada")
	p, _ := newMirrorTestBridge(t, hs, ada)
	// MAS settings left empty

	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	users, links := masServer.snapshot()
	assert.Empty(t, users)
	assert.Empty(t, links)
}

func TestClaimWithMASSetsNoPasswordAndStillClaims(t *testing.T) {
	hs := newFakeSynapse(t)
	masServer := newFakeMASServer(t)
	ada := oidcUser("mm-ada", "ada", "idp-subject-ada")
	p, _ := newMirrorTestBridge(t, hs, ada)
	withMAS(p, masServer)

	assert.False(t, p.MirrorStatus().ClaimSupported, "signing in goes through the identity provider, not a password")
	matrixID, password, err := p.ClaimMirrorAccount("mm-ada")
	require.NoError(t, err)
	assert.Equal(t, "@ada:example.com", matrixID)
	assert.Empty(t, password)
	assert.Empty(t, hs.passwords, "Synapse no longer owns passwords when MAS delegates sign-in")
	acct, _ := newMirrorStore(p.kvstore).Account("mm-ada")
	assert.True(t, acct.Claimed)
	assert.True(t, acct.MASLinked)
}
