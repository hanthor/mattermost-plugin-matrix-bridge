package command

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// sanitizeShareName creates a valid ShareName matching the regex: ^[a-z0-9]+([a-z\-\_0-9]+|(__)?)[a-z0-9]*$
func sanitizeShareName(name string) string {
	// Convert to lowercase and replace spaces with hyphens
	shareName := strings.ToLower(name)
	shareName = strings.ReplaceAll(shareName, " ", "-")

	// Remove any characters that aren't lowercase letters, numbers, hyphens, or underscores
	var validShareName strings.Builder
	for _, r := range shareName {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			validShareName.WriteRune(r)
		}
	}

	result := validShareName.String()
	if result == "" {
		return "matrixbridge" // fallback if no valid characters
	}

	// Ensure it starts with alphanumeric
	for len(result) > 0 && (result[0] == '-' || result[0] == '_') {
		result = result[1:]
	}

	// Ensure it ends with alphanumeric
	for len(result) > 0 && (result[len(result)-1] == '-' || result[len(result)-1] == '_') {
		result = result[:len(result)-1]
	}

	// Final fallback check
	if result == "" {
		return "matrixbridge"
	}

	return result
}

// syncChannelMembersToMatrixRoom creates ghost users for all channel members and joins them to the Matrix room
func (c *Handler) syncChannelMembersToMatrixRoom(channelID, roomID string) (int, int, error) {
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		return 0, 0, errors.New("matrix client not available")
	}

	offset := 0
	limit := 100
	totalMembers := 0
	joinedCount := 0

	c.client.Log.Info("Starting to sync channel members to Matrix room", "channel_id", channelID, "room_id", roomID)

	// Process channel members with pagination - combine fetching and processing for memory efficiency
	for {
		pageMembers, appErr := c.pluginAPI.GetChannelMembers(channelID, offset, limit)
		if appErr != nil {
			c.client.Log.Warn("Failed to get channel members for ghost user creation", "error", appErr, "channel_id", channelID, "offset", offset)
			return joinedCount, totalMembers, errors.Wrap(appErr, "failed to get channel members")
		}
		if len(pageMembers) == 0 {
			break
		}

		totalMembers += len(pageMembers)

		// Process each member in this page immediately
		for _, member := range pageMembers {
			user, appErr := c.client.User.Get(member.UserId)
			if appErr != nil {
				c.client.Log.Warn("Failed to get user for processing", "error", appErr, "user_id", member.UserId)
				continue
			}

			if user.IsRemote() {
				// This is a Matrix-originated user - invite the original Matrix user to the room
				originalMatrixUserID, err := c.plugin.GetMatrixUserIDFromMattermostUser(user.Id)
				if err != nil {
					c.client.Log.Warn("Failed to get original Matrix user ID for remote user", "error", err, "user_id", user.Id, "username", user.Username)
					continue
				}

				// Invite the original Matrix user to the room
				if err := matrixClient.InviteUserToRoom(roomID, originalMatrixUserID); err != nil {
					c.client.Log.Warn("Failed to invite Matrix user to room", "error", err, "matrix_user_id", originalMatrixUserID, "mattermost_user_id", user.Id, "room_id", roomID)
				} else {
					c.client.Log.Debug("Successfully invited Matrix user to room", "matrix_user_id", originalMatrixUserID, "mattermost_user_id", user.Id, "username", user.Username, "room_id", roomID)
					joinedCount++
				}
			} else {
				// This is a local Mattermost user - create ghost user and join to room
				ghostUserID, err := c.plugin.CreateOrGetGhostUser(user.Id)
				if err != nil {
					c.client.Log.Warn("Failed to create or get ghost user", "error", err, "user_id", user.Id, "username", user.Username)
					continue
				}

				// Join the ghost user to the room (handles both public and private rooms)
				if err := matrixClient.InviteAndJoinGhostUser(roomID, ghostUserID); err != nil {
					c.client.Log.Warn("Failed to join ghost user to Matrix room", "error", err, "ghost_user_id", ghostUserID, "user_id", user.Id, "room_id", roomID)
				} else {
					c.client.Log.Debug("Successfully joined ghost user to Matrix room", "ghost_user_id", ghostUserID, "user_id", user.Id, "username", user.Username, "room_id", roomID)
					joinedCount++
				}
			}
		}

		c.client.Log.Debug("Processed page of channel members", "processed_in_page", len(pageMembers), "total_processed", totalMembers, "total_joined", joinedCount)

		// Move to next page
		offset += limit
	}

	c.client.Log.Info("Completed syncing channel members to Matrix room", "joined_count", joinedCount, "total_members", totalMembers, "room_id", roomID)
	return joinedCount, totalMembers, nil
}

