package mas

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMAS implements the parts of MAS the client uses.
type fakeMAS struct {
	tokens   int32
	users    map[string]string // localpart -> id
	links    []map[string]any
	lastBody map[string]any
}

func newFakeMAS(t *testing.T) (*fakeMAS, *httptest.Server) {
	t.Helper()
	f := &fakeMAS{users: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth2/token":
			user, pass, _ := r.BasicAuth()
			if user != "client-id" || pass != "client-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = r.ParseForm()
			if r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("scope") != "urn:mas:admin" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			atomic.AddInt32(&f.tokens, 1)
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":300}`))
			return
		case r.Header.Get("Authorization") != "Bearer tok":
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/v1/users":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.lastBody = req
			name, _ := req["username"].(string)
			if _, exists := f.users[name]; exists {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errors":[{"title":"User already exists or the username is reserved by the homeserver"}]}`))
				return
			}
			f.users[name] = "01" + name
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"type":"user","id":"01` + name + `"}}`))
		case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/admin/v1/users/by-username/"):
			name := r.URL.Path[len("/api/admin/v1/users/by-username/"):]
			if id, ok := f.users[name]; ok {
				_, _ = w.Write([]byte(`{"data":{"type":"user","id":"` + id + `"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/v1/upstream-oauth-links":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.links = append(f.links, req)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"type":"upstream-oauth-link","id":"01link"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestEnsureUserSkipsTheHomeserverCheckAndIsRepeatable(t *testing.T) {
	f, srv := newFakeMAS(t)
	c := New(srv.URL, "client-id", "client-secret")

	id, created, err := c.EnsureUser(context.Background(), "ada", "Ada Admin")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "01ada", id)
	assert.Equal(t, true, f.lastBody["skip_homeserver_check"], "the account already exists in Synapse")
	assert.Equal(t, "Ada Admin", f.lastBody["displayname"])

	again, created, err := c.EnsureUser(context.Background(), "ada", "Ada Admin")
	require.NoError(t, err)
	assert.False(t, created, "a user MAS already has is found, not an error")
	assert.Equal(t, id, again)
	assert.EqualValues(t, 1, f.tokens, "the access token is reused")
}

func TestLinkUpstreamSendsTheProviderSubject(t *testing.T) {
	f, srv := newFakeMAS(t)
	c := New(srv.URL, "client-id", "client-secret")

	require.NoError(t, c.LinkUpstream(context.Background(), "01ada", "01PROVIDER", "oidc-sub-123", "ada@example.com"))
	require.Len(t, f.links, 1)
	assert.Equal(t, "01ada", f.links[0]["user_id"])
	assert.Equal(t, "01PROVIDER", f.links[0]["provider_id"])
	assert.Equal(t, "oidc-sub-123", f.links[0]["subject"])
	assert.Equal(t, "ada@example.com", f.links[0]["human_account_name"])
}

func TestBadCredentialsAreReported(t *testing.T) {
	_, srv := newFakeMAS(t)
	c := New(srv.URL, "client-id", "wrong")
	_, _, err := c.EnsureUser(context.Background(), "ada", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refused the admin client credentials")
}

func TestConfiguredNeedsEverySetting(t *testing.T) {
	assert.True(t, Configured("https://mas", "id", "secret"))
	assert.False(t, Configured("", "id", "secret"))
	assert.False(t, Configured("https://mas", "", "secret"))
	assert.False(t, Configured("https://mas", "id", ""))
}

func TestSharedReusesOneClientPerSettings(t *testing.T) {
	assert.Same(t, Shared("https://a", "i", "s"), Shared("https://a", "i", "s"))
	assert.NotSame(t, Shared("https://a", "i", "s"), Shared("https://b", "i", "s"))
}
