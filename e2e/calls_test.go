package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/complement/client"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestCalls checks call announcements in both directions and a call both
// sides can join. Audio and video are not relayed; see calls.media in the
// capability matrix.
func TestCalls(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{
		CallsMode:      "element_call",
		ElementCallURL: "https://call.example.com/room?roomId={room_id}",
	})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("calls", "Calls")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Calls")
	h.GrantBridgePower(alice, roomID, 50)
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(h.GhostUserID(carol.Id), roomID))

	t.Run("a Mattermost call is announced in the room and its end is shown", func(t *testing.T) {
		start := time.Now().UnixMilli()
		post, _, err := carolClient.CreatePost(ctx, &model.Post{
			ChannelId: channel.Id,
			Type:      "custom_calls",
			Message:   "",
			Props:     model.StringInterface{"start_at": start, "end_at": 0, "participants": []string{carol.Id}},
		})
		require.NoError(t, err)

		notice := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return ev.Get("type").Str == "m.room.message" && ev.Get("content.msgtype").Str == "m.notice" &&
				ev.Get("content.body").Str != "" && containsFold(ev.Get("content.body").Str, "started a call in Mattermost")
		})
		assert.Equal(t, h.GhostUserID(carol.Id), notice.Get("sender").Str, "announced by the person who started it")
		capabilities.Verify(t, "calls.presence", true)

		post.Props["end_at"] = start + 5*60_000
		post.Props["participants"] = []string{carol.Id}
		_, _, err = carolClient.UpdatePost(ctx, post.Id, post)
		require.NoError(t, err)
		WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return containsFold(ev.Get("content.m\\.new_content.body").Str, "Call ended")
		})
	})

	t.Run("a Matrix call becomes a card in the channel and ends", func(t *testing.T) {
		member := "_" + alice.UserID + "_DEVICE1"
		alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.rtc.member", member},
			client.WithJSONBody(t, map[string]any{"application": map[string]any{"type": "m.call"}, "member": map[string]any{"claimed_user_id": alice.UserID}}))

		card := WaitForPost(t, h.Admin, channel.Id, "a call card", func(p *model.Post) bool { return p.Type == "custom_matrix_call" })
		assert.Equal(t, true, card.Props["from_matrix"])
		assert.Equal(t, roomID, card.Props["matrix_room_id"])
		assert.NotEmpty(t, card.Props["join_url"])
		capabilities.Verify(t, "calls.presence.in", true)

		alice.MustDo(t, "PUT", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "m.rtc.member", member},
			client.WithJSONBody(t, map[string]any{}))
		WaitFor(t, bridgeTimeout, "the call card to show the end", func() bool {
			p, _, err := h.Admin.GetPost(ctx, card.Id, "")
			return err == nil && containsFold(p.Message, "Call ended")
		})
	})

	t.Run("/matrix call starts a call both sides can join", func(t *testing.T) {
		resp, _, err := carolClient.ExecuteCommand(ctx, channel.Id, "/matrix call Weekly sync")
		require.NoError(t, err)
		require.NotContains(t, resp.Text, "❌", resp.Text)
		require.Contains(t, resp.Text, "https://call.example.com/room?roomId=")

		WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
			return containsFold(ev.Get("content.body").Str, "started a call: Weekly sync")
		})
		widget := alice.MustDo(t, "GET", []string{"_matrix", "client", "v3", "rooms", roomID, "state", "im.vector.modular.widgets", "mattermost_call_" + channel.Id})
		body := gjson.ParseBytes(client.ParseJSON(t, widget))
		assert.Contains(t, body.Get("url").Str, "https://call.example.com/room?roomId=")
		assert.Equal(t, "Call: Weekly sync", body.Get("name").Str)

		card := WaitForPost(t, h.Admin, channel.Id, "the join card", func(p *model.Post) bool {
			return p.Type == "custom_matrix_call" && p.Props["title"] == "Weekly sync"
		})
		assert.Equal(t, body.Get("url").Str, card.Props["join_url"])
		capabilities.Verify(t, "calls.join", true)
	})
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