func (c *Handler) executeMapUserCommand(args *model.CommandArgs, username, matrixUserID string) *model.CommandResponse {
	// 1. Parse the username (remove leading @ if present).
	username = strings.TrimPrefix(username, "@")

	// 2. Parse the Matrix User ID (must start with @).
	if !strings.HasPrefix(matrixUserID, "@") || !strings.Contains(matrixUserID, ":") {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         mapUserInvalidMatrix,
		}
	}

	// 3. Lookup the Mattermost user by username
	user, appErr := c.client.User.GetByUsername(username)
	if appErr != nil {
		c.client.Log.Warn("Failed to find user for mapping", "error", appErr, "username", username)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf(mapUserFailed, "User not found or error accessing database"),
		}
	}

	// 4. Map the user ID in the KVStore
	// Key: ghost_user_<mmUserID> -> Value: <matrixUserID>
	// This overrides the ghost user lookup to point to a real Matrix user
	key := kvstore.BuildGhostUserKey(user.Id)
	if err := c.kvstore.Set(key, []byte(matrixUserID)); err != nil {
		c.client.Log.Error("Failed to save user mapping", "error", err, "user_id", user.Id, "matrix_user_id", matrixUserID)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf(mapUserFailed, "Failed to save to KVStore"),
		}
	}

	c.client.Log.Info("Successfully mapped Mattermost user to Matrix user", "username", username, "user_id", user.Id, "matrix_user_id", matrixUserID)

	// 5. Return success message.
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf(mapUserSuccess, username, matrixUserID),
	}
}

// encryptedRoomRefusal explains why a room is not bridged.
const encryptedRoomRefusal = "❌ That Matrix room is end-to-end encrypted. The bridge cannot read or send into encrypted rooms (it is not set up for it), so the room is not bridged. Use an unencrypted room, or see docs/ENCRYPTION.md."

// roomIsEncrypted reports whether the room has end-to-end encryption that the
// bridge cannot handle. When the homeserver cannot say, it does not block.
func (c *Handler) roomIsEncrypted(matrixClient *matrix.Client, identifier string) bool {
	if c.plugin.EncryptionSupported() {
		return false
	}
	roomID := identifier
	if strings.HasPrefix(identifier, "#") {
		resolved, err := matrixClient.ResolveRoomAlias(identifier)
		if err != nil {
			return false
		}
		roomID = resolved
	}
	encrypted, err := matrixClient.IsRoomEncrypted(roomID)
	return err == nil && encrypted
}

