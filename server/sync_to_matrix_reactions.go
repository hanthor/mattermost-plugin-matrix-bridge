package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

func (b *MattermostToMatrixBridge) convertEmojiForMatrix(emojiName string) string {
	// Remove colons if present
	cleanName := strings.Trim(emojiName, ":")

	// Get emoji index from name
	index, exists := emojiNameToIndex[cleanName]
	if !exists {
		b.logger.LogDebug("Unknown emoji name", "emoji", cleanName)
		return ":" + cleanName + ":"
	}

	// Get Unicode code point from index
	unicodeHex, exists := emojiIndexToUnicode[index]
	if !exists {
		b.logger.LogDebug("No Unicode mapping for emoji index", "emoji", cleanName, "index", index)
		return ":" + cleanName + ":"
	}

	// Convert hex string to Unicode character
	unicode := hexToUnicode(unicodeHex)
	if unicode == "" {
		b.logger.LogDebug("Failed to convert hex to Unicode", "emoji", cleanName, "hex", unicodeHex)
		return ":" + cleanName + ":"
	}

	return unicode
}

// SyncReactionToMatrix handles syncing a reaction from Mattermost to Matrix
func (b *MattermostToMatrixBridge) SyncReactionToMatrix(reaction *model.Reaction, channelID string) error {
	// Check if this is a reaction deletion
	if reaction.DeleteAt != 0 {
		return b.removeReactionFromMatrix(reaction, channelID)
	}

	// This is a new reaction - add it to Matrix
	return b.addReactionToMatrix(reaction, channelID)
}

// addReactionToMatrix adds a new reaction to Matrix
func (b *MattermostToMatrixBridge) addReactionToMatrix(reaction *model.Reaction, channelID string) error {
	return b.addReactionToMatrixAt(reaction, channelID, 0)
}

// addReactionToMatrixAt is addReactionToMatrix with the reaction's time set to
// timestampMs when it is not zero (history import).
func (b *MattermostToMatrixBridge) addReactionToMatrixAt(reaction *model.Reaction, channelID string, timestampMs int64) error {
	// Get Matrix room identifier
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix room identifier")
	}

	if matrixRoomIdentifier == "" {
		b.logger.LogWarn("No Matrix room mapped for channel", "channel_id", channelID)
		return nil
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Matrix room identifier")
	}

	// Get the post to find the Matrix event ID
	post, appErr := b.API.GetPost(reaction.PostId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get post for reaction")
	}

	// Get Matrix event ID from post properties
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain

	var matrixEventID string
	if post.Props != nil {
		if eventID, ok := post.Props[propertyKey].(string); ok {
			matrixEventID = eventID
		}
	}

	if matrixEventID == "" {
		b.logger.LogWarn("No Matrix event ID found for post", "post_id", reaction.PostId, "property_key", propertyKey)
		return nil // Can't react to a message that wasn't synced to Matrix
	}

	// Get user for ghost user creation
	user, appErr := b.API.GetUser(reaction.UserId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get user for reaction")
	}

	// Skip creating reactions for Matrix-originated users to prevent loops
	if user.IsRemote() {
		b.logger.LogDebug("Skipping reaction sync for remote user", "user_id", user.Id, "username", user.Username)
		return nil
	}

	// Resolve the sending identity (linked account or ghost)
	ghostUserID, sender, _, err := b.resolveSender(user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to create or get ghost user for reaction")
	}

	// Ensure the sender is in the room
	err = b.ensureSenderInRoom(sender, ghostUserID, matrixRoomID, user.Id)
	if err != nil {
		return errors.Wrap(err, "failed to ensure ghost user is in room for reaction")
	}

	// A custom emoji goes as an image, anything else as its Unicode character.
	emoji, shortcode := b.reactionKeyFor(reaction.EmojiName)

	// Send reaction as ghost user
	if shortcode != "" {
		_, err = sender.SendImageReactionAt(matrixRoomID, matrixEventID, emoji, shortcode, ghostUserID, timestampMs)
	} else {
		_, err = sender.SendReactionAsGhostAt(matrixRoomID, matrixEventID, emoji, ghostUserID, timestampMs)
	}
	if err != nil {
		// The same reaction already exists in the room (hooks can fire more than
		// once for one reaction); the desired state is reached.
		if strings.Contains(err.Error(), "M_DUPLICATE_ANNOTATION") {
			b.logger.LogDebug("Reaction already present in Matrix, treating as synced", "post_id", reaction.PostId, "emoji", reaction.EmojiName, "ghost_user_id", ghostUserID)
			return nil
		}
		return errors.Wrap(err, "failed to send reaction as ghost user")
	}

	b.logger.LogDebug("Successfully synced reaction as ghost user", "post_id", reaction.PostId, "emoji", reaction.EmojiName, "ghost_user_id", ghostUserID, "matrix_event_id", matrixEventID)
	return nil
}

