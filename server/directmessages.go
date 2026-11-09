package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// Direct and group messages are private, so they are only bridged when an
// administrator allows it (the "Bridge Direct Messages" setting) and every
// person in the conversation has said yes with /matrix dm on. Leaving
// (/matrix dm off) unbridges the person's conversations from then on.

const (
	dmConsentKeyPrefix = "dm_consent_" // + Mattermost user ID -> "1"
	dmBridgedKeyPrefix = "dm_bridged_" // + channel ID -> "1", set when the bridge made its room
	dmHelp             = "`/matrix dm on` | `/matrix dm off` | `/matrix dm status`"
	dmRoomMembersLimit = 100
)

func (p *Plugin) dmConsent(userID string) bool {
	data, err := p.kvstore.Get(dmConsentKeyPrefix + userID)
	return err == nil && len(data) > 0
}

// directMessagesAllowed reports whether a conversation between these people
// may be bridged: the setting is on and every local participant agreed.
func (b *BridgeUtils) directMessagesAllowed(userIDs []string) bool {
	if len(userIDs) < 2 {
		return false
	}
	var local []string
	for _, id := range userIDs {
		user, appErr := b.API.GetUser(id)
		if appErr != nil {
			return false
		}
		if user.IsRemote() {
			// A conversation with a Matrix user was started from Matrix, so it
			// is bridged already; it needs no extra agreement here.
			return true
		}
		local = append(local, id)
	}
	if !b.configGetter.getConfiguration().EnableDirectMessages {
		return false
	}
	for _, id := range local {
		data, err := b.kvstore.Get(dmConsentKeyPrefix + id)
		if err != nil || len(data) == 0 {
			return false
		}
	}
	return true
}

// directChannelParticipants returns the people in a direct or group channel,
// or nil for any other channel.
func (p *Plugin) directChannelParticipants(channelID string) []string {
	if p.mattermostToMatrixBridge == nil {
		return nil
	}
	isDirect, userIDs, err := p.mattermostToMatrixBridge.isDirectChannel(channelID)
	if err != nil || !isDirect {
		return nil
	}
	return userIDs
}

// shouldBridgeUnmappedPost decides, for a post in a channel without a room,
// whether it is a direct conversation that may be bridged now.
func (p *Plugin) shouldBridgeUnmappedPost(channelID string) bool {
	if !p.getConfiguration().EnableDirectMessages {
		return false
	}
	userIDs := p.directChannelParticipants(channelID)
	return userIDs != nil && p.mattermostToMatrixBridge.directMessagesAllowed(userIDs)
}

// DirectMessages implements command.PluginAccessor for /matrix dm.
func (p *Plugin) DirectMessages(userID, action string) (string, error) {
	enabled := p.getConfiguration().EnableDirectMessages
	consented := p.dmConsent(userID)

	switch action {
	case "", "status":
		var sb strings.Builder
		if !enabled {
			sb.WriteString("Direct and group messages are **not bridged**: an administrator has not turned on *Bridge Direct Messages*.")
			return sb.String(), nil
		}
		if consented {
			sb.WriteString("✅ You agreed: your direct and group messages are bridged to Matrix, but only in conversations where **everyone** has agreed.")
		} else {
			sb.WriteString("Your direct and group messages are **not bridged**. Run `/matrix dm on` to agree. A conversation is bridged only when everyone in it has agreed.")
		}
		if n := len(p.bridgedDirectChannels(userID)); n > 0 {
			sb.WriteString(fmt.Sprintf("\n\n%d of your conversations currently have a Matrix room.", n))
		}
		return sb.String(), nil

	case "on":
		if !enabled {
			return "", fmt.Errorf("an administrator has not turned on *Bridge Direct Messages*")
		}
		if err := p.kvstore.Set(dmConsentKeyPrefix+userID, []byte("1")); err != nil {
			return "", err
		}
		return "✅ **Direct messages can be bridged.**\n\nFrom now on, in a direct or group conversation where everyone has run `/matrix dm on`, new messages are copied to a private Matrix room that only the people in the conversation can join. Matrix server administrators can read what is in that room. Earlier messages are not copied. Turn it off any time with `/matrix dm off`.", nil

	case "off":
		if err := p.kvstore.Delete(dmConsentKeyPrefix + userID); err != nil {
			return "", err
		}
		n := p.unbridgeDirectChannels(userID)
		msg := "✅ **Your direct messages are no longer bridged.**"
		if n > 0 {
			msg += fmt.Sprintf(" %d conversations stopped syncing in both directions; their Matrix rooms stay as they are.", n)
		}
		return msg, nil
	}
	return "", fmt.Errorf("usage: %s", dmHelp)
}

// bridgedDirectChannels lists the direct channels the bridge made rooms for
// that this person is in.
func (p *Plugin) bridgedDirectChannels(userID string) []string {
	var out []string
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, dmBridgedKeyPrefix)
		if err != nil {
			return out
		}
		for _, key := range keys {
			channelID := strings.TrimPrefix(key, dmBridgedKeyPrefix)
			members, appErr := p.API.GetChannelMembers(channelID, 0, dmRoomMembersLimit)
			if appErr != nil {
				continue
			}
			for _, m := range members {
				if m.UserId == userID {
					out = append(out, channelID)
					break
				}
			}
		}
		if len(keys) < 200 {
			return out
		}
	}
}

// unbridgeDirectChannels removes the room mapping of every bridged
// conversation the person is in.
func (p *Plugin) unbridgeDirectChannels(userID string) int {
	n := 0
	for _, channelID := range p.bridgedDirectChannels(userID) {
		roomBytes, err := p.kvstore.Get(kvstore.BuildChannelMappingKey(channelID))
		if err == nil && len(roomBytes) > 0 {
			_ = p.kvstore.Delete(kvstore.BuildRoomMappingKey(string(roomBytes)))
		}
		_ = p.kvstore.Delete(kvstore.BuildChannelMappingKey(channelID))
		_ = p.kvstore.Delete(dmBridgedKeyPrefix + channelID)
		p.InvalidateChannelMapping(channelID)
		n++
	}
	return n
}

// markDirectChannelBridged records that the bridge made a room for a channel.
func (b *BridgeUtils) markDirectChannelBridged(channelID string) {
	_ = b.kvstore.Set(dmBridgedKeyPrefix+channelID, []byte("1"))
}
