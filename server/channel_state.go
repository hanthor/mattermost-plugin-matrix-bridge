package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// A channel's name, header and archived state follow it into the room: the
// name becomes the room name, the header the topic, and archiving is announced.
// Mattermost has no plugin hook for these changes, so a sweep finds them; the
// last state agreed on is kept per channel, and the Matrix side updates it
// when it changes the channel, so a change does not bounce back.

const (
	channelStateKeyPrefix = "chanstate_"
	channelStateEach      = 200
	channelStateCursorKey = "chanstate_cursor"
)

type channelState struct {
	Name     string `json:"name"`
	Header   string `json:"header"`
	Archived bool   `json:"archived"`
}

func stateOf(c *model.Channel) channelState {
	return channelState{Name: c.DisplayName, Header: c.Header, Archived: c.DeleteAt != 0}
}

func (p *Plugin) loadChannelState(channelID string) (channelState, bool) {
	var s channelState
	data, err := p.kvstore.Get(channelStateKeyPrefix + channelID)
	if err != nil || len(data) == 0 || json.Unmarshal(data, &s) != nil {
		return s, false
	}
	return s, true
}

func (p *Plugin) saveChannelState(channelID string, s channelState) {
	if data, err := json.Marshal(s); err == nil {
		_ = p.kvstore.Set(channelStateKeyPrefix+channelID, data)
	}
}

// syncChannelState sends what changed since the last agreed state to the room.
func (p *Plugin) syncChannelState(channel *model.Channel) { p.syncChannelStateFrom(channel, false) }

// syncChannelStateFrom is syncChannelState; with changed set the caller knows
// something changed (a system post said so), so a channel without a recorded
// state sends everything instead of only recording a baseline.
func (p *Plugin) syncChannelStateFrom(channel *model.Channel, changed bool) {
	if channel.IsGroupOrDirect() {
		return
	}
	now := stateOf(channel)
	last, known := p.loadChannelState(channel.Id)
	if !known && !changed {
		p.saveChannelState(channel.Id, now) // the baseline: nothing to send yet
		return
	}
	if now == last {
		return
	}
	roomID := p.roomIDForChannel(channel.Id)
	if roomID == "" {
		return
	}
	bot := p.BridgeBotUserID()
	ok := true
	if now.Name != last.Name && now.Name != "" {
		if _, err := p.matrixClient.SetRoomState(roomID, "m.room.name", "", map[string]any{"name": now.Name}, bot); err != nil {
			p.logger.LogDebug("Could not rename the room", "error", err, "room_id", roomID)
			ok = false
		}
	}
	if now.Header != last.Header {
		if _, err := p.matrixClient.SetRoomState(roomID, "m.room.topic", "", map[string]any{"topic": now.Header}, bot); err != nil {
			p.logger.LogDebug("Could not change the room topic", "error", err, "room_id", roomID)
			ok = false
		}
	}
	if now.Archived != last.Archived {
		if now.Archived {
			_ = p.matrixClient.SendNotice(roomID, "📁 This channel was archived in Mattermost; messages sent here will no longer be bridged.")
			// The bridge leaves, as it does when a room is unmapped.
			if err := p.matrixClient.LeaveRoomAsUser(roomID, bot); err != nil {
				p.logger.LogDebug("The bridge bot could not leave the room of an archived channel", "error", err, "room_id", roomID)
			}
		} else {
			if err := p.matrixClient.JoinRoom(roomID); err != nil {
				p.logger.LogDebug("The bridge bot could not rejoin the room of a restored channel", "error", err, "room_id", roomID)
			}
			_ = p.matrixClient.SendNotice(roomID, "📂 This channel was restored in Mattermost; bridging continues.")
		}
	}
	if ok {
		p.saveChannelState(channel.Id, now)
	}
}

