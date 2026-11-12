package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/matrix-org/complement/b"
	"github.com/matrix-org/complement/client"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// TestEncryptedRoomsAreBridged runs with encryption switched on: a person in
// Element, whose client really encrypts, talks to Mattermost through a room
// with end-to-end encryption.
func TestEncryptedRoomsAreBridged(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true})
	ctx := t.Context()
	alice := h.NewMatrixUser("alice")
	carol, carolClient := h.CreateMattermostUser("carol")

	channel := h.CreateChannel("secret", "Secret")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)
	encryption := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
	h.WaitForChannelRoom(channel)
	room := h.CreateMappedRoomWith(alice, channel.Id, map[string]any{"name": "Secret", "initial_state": encryption})
	roomID := id.RoomID(room)

	t.Run("the bridge accepts the room and says so", func(t *testing.T) {
		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix status")
		require.NoError(t, err)
		assert.Contains(t, resp.Text, "encrypts and decrypts")
		status, ch := pluginAPI(t, h, carolClient, http.MethodGet, "/channels/"+channel.Id)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, true, ch["encrypted"])
		assert.Equal(t, true, ch["encryption_works"])
	})

	ec := newEncryptedClient(t, alice)

	t.Run("an encrypted Matrix message arrives in Mattermost", func(t *testing.T) {
		ec.Send(roomID, "Hello from an encrypted client")
		defer func() {
			if t.Failed() {
				posts, _, _ := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 50, "", false, false)
				for _, p := range posts.Posts {
					t.Logf("post by %s: %q", p.UserId, p.Message)
				}
			}
		}()
		post := WaitForPostWithMessage(t, carolClient, channel.Id, "Hello from an encrypted client")
		assert.NotEmpty(t, post.Id)
		capabilities.Verify(t, "rooms.encryption.in", post.Id != "")
	})

	t.Run("a Mattermost post arrives encrypted in Matrix", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Hello from Mattermost, encrypted"})
		require.NoError(t, err)
		ec.WaitForMessage(roomID, "Hello from Mattermost, encrypted")
		// The homeserver's copy is ciphertext.
		assert.False(t, ec.ServerCopyContains(roomID, "Hello from Mattermost, encrypted"), "no plaintext on the homeserver")
		assert.Equal(t, 0, CountMatrixMessages(t, alice, room, "Hello from Mattermost, encrypted"))
		capabilities.Verify(t, "rooms.encryption.out", true)
	})

	t.Run("a second message reuses the room key", func(t *testing.T) {
		_, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "Second encrypted post"})
		require.NoError(t, err)
		ec.WaitForMessage(roomID, "Second encrypted post")
	})

	t.Run("a Mattermost attachment is encrypted before it is uploaded", func(t *testing.T) {
		upload, _, err := h.Admin.UploadFile(ctx, []byte("secret attachment body"), channel.Id, "notes.txt")
		require.NoError(t, err)
		_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "See the attachment", FileIds: []string{upload.FileInfos[0].Id}})
		require.NoError(t, err)

		msg := ec.WaitForFile(roomID, "notes.txt")
		file := msg.Content.AsMessage().File
		require.NotNil(t, file, "the message points at an encrypted file")
		stored, _ := alice.DownloadContentAuthenticated(t, string(file.URL))
		assert.NotContains(t, string(stored), "secret attachment body", "the media repository holds ciphertext")
		require.NoError(t, file.PrepareForDecryption())
		plain, err := file.Decrypt(stored)
		require.NoError(t, err)
		assert.Equal(t, "secret attachment body", string(plain))
	})

	t.Run("an encrypted Matrix attachment arrives in Mattermost", func(t *testing.T) {
		ec.SendFile(roomID, alice, "report.txt", []byte("report from an encrypted client"))
		post := WaitForPost(t, carolClient, channel.Id, "the attachment", func(p *model.Post) bool {
			if len(p.FileIds) != 1 {
				return false
			}
			info, _, err := h.Admin.GetFileInfo(ctx, p.FileIds[0])
			return err == nil && info.Name == "report.txt"
		})
		info, _, err := h.Admin.GetFileInfo(ctx, post.FileIds[0])
		require.NoError(t, err)
		assert.Equal(t, "report.txt", info.Name)
		data, _, err := h.Admin.GetFile(ctx, post.FileIds[0])
		require.NoError(t, err)
		assert.Equal(t, "report from an encrypted client", string(data))
	})

	t.Run("a message the bridge cannot decrypt is announced in the channel", func(t *testing.T) {
		// A message encrypted with a key the bridge was never given, as one sent
		// before the bridge joined would be.
		alice.SendEventSynced(t, room, b.Event{Type: "m.room.encrypted", Content: map[string]any{
			"algorithm": "m.megolm.v1.aes-sha2", "sender_key": "unknown-sender-key",
			"session_id": "unknown-session", "device_id": "UNKNOWN", "ciphertext": "AwgAEiBunknownciphertext",
		}})
		post := WaitForPost(t, carolClient, channel.Id, "the notice", func(p *model.Post) bool {
			return strings.Contains(p.Message, "could not decrypt")
		})
		capabilities.Verify(t, "rooms.encryption.undecryptable_notice", post.Id != "")
	})

	t.Run("edits, reactions and replies in an encrypted room cross too", func(t *testing.T) {
		original := ec.Send(roomID, "encrypted original")
		post := WaitForPostWithMessage(t, carolClient, channel.Id, "encrypted original")

		ec.SendEvent(roomID, event.EventMessage, map[string]any{
			"msgtype": "m.text", "body": "* encrypted edited",
			"m.new_content": map[string]any{"msgtype": "m.text", "body": "encrypted edited"},
			"m.relates_to":  map[string]any{"rel_type": "m.replace", "event_id": string(original)},
		})
		WaitFor(t, bridgeTimeout, "the edit", func() bool {
			p, _, err := h.Admin.GetPost(ctx, post.Id, "")
			return err == nil && p.Message == "encrypted edited"
		})

		ec.SendEvent(roomID, event.EventReaction, map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": string(original), "key": "👍"},
		})
		WaitFor(t, bridgeTimeout, "the reaction", func() bool {
			reactions, _, err := h.Admin.GetReactions(ctx, post.Id)
			return err == nil && len(reactions) == 1
		})

		ec.SendEvent(roomID, event.EventMessage, map[string]any{
			"msgtype": "m.text", "body": "an encrypted reply",
			"m.relates_to": map[string]any{"m.in_reply_to": map[string]any{"event_id": string(original)}},
		})
		reply := WaitForPostWithMessage(t, carolClient, channel.Id, "an encrypted reply")
		assert.NotEmpty(t, reply.Id)
	})

	t.Run("the bridge's device is verified and the recovery key is shown once", func(t *testing.T) {
		preview, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix encryption-verify")
		require.NoError(t, err)
		assert.Contains(t, preview.Text, "recovery key")
		assert.NotContains(t, preview.Text, "```", "the preview shows no key")

		resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix encryption-verify confirm")
		require.NoError(t, err)
		require.Contains(t, resp.Text, "The bridge's device is verified", resp.Text)
		assert.Contains(t, resp.Text, "```", "the recovery key is in the reply")

		// The homeserver now holds the bot's cross-signing keys, and its device
		// carries a signature from the self-signing key.
		bot := h.BridgeBotUserID()
		res := alice.MustDo(t, http.MethodPost, []string{"_matrix", "client", "v3", "keys", "query"},
			client.WithJSONBody(t, map[string]any{"device_keys": map[string]any{bot: []string{}}}))
		parsed := gjson.ParseBytes(client.ParseJSON(t, res))
		assert.NotEmpty(t, parsed.Get("master_keys."+gjson.Escape(bot)).Raw, "master key published")
		selfSigning := parsed.Get("self_signing_keys." + gjson.Escape(bot) + ".keys")
		require.True(t, selfSigning.Exists(), "self-signing key published: %s", parsed.Raw)
		var selfKeyID string
		selfSigning.ForEach(func(k, _ gjson.Result) bool { selfKeyID = k.Str; return false })
		signed := false
		parsed.Get("device_keys." + gjson.Escape(bot)).ForEach(func(_, device gjson.Result) bool {
			if device.Get("signatures." + gjson.Escape(bot) + "." + gjson.Escape(selfKeyID)).Exists() {
				signed = true
			}
			return true
		})
		assert.True(t, signed, "the bot's device is signed by its self-signing key: %s", parsed.Raw)

		// Asking again does not replace the identity.
		again, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix encryption-verify confirm")
		require.NoError(t, err)
		assert.NotContains(t, again.Text, "```")
		assert.Contains(t, again.Text, "already has a cross-signing identity")
		capabilities.Verify(t, "rooms.encryption.verified_device", signed)
	})

	t.Run("after a plugin restart the bridge still reads the room's keys", func(t *testing.T) {
		_, err := h.Admin.DisablePlugin(ctx, PluginID)
		require.NoError(t, err)
		_, err = h.Admin.EnablePlugin(ctx, PluginID)
		require.NoError(t, err)
		// The same room key as before the restart: only a device kept in the
		// key-value store can open it.
		WaitFor(t, bridgeTimeout, "the bridge to read a message after the restart", func() bool {
			ec.Send(roomID, "sent after the restart")
			posts, _, err := h.Admin.GetPostsForChannel(ctx, channel.Id, 0, 60, "", false, false)
			if err != nil {
				return false
			}
			for _, id := range posts.Order {
				if posts.Posts[id].Message == "sent after the restart" {
					return true
				}
			}
			return false
		})
	})
}
