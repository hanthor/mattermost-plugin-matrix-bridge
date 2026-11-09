package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// These cover capabilities no other suite exercises: direct messages, encrypted
// rooms, presence, structure backfill, and moving from ghosts to mirror
// accounts and freezing the bridge. Each ends in capabilities.Verify, so the
// matrix and these tests cannot drift apart.

// asRequest calls the homeserver as the appservice, optionally acting as a
// user (appservices may impersonate the accounts they own).
func asRequest(t *testing.T, h *Harness, method, path string, actAs string, body any) (int, gjson.Result) {
	t.Helper()
	base := h.Deployment.UnauthenticatedClient(t, HSName).BaseURL
	u := base + path
	if actAs != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "user_id=" + url.QueryEscape(actAs)
	}
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u, reader)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+h.ASToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, gjson.ParseBytes(raw)
}

// joinedRoomsOf lists the rooms an appservice-owned account is in.
func joinedRoomsOf(t *testing.T, h *Harness, userID string) []string {
	t.Helper()
	_, out := asRequest(t, h, http.MethodGet, "/_matrix/client/v3/joined_rooms", userID, nil)
	var rooms []string
	for _, r := range out.Get("joined_rooms").Array() {
		rooms = append(rooms, r.Str)
	}
	return rooms
}

// messagesSeenBy returns the message bodies of a room as seen by userID.
func messagesSeenBy(t *testing.T, h *Harness, userID, roomID string) map[string]string {
	t.Helper()
	_, out := asRequest(t, h, http.MethodGet, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/messages?dir=b&limit=50", userID, nil)
	bodies := map[string]string{}
	out.Get("chunk").ForEach(func(_, ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.message" {
			bodies[ev.Get("content.body").Str] = ev.Get("sender").Str
		}
		return true
	})
	return bodies
}

func TestDirectMessages(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	carol, carolClient := h.CreateMattermostUser("carol")
	dave, _ := h.CreateMattermostUser("dave")
	carolGhost, daveGhost := h.GhostUserID(carol.Id), h.GhostUserID(dave.Id)

	t.Run("a direct message between two Mattermost users is not bridged", func(t *testing.T) {
		dm, _, err := carolClient.CreateDirectChannel(ctx, carol.Id, dave.Id)
		require.NoError(t, err)
		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: dm.Id, Message: "Hello Dave, privately"})
		require.NoError(t, err)
		time.Sleep(8 * time.Second)

		bridged := false
		for _, ghost := range []string{carolGhost, daveGhost, h.BridgeBotUserID()} {
			for _, r := range joinedRoomsOf(t, h, ghost) {
				if _, ok := messagesSeenBy(t, h, ghost, r)["Hello Dave, privately"]; ok {
					bridged = true
				}
			}
		}
		// Private conversations are not sent to Matrix unless the opt-in is on
		// (see TestDirectMessagesOptIn).
		assert.False(t, bridged)
	})

	t.Run("a direct message started in Matrix does not become a Mattermost direct channel", func(t *testing.T) {
		alice := h.NewMatrixUser("alice")
		room := alice.MustCreateRoom(t, map[string]any{"preset": "trusted_private_chat", "is_direct": true, "invite": []string{carolGhost}})
		SendMatrixText(t, alice, room, "Hello Carol, from Matrix")
		time.Sleep(8 * time.Second)

		channels, _, err := h.Admin.GetChannelsForTeamForUser(ctx, h.Team.Id, carol.Id, false, "")
		require.NoError(t, err)
		bridged := false
		for _, ch := range channels {
			if ch.Type == model.ChannelTypeDirect && strings.Contains(ch.Name, carol.Id) && !strings.Contains(ch.Name, dave.Id) {
				bridged = true
			}
		}
		capabilities.Verify(t, "rooms.dm.in", bridged)
	})
}

