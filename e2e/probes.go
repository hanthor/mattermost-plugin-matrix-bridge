package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/tidwall/gjson"
)

// probeTimeout bounds how long a gap probe waits before concluding that a
// capability does not work. Gaps are the common case, so keep it short.
const probeTimeout = 10 * time.Second

// TryFor polls cond until it returns true or the timeout elapses and reports
// the outcome instead of failing the test.
func TryFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

// roomState fetches a state event and reports whether it exists.
func roomState(t *testing.T, cs *client.CSAPI, roomID, eventType, stateKey string) (gjson.Result, bool) {
	t.Helper()
	res := cs.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", roomID, "state", eventType, stateKey})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return gjson.Result{}, false
	}
	return gjson.ParseBytes(client.ParseJSON(t, res)), true
}

// timelineHas reports whether any timeline event in the room satisfies pred
// in a fresh sync.
func timelineHas(t *testing.T, cs *client.CSAPI, roomID string, pred func(gjson.Result) bool) bool {
	t.Helper()
	res, _ := cs.MustSync(t, client.SyncReq{Filter: wideTimelineFilter})
	found := false
	res.Get("rooms.join." + client.GjsonEscape(roomID) + ".timeline.events").ForEach(func(_, ev gjson.Result) bool {
		if pred(ev) {
			found = true
			return false
		}
		return true
	})
	return found
}

// ephemeralHas reports whether any ephemeral event in the room satisfies
// pred in a fresh sync.
func ephemeralHas(t *testing.T, cs *client.CSAPI, roomID string, pred func(gjson.Result) bool) bool {
	t.Helper()
	res, _ := cs.MustSync(t, client.SyncReq{})
	found := false
	res.Get("rooms.join." + client.GjsonEscape(roomID) + ".ephemeral.events").ForEach(func(_, ev gjson.Result) bool {
		if pred(ev) {
			found = true
			return false
		}
		return true
	})
	return found
}

// presenceOf fetches a user's presence state, or "" when unknown.
func presenceOf(t *testing.T, cs *client.CSAPI, userID string) (presence, statusMsg string) {
	t.Helper()
	res := cs.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "presence", userID, "status"})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", ""
	}
	body := gjson.ParseBytes(client.ParseJSON(t, res))
	return body.Get("presence").Str, body.Get("status_msg").Str
}
