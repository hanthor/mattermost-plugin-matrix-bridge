package e2e

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}