func (c *Handler) executeJoinCommand(args *model.CommandArgs, roomIdentifier string, createChannel bool) *model.CommandResponse {
	// Get current Matrix client and fail fast if not configured
	matrixClient, errResponse := c.getMatrixClientOrError()
	if errResponse != nil {
		return errResponse
	}

	// Validate room identifier format (should start with ! or # and contain a colon)
	if !isValidRoomIdentifier(roomIdentifier) {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         roomIdentifierError,
		}
	}

	c.client.Log.Info("Joining Matrix room", "room_identifier", roomIdentifier, "user_id", args.UserId, "create_channel", createChannel)

	// Join the AS bot to establish bridge presence
	if err := matrixClient.JoinRoom(roomIdentifier); err != nil {
		c.client.Log.Error("Failed to join Matrix room as AS bot", "error", err, "room_identifier", roomIdentifier)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to join Matrix room: %s\n\nMake sure:\n• The room exists\n• The room is public, or the bridge has been invited\n• The room identifier is correct", err.Error()),
		}
	}

	c.client.Log.Info("Successfully joined Matrix room as AS bot", "room_identifier", roomIdentifier)

	if c.roomIsEncrypted(matrixClient, roomIdentifier) {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: encryptedRoomRefusal}
	}

	// Join the command issuer's ghost user for immediate messaging capability
	user, appErr := c.client.User.Get(args.UserId)
	if appErr == nil {
		ghostUserID, err := c.plugin.CreateOrGetGhostUser(user.Id)
		if err == nil {
			if err := matrixClient.InviteAndJoinGhostUser(roomIdentifier, ghostUserID); err != nil {
				c.client.Log.Warn("Failed to join ghost user to room", "error", err, "ghost_user_id", ghostUserID, "room_identifier", roomIdentifier)
			} else {
				c.client.Log.Info("Successfully joined ghost user to room", "ghost_user_id", ghostUserID, "room_identifier", roomIdentifier)
			}
		}
	}

	var responseText strings.Builder
	responseText.WriteString(fmt.Sprintf("✅ **Successfully joined Matrix room** `%s`\n\n", roomIdentifier))

	// If createChannel is true, create a new Mattermost channel and map it to this room
	if createChannel {
		// Extract room name from identifier for channel name
		var channelName string
		if strings.HasPrefix(roomIdentifier, "#") {
			// Extract local part from room alias (#name:server.com -> name)
			parts := strings.Split(roomIdentifier[1:], ":")
			if len(parts) > 0 {
				channelName = parts[0]
			}
		}

		if channelName == "" {
			channelName = "matrix-room"
		}

		// Create the channel
		channel := &model.Channel{
			TeamId:      args.TeamId,
			Type:        model.ChannelTypeOpen,
			DisplayName: titleCase(strings.ReplaceAll(channelName, "-", " ")),
			Name:        channelName,
			Header:      fmt.Sprintf("Bridged to Matrix room: %s", roomIdentifier),
		}

		createdChannel, appErr := c.pluginAPI.CreateChannel(channel)
		if appErr != nil {
			c.client.Log.Error("Failed to create channel for Matrix room", "error", appErr, "channel_name", channelName)
			responseText.WriteString(fmt.Sprintf("⚠️ Failed to create Mattermost channel: %s\n\n", appErr.Error()))
			responseText.WriteString(fmt.Sprintf("You can manually map an existing channel using:\n`/matrix map %s`", roomIdentifier))
		} else {
			// Save mapping
			mappingKey := kvstore.BuildChannelMappingKey(createdChannel.Id)
			if err := c.kvstore.Set(mappingKey, []byte(roomIdentifier)); err != nil {
				c.client.Log.Error("Failed to save channel mapping", "error", err, "channel_id", createdChannel.Id, "room_identifier", roomIdentifier)
			}

			// Store reverse mapping
			roomMappingKey := kvstore.BuildRoomMappingKey(roomIdentifier)
			if err := c.kvstore.Set(roomMappingKey, []byte(createdChannel.Id)); err != nil {
				c.client.Log.Error("Failed to save room mapping", "error", err, "room_identifier", roomIdentifier, "channel_id", createdChannel.Id)
			}

			// If roomIdentifier is an alias, also resolve to room ID and store that mapping
			if strings.HasPrefix(roomIdentifier, "#") {
				if resolvedRoomID, err := matrixClient.ResolveRoomAlias(roomIdentifier); err == nil {
					roomIDMappingKey := kvstore.BuildRoomMappingKey(resolvedRoomID)
					if err := c.kvstore.Set(roomIDMappingKey, []byte(createdChannel.Id)); err != nil {
						c.client.Log.Error("Failed to save room ID mapping", "error", err, "room_id", resolvedRoomID, "channel_id", createdChannel.Id)
					}
				}
			}
			c.plugin.InvalidateChannelMapping(createdChannel.Id)

			c.client.Log.Info("Created and mapped channel for Matrix room", "channel_id", createdChannel.Id, "channel_name", createdChannel.Name, "room_identifier", roomIdentifier)

			responseText.WriteString(fmt.Sprintf("✅ **Created Mattermost channel** ~%s\n\n", createdChannel.Name))
			responseText.WriteString("Messages between the Matrix room and Mattermost channel will now sync automatically.")

			// Add user to the channel
			if _, appErr := c.client.Channel.AddUser(createdChannel.Id, args.UserId, args.UserId); appErr != nil {
				c.client.Log.Warn("Failed to add user to created channel", "error", appErr, "channel_id", createdChannel.Id, "user_id", args.UserId)
			}
		}
	} else {
		responseText.WriteString("**Next Steps:**\n")
		responseText.WriteString(fmt.Sprintf("• Use `/matrix map %s` in a channel to bridge it to this room\n", roomIdentifier))
		responseText.WriteString(fmt.Sprintf("• Or use `/matrix join %s create_channel=true` to auto-create a bridged channel", roomIdentifier))
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         responseText.String(),
	}
}

