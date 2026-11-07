package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

const (
	callTestRoom    = "!room:example.com"
	callTestChannel = "ch1"
)

type recordedRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

// recordingHomeserver accepts every request and remembers the writes.
type recordingHomeserver struct {
	mu       sync.Mutex
	requests []recordedRequest
	server   *httptest.Server
	counter  int
}

func newRecordingHomeserver(t *testing.T) *recordingHomeserver {
	t.Helper()
	h := &recordingHomeserver{}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		path := r.URL.Path
		if r.Method == http.MethodPut {
			h.requests = append(h.requests, recordedRequest{Method: r.Method, Path: path, Body: body})
			h.counter++
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$event" + string(rune('0'+h.counter))})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *recordingHomeserver) puts(contains string) []recordedRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []recordedRequest
	for _, r := range h.requests {
		if strings.Contains(r.Path, contains) {
			out = append(out, r)
		}
	}
	return out
}

func newCallsTestPlugin(t *testing.T, hs *recordingHomeserver, mode string) (*Plugin, *plugintest.API) {
	t.Helper()
	p, api := newAppServiceTestPlugin(t, true)

	// Replace the default GetConfig expectation with one that has a site URL.
	kept := api.ExpectedCalls[:0]
	for _, c := range api.ExpectedCalls {
		if c.Method != "GetConfig" {
			kept = append(kept, c)
		}
	}
	api.ExpectedCalls = kept
	site := "https://chat.example.com"
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &site}}).Maybe()

	p.configuration.MatrixServerURL = hs.server.URL
	p.configuration.MatrixServerDomain = "example.com"
	p.configuration.CallsMode = mode
	p.matrixClient = matrix.NewClientWithLoggerAndRateLimit(hs.server.URL, "as-token", "remote", matrix.NewTestLogger(t), matrix.TestRateLimitConfig())
	p.matrixClient.SetServerDomain("example.com")
	p.metrics = NewMetrics()
	p.initBridges()

	// The channel is bridged to the room.
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey(callTestChannel), []byte(callTestRoom)))
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey(callTestRoom), []byte(callTestChannel)))

	api.On("GetChannel", callTestChannel).Return(&model.Channel{Id: callTestChannel, TeamId: "team1", Name: "town", DisplayName: "Town"}, nil).Maybe()
	api.On("GetTeam", "team1").Return(&model.Team{Id: "team1", Name: "acme"}, nil).Maybe()
	return p, api
}

// seedGhost gives a Mattermost user a ghost that is already in the room.
func seedGhost(t *testing.T, p *Plugin, api *plugintest.API, u *model.User) string {
	t.Helper()
	ghost := "@_mattermost_" + u.Id + ":example.com"
	require.NoError(t, p.kvstore.Set(kvstore.BuildGhostUserKey(u.Id), []byte(ghost)))
	require.NoError(t, p.kvstore.Set(kvstore.BuildGhostRoomKey(u.Id, callTestRoom), []byte("joined")))
	api.On("GetUser", u.Id).Return(u, nil).Maybe()
	return ghost
}

func callPost(id string, props map[string]any) *model.Post {
	return &model.Post{Id: id, UserId: "mm-ada", ChannelId: callTestChannel, Type: callPostType, Props: props}
}

