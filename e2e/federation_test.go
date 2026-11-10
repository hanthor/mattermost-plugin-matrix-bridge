package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestFederation bridges a room that lives on another homeserver. The bridge
// is attached to hs1; the room and its people are on hs2, so everything crosses
// Matrix federation as well as the bridge.
func TestFederation(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Federation: true})
	ctx := context.Background()

	dora := h.NewRemoteMatrixUser("dora")
	alias := "#federated:" + RemoteHSName
	roomID := dora.MustCreateRoom(t, map[string]interface{}{
		"preset": "public_chat", "name": "Federated", "room_alias_name": "federated",
	})
	channel := h.CreateChannel("federated", "Federated")

	t.Run("a room on another homeserver can be mapped", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix map "+alias)
		require.NoError(t, err)
		require.Contains(t, resp.Text, "Mapping Saved", resp.Text)
		WaitFor(t, bridgeTimeout, "the bridge bot to join the remote room", func() bool {
			res := dora.Do(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.room.member", h.BridgeBotUserID()})
			defer func() { _ = res.Body.Close() }()
			return res.StatusCode == 200 && strings.Contains(string(readAll(t, res.Body)), `"join"`)
		})
	})

	t.Run("a message from a user on another homeserver reaches Mattermost", func(t *testing.T) {
		SendMatrixText(t, dora, roomID, "Hello across federation")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Hello across federation")
		author, _, err := h.Admin.GetUser(ctx, post.UserId, "")
		require.NoError(t, err)
		assert.True(t, author.IsRemote())
		assert.Contains(t, author.Username, "dora")
	})

	t.Run("a Mattermost post reaches users on the other homeserver", func(t *testing.T) {
		_, _, err := h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Hello from Mattermost to hs2"})
		require.NoError(t, err)
		ev := WaitForMatrixMessage(t, dora, roomID, "Hello from Mattermost to hs2")
		assert.Contains(t, ev.Get("sender").Str, ":"+HSName, "sent by an account of the bridge's own homeserver")
	})

	t.Run("edits and reactions from the other homeserver arrive", func(t *testing.T) {
		eventID := SendMatrixText(t, dora, roomID, "typo here")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "typo here")

		dora.SendEventSynced(t, roomID, b.Event{Type: "m.room.message", Content: map[string]interface{}{
			"msgtype": "m.text", "body": "* fixed here",
			"m.new_content": map[string]interface{}{"msgtype": "m.text", "body": "fixed here"},
			"m.relates_to":  map[string]interface{}{"rel_type": "m.replace", "event_id": eventID},
		}})
		WaitFor(t, bridgeTimeout, "the edit", func() bool {
			p, _, err := h.Admin.GetPost(ctx, post.Id, "")
			return err == nil && p.Message == "fixed here"
		})

		dora.SendEventSynced(t, roomID, b.Event{Type: "m.reaction", Content: map[string]interface{}{
			"m.relates_to": map[string]interface{}{"rel_type": "m.annotation", "event_id": eventID, "key": "👍"},
		}})
		WaitFor(t, bridgeTimeout, "the reaction", func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, post.Id)
			return err == nil && len(reactions) == 1
		})
	})

	t.Run("a file from the other homeserver is downloaded across federation", func(t *testing.T) {
		mxc := dora.UploadContent(t, []byte("federated file body"), "fed.txt", "text/plain")
		dora.SendEventSynced(t, roomID, b.Event{Type: "m.room.message", Content: map[string]interface{}{
			"msgtype": "m.file", "body": "fed.txt", "url": mxc,
			"info": map[string]interface{}{"mimetype": "text/plain", "size": 19},
		}})
		WaitFor(t, bridgeTimeout, "the file post", func() bool {
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 30, "", false, false)
			if err != nil {
				return false
			}
			for _, id := range posts.Order {
				if len(posts.Posts[id].FileIds) > 0 {
					return true
				}
			}
			return false
		})
	})

	t.Run("people who join from the other homeserver stay in step", func(t *testing.T) {
		erin := h.NewRemoteMatrixUser("erin")
		erin.MustJoinRoom(t, roomID, []spec.ServerName{spec.ServerName(RemoteHSName)})
		SendMatrixText(t, erin, roomID, "Erin here")
		post := WaitForPostWithMessage(t, h.Admin, channel.Id, "Erin here")
		author, _, err := h.Admin.GetUser(ctx, post.UserId, "")
		require.NoError(t, err)
		assert.Contains(t, author.Username, "erin")
		capabilities.Verify(t, "federation.remote_room", strings.Contains(author.Username, "erin"))
	})
}

func readAll(t *testing.T, res interface{ Read([]byte) (int, error) }) []byte {
	t.Helper()
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := res.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out
		}
	}
}