func (c *Handler) executeMapCommand(args *model.CommandArgs, roomIdentifier string) *model.CommandResponse {
	// Get current Matrix client and fail fast if not configured
	matrixClient, errResponse := c.getMatrixClientOrError()
	if errResponse != nil {
		return errResponse
	}

	// Validate room identifier format (should start with ! or # and contain a colon)
	if !isValidRoomIdentifier(roomIdentifier) {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         roomIdentifierError,
		}
	}

	// Get channel info for display
	channel, appErr := c.client.Channel.Get(args.ChannelId)
	channelName := args.ChannelId
	if appErr == nil {
		channelName = channel.DisplayName
		if channelName == "" {
			channelName = channel.Name
		}
	}

	// Try to join the Matrix room automatically
	var joinStatus string
	// Join the AS bot to establish bridge presence
	if err := matrixClient.JoinRoom(roomIdentifier); err != nil {
		c.client.Log.Warn("Failed to auto-join Matrix room", "error", err, "room_identifier", roomIdentifier)
		joinStatus = autoJoinFailed
	} else {
		c.client.Log.Info("Successfully joined Matrix room as AS bot", "room_identifier", roomIdentifier)

		// Also join the ghost user of the command issuer for immediate messaging capability
		user, appErr := c.client.User.Get(args.UserId)
		if appErr != nil {
			c.client.Log.Warn("Failed to get command issuer for ghost user join", "error", appErr, "user_id", args.UserId)
			joinStatus = autoJoinSuccess
		} else {
			// Create or get ghost user for the command issuer
			ghostUserID, err := c.plugin.CreateOrGetGhostUser(user.Id)
			if err != nil {
				c.client.Log.Warn("Failed to create or get ghost user for command issuer", "error", err, "user_id", user.Id)
				joinStatus = autoJoinSuccess
			} else {
				// Join the ghost user to the room (handles both public and private rooms)
				if err := matrixClient.InviteAndJoinGhostUser(roomIdentifier, ghostUserID); err != nil {
					c.client.Log.Warn("Failed to join ghost user to room", "error", err, "ghost_user_id", ghostUserID, "room_identifier", roomIdentifier)
					joinStatus = autoJoinSuccess
				} else {
					c.client.Log.Info("Successfully joined ghost user to room", "ghost_user_id", ghostUserID, "room_identifier", roomIdentifier)
					joinStatus = autoJoinWithUser
				}
			}
		}
	}

	if c.roomIsEncrypted(matrixClient, roomIdentifier) {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: encryptedRoomRefusal}
	}

	// Save both directions of the mapping
	mappingKey := kvstore.BuildChannelMappingKey(args.ChannelId)
	err := c.kvstore.Set(mappingKey, []byte(roomIdentifier))
	if err != nil {
		c.client.Log.Error("Failed to save channel mapping", "error", err, "channel_id", args.ChannelId, "room_identifier", roomIdentifier)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to save channel mapping. Check plugin logs for details.%s", joinStatus),
		}
	}

	// Store reverse mapping: room_mapping_<roomIdentifier> -> channelID
	roomMappingKey := kvstore.BuildRoomMappingKey(roomIdentifier)
	err = c.kvstore.Set(roomMappingKey, []byte(args.ChannelId))
	if err != nil {
		c.client.Log.Error("Failed to save room mapping", "error", err, "room_identifier", roomIdentifier, "channel_id", args.ChannelId)
		// Continue anyway - the forward mapping was saved successfully
	}

	// If roomIdentifier is an alias, also resolve to room ID and store that mapping
	if strings.HasPrefix(roomIdentifier, "#") {
		if resolvedRoomID, err := matrixClient.ResolveRoomAlias(roomIdentifier); err == nil {
			roomIDMappingKey := kvstore.BuildRoomMappingKey(resolvedRoomID)
			if err := c.kvstore.Set(roomIDMappingKey, []byte(args.ChannelId)); err != nil {
				c.client.Log.Error("Failed to save room ID mapping", "error", err, "room_id", resolvedRoomID, "channel_id", args.ChannelId)
			}
		}
	}
	c.plugin.InvalidateChannelMapping(args.ChannelId)

	c.client.Log.Info("Channel mapping saved", "channel_id", args.ChannelId, "channel_name", channelName, "room_identifier", roomIdentifier)

	// Add bridge alias for Matrix Application Service filtering
	// Extract room name from the identifier for the bridge alias
	var roomName string
	if strings.HasPrefix(roomIdentifier, "#") {
		// Extract local part from room alias (#name:server.com -> name)
		parts := strings.Split(roomIdentifier[1:], ":")
		if len(parts) > 0 {
			roomName = parts[0]
		}
	} else {
		// For room IDs, use channel name as fallback
		roomName = strings.ToLower(strings.ReplaceAll(channelName, " ", "-"))
		roomName = strings.ReplaceAll(roomName, "_", "-")
	}

	if roomName != "" {
		// Create bridge alias
		serverDomain := c.extractServerDomain()
		bridgeAlias := "#mattermost-bridge-" + roomName + ":" + serverDomain

		// Resolve room identifier to room ID (handles both aliases and room IDs)
		roomID, err := matrixClient.ResolveRoomAlias(roomIdentifier)
		if err != nil {
			c.client.Log.Warn("Failed to resolve room identifier for bridge alias", "error", err, "room_identifier", roomIdentifier)
			roomID = ""
		}

		if roomID != "" {
			err = matrixClient.AddRoomAlias(roomID, bridgeAlias)
			if err != nil {
				c.client.Log.Warn("Failed to add bridge filtering alias for manual mapping", "error", err, "bridge_alias", bridgeAlias, "room_id", roomID)
				// Continue - mapping still works, just no filtering alias
			} else {
				c.client.Log.Info("Successfully added bridge filtering alias for manual mapping", "room_id", roomID, "bridge_alias", bridgeAlias, "original_identifier", roomIdentifier)
			}
		}
	}

	// Sync all channel members to the Matrix room (same as /matrix create does)
	var memberSyncStatus string
	roomID := roomIdentifier
	// Resolve to actual room ID if it's an alias
	if resolvedRoomID, err := matrixClient.ResolveRoomAlias(roomIdentifier); err == nil && resolvedRoomID != "" {
		roomID = resolvedRoomID
	}

	joinedCount, totalMembers, syncErr := c.syncChannelMembersToMatrixRoom(args.ChannelId, roomID)
	if syncErr != nil {
		c.client.Log.Error("Failed to sync channel members to Matrix room", "error", syncErr, "room_id", roomID, "channel_id", args.ChannelId)
		memberSyncStatus = roomMemberSyncFailed
	} else {
		// Generate appropriate status message based on sync results
		if joinedCount == 0 {
			memberSyncStatus = ""
		} else if joinedCount == 1 && totalMembers == 1 {
			// Only one member (likely the command issuer) in a single-member channel
			memberSyncStatus = roomCreatorWithUserReady
		} else {
			// Multiple members were synced
			memberSyncStatus = fmt.Sprintf("\n\n✅ **All channel members synced to Matrix** - %d of %d users joined the room.", joinedCount, totalMembers)
		}
	}

	// Share the channel and invite this plugin to receive sync messages
	shareStatus := c.shareChannelAndInvitePlugin(args, channelName, fmt.Sprintf("Mapped to Matrix room: %s", roomIdentifier))

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ **Mapping Saved**\n\n**Channel:** %s\n**Matrix Room:** `%s`%s%s%s", channelName, roomIdentifier, joinStatus, memberSyncStatus, shareStatus),
	}
}

