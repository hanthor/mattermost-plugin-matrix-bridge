package e2e

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestImageSizeIsSentWithTheFile checks that Matrix clients are told an
// image's dimensions.
func TestImageSizeIsSentWithTheFile(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	alice := h.NewMatrixUser("alice")
	channel := h.CreateChannel("media", "Media")
	roomID := h.CreateMappedRoom(alice, channel.Id, "Media")

	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for x := 0; x < 64; x++ {
		for y := 0; y < 48; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 5), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	upload, _, err := h.Admin.UploadFile(ctx, buf.Bytes(), channel.Id, "picture.png")
	require.NoError(t, err)
	_, _, err = h.Admin.CreatePost(ctx, &model.Post{ChannelId: channel.Id, Message: "a picture", FileIds: []string{upload.FileInfos[0].Id}})
	require.NoError(t, err)

	ev := WaitForMatrixEvent(t, alice, roomID, func(ev gjson.Result) bool {
		return ev.Get("type").Str == "m.room.message" && ev.Get("content.msgtype").Str == "m.image"
	})
	assert.Equal(t, int64(64), ev.Get("content.info.w").Int())
	assert.Equal(t, int64(48), ev.Get("content.info.h").Int())
	capabilities.Verify(t, "files.dimensions", ev.Get("content.info.w").Int() == 64 && ev.Get("content.info.h").Int() == 48)
}
