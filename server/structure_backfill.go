package main

import (
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// BackfillStructure brings the structure of every bridged channel's room up
// to date in one pass: the bridge information and widget, the room's name,
// topic and archived state, the power levels of channel admins, and the pinned
// posts. Each of these is otherwise kept current by a change in Mattermost, so
// a room that was bridged before they existed, or that was changed on the
// Matrix side, stays behind until this runs. It is safe to repeat and returns
// how many channels it went through.
func (p *Plugin) BackfillStructure() int {
	if p.kvstore == nil || p.matrixClient == nil {
		return 0
	}
	var ids []string
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, kvstore.KeyPrefixChannelMapping)
		if err != nil {
			break
		}
		for _, k := range keys {
			ids = append(ids, strings.TrimPrefix(k, kvstore.KeyPrefixChannelMapping))
		}
		if len(keys) < 200 {
			break
		}
	}
	sort.Strings(ids)
	for _, channelID := range ids {
		p.backfillChannelStructure(channelID)
	}
	return len(ids)
}

func (p *Plugin) backfillChannelStructure(channelID string) {
	// The room's name and topic are normally sent when they change in
	// Mattermost. Forgetting what was last sent makes the next pass send them
	// again, which restores a room that drifted.
	_ = p.kvstore.Delete(channelStateKeyPrefix + channelID)
	for _, step := range []func(string){p.syncBridgeInfo, p.syncChannelStateByID, p.syncChannelRoles, p.syncPinsToMatrix} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					p.logger.LogWarn("A structure backfill step failed", "channel_id", channelID, "panic", r)
				}
			}()
			step(channelID)
		}()
	}
}