func (c *Handler) executeUnmapCommand(args *model.CommandArgs) *model.CommandResponse {
	// Get channel info for display
	channel, appErr := c.client.Channel.Get(args.ChannelId)
	channelName := args.ChannelId
	if appErr == nil {
		channelName = channel.DisplayName
		if channelName == "" {
			channelName = channel.Name
		}
	}

	// Check if this channel has a Matrix room mapping
	channelMappingKey := kvstore.BuildChannelMappingKey(args.ChannelId)
	roomIDBytes, err := c.kvstore.Get(channelMappingKey)
	if err != nil || len(roomIDBytes) == 0 {
		// Key not found is expected for unmapped channels
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ **No Mapping Found**\n\nChannel `%s` is not currently mapped to any Matrix room.", channelName),
		}
	}

	matrixRoomIdentifier := string(roomIDBytes)

	// Clear the Matrix room state to prevent fallback lookups - this is critical
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		c.client.Log.Error("Matrix client not available, cannot clear room state", "room_id", matrixRoomIdentifier)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ **Error:** Matrix client not configured. Cannot safely unmap - sync messages would continue.",
		}
	}

	if err := matrixClient.RemoveMattermostChannelID(matrixRoomIdentifier); err != nil {
		c.client.Log.Error("Failed to clear Matrix room state - sync messages would continue", "error", err, "room_id", matrixRoomIdentifier, "channel_id", args.ChannelId)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ **Error:** Failed to clear Matrix room state. Cannot safely unmap - sync messages would continue. Check plugin logs for details.",
		}
	}

	c.client.Log.Info("Successfully cleared Matrix room state", "room_id", matrixRoomIdentifier)

	// Remove the channel->room mapping
	if err := c.kvstore.Delete(channelMappingKey); err != nil {
		c.client.Log.Error("Failed to remove channel mapping", "error", err, "channel_id", args.ChannelId, "room_identifier", matrixRoomIdentifier)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ **Error:** Failed to remove channel mapping. Check plugin logs for details.",
		}
	}

	// Remove the room->channel mapping
	roomMappingKey := kvstore.BuildRoomMappingKey(matrixRoomIdentifier)
	if err := c.kvstore.Delete(roomMappingKey); err != nil {
		c.client.Log.Warn("Failed to remove room mapping", "error", err, "room_identifier", matrixRoomIdentifier, "channel_id", args.ChannelId)
		// Continue - the main mapping was removed
	}

	c.plugin.InvalidateChannelMapping(args.ChannelId)
	c.client.Log.Info("Removed Matrix room mapping", "channel_id", args.ChannelId, "room_identifier", matrixRoomIdentifier)

	// Uninvite this plugin from the shared channel
	uninviteErr := c.pluginAPI.UninviteRemoteFromChannel(args.ChannelId, c.plugin.GetRemoteID())

	var responseIcon, responseTitle, uninviteStatus string
	if uninviteErr != nil {
		c.client.Log.Warn("Failed to uninvite plugin from shared channel", "error", uninviteErr, "channel_id", args.ChannelId, "remote_id", c.plugin.GetRemoteID())
		responseIcon = "⚠️"
		responseTitle = "**Mapping Partially Removed**"
		uninviteStatus = "\n\n⚠️ **Note:** Failed to uninvite plugin from shared channel. The channel may still receive some sync events."
	} else {
		c.client.Log.Info("Successfully uninvited plugin from shared channel", "channel_id", args.ChannelId, "remote_id", c.plugin.GetRemoteID())
		responseIcon = "✅"
		responseTitle = "**Mapping Removed**"
		uninviteStatus = "\n\n✅ **Plugin uninvited** from shared channel successfully!"
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("%s %s\n\n**Channel:** %s\n**Matrix Room:** `%s`%s", responseIcon, responseTitle, channelName, matrixRoomIdentifier, uninviteStatus),
	}
}

