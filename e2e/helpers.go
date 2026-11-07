package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// wideTimelineFilter asks /sync for a long timeline so events sent earlier in
// a test are still visible to a fresh initial sync.
const wideTimelineFilter = `{"room":{"timeline":{"limit":200}}}`

// WaitForMatrixEvent syncs as cs until an event in roomID satisfies pred and
// returns that event.
func WaitForMatrixEvent(t *testing.T, cs *client.CSAPI, roomID string, pred func(ev gjson.Result) bool) gjson.Result {
	t.Helper()
	var found gjson.Result
	cs.MustSyncUntil(t, client.SyncReq{Filter: wideTimelineFilter}, client.SyncTimelineHas(roomID, func(ev gjson.Result) bool {
		if pred(ev) {
			found = ev
			return true
		}
		return false
	}))
	return found
}

// WaitForMatrixMessage waits for an m.room.message whose body equals body.
func WaitForMatrixMessage(t *testing.T, cs *client.CSAPI, roomID, body string) gjson.Result {
	t.Helper()
	return WaitForMatrixEvent(t, cs, roomID, func(ev gjson.Result) bool {
		return ev.Get("type").Str == "m.room.message" && ev.Get("content.body").Str == body
	})
}

// CountMatrixMessages returns how many m.room.message events in the room
// currently have the given body, as seen by cs.
func CountMatrixMessages(t *testing.T, cs *client.CSAPI, roomID, body string) int {
	t.Helper()
	res, _ := cs.MustSync(t, client.SyncReq{Filter: wideTimelineFilter})
	count := 0
	res.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").ForEach(func(_, ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.message" && ev.Get("content.body").Str == body {
			count++
		}
		return true
	})
	return count
}

// SendMatrixText sends a plain text message and returns its event ID.
func SendMatrixText(t *testing.T, cs *client.CSAPI, roomID, body string) string {
	t.Helper()
	return cs.SendEventSynced(t, roomID, b.Event{
		Type: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    body,
		},
	})
}

// WaitForPost polls the channel until a post satisfies pred and returns it.
func WaitForPost(t *testing.T, cl *model.Client4, channelID string, what string, pred func(*model.Post) bool) *model.Post {
	t.Helper()
	var found *model.Post
	WaitFor(t, bridgeTimeout, what, func() bool {
		posts, _, err := cl.GetPostsForChannel(context.Background(), channelID, 0, 100, "", false, false)
		if err != nil {
			return false
		}
		for _, post := range posts.Posts {
			if pred(post) {
				found = post
				return true
			}
		}
		return false
	})
	return found
}

// WaitForPostWithMessage waits for a post whose message equals message.
func WaitForPostWithMessage(t *testing.T, cl *model.Client4, channelID, message string) *model.Post {
	t.Helper()
	return WaitForPost(t, cl, channelID, fmt.Sprintf("post %q", message), func(p *model.Post) bool {
		return p.Message == message
	})
}

// CountPostsWithMessage returns how many non-deleted posts in the channel have
// exactly the given message.
func CountPostsWithMessage(t *testing.T, cl *model.Client4, channelID, message string) int {
	t.Helper()
	posts, _, err := cl.GetPostsForChannel(context.Background(), channelID, 0, 200, "", false, false)
	require.NoError(t, err)
	count := 0
	for _, post := range posts.Posts {
		if post.Message == message && post.DeleteAt == 0 {
			count++
		}
	}
	return count
}

// WaitForBridgedUserInChannel waits until a Matrix-originated (remote) user
// with the given Matrix localpart is a member of the channel and returns it.
func WaitForBridgedUserInChannel(t *testing.T, h *Harness, channelID, localpart string) *model.User {
	t.Helper()
	ctx := context.Background()
	var found *model.User
	WaitFor(t, bridgeTimeout, fmt.Sprintf("bridged Matrix user %s in channel", localpart), func() bool {
		users, _, err := h.Admin.GetUsersInChannel(ctx, channelID, 0, 200, "")
		if err != nil {
			return false
		}
		for _, u := range users {
			if u.IsRemote() && strings.HasSuffix(u.Username, localpart) {
				found = u
				return true
			}
		}
		return false
	})
	return found
}

// IsUserInChannel reports whether the user is currently a channel member.
func IsUserInChannel(t *testing.T, cl *model.Client4, channelID, userID string) bool {
	t.Helper()
	_, resp, err := cl.GetChannelMember(context.Background(), channelID, userID, "")
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return false
		}
		return false
	}
	return true
}

// WaitForTypingEvent listens on the Mattermost websocket for a typing event
// from userID in channelID.
func WaitForTypingEvent(t *testing.T, mmURL, authToken, channelID, userID string, timeout time.Duration) bool {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(mmURL, "http")
	ws, err := model.NewWebSocketClient4(wsURL, authToken)
	require.NoError(t, err, "websocket connect")
	defer ws.Close()
	ws.Listen()

	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ws.EventChannel:
			if !ok {
				return false
			}
			if ev.EventType() == model.WebsocketEventTyping &&
				ev.GetBroadcast().ChannelId == channelID &&
				ev.GetData()["user_id"] == userID {
				return true
			}
		case <-ws.PingTimeoutChannel:
			return false
		case <-deadline:
			return false
		}
	}
}

// mustString fails the test when a gjson result is not a non-empty string.
func mustString(t *testing.T, r gjson.Result, what string) string {
	t.Helper()
	require.True(t, r.Exists() && r.Str != "", "%s missing in %s", what, r.Raw)
	return r.Str
}

// Annotations returns the m.annotation relations of an event (reactions) as
// seen by cs. Redacted reactions are not included.
func Annotations(t *testing.T, cs *client.CSAPI, roomID, eventID string) []gjson.Result {
	t.Helper()
	res := cs.Do(t, "GET", []string{"_matrix", "client", "v1", "rooms", roomID, "relations", eventID, "m.annotation"})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return nil
	}
	return gjson.ParseBytes(client.ParseJSON(t, res)).Get("chunk").Array()
}

// FindBridgedRoomForChannel looks through the rooms the bridge bot has joined
// for the one carrying the com.mattermost.bridge.channel marker of channelID.
// It returns "" when none matches.
func FindBridgedRoomForChannel(t *testing.T, bot *client.CSAPI, channelID string) string {
	t.Helper()
	res := bot.Do(t, "GET", []string{"_matrix", "client", "v3", "joined_rooms"})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return ""
	}
	for _, r := range gjson.ParseBytes(client.ParseJSON(t, res)).Get("joined_rooms").Array() {
		state := bot.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", r.Str, "state", "com.mattermost.bridge.channel", ""})
		body := client.ParseJSON(t, state)
		_ = state.Body.Close()
		if state.StatusCode == 200 && gjson.GetBytes(body, "mattermost_channel_id").Str == channelID {
			return r.Str
		}
	}
	return ""
}
