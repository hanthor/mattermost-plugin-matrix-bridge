package main

import (
	"github.com/mattermost/mattermost/server/public/model"
)

// Moderation crosses in both directions. Removing someone from a channel
// removes their account from the room (as a kick, with the reason), and a
// Matrix moderator kicking or banning an account removes that person from the
// channel. Who may do it is decided by each side's own rules: channel
// permissions in Mattermost and power levels in Matrix.

// kickIfRemoved makes the room see a kick when another person removed someone
// from the channel.
func (p *Plugin) kickIfRemoved(channelMember *model.ChannelMember, actor *model.User) {
	defer func() { _ = recover() }()
	if actor == nil || actor.Id == channelMember.UserId || p.matrixClient == nil || p.mattermostToMatrixBridge == nil {
		return
	}
	roomID := p.roomIDForChannel(channelMember.ChannelId)
	if roomID == "" {
		return
	}
	account, ok := lookupBridgeAccount(p.kvstore, channelMember.UserId)
	if !ok {
		return
	}
	reason := "Removed from the channel by " + actor.Username
	if err := p.matrixClient.KickUser(roomID, account, reason); err != nil {
		p.logger.LogDebug("Could not kick an account from the room", "error", err, "room_id", roomID)
	}
}

// handleMatrixRemoval handles a member event where someone other than the
// subject removed them (a kick, a ban or an unban). It returns true when the
// event was such a removal.
func (p *Plugin) handleMatrixRemoval(event MatrixEvent, channelID string) bool {
	if event.Type != "m.room.member" || event.StateKey == nil || *event.StateKey == event.Sender {
		return false
	}
	membership, _ := event.Content["membership"].(string)
	if membership != "leave" && membership != "ban" {
		return false
	}
	// The bridge's own removals come back as events too.
	if event.Sender == p.BridgeBotUserID() || p.isBridgeAccount(event.Sender) {
		return true
	}
	target := *event.StateKey
	userID := p.mattermostUserForBridgeAccount(target)
	if userID == "" {
		userID = p.lookupMattermostUserForMatrixUser(target)
	}
	if userID == "" {
		return true
	}
	if _, appErr := p.API.GetChannelMember(channelID, userID); appErr != nil {
		return true // not in the channel
	}
	if appErr := p.API.DeleteChannelMember(channelID, userID); appErr != nil {
		p.logger.LogWarn("Could not remove someone from a channel after a Matrix kick", "error", appErr, "user_id", userID, "channel_id", channelID)
		return true
	}
	p.logger.LogInfo("Someone was removed from a channel by a Matrix moderator", "user_id", userID, "channel_id", channelID, "moderator", event.Sender, "membership", membership)
	return true
}