func TestMattermostCallIsAnnouncedAndEnded(t *testing.T) {
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, "")
	ada := &model.User{Id: "mm-ada", Username: "ada", FirstName: "Ada", LastName: "Admin"}
	dave := &model.User{Id: "mm-dave", Username: "dave", FirstName: "Dave", LastName: "Diaz"}
	ghost := seedGhost(t, p, api, ada)
	api.On("GetUser", dave.Id).Return(dave, nil).Maybe()
	bridge := p.mattermostToMatrixBridge

	// The call starts.
	start := callPost("call1", map[string]any{"start_at": float64(1_000_000), "end_at": float64(0), "participants": []any{"mm-ada"}})
	require.NoError(t, bridge.SyncPostToMatrix(start, callTestChannel))

	notices := hs.puts("/send/m.room.message/")
	require.Len(t, notices, 1)
	assert.Equal(t, "m.notice", notices[0].Body["msgtype"])
	body, _ := notices[0].Body["body"].(string)
	assert.Contains(t, body, "Ada Admin started a call in Mattermost")
	assert.Contains(t, body, "https://chat.example.com/acme/channels/town?join_call=true")
	assert.Contains(t, notices[0].Body["formatted_body"], `<a href="https://chat.example.com/acme/channels/town?join_call=true">Join the call</a>`)

	states := hs.puts("/state/" + callStateEventType + "/call1")
	require.Len(t, states, 1)
	assert.Equal(t, "call1", states[0].Body["post_id"])
	assert.Equal(t, []any{"Ada Admin"}, states[0].Body["participants"])
	_ = ghost

	// Someone joins: the state follows, no new notice.
	joined := callPost("call1", map[string]any{"start_at": float64(1_000_000), "end_at": float64(0), "participants": []any{"mm-ada", "mm-dave"}})
	require.NoError(t, bridge.SyncPostToMatrix(joined, callTestChannel))
	assert.Len(t, hs.puts("/send/m.room.message/"), 1, "participant changes do not repeat the announcement")
	states = hs.puts("/state/" + callStateEventType + "/call1")
	require.Len(t, states, 2)
	assert.Equal(t, []any{"Ada Admin", "Dave Diaz"}, states[1].Body["participants"])

	// An unchanged update does nothing.
	require.NoError(t, bridge.SyncPostToMatrix(joined, callTestChannel))
	assert.Len(t, hs.puts("/state/"+callStateEventType+"/call1"), 2)

	// The call ends: the notice is edited and the state cleared.
	ended := callPost("call1", map[string]any{"start_at": float64(1_000_000), "end_at": float64(1_000_000 + 12*60_000), "participants": []any{"mm-ada", "mm-dave"}})
	require.NoError(t, bridge.SyncPostToMatrix(ended, callTestChannel))
	edits := hs.puts("/send/m.room.message/")
	require.Len(t, edits, 2)
	relates, _ := edits[1].Body["m.relates_to"].(map[string]any)
	assert.Equal(t, "m.replace", relates["rel_type"])
	newContent, _ := edits[1].Body["m.new_content"].(map[string]any)
	assert.Equal(t, "📞 Call ended · 12 min · 2 participant(s)", newContent["body"])
	states = hs.puts("/state/" + callStateEventType + "/call1")
	require.Len(t, states, 3)
	assert.Empty(t, states[2].Body, "state is cleared when the call ends")

	// A late update after the end does nothing more.
	require.NoError(t, bridge.SyncPostToMatrix(ended, callTestChannel))
	assert.Len(t, hs.puts("/send/m.room.message/"), 2)
}

func TestCallThatEndedBeforeTheBridgeSawItIsIgnored(t *testing.T) {
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, "")
	seedGhost(t, p, api, &model.User{Id: "mm-ada", Username: "ada"})
	ended := callPost("old", map[string]any{"start_at": float64(1), "end_at": float64(2)})
	require.NoError(t, p.mattermostToMatrixBridge.SyncPostToMatrix(ended, callTestChannel))
	assert.Empty(t, hs.puts("/send/"))
}

func TestCallsCanBeSwitchedOff(t *testing.T) {
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, callsModeOff)
	seedGhost(t, p, api, &model.User{Id: "mm-ada", Username: "ada"})
	require.NoError(t, p.mattermostToMatrixBridge.SyncPostToMatrix(callPost("c", map[string]any{"start_at": float64(1)}), callTestChannel))
	assert.Empty(t, hs.puts("/send/"))

	event := MatrixEvent{Type: callMemberEventRTC, RoomID: callTestRoom, Sender: "@alice:example.com", StateKey: strPtr("k"), Content: map[string]any{"x": 1}}
	require.NoError(t, p.handleMatrixCallEvent(event, callTestChannel))
	api.AssertNotCalled(t, "CreatePost", mock.Anything)
}

func strPtr(s string) *string { return &s }

