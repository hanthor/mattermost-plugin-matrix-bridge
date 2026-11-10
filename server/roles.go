package main

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Channel admins are moderators of the room: power level 50. The other way
// round, a Matrix person at 50 or more becomes a channel admin. Each side only
// changes the people the other side's account stands for, so the two do not
// chase each other.

const (
	channelAdminRoles = "channel_user channel_admin"
	channelUserRoles  = "channel_user"
	rolesBatch        = 20
	rolesCursorKey    = "roles_cursor"
)

// syncChannelRoles raises the room power level of channel admins to 50 and
// returns it to the default for people who are no longer admins.
func (p *Plugin) syncChannelRoles(channelID string) {
	defer func() { _ = recover() }()
	if p.matrixClient == nil || p.mattermostToMatrixBridge == nil || p.kvstore == nil {
		return
	}
	roomID := p.roomIDForChannel(channelID)
	if roomID == "" {
		return
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.DeleteAt != 0 || channel.IsGroupOrDirect() {
		return
	}
	levels, err := p.matrixClient.GetPowerLevels(roomID)
	if err != nil {
		return
	}
	users, _ := levels["users"].(map[string]any)
	if users == nil {
		users = map[string]any{}
	}
	defaultLevel := 0
	if d, ok := levels["users_default"].(float64); ok {
		defaultLevel = int(d)
	}
	changed := false
	for page := 0; ; page++ {
		members, appErr := p.API.GetChannelMembers(channelID, page, 200)
		if appErr != nil {
			return
		}
		for _, m := range members {
			matrixID, ok := lookupBridgeAccount(p.kvstore, m.UserId)
			if !ok {
				continue
			}
			level := defaultLevel
			if v, ok := users[matrixID].(float64); ok {
				level = int(v)
			}
			switch {
			case m.SchemeAdmin && level < moderatorPower:
				users[matrixID] = moderatorPower
				changed = true
			case !m.SchemeAdmin && level == moderatorPower:
				delete(users, matrixID)
				changed = true
			}
		}
		if len(members) < 200 {
			break
		}
	}
	if !changed {
		return
	}
	levels["users"] = users
	if _, err := p.matrixClient.SetRoomState(roomID, "m.room.power_levels", "", levels, p.BridgeBotUserID()); err != nil {
		p.logger.LogDebug("Could not update the room's power levels; the bridge bot may not be allowed to", "error", err, "room_id", roomID)
	}
}

// syncSomeChannelRoles checks a slice of the mapped channels, continuing where
// the last call stopped.
func (p *Plugin) syncSomeChannelRoles() {
	if p.kvstore == nil || !p.getConfiguration().EnableSync {
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
	cursor, _ := p.kvstore.Get(rolesCursorKey)
	start := sort.SearchStrings(ids, string(cursor))
	if start >= len(ids) {
		start = 0
	}
	end := start + rolesBatch
	if end > len(ids) {
		end = len(ids)
	}
	for _, id := range ids[start:end] {
		p.syncChannelRoles(id)
	}
	next := ""
	if end < len(ids) {
		next = ids[end]
	}
	_ = p.kvstore.Set(rolesCursorKey, []byte(next))
}

// syncMatrixPowerLevels applies a power level change from Matrix: people at
// moderator level or above become channel admins, the rest ordinary members.
func (p *Plugin) syncMatrixPowerLevels(event MatrixEvent, channelID string) error {
	users, _ := event.Content["users"].(map[string]any)
	defaultLevel := 0
	if d, ok := event.Content["users_default"].(float64); ok {
		defaultLevel = int(d)
	}
	for matrixID := range p.matrixPeopleInChannel(channelID, users) {
		level := defaultLevel
		if v, ok := users[matrixID].(float64); ok {
			level = int(v)
		}
		userID := p.lookupMattermostUserForMatrixUser(matrixID)
		if userID == "" || p.isBridgeAccount(matrixID) {
			continue
		}
		member, appErr := p.API.GetChannelMember(channelID, userID)
		if appErr != nil || member == nil {
			continue
		}
		wantAdmin := level >= moderatorPower
		if member.SchemeAdmin == wantAdmin {
			continue
		}
		roles := channelUserRoles
		if wantAdmin {
			roles = channelAdminRoles
		}
		if _, appErr := p.API.UpdateChannelMemberRoles(channelID, userID, roles); appErr != nil {
			p.logger.LogDebug("Could not change a channel role from a Matrix power level", "error", appErr, "user_id", userID)
		}
	}
	return nil
}

// matrixPeopleInChannel returns the Matrix IDs worth looking at: everyone the
// power levels name, plus the Matrix people in the channel (who may have been
// at the default level before and are now at a listed one).
func (p *Plugin) matrixPeopleInChannel(channelID string, listed map[string]any) map[string]bool {
	out := map[string]bool{}
	for id := range listed {
		out[id] = true
	}
	for page := 0; ; page++ {
		members, appErr := p.API.GetChannelMembers(channelID, page, 200)
		if appErr != nil {
			break
		}
		for _, m := range members {
			if data, err := p.kvstore.Get(kvstore.BuildMattermostUserKey(m.UserId)); err == nil && len(data) > 0 {
				id := string(data)
				if !p.isBridgeAccount(id) {
					out[id] = true
				}
			}
		}
		if len(members) < 200 {
			break
		}
	}
	return out
}