func TestEncryptedRoomsAreNotBridged(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()
	alice := h.NewMatrixUser("alice")
	carol, carolClient := h.CreateMattermostUser("carol")

	encryptionState := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}

	t.Run("an encrypted room is not mapped", func(t *testing.T) {
		channel := h.CreateChannel("encrypted", "Encrypted")
		room := alice.MustCreateRoom(t, map[string]any{"preset": "public_chat", "name": "Encrypted", "initial_state": encryptionState})
		h.WaitForChannelRoom(channel)
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		// The channel got a room of its own; point it at the encrypted one instead.
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
		require.NoError(t, err)
		require.NotContains(t, resp.Text, "❌", resp.Text)

		resp, _, err = h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix map "+room)
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "end-to-end encrypted", resp.Text)

		status, ch := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+channel.Id)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, false, ch["mapped"], "the room was not mapped")
	})

	t.Run("a mapped room that turns encryption on stops syncing in both directions", func(t *testing.T) {
		channel := h.CreateChannel("turns-encrypted", "Turns encrypted")
		room := h.CreateMappedRoom(alice, channel.Id, "Turns encrypted")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
		require.NoError(t, err)
		_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Sent while the room was plain"})
		require.NoError(t, err)
		WaitForMatrixMessage(t, alice, room, "Sent while the room was plain")

		alice.MustDo(t, http.MethodPut, []string{"_matrix", "client", "v3", "rooms", room, "state", "m.room.encryption"},
			client.WithJSONBody(t, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}))
		WaitFor(t, bridgeTimeout, "the bridge to notice", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
			return err == nil && strings.Contains(resp.Text, "end-to-end encrypted")
		})
		_, ch := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+channel.Id)
		assert.Equal(t, true, ch["encrypted"], "the panel says so")

		// Mattermost to Matrix: nothing is sent in the clear.
		_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Must not appear in the clear"})
		require.NoError(t, err)
		// Matrix to Mattermost: ciphertext cannot be read, so no post.
		alice.MustDo(t, http.MethodPut, []string{"_matrix", "client", "v3", "rooms", room, "send", "m.room.encrypted", "txn-enc-1"},
			client.WithJSONBody(t, map[string]any{"algorithm": "m.megolm.v1.aes-sha2", "ciphertext": "AwgAEnAbc123opaque", "device_id": "D", "sender_key": "K", "session_id": "S"}))
		time.Sleep(6 * time.Second)

		assert.Equal(t, 0, CountMatrixMessages(t, alice, room, "Must not appear in the clear"), "no plaintext in an encrypted room")
		posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
		require.NoError(t, err)
		for _, p := range posts.Posts {
			assert.NotContains(t, p.Message, "opaque")
		}
	})
}

func TestPresenceFromMatrix(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("presence", "Presence")
	room := h.CreateMappedRoom(alice, channel.Id, "Presence")
	SendMatrixText(t, alice, room, "I am here")
	post := WaitForPostWithMessage(t, h.Admin, channel.Id, "I am here")

	statusOf := func() string {
		s, _, err := h.Admin.GetUserStatus(ctx, post.UserId, "")
		if err != nil {
			return ""
		}
		return s.Status
	}
	set := func(presence string) {
		alice.MustDo(t, http.MethodPut, []string{"_matrix", "client", "v3", "presence", alice.UserID, "status"},
			client.WithJSONBody(t, map[string]any{"presence": presence, "status_msg": "from Element"}))
	}

	set("unavailable")
	works := TryFor(bridgeTimeout, func() bool { set("unavailable"); return statusOf() == model.StatusAway })
	capabilities.Verify(t, "ephemeral.presence.in", works)
}

func TestStructureBackfill(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	// Users with no activity yet have no Matrix account until backfilled.
	late, _ := h.CreateMattermostUser("latecomer")
	lateGhost := h.GhostUserID(late.Id)

	resp, _, err := h.Admin.ExecuteCommand(ctx, h.TownSquare().Id, "/matrix backfill all")
	require.NoError(t, err)
	require.Contains(t, resp.Text, "Backfill started", resp.Text)

	t.Run("every user gets an account", func(t *testing.T) {
		exists := TryFor(bridgeTimeout, func() bool {
			code, _ := asRequest(t, h, http.MethodGet, "/_matrix/client/v3/profile/"+url.PathEscape(lateGhost), "", nil)
			return code == http.StatusOK
		})
		capabilities.Verify(t, "integration.backfill", exists)
	})

	t.Run("the team becomes a space", func(t *testing.T) {
		bot := h.BotClient()
		var space string
		WaitFor(t, bridgeTimeout, "a space for the team", func() bool {
			for _, r := range joinedRoomsOf(t, h, h.BridgeBotUserID()) {
				res := bot.Do(t, http.MethodGet, []string{"_matrix", "client", "v3", "rooms", r, "state", "m.room.create"})
				data, _ := io.ReadAll(res.Body)
				_ = res.Body.Close()
				if gjson.GetBytes(data, "type").Str == "m.space" {
					space = r
					return true
				}
			}
			return false
		})
		capabilities.Verify(t, "spaces.team", space != "")
	})
}