func (c *Handler) executeCreateRoomCommand(args *model.CommandArgs, roomName string, publish bool) *model.CommandResponse {
	// Get current Matrix client and fail fast if not configured
	matrixClient, errResponse := c.getMatrixClientOrError()
	if errResponse != nil {
		return errResponse
	}

	// Channels created after the plugin was enabled already have a room;
	// creating another would silently split the conversation.
	if existing, err := c.kvstore.Get(kvstore.BuildChannelMappingKey(args.ChannelId)); err == nil && len(existing) > 0 {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text: fmt.Sprintf("ℹ️ This channel is already bridged to `%s`.\n\n"+
				"Use `/matrix status` to inspect it, or `/matrix unmap` first if you really want a new room.", string(existing)),
		}
	}

	// Get channel info for room name (if not provided) and topic
	channel, appErr := c.client.Channel.Get(args.ChannelId)
	channelName := args.ChannelId
	if appErr == nil {
		channelName = channel.DisplayName
		if channelName == "" {
			channelName = channel.Name
		}
	}

	// Use channel name as room name if not provided
	if roomName == "" {
		roomName = channelName
	}

	topic := fmt.Sprintf("Matrix room for Mattermost channel: %s", channelName)

	// Get team name to include in room alias (prevents collisions across teams)
	teamName := ""
	if args.TeamId != "" {
		if team, err := c.pluginAPI.GetTeam(args.TeamId); err == nil && team != nil {
			teamName = team.Name
		}
	}

	// Create the Matrix room
	// Extract server domain from Matrix server URL
	serverDomain := c.extractServerDomain()

	// In Mirror Mode, use clean room names without _mattermost_ prefix
	config := c.plugin.GetConfiguration()
	skipPrefix := config.GetEnableMirrorMode()

	roomID, err := matrixClient.CreateRoom(roomName, topic, serverDomain, publish, args.ChannelId, skipPrefix, teamName)
	if err != nil {
		c.client.Log.Error("Failed to create Matrix room", "error", err, "room_name", roomName)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to create Matrix room '%s'. Check plugin logs for details.", roomName),
		}
	}

	c.client.Log.Info("Created Matrix room and published to directory", "room_id", roomID, "room_name", roomName)

	// Sync all channel members to the newly created Matrix room
	var joinStatus string
	joinedCount, totalMembers, syncErr := c.syncChannelMembersToMatrixRoom(args.ChannelId, roomID)
	if syncErr != nil {
		c.client.Log.Error("Failed to sync channel members to Matrix room", "error", syncErr, "room_id", roomID, "channel_id", args.ChannelId)
		joinStatus = roomMemberSyncFailed
	} else {
		// Generate appropriate status message based on sync results
		if joinedCount == 0 {
			joinStatus = roomCreatorJoined
		} else if joinedCount == 1 && totalMembers == 1 {
			// Only one member (likely the command issuer) in a single-member channel
			joinStatus = roomCreatorWithUserReady
		} else {
			// Multiple members were synced
			joinStatus = fmt.Sprintf("\n\n✅ **All channel members synced to Matrix** - %d of %d users joined the room.", joinedCount, totalMembers)
		}
	}

	// Automatically map the created room to this channel (both directions)
	mappingKey := kvstore.BuildChannelMappingKey(args.ChannelId)
	if err := c.kvstore.Set(mappingKey, []byte(roomID)); err != nil {
		c.client.Log.Error("Failed to save channel mapping", "error", err, "channel_id", args.ChannelId, "room_id", roomID)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("✅ **Matrix Room Created:** `%s`\n\n❌ Failed to save channel mapping. Use `/matrix map %s` to map manually.", roomID, roomID),
		}
	}

	// Store reverse mapping: room_mapping_<roomID> -> channelID
	roomMappingKey := kvstore.BuildRoomMappingKey(roomID)
	if err := c.kvstore.Set(roomMappingKey, []byte(args.ChannelId)); err != nil {
		c.client.Log.Error("Failed to save room mapping", "error", err, "room_id", roomID, "channel_id", args.ChannelId)
		// Continue anyway - the forward mapping was saved successfully
	}
	c.plugin.InvalidateChannelMapping(args.ChannelId)

	// Share the channel and invite this plugin to receive sync messages
	shareStatus := c.shareChannelAndInvitePlugin(args, channelName, topic)

	// Build status message based on publish parameter
	publishStatus := ""
	if publish {
		publishStatus = publishedToDirectory
	} else {
		publishStatus = notPublishedToDirectory
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("✅ **Matrix Room Created & Mapped**\n\n**Room Name:** %s\n**Room ID:** `%s`\n**Channel:** %s%s%s%s", roomName, roomID, channelName, publishStatus, joinStatus, shareStatus),
	}
}

