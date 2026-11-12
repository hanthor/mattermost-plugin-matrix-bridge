package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/id"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
)

// TestHistoryImportIntoAnEncryptedRoom brings a channel's past into a room that
// has end-to-end encryption: the messages must arrive encrypted (the homeserver
// never holds plaintext) and still carry the time they were written.
func TestHistoryImportIntoAnEncryptedRoom(t *testing.T) {
	h := NewHarnessWithOptions(t, HarnessOptions{Encryption: true})
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("secret-history", "Secret history")
	carol, carolClient := h.CreateMattermostUser("carol")
	_, _, err := h.Admin.AddChannelMember(ctx, channel.Id, carol.Id)
	require.NoError(t, err)

	h.WaitForChannelRoom(channel)
	resp, _, err := h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix unmap")
	require.NoError(t, err)
	require.NotContains(t, resp.Text, "❌", resp.Text)

	yearAgo := time.Now().AddDate(-1, 0, 0).UnixMilli()
	old, _, err := carolClient.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "A secret from last year"})
	require.NoError(t, err)
	h.Mattermost.ExecSQL(t, fmt.Sprintf("UPDATE posts SET createat = %d WHERE id = '%s'", yearAgo, old.Id))

	encryption := []map[string]any{{"type": "m.room.encryption", "state_key": "", "content": map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}}
	room := h.CreateMappedRoomWith(alice, channel.Id, map[string]any{"name": "Secret history", "initial_state": encryption})
	roomID := id.RoomID(room)
	ec := newEncryptedClient(t, alice)

	resp, _, err = h.Admin.ExecuteCommand(ctx, channel.Id, "/matrix history confirm")
	require.NoError(t, err)
	require.Contains(t, resp.Text, "History import started", resp.Text)

	ev := ec.WaitForMessage(roomID, "A secret from last year")
	assert.Equal(t, yearAgo, ev.Timestamp, "the imported event keeps the time the post was written")
	assert.Equal(t, h.GhostUserID(carol.Id), ev.Sender.String(), "sent by the author's account")
	assert.False(t, ec.ServerCopyContains(roomID, "A secret from last year"), "no plaintext on the homeserver")
	assert.Equal(t, 0, CountMatrixMessages(t, alice, room, "A secret from last year"))
	capabilities.Verify(t, "history.import.encrypted", !ec.ServerCopyContains(roomID, "A secret from last year"))
}