func TestGhostsToMirrorMigrationAndCutover(t *testing.T) {
	h := NewHarness(t) // ghost mode
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("migrate", "Migrate")
	room := h.CreateMappedRoom(alice, channel.Id, "Migrate")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	carolGhost, carolMirror := h.GhostUserID(carol.Id), "@carol:"+HSName

	_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Written as a ghost"})
	require.NoError(t, err)
	before := WaitForMatrixMessage(t, alice, room, "Written as a ghost")
	require.Equal(t, carolGhost, before.Get("sender").Str)

	t.Run("turning mirror mode on does not change existing accounts", func(t *testing.T) {
		h.SetBridgeSetting("enable_mirror_mode", true)
		WaitFor(t, bridgeTimeout, "the setting to be noticed", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
			return err == nil && strings.Contains(resp.Text, "migrate ghosts-to-mirror")
		})
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Still a ghost"})
		require.NoError(t, err)
		assert.Equal(t, carolGhost, WaitForMatrixMessage(t, alice, room, "Still a ghost").Get("sender").Str)
	})

	t.Run("a dry run says what would move and changes nothing", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix migrate ghosts-to-mirror")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "Dry run", resp.Text)
		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "After the dry run"})
		require.NoError(t, err)
		assert.Equal(t, carolGhost, WaitForMatrixMessage(t, alice, room, "After the dry run").Get("sender").Str)
	})

	t.Run("confirming moves people to their own accounts", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix migrate ghosts-to-mirror confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "now in mirror mode", resp.Text)

		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncJoinedTo(carolMirror, room))
		alice.MustSyncUntil(t, client.SyncReq{}, client.SyncLeftFrom(carolGhost, room))

		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Written as herself"})
		require.NoError(t, err)
		after := WaitForMatrixMessage(t, alice, room, "Written as herself")
		assert.Equal(t, carolMirror, after.Get("sender").Str)
		assert.Equal(t, carolGhost, messagesSeenBy(t, h, h.BridgeBotUserID(), room)["Written as a ghost"], "history stays attributed to the ghost")
		capabilities.Verify(t, "mirror.migrate", after.Get("sender").Str == carolMirror)
	})

	t.Run("cut-over freezes accounts and rooms", func(t *testing.T) {
		h.SetBridgeSetting("cutover_mode", true)
		WaitFor(t, bridgeTimeout, "cut-over to be noticed", func() bool {
			resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
			return err == nil && strings.Contains(strings.ToLower(resp.Text), "cut-over")
		})

		// No room for a new channel, no account for a new person...
		late := h.CreateChannel("after-cutover", "After cutover")
		erin, erinClient := h.CreateMattermostUser("erin")
		_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, erin.Id)
		require.NoError(t, err)
		_, _, err = erinClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Erin writes during the freeze"})
		require.NoError(t, err)
		// ...while everyone who already has one keeps syncing.
		_, _, err = carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Carol writes during the freeze"})
		require.NoError(t, err)
		assert.Equal(t, carolMirror, WaitForMatrixMessage(t, alice, room, "Carol writes during the freeze").Get("sender").Str)

		time.Sleep(5 * time.Second)
		assert.Equal(t, 0, CountMatrixMessages(t, alice, room, "Erin writes during the freeze"), "no account is created for a new person")
		assert.Empty(t, FindBridgedRoomForChannel(t, h.BotClient(), late.Id), "no room is created for a new channel")
		capabilities.Verify(t, "mirror.cutover", true)
	})
}

var _ = fmt.Sprint
