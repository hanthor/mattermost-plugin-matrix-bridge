package main

import (
	"encoding/json"
	"fmt"
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
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// fakeSynapse implements the few homeserver endpoints mirror mode uses.
type fakeSynapse struct {
	states []string // state events PUT: "room type/key body"
	mu           sync.Mutex
	existing     map[string]bool // accounts that already exist (natives)
	created      []string
	passwords    map[string]string
	inhibited    map[string]bool
	joins        []string
	leaves       []string
	invites      []string
	uploads      []string
	createdRooms []map[string]any
	down         bool // the homeserver answers 503 to message sends
	// doctor endpoints
	whoami      string
	pingFails   bool
	joinedRooms []string
	botPower    int
	members     []string // joined_members of any room
	reactions   []fakeSent
	deactivated []string
	// sent records the messages sent to rooms, in order.
	sent   []fakeSent
	server *httptest.Server
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
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/") && f.down:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"errcode":"M_UNKNOWN","error":"down"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/"):
			var content map[string]any
			_ = json.Unmarshal(body, &content)
			f.sent = append(f.sent, fakeSent{
				Room: roomFromPath(r.URL.Path), UserID: r.URL.Query().Get("user_id"), TS: r.URL.Query().Get("ts"),
				Body: fmt.Sprint(content["body"]), Content: content,
			})
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": fmt.Sprintf("$ev%d", len(f.sent))})
		case strings.HasPrefix(r.URL.Path, "/_matrix/media/") && strings.HasSuffix(r.URL.Path, "/upload"):
			f.uploads = append(f.uploads, r.URL.Query().Get("filename"))
			_, _ = w.Write([]byte(`{"content_uri":"mxc://example.com/uploaded"}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.reaction/"):
			var content map[string]any
			_ = json.Unmarshal(body, &content)
			relates, _ := content["m.relates_to"].(map[string]any)
			f.reactions = append(f.reactions, fakeSent{
				Room: roomFromPath(r.URL.Path), UserID: r.URL.Query().Get("user_id"), TS: r.URL.Query().Get("ts"),
				Body: fmt.Sprint(relates["key"]), Content: content,
			})
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": fmt.Sprintf("$re%d", len(f.reactions))})
		case strings.HasPrefix(r.URL.Path, "/_matrix/client/v3/join/"):
			f.joins = append(f.joins, r.URL.Query().Get("user_id")+" in "+strings.TrimPrefix(r.URL.Path, "/_matrix/client/v3/join/"))
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/_matrix/client/versions":
			_, _ = w.Write([]byte(`{"versions":["v1.1","v1.12"]}`))
		case r.URL.Path == "/_matrix/client/v3/account/whoami":
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": f.whoami})
		case strings.HasSuffix(r.URL.Path, "/ping") && strings.Contains(r.URL.Path, "/appservice/"):
			if f.pingFails {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"errcode":"M_URL_NOT_REACHABLE","error":"cannot connect"}`))
				return
			}
			_, _ = w.Write([]byte(`{"duration_ms":12}`))
		case strings.HasSuffix(r.URL.Path, "/joined_members"):
			joined := map[string]any{}
			for _, id := range f.members {
				joined[id] = map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"joined": joined})
		case r.URL.Path == "/_matrix/client/v3/joined_rooms":
			_ = json.NewEncoder(w).Encode(map[string]any{"joined_rooms": f.joinedRooms})
		case strings.Contains(r.URL.Path, "/state/m.room.power_levels"):
			_ = json.NewEncoder(w).Encode(map[string]any{"users": map[string]int{"@_mattermost_bridge:example.com": f.botPower}, "users_default": 0})
		case r.Method == http.MethodPost && r.URL.Path == "/_matrix/client/v3/createRoom":
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.createdRooms = append(f.createdRooms, req)
			_, _ = w.Write([]byte(`{"room_id":"!dm:example.com"}`))
		case strings.HasPrefix(r.URL.Path, "/_synapse/admin/v1/deactivate/"):
			f.deactivated = append(f.deactivated, strings.TrimPrefix(r.URL.Path, "/_synapse/admin/v1/deactivate/"))
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/") && !strings.Contains(r.URL.Path, "power_levels"):
			f.states = append(f.states, roomFromPath(r.URL.Path)+" "+r.URL.Path[strings.Index(r.URL.Path, "/state/")+7:]+" "+string(body))
			_, _ = w.Write([]byte(`{"event_id":"$state"}`))
		case strings.HasSuffix(r.URL.Path, "/invite"):
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			f.invites = append(f.invites, fmt.Sprint(req["user_id"])+" to "+roomFromPath(r.URL.Path))
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/leave"):
			f.leaves = append(f.leaves, r.URL.Query().Get("user_id")+" from "+roomFromPath(r.URL.Path))
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

type fakeSent struct {
	Room, UserID, TS, Body string
	Content                map[string]any
}

func (f *fakeSynapse) messages() []fakeSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSent(nil), f.sent...)
}

func roomFromPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "rooms" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (f *fakeSynapse) joinsAndLeaves() (joins, leaves []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.joins...), append([]string(nil), f.leaves...)
}

func kvstoreGhostUserKey(id string) string { return kvstore.BuildGhostUserKey(id) }

func kvstoreGhostRoomKey(id, room string) string { return kvstore.BuildGhostRoomKey(id, room) }

// withBase applies the pinned fields of a locked configuration on top of the
// plugin's current one.
func withBase(base, locked *configuration) *configuration {
	next := base.Clone()
	next.EnableMirrorMode = locked.EnableMirrorMode
	next.accountModePinned = locked.accountModePinned
	return next
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

func seedGhostWithRooms(t *testing.T, p *Plugin, userID string, rooms ...string) string {
	t.Helper()
	ghost := "@_mattermost_" + userID + ":example.com"
	require.NoError(t, p.kvstore.Set(kvstoreGhostUserKey(userID), []byte(ghost)))
	for _, r := range rooms {
		require.NoError(t, p.kvstore.Set(kvstoreGhostRoomKey(userID, r), []byte("joined")))
	}
	return ghost
}

func TestMigrateGhostsToMirror(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	carol := &model.User{Id: "mm-carol", Username: "carol"}
	p, _ := newMirrorTestBridge(t, hs, ada, bob, carol)

	// Start as a ghost deployment that later turns mirror mode on.
	p.configuration.EnableMirrorMode = false
	recordAccountMode(p.kvstore, accountModeGhost)
	seedGhostWithRooms(t, p, "mm-ada", "!r1:example.com", "!r2:example.com")
	seedGhostWithRooms(t, p, "mm-bob", "!r1:example.com")
	seedGhostWithRooms(t, p, "mm-carol", "!r1:example.com")
	require.NoError(t, p.identity().save(&IdentityLink{MattermostUserID: "mm-carol", MatrixUserID: "@carol:other.org", AccessToken: "syt_x"}))

	// The setting asks for mirror mode but the existing ghosts pin ghost mode.
	cfg := &configuration{EnableMirrorMode: true}
	p.API.(*plugintest.API).On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	p.applyAccountModeLock(cfg)
	p.setConfiguration(withBase(p.configuration, cfg))
	require.True(t, p.getConfiguration().accountModePinned)
	assert.Equal(t, accountModeGhost, p.MirrorStatus().Mode)
	assert.True(t, p.MirrorStatus().Pinned)

	// Dry run changes nothing.
	dry, err := p.migrateGhostsToMirror(false)
	require.NoError(t, err)
	assert.True(t, dry.DryRun)
	assert.Equal(t, 3, dry.Users)
	assert.Equal(t, 4, dry.Rooms)
	assert.Equal(t, 1, dry.Skipped, "carol already posts as her own linked account")
	assert.Empty(t, hs.created)
	_, stillGhost := p.kvstore.Get(kvstoreGhostUserKey("mm-ada"))
	assert.NoError(t, stillGhost)

	// Confirm moves everyone else and switches the mode.
	done, err := p.migrateGhostsToMirror(true)
	require.NoError(t, err)
	assert.Equal(t, 2, done.Moved)
	assert.Equal(t, 0, done.Failed)
	assert.True(t, done.Flipped)
	assert.ElementsMatch(t, []string{"ada", "bob"}, hs.created)

	id, ok := p.getGhostUser("mm-ada")
	require.True(t, ok)
	assert.Equal(t, "@ada:example.com", id, "everything now resolves to the mirror account")
	assert.Equal(t, accountModeMirror, effectiveAccountMode(p.kvstore))
	assert.True(t, p.getConfiguration().EnableMirrorMode)
	assert.False(t, p.getConfiguration().accountModePinned)

	// Rooms: the mirror account joined, the ghost left.
	joined, left := hs.joinsAndLeaves()
	assert.Contains(t, joined, "@ada:example.com in !r1:example.com")
	assert.Contains(t, joined, "@ada:example.com in !r2:example.com")
	assert.Contains(t, left, "@_mattermost_mm-ada:example.com from !r1:example.com")

	// Running it again has nothing to do.
	_, err = p.migrateGhostsToMirror(true)
	require.Error(t, err)
}

func TestMigrationRefusesWithoutMirrorModeSetting(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	p.configuration.EnableMirrorMode = false
	_, err := p.migrateGhostsToMirror(false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Mirror Mode")
}

func TestCutoverFreezesAccounts(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	eve := &model.User{Id: "mm-eve", Username: "eve"}
	p, _ := newMirrorTestBridge(t, hs, ada, eve)

	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)

	p.configuration.CutoverMode = true
	existing, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err, "existing accounts keep working")
	assert.Equal(t, "@ada:example.com", existing)

	_, err = p.mattermostToMatrixBridge.ensureAccount("mm-eve")
	require.Error(t, err, "no new accounts in cut-over mode")
	assert.Contains(t, err.Error(), "cut-over")
	assert.Equal(t, []string{"ada"}, hs.created)
	assert.True(t, p.MirrorStatus().Cutover)
}

func (f *fakeSynapse) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}