func (c *Handler) executeListMappingsCommand(args *model.CommandArgs) *model.CommandResponse {
	var responseText strings.Builder
	responseText.WriteString("**Channel-to-Room Mappings:**\n\n")

	// Get channel mapping keys using efficient prefix filtering
	mappings := make(map[string]string)
	channelMappingPrefix := kvstore.KeyPrefixChannelMapping
	page := 0
	batchSize := 1000

	for {
		keys, err := c.kvstore.ListKeysWithPrefix(page, batchSize, channelMappingPrefix)
		if err != nil {
			c.client.Log.Error("Failed to list KV store keys with prefix", "error", err, "page", page, "prefix", channelMappingPrefix)
			responseText.WriteString("❌ Failed to retrieve mappings. Check plugin logs for details.\n")
			return &model.CommandResponse{
				ResponseType: model.CommandResponseTypeEphemeral,
				Text:         responseText.String(),
			}
		}

		if len(keys) == 0 {
			break // No more keys
		}

		// Build mappings directly (no need to filter since prefix filtering is server-side)
		for _, key := range keys {
			channelID := strings.TrimPrefix(key, channelMappingPrefix)
			roomIDBytes, err := c.kvstore.Get(key)
			if err == nil && len(roomIDBytes) > 0 {
				mappings[channelID] = string(roomIDBytes)
			}
		}

		// If we got fewer keys than the batch size, we've reached the end
		if len(keys) < batchSize {
			break
		}

		page++
	}

	if len(mappings) == 0 {
		responseText.WriteString("No channel mappings found.\n\n")
		responseText.WriteString(getStartedHelp)
	} else {
		// Show current channel first if it has a mapping
		currentChannelMapping := mappings[args.ChannelId]
		if currentChannelMapping != "" {
			channel, appErr := c.client.Channel.Get(args.ChannelId)
			channelName := args.ChannelId
			if appErr == nil {
				channelName = channel.DisplayName
				if channelName == "" {
					channelName = channel.Name
				}
			}
			responseText.WriteString(fmt.Sprintf("**Current Channel:** %s → `%s`\n\n", channelName, currentChannelMapping))
		}

		// Show all mappings
		responseText.WriteString(fmt.Sprintf("**All Mappings (%d total):**\n", len(mappings)))
		for channelID, roomID := range mappings {
			// Get channel info
			channel, appErr := c.client.Channel.Get(channelID)
			channelName := channelID
			if appErr == nil {
				channelName = channel.DisplayName
				if channelName == "" {
					channelName = channel.Name
				}
			}

			// Mark current channel
			currentMarker := ""
			if channelID == args.ChannelId {
				currentMarker = " *(current)*"
			}

			responseText.WriteString(fmt.Sprintf("• %s → `%s`%s\n", channelName, roomID, currentMarker))
		}
	}

	responseText.WriteString("\n")
	responseText.WriteString(commandsHelp)

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         responseText.String(),
	}
}

