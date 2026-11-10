package main

import (
	"encoding/json"
	"sort"

	"github.com/mattermost/mattermost/server/public/model"
)

// Pinned messages are kept in step both ways: pinning a post updates the
// room's m.room.pinned_events, and pinning an event in Matrix pins its post.
// The last list agreed on is kept per room, so each side's change is applied
// once and does not bounce back.

const pinnedEventsKeyPrefix = "pins_"

const pinnedEventsType = "m.room.pinned_events"

func (p *Plugin) lastPinnedEvents(roomID string) []string {
	var ids []string
	if data, err := p.kvstore.Get(pinnedEventsKeyPrefix + roomID); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &ids)
	}
	return ids
}

func (p *Plugin) rememberPinnedEvents(roomID string, ids []string) {
	sort.Strings(ids)
	if data, err := json.Marshal(ids); err == nil {
		_ = p.kvstore.Set(pinnedEventsKeyPrefix+roomID, data)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// syncPinsToMatrix sets the room's pinned events to the channel's pinned
// posts that have a Matrix event.
func (p *Plugin) syncPinsToMatrix(channelID string) {
	if p.matrixClient == nil || p.kvstore == nil {
		return
	}
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return
	}
	list, appErr := p.API.GetPostsForChannel(channelID, 0, 200)
	if appErr != nil || list == nil {
		return
	}
	key := p.historyEventKey()
	var pinned []string
	for _, id := range list.Order {
		post := list.Posts[id]
		if post == nil || !post.IsPinned || post.DeleteAt != 0 {
			continue
		}
		if eventID, _ := post.Props[key].(string); eventID != "" {
			pinned = append(pinned, eventID)
		}
	}
	if pinned == nil {
		pinned = []string{}
	}
	if sameStrings(pinned, p.lastPinnedEvents(roomID)) {
		return
	}
	if _, err := p.matrixClient.SetRoomState(roomID, pinnedEventsType, "", map[string]any{"pinned": pinned}, p.BridgeBotUserID()); err != nil {
		p.logger.LogWarn("Could not update the room's pinned messages", "error", err, "room_id", roomID)
		return
	}
	p.rememberPinnedEvents(roomID, pinned)
}

// pinChanged reports whether an update changed only the pin.
func pinChanged(newPost, oldPost *model.Post) bool {
	return oldPost != nil && newPost.IsPinned != oldPost.IsPinned
}

// syncMatrixPins applies the room's pinned events to the channel: events that
// were added are pinned, events that were removed are unpinned.
func (p *Plugin) syncMatrixPins(event MatrixEvent, channelID string) error {
	raw, _ := event.Content["pinned"].([]any)
	now := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			now = append(now, s)
		}
	}
	before := p.lastPinnedEvents(event.RoomID)
	p.rememberPinnedEvents(event.RoomID, append([]string(nil), now...))

	setPinned := func(eventIDs []string, pinned bool) {
		for _, eventID := range eventIDs {
			postID := p.matrixToMattermostBridge.getPostIDFromMatrixEvent(eventID, channelID)
			if postID == "" {
				continue
			}
			post, appErr := p.API.GetPost(postID)
			if appErr != nil || post == nil || post.IsPinned == pinned {
				continue
			}
			post.IsPinned = pinned
			if _, appErr := p.API.UpdatePost(post); appErr != nil {
				p.logger.LogWarn("Could not change a post's pin from Matrix", "error", appErr, "post_id", postID)
			}
		}
	}
	setPinned(difference(now, before), true)
	setPinned(difference(before, now), false)
	return nil
}

// difference returns the elements of a that are not in b.
func difference(a, b []string) []string {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