func newInboundCallTest(t *testing.T) (*Plugin, *plugintest.API, *[]*model.Post, *[]*model.Post) {
	t.Helper()
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, "")
	p.configuration.ElementURL = "https://chat.example.com"

	alice := &model.User{Id: "mm-alice", Username: "matrix_alice", FirstName: "Alice", LastName: "Anders"}
	bob := &model.User{Id: "mm-bob", Username: "matrix_bob", FirstName: "Bob", LastName: "Brandt"}
	for matrixID, u := range map[string]*model.User{"@alice:example.com": alice, "@bob:example.com": bob} {
		require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixUserKey(matrixID), []byte(u.Id)))
		api.On("GetUser", u.Id).Return(u, nil).Maybe()
	}

	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil).Maybe()

	var created, updated []*model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		post := args.Get(0).(*model.Post).Clone()
		post.Id = "card1"
		created = append(created, post)
	}).Return(&model.Post{Id: "card1"}, nil).Maybe()
	api.On("GetPost", "card1").Return(func(string) *model.Post {
		if len(updated) > 0 {
			return updated[len(updated)-1].Clone()
		}
		if len(created) > 0 {
			return created[0].Clone()
		}
		return &model.Post{Id: "card1"}
	}, nil).Maybe()
	api.On("UpdatePost", mock.Anything).Run(func(args mock.Arguments) {
		updated = append(updated, args.Get(0).(*model.Post).Clone())
	}).Return(func(post *model.Post) *model.Post { return post }, nil).Maybe()
	return p, api, &created, &updated
}

func memberEvent(typ, user, key string, content map[string]any, ts int64) MatrixEvent {
	return MatrixEvent{Type: typ, RoomID: callTestRoom, Sender: user, StateKey: &key, Content: content, Timestamp: ts}
}

func TestMatrixCallIsShownAsACardAndEnds(t *testing.T) {
	p, _, created, updated := newInboundCallTest(t)
	now := time.Now().UnixMilli()

	// Alice joins an Element Call: a card appears.
	require.NoError(t, p.handleMatrixCallEvent(memberEvent(callMemberEventRTC, "@alice:example.com", "_@alice:example.com_D1", map[string]any{"application": "m.call"}, now), callTestChannel))
	require.Len(t, *created, 1)
	card := (*created)[0]
	assert.Equal(t, matrixCallPostType, card.Type)
	assert.Equal(t, "mm-alice", card.UserId, "posted as the Matrix user's Mattermost account")
	assert.Equal(t, true, card.Props["from_matrix"], "the card must not be relayed back to Matrix")
	assert.Equal(t, []string{"Alice Anders"}, card.Props["participants"])
	assert.Equal(t, "https://chat.example.com/#/room/!room:example.com", card.Props["join_url"])
	assert.Contains(t, card.Message, "Alice Anders started a call in Matrix")

	// Bob joins through the legacy event type: the same card is updated.
	require.NoError(t, p.handleMatrixCallEvent(memberEvent(callMemberEventLegacy, "@bob:example.com", "_@bob:example.com_D2", map[string]any{"application": "m.call"}, now+1000), callTestChannel))
	assert.Len(t, *created, 1, "one card per call")
	require.Len(t, *updated, 1)
	assert.Equal(t, []string{"Alice Anders", "Bob Brandt"}, (*updated)[0].Props["participants"])

	// Alice leaves: still running. Bob leaves: the call ends.
	require.NoError(t, p.handleMatrixCallEvent(memberEvent(callMemberEventRTC, "@alice:example.com", "_@alice:example.com_D1", map[string]any{}, now+2000), callTestChannel))
	require.NoError(t, p.handleMatrixCallEvent(memberEvent(callMemberEventLegacy, "@bob:example.com", "_@bob:example.com_D2", map[string]any{}, now+3000), callTestChannel))
	last := (*updated)[len(*updated)-1]
	assert.Greater(t, propInt64(last.Props, "end_at"), int64(0))
	assert.Contains(t, last.Message, "Call ended")
	assert.Equal(t, []string{"Alice Anders", "Bob Brandt"}, last.Props["participants"], "the card keeps who was in the call")

	// A new call in the same room gets a new card.
	*created = nil
	require.NoError(t, p.handleMatrixCallEvent(memberEvent(callMemberEventRTC, "@bob:example.com", "_@bob:example.com_D3", map[string]any{"application": "m.call"}, now+60_000), callTestChannel))
	assert.Len(t, *created, 1)
}