// shareChannelAndInvitePlugin shares a channel and invites this plugin to receive sync messages
func (c *Handler) shareChannelAndInvitePlugin(args *model.CommandArgs, channelName, purpose string) string {
	// Without Shared Channels the bridge runs on plugin hooks alone, which is
	// a supported mode: say so instead of reporting a failure.
	if !c.sharedChannelsEnabled() {
		return channelSharingUnavailable
	}
	sharedChannel := &model.SharedChannel{
		ChannelId:        args.ChannelId,
		TeamId:           args.TeamId,
		Home:             true,
		ReadOnly:         false,
		ShareName:        sanitizeShareName(channelName),
		ShareDisplayName: channelName,
		SharePurpose:     purpose,
		ShareHeader:      "",
		CreatorId:        args.UserId,
		CreateAt:         model.GetMillis(),
		UpdateAt:         model.GetMillis(),
		RemoteId:         "",
	}

	_, shareErr := c.pluginAPI.ShareChannel(sharedChannel)
	if shareErr != nil {
		c.client.Log.Warn("Failed to automatically share channel", "error", shareErr, "channel_id", args.ChannelId)
		return channelSharingFailed
	}

	c.client.Log.Info("Automatically shared channel", "channel_id", args.ChannelId)

	// Invite this plugin to the shared channel to ensure we receive sync messages
	// This is critical - without this invitation, the channel won't receive sync events
	inviteErr := c.pluginAPI.InviteRemoteToChannel(args.ChannelId, c.plugin.GetRemoteID(), args.UserId, false)
	if inviteErr != nil {
		c.client.Log.Error("Failed to invite plugin to shared channel - bridge will not receive sync events", "error", inviteErr, "channel_id", args.ChannelId, "remote_id", c.plugin.GetRemoteID())
		return channelSharingFailed
	}

	c.client.Log.Info("Successfully invited plugin to shared channel", "channel_id", args.ChannelId, "remote_id", c.plugin.GetRemoteID())
	return channelSharingEnabled
}

// sharedChannelsEnabled reports whether the server has Shared Channels
// (Connected Workspaces) switched on.
func (c *Handler) sharedChannelsEnabled() bool {
	if c.pluginAPI == nil {
		return false
	}
	cfg := c.pluginAPI.GetConfig()
	if cfg == nil || cfg.ConnectedWorkspacesSettings.EnableSharedChannels == nil {
		return false
	}
	return *cfg.ConnectedWorkspacesSettings.EnableSharedChannels
}

// extractServerDomain extracts the domain from the Matrix server URL
func (c *Handler) extractServerDomain() string {
	// Get the current plugin configuration
	config := c.plugin.GetConfiguration()
	if config == nil {
		c.client.Log.Warn("Plugin configuration not available")
		return "matrix.org" // fallback
	}

	serverURL := config.GetMatrixServerURL()
	if serverURL == "" {
		c.client.Log.Warn("Matrix server URL not configured")
		return "matrix.org"
	}

	// Parse the URL to extract the hostname
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		c.client.Log.Warn("Failed to parse Matrix server URL", "url", serverURL, "error", err)
		return "matrix.org"
	}

	hostname := parsedURL.Hostname()
	if hostname == "" {
		c.client.Log.Warn("Could not extract hostname from Matrix server URL", "url", serverURL)
		return "matrix.org"
	}

	return hostname
}

// isValidRoomIdentifier accepts a room alias (#name:server) or a room ID.
// Room IDs carry a ":server" suffix up to room version 11; from room version
// 12 (MSC4291) they are an opaque "!<hash>" without a server part, so a
// colon is only required for aliases.
func isValidRoomIdentifier(roomIdentifier string) bool {
	switch {
	case strings.HasPrefix(roomIdentifier, "#"):
		return strings.Contains(roomIdentifier, ":") && len(roomIdentifier) > 2
	case strings.HasPrefix(roomIdentifier, "!"):
		return len(roomIdentifier) > 1 && !strings.ContainsAny(roomIdentifier, " \t\n")
	default:
		return false
	}
}
