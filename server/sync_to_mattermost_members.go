package main

import (
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/pkg/errors"
)

// syncMatrixMemberEventToMattermost handles Matrix member events (joins, leaves, profile changes)
func (b *MatrixToMattermostBridge) syncMatrixMemberEventToMattermost(event MatrixEvent, channelID string) error {
	b.logger.LogDebug("Processing Matrix member event", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)

	// Skip events from accounts the bridge drives to prevent loops
	if b.isGhostUser(event.Sender) || b.ownsAccount(event.Sender) {
		b.logger.LogDebug("Ignoring member event from a bridge account", "sender", event.Sender, "event_id", event.EventID)
		return nil
	}

	// Extract membership state and content
	membership, ok := event.Content["membership"].(string)
	if !ok {
		b.logger.LogDebug("Member event missing membership field", "event_id", event.EventID, "sender", event.Sender)
		return nil
	}

	// Check if we have a Mattermost user for this Matrix user
	userMapKey := kvstore.BuildMatrixUserKey(event.Sender)
	userIDBytes, err := b.kvstore.Get(userMapKey)
	existingUserID := ""
	userExists := false
	if err == nil && len(userIDBytes) > 0 {
		existingUserID = string(userIDBytes)
		userExists = true
	}

	switch membership {
	case "join":
		return b.handleMatrixMemberJoin(event, channelID, existingUserID, userExists)
	case "leave", "ban":
		return b.handleMatrixMemberLeave(event, channelID, existingUserID, userExists)
	default:
		b.logger.LogDebug("Ignoring unsupported membership state", "event_id", event.EventID, "sender", event.Sender, "membership", membership)
		return nil
	}
}

// handleMatrixMemberJoin processes Matrix member join events - both new joins and profile changes
func (b *MatrixToMattermostBridge) handleMatrixMemberJoin(event MatrixEvent, channelID, existingUserID string, userExists bool) error {
	// Check if this is a profile change (has displayname or avatar_url in content)
	displayName, hasDisplayName := event.Content["displayname"].(string)
	avatarURL, hasAvatarURL := event.Content["avatar_url"].(string)
	hasProfileData := hasDisplayName || hasAvatarURL

	if userExists {
		// Existing user joining - always ensure they're in the channel first
		b.logger.LogDebug("Ensuring existing Matrix user is in Mattermost channel", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)
		if err := b.ensureUserInChannel(existingUserID, channelID); err != nil {
			b.logger.LogWarn("Failed to ensure existing user is in channel", "error", err, "user_id", existingUserID, "channel_id", channelID)
		}

		// Also handle profile change if profile data is present
		if hasProfileData {
			b.logger.LogDebug("Detected Matrix profile change for existing user", "event_id", event.EventID, "sender", event.Sender, "display_name", displayName, "avatar_url", avatarURL)
			return b.updateExistingUserProfile(existingUserID, event.Sender, event.EventID, displayName, avatarURL)
		}

		return nil
	}

	// New user joining - create them and add to channel
	b.logger.LogDebug("New Matrix user joining room", "event_id", event.EventID, "sender", event.Sender, "channel_id", channelID)
	mattermostUserID, err := b.getOrCreateMattermostUser(event.Sender, channelID)
	if err != nil {
		return errors.Wrap(err, "failed to create Mattermost user for Matrix join")
	}

	// Add the new user to the channel
	return b.addUserToChannel(mattermostUserID, channelID)
}

// handleMatrixMemberLeave processes Matrix member leave/ban events
func (b *MatrixToMattermostBridge) handleMatrixMemberLeave(event MatrixEvent, channelID, existingUserID string, userExists bool) error {
	if !userExists {
		b.logger.LogDebug("Matrix user leaving room but no Mattermost user exists", "event_id", event.EventID, "sender", event.Sender)
		return nil
	}

	membership := event.Content["membership"].(string)
	b.logger.LogDebug("Matrix user leaving room", "event_id", event.EventID, "sender", event.Sender, "membership", membership, "channel_id", channelID)

	// Remove user from the Mattermost channel
	return b.removeUserFromChannel(existingUserID, channelID)
}

// ensureUserInChannel ensures a user is a member of the specified channel
func (b *MatrixToMattermostBridge) ensureUserInChannel(userID, channelID string) error {
	// Check if user is already a channel member
	_, appErr := b.API.GetChannelMember(channelID, userID)
	if appErr == nil {
		// User is already a channel member
		b.logger.LogDebug("User already member of channel", "user_id", userID, "channel_id", channelID)
		return nil
	}

	// Add user to the channel
	return b.addUserToChannel(userID, channelID)
}

// addUserToChannel adds a user to a Mattermost channel
func (b *MatrixToMattermostBridge) addUserToChannel(userID, channelID string) error {
	// Ensure user is in the team first
	if err := b.addUserToChannelTeam(userID, channelID); err != nil {
		return errors.Wrap(err, "failed to add user to team")
	}

	// Add user to the channel
	_, appErr := b.API.AddChannelMember(channelID, userID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to add user to channel")
	}

	b.logger.LogDebug("Added Matrix user to Mattermost channel", "user_id", userID, "channel_id", channelID)
	return nil
}

// removeUserFromChannel removes a user from a Mattermost channel
func (b *MatrixToMattermostBridge) removeUserFromChannel(userID, channelID string) error {
	// Remove user from the channel
	appErr := b.API.DeleteChannelMember(channelID, userID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to remove user from channel")
	}

	b.logger.LogDebug("Removed Matrix user from Mattermost channel", "user_id", userID, "channel_id", channelID)
	return nil
}