// sweepChannelStates checks a slice of the mapped channels per run.
func (p *Plugin) sweepChannelStates() {
	if p.kvstore == nil || p.matrixClient == nil || p.mattermostToMatrixBridge == nil || !p.getConfiguration().EnableSync {
		return
	}
	var ids []string
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			return
		}
		for _, k := range keys {
			ids = append(ids, strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping))
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Strings(ids)
	cursor, _ := p.kvstore.Get(channelStateCursorKey)
	start := sort.SearchStrings(ids, string(cursor))
	if start >= len(ids) {
		start = 0
	}
	end := start + channelStateEach
	if end > len(ids) {
		end = len(ids)
	}
	for _, id := range ids[start:end] {
		if channel, appErr := p.API.GetChannel(id); appErr == nil && channel != nil {
			p.syncChannelState(channel)
		}
	}
	next := ""
	if end < len(ids) {
		next = ids[end]
	}
	_ = p.kvstore.Set(channelStateCursorKey, []byte(next))
}

// noteChannelStateFromMatrix records a change that came from Matrix as agreed.
func (p *Plugin) noteChannelStateFromMatrix(channel *model.Channel) {
	p.saveChannelState(channel.Id, stateOf(channel))
}

// archiveChannelFromMatrix archives the channel of a room that was closed
// with a tombstone.
func (p *Plugin) archiveChannelFromMatrix(channelID string) error {
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.DeleteAt != 0 || channel.IsGroupOrDirect() {
		return nil
	}
	if appErr := p.API.DeleteChannel(channelID); appErr != nil {
		return appErr
	}
	channel.DeleteAt = 1
	p.saveChannelState(channelID, stateOf(channel))
	return nil
}

// baselineChannelState records a channel's state when it is mapped, so a later
// change is seen as a change.
func (p *Plugin) baselineChannelState(channelID string) {
	defer func() { _ = recover() }()
	if p.kvstore == nil {
		return
	}
	if _, known := p.loadChannelState(channelID); known {
		return
	}
	if channel, appErr := p.API.GetChannel(channelID); appErr == nil && channel != nil && !channel.IsGroupOrDirect() {
		p.saveChannelState(channelID, stateOf(channel))
	}
}

// isChannelStatePost reports whether a system post announces a change of the
// channel's name, header, purpose or archived state.
func isChannelStatePost(postType string) bool {
	switch postType {
	case model.PostTypeDisplaynameChange, model.PostTypeHeaderChange, model.PostTypePurposeChange,
		model.PostTypeChannelDeleted, model.PostTypeChannelRestored:
		return true
	}
	return false
}

// syncChannelStateByID is syncChannelState for a channel ID, safe to run in the
// background.
func (p *Plugin) syncChannelStateByID(channelID string) {
	defer func() { _ = recover() }()
	if p.matrixClient == nil || p.kvstore == nil {
		return
	}
	if channel, appErr := p.API.GetChannel(channelID); appErr == nil && channel != nil {
		p.syncChannelStateFrom(channel, true)
	}
}

// ChannelWillBeUpdated is called before a channel's name, header or purpose
// changes; the room is brought along without waiting for the sweep.
func (p *Plugin) ChannelWillBeUpdated(_ *plugin.Context, newChannel, _ *model.Channel) (*model.Channel, string) {
	if newChannel != nil && p.getConfiguration().EnableSync {
		next := *newChannel
		go p.syncChannelStateNow(&next)
	}
	return newChannel, ""
}

// ChannelWillBeArchived is called before a channel is archived.
func (p *Plugin) ChannelWillBeArchived(_ *plugin.Context, channel *model.Channel) string {
	if channel != nil && p.getConfiguration().EnableSync {
		next := *channel
		next.DeleteAt = model.GetMillis()
		go p.syncChannelStateNow(&next)
	}
	return ""
}

// ChannelWillBeRestored is called before an archived channel is restored.
func (p *Plugin) ChannelWillBeRestored(_ *plugin.Context, channel *model.Channel) string {
	if channel != nil && p.getConfiguration().EnableSync {
		next := *channel
		next.DeleteAt = 0
		go p.syncChannelStateNow(&next)
	}
	return ""
}

func (p *Plugin) syncChannelStateNow(channel *model.Channel) {
	defer func() { _ = recover() }()
	if p.matrixClient == nil || p.kvstore == nil {
		return
	}
	p.syncChannelStateFrom(channel, true)
}
