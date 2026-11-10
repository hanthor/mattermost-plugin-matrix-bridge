package main

import (
	"sort"
)

// Mattermost's custom emoji are offered to Matrix clients as an emoji pack
// (MSC2545, im.ponies.room_emotes) in each bridged room, so people on the
// Matrix side can pick them, not only see them in reactions.

const (
	emojiPackEventType = "im.ponies.room_emotes"
	emojiPackStateKey  = "com.mattermost.emoji"
	emojiPackLimit     = 200
)

func (p *Plugin) emojiPackContent() map[string]any {
	if p.mattermostToMatrixBridge == nil {
		return nil
	}
	emojis, appErr := p.API.GetEmojiList("name", 0, emojiPackLimit)
	if appErr != nil || len(emojis) == 0 {
		return nil
	}
	sort.Slice(emojis, func(i, j int) bool { return emojis[i].Name < emojis[j].Name })
	images := map[string]any{}
	for _, e := range emojis {
		if e == nil || e.DeleteAt != 0 {
			continue
		}
		mxc, err := p.mattermostToMatrixBridge.emojiMXC(e)
		if err != nil {
			p.logger.LogDebug("Could not upload a custom emoji for the room's emoji pack", "emoji", e.Name, "error", err)
			continue
		}
		images[e.Name] = map[string]any{"url": mxc}
	}
	if len(images) == 0 {
		return nil
	}
	return map[string]any{
		"pack":   map[string]any{"display_name": "Mattermost emoji"},
		"images": images,
	}
}

// syncEmojiPack publishes the custom emoji as the room's emoji pack.
func (p *Plugin) syncEmojiPack(channelID string) {
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return
	}
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil && ch != nil && ch.IsGroupOrDirect() {
		return
	}
	content := p.emojiPackContent()
	if content == nil {
		return
	}
	if err := p.setBridgeState(roomID, emojiPackEventType, emojiPackStateKey, content); err != nil {
		p.logger.LogDebug("Could not set the emoji pack in the room", "error", err, "room_id", roomID)
	}
}