// removeReactionFromMatrix removes a reaction from Matrix by finding and redacting the matching reaction event
func (b *MattermostToMatrixBridge) removeReactionFromMatrix(reaction *model.Reaction, channelID string) error {
	// Get Matrix room identifier
	matrixRoomIdentifier, err := b.GetMatrixRoomID(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get Matrix room identifier for reaction removal")
	}

	if matrixRoomIdentifier == "" {
		b.logger.LogWarn("No Matrix room mapped for channel", "channel_id", channelID)
		return nil
	}

	// Resolve room alias to room ID if needed
	matrixRoomID, err := b.matrixClient.ResolveRoomAlias(matrixRoomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Matrix room identifier for reaction removal")
	}

	// Get the post to find the Matrix event ID
	post, appErr := b.API.GetPost(reaction.PostId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get post for reaction removal")
	}

	// Get Matrix event ID from post properties
	config := b.getConfiguration()
	serverDomain := extractServerDomain(b.logger, config.MatrixServerURL)
	propertyKey := "matrix_event_id_" + serverDomain

	var matrixEventID string
	if post.Props != nil {
		if eventID, ok := post.Props[propertyKey].(string); ok {
			matrixEventID = eventID
		}
	}

	if matrixEventID == "" {
		b.logger.LogWarn("No Matrix event ID found for post", "post_id", reaction.PostId, "property_key", propertyKey)
		return nil // Can't remove reaction from a message that wasn't synced to Matrix
	}

	// Get user for ghost user creation
	user, appErr := b.API.GetUser(reaction.UserId)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get user for reaction removal")
	}

	// Check if ghost user exists (needed for determining which reaction to remove)
	ghostUserID, exists := b.getGhostUser(user.Id)
	if !exists {
		b.logger.LogWarn("No ghost user found for reaction removal", "user_id", reaction.UserId, "post_id", reaction.PostId)
		return nil // Can't remove a reaction from a user that doesn't have a ghost user
	}

	// Convert Mattermost emoji name to Matrix reaction format for matching
	emoji, _ := b.reactionKeyFor(reaction.EmojiName)

	// Get all reactions for this message from Matrix (as the ghost user)
	relations, err := b.matrixClient.GetEventRelationsAsUser(matrixRoomID, matrixEventID, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to get event relations from Matrix")
	}

	// Find the matching reaction event to redact
	var reactionEventID string
	for _, event := range relations {
		// Check if this is a reaction event
		eventType, ok := event["type"].(string)
		if !ok || eventType != "m.reaction" {
			continue
		}

		// Check if this reaction is from our ghost user
		sender, ok := event["sender"].(string)
		if !ok || sender != ghostUserID {
			continue
		}

		// Check if this reaction has the matching emoji
		content, ok := event["content"].(map[string]any)
		if !ok {
			continue
		}

		relatesTo, ok := content["m.relates_to"].(map[string]any)
		if !ok {
			continue
		}

		key, ok := relatesTo["key"].(string)
		if !ok || key != emoji {
			continue
		}

		// Found the matching reaction event
		eventID, ok := event["event_id"].(string)
		if ok {
			reactionEventID = eventID
			break
		}
	}

	if reactionEventID == "" {
		b.logger.LogWarn("No matching reaction found in Matrix to remove", "post_id", reaction.PostId, "emoji", reaction.EmojiName, "ghost_user_id", ghostUserID)
		return nil // No matching reaction found to remove
	}

	// Redact the reaction event
	_, err = b.matrixClient.RedactEventAsGhost(matrixRoomID, reactionEventID, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to redact reaction in Matrix")
	}

	b.logger.LogDebug("Successfully removed reaction from Matrix", "post_id", reaction.PostId, "emoji", reaction.EmojiName, "ghost_user_id", ghostUserID, "reaction_event_id", reactionEventID)
	return nil
}
