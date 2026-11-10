package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Custom emoji cross as images. A reaction with a Mattermost custom emoji is
// sent as an image reaction (MSC4027: the key is the uploaded image, the
// shortcode names it), and an image reaction from Matrix becomes a custom emoji
// with that shortcode, made once and reused.

const (
	customEmojiMXCKeyPrefix = "emoji_mxc_" // + emoji ID -> mxc URI
	maxEmojiBytes           = 512 << 10
	emojiBotUsername        = "matrix-bridge-emoji"
	emojiTokenKey           = "emoji_token"
)

var emojiShortcodeName = regexp.MustCompile(`[^a-z0-9_-]+`)

// customEmojiName returns the emoji's name when it is a custom emoji (not one
// of the standard ones), else "".
func (b *BridgeUtils) customEmoji(name string) *model.Emoji {
	clean := strings.Trim(name, ":")
	if _, standard := emojiNameToIndex[clean]; standard {
		return nil
	}
	emoji, appErr := b.API.GetEmojiByName(clean)
	if appErr != nil || emoji == nil {
		return nil
	}
	return emoji
}

// emojiMXC uploads a custom emoji's image to Matrix once and returns its mxc URI.
func (b *BridgeUtils) emojiMXC(emoji *model.Emoji) (string, error) {
	key := customEmojiMXCKeyPrefix + emoji.Id
	if data, err := b.kvstore.Get(key); err == nil && len(data) > 0 {
		return string(data), nil
	}
	image, _, appErr := b.API.GetEmojiImage(emoji.Id)
	if appErr != nil {
		return "", appErr
	}
	mxc, err := b.matrixClient.UploadMedia(image, emoji.Name+".png", "image/png")
	if err != nil {
		return "", err
	}
	_ = b.kvstore.Set(key, []byte(mxc))
	return mxc, nil
}

// reactionKeyFor returns the Matrix reaction key for an emoji name and, for a
// custom emoji, its shortcode (empty otherwise).
func (b *MattermostToMatrixBridge) reactionKeyFor(emojiName string) (key, shortcode string) {
	if emoji := b.customEmoji(emojiName); emoji != nil {
		mxc, err := b.emojiMXC(emoji)
		if err == nil {
			return mxc, ":" + emoji.Name + ":"
		}
		b.logger.LogDebug("Could not send a custom emoji as an image; sending its name", "emoji", emoji.Name, "error", err)
	}
	return b.convertEmojiForMatrix(emojiName), ""
}

// emojiForImageReaction finds or creates the custom emoji for an image
// reaction and returns its name.
func (b *MatrixToMattermostBridge) emojiForImageReaction(mxc, shortcode string) (string, bool) {
	name := emojiShortcodeName.ReplaceAllString(strings.ToLower(strings.Trim(shortcode, ": ")), "_")
	name = strings.Trim(name, "_-")
	if name == "" {
		name = "matrix_emoji"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	if existing, appErr := b.API.GetEmojiByName(name); appErr == nil && existing != nil {
		return existing.Name, true
	}
	image, err := b.matrixClient.DownloadFile(mxc, maxEmojiBytes, "image/")
	if err != nil {
		b.logger.LogDebug("Could not fetch the image of a reaction", "error", err, "mxc", mxc)
		return "", false
	}
	if err := b.createEmoji(name, image); err != nil {
		b.logger.LogDebug("Could not create a custom emoji from a reaction", "error", err, "name", name)
		return "", false
	}
	return name, true
}

// createEmoji makes a custom emoji through Mattermost's own API as a small bot
// account, since the plugin API cannot create emoji.
func (b *BridgeUtils) createEmoji(name string, image []byte) error {
	if b.emojiCreator == nil {
		return fmt.Errorf("custom emoji cannot be created here")
	}
	return b.emojiCreator(context.Background(), name, image)
}

// createEmojiAsBot is the EmojiCreator: it signs in to Mattermost's REST API
// as a small bot account.
func (p *Plugin) createEmojiAsBot(ctx context.Context, name string, image []byte) error {
	botID, token, err := p.serviceBotToken(emojiBotUsername, "Matrix bridge emoji", "Creates custom emoji for image reactions from Matrix", emojiTokenKey)
	if err != nil {
		return err
	}
	client := model.NewAPIv4Client(strings.Replace(p.websocketURL(), "ws://", "http://", 1))
	client.SetToken(token)
	ext := ".png"
	switch http.DetectContentType(image) {
	case "image/gif":
		ext = ".gif"
	case "image/jpeg":
		ext = ".jpg"
	}
	if _, _, err := client.CreateEmoji(ctx, &model.Emoji{CreatorId: botID, Name: name}, image, name+ext); err != nil {
		return err
	}
	return nil
}
