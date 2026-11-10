package main

// handleInviteOfBridgeAccount handles a Matrix person inviting the account that
// represents a Mattermost person to a bridged room. The Mattermost person is
// added to the channel, so "add Alice" works from Matrix too. It returns true
// when the event was this kind of invite.
func (p *Plugin) handleInviteOfBridgeAccount(event MatrixEvent, channelID string) bool {
	if event.Type != "m.room.member" || event.StateKey == nil {
		return false
	}
	if membership, _ := event.Content["membership"].(string); membership != "invite" {
		return false
	}
	// Only a real Matrix person's invitation counts, never the bridge's own.
	if event.Sender == p.BridgeBotUserID() || p.isBridgeAccount(event.Sender) {
		return false
	}
	userID := p.mattermostUserForBridgeAccount(*event.StateKey)
	if userID == "" {
		return false
	}
	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil || user.IsBot || user.DeleteAt != 0 || user.IsRemote() {
		return true
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.IsGroupOrDirect() || channel.DeleteAt != 0 {
		return true
	}
	if _, appErr := p.API.GetChannelMember(channelID, userID); appErr == nil {
		return true // already in the channel
	}
	// A person must belong to the team to join one of its channels.
	if channel.TeamId != "" {
		if _, appErr := p.API.GetTeamMember(channel.TeamId, userID); appErr != nil {
			p.logger.LogInfo("A Matrix invitation names someone who is not in the channel's team; not added", "user_id", userID, "channel_id", channelID)
			return true
		}
	}
	if _, appErr := p.API.AddChannelMember(channelID, userID); appErr != nil {
		p.logger.LogWarn("Could not add a Mattermost person invited from Matrix", "error", appErr, "user_id", userID, "channel_id", channelID)
		return true
	}
	p.logger.LogInfo("A Mattermost person was added to a channel by an invitation from Matrix", "user_id", userID, "channel_id", channelID, "inviter", event.Sender)
	return true
}