func TestMatrixCallMembershipExpires(t *testing.T) {
	p, _, created, updated := newInboundCallTest(t)
	now := time.Now().UnixMilli()

	// Membership that expired in the past does not start a call.
	expired := memberEvent(callMemberEventLegacy, "@alice:example.com", "k1", map[string]any{"expires": float64(1000)}, now-60_000)
	require.NoError(t, p.handleMatrixCallEvent(expired, callTestChannel))
	assert.Empty(t, *created)

	// A live one does, and the sweep ends it once it lapses.
	live := memberEvent(callMemberEventLegacy, "@alice:example.com", "k1", map[string]any{"expires": float64(2000)}, now)
	require.NoError(t, p.handleMatrixCallEvent(live, callTestChannel))
	require.Len(t, *created, 1)

	var rec matrixCallRecord
	require.True(t, loadJSON(p.kvstore, matrixCallKeyPrefix+callTestRoom, &rec))
	m := rec.Members["k1"]
	m.Expires = now - 1
	rec.Members["k1"] = m
	require.NoError(t, storeJSON(p.kvstore, matrixCallKeyPrefix+callTestRoom, &rec))

	p.sweepCalls()
	require.NotEmpty(t, *updated)
	assert.Contains(t, (*updated)[len(*updated)-1].Message, "Call ended")
}

func TestStartCallJoinableFromBothSides(t *testing.T) {
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, callsModeElementCall)
	p.configuration.ElementCallURL = "https://call.example.com/room?roomId={room_id}&name={display_name}"
	ada := &model.User{Id: "mm-ada", Username: "ada", FirstName: "Ada", LastName: "Admin"}
	seedGhost(t, p, api, ada)

	var posts []*model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		posts = append(posts, args.Get(0).(*model.Post).Clone())
	}).Return(&model.Post{Id: "card1"}, nil).Maybe()

	joinURL, err := p.StartCall("mm-ada", callTestChannel, "")
	require.NoError(t, err)
	assert.Equal(t, "https://call.example.com/room?roomId=%21room%3Aexample.com&name=Ada+Admin", joinURL)

	widgets := hs.puts("/state/" + widgetStateEventType + "/" + callWidgetID(callTestChannel))
	require.Len(t, widgets, 1, "Element gets a widget for the call")
	assert.Equal(t, joinURL, widgets[0].Body["url"])
	notices := hs.puts("/send/m.room.message/")
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0].Body["body"], joinURL)

	require.Len(t, posts, 1, "Mattermost gets a card with a join button")
	assert.Equal(t, matrixCallPostType, posts[0].Type)
	assert.Equal(t, joinURL, posts[0].Props["join_url"])
	assert.Equal(t, "Town", posts[0].Props["title"], "defaults to the channel name")

	// One call per room: asking again returns the running one.
	again, err := p.StartCall("mm-ada", callTestChannel, "Another")
	require.NoError(t, err)
	assert.Equal(t, joinURL, again)
	assert.Len(t, posts, 1)
	assert.Len(t, hs.puts("/send/m.room.message/"), 1)
}

func TestStartCallExplainsWhatIsMissing(t *testing.T) {
	hs := newRecordingHomeserver(t)
	p, api := newCallsTestPlugin(t, hs, callsModePresence)
	api.On("GetUser", "mm-ada").Return(&model.User{Id: "mm-ada", Username: "ada"}, nil).Maybe()
	_, err := p.StartCall("mm-ada", callTestChannel, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")

	p.configuration.CallsMode = callsModeElementCall
	p.configuration.ElementCallURL = "javascript:alert({room_id})"
	_, err = p.StartCall("mm-ada", callTestChannel, "")
	require.Error(t, err)

	p.configuration.ElementCallURL = "https://call.example.com/{room_id}"
	require.NoError(t, p.kvstore.Delete(kvstore.BuildChannelMappingKey(callTestChannel)))
	_, err = p.StartCall("mm-ada", callTestChannel, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not bridged")
}

func TestExpandCallURL(t *testing.T) {
	got, err := expandCallURL("https://c.example/r?id={room_id}&n={display_name}", map[string]string{"room_id": "!a:b", "display_name": "Ada & Bob"})
	require.NoError(t, err)
	assert.Equal(t, "https://c.example/r?id=%21a%3Ab&n=Ada+%26+Bob", got)

	for _, bad := range []string{"ftp://x.example", "javascript:alert(1)", "//no-scheme.example", "not a url"} {
		_, err := expandCallURL(bad, nil)
		assert.Error(t, err, bad)
	}
}

func TestFormatCallDuration(t *testing.T) {
	assert.Equal(t, "1 min", formatCallDuration(1000, 2000))
	assert.Equal(t, "12 min", formatCallDuration(1000, 1000+12*60_000))
	assert.Equal(t, "1 h", formatCallDuration(1000, 1000+60*60_000))
	assert.Equal(t, "1 h 5 min", formatCallDuration(1000, 1000+65*60_000))
	assert.Equal(t, "", formatCallDuration(0, 5000))
}
