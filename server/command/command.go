// Package command implements slash command handlers for the Matrix Bridge plugin.
package command

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

// Configuration interface for accessing plugin configuration
type Configuration interface {
	GetMatrixServerURL() string
	GetMatrixUsernamePrefixForServer(serverURL string) string
}

// MigrationResult holds the results of a migration operation
type MigrationResult struct {
	UserMappingsCreated      int
	ChannelMappingsCreated   int
	RoomMappingsCreated      int
	DMMappingsCreated        int
	ReverseDMMappingsCreated int
}

// PluginAccessor defines the interface for plugin functionality needed by command handlers
type PluginAccessor interface {
	// Matrix client access
	GetMatrixClient() *matrix.Client

	// Storage access
	GetKVStore() kvstore.KVStore

	// Configuration access
	GetConfiguration() Configuration

	// Ghost user management
	CreateOrGetGhostUser(mattermostUserID string) (string, error)

	// Matrix user mapping access
	GetMatrixUserIDFromMattermostUser(mattermostUserID string) (string, error)

	// Mattermost API access
	GetPluginAPI() plugin.API
	GetPluginAPIClient() *pluginapi.Client

	// Shared channel access
	GetRemoteID() string

	// Migration access
	RunKVStoreMigrations() error
	RunKVStoreMigrationsWithResults() (*MigrationResult, error)

	// Backfill synchronization
	SyncUser(userID string) error
	SyncTeam(teamID string) error
	SyncChannel(channelID string) error
}

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

// Handler implements slash command processing for the Matrix Bridge plugin.
type Handler struct {
	plugin    PluginAccessor
	client    *pluginapi.Client
	kvstore   kvstore.KVStore
	pluginAPI plugin.API
}

// Command defines the interface for handling Matrix Bridge slash commands.
type Command interface {
	Handle(args *model.CommandArgs) (*model.CommandResponse, error)
	executeMatrixCommand(args *model.CommandArgs) *model.CommandResponse
}

// Command usage and help text constants
const (
	// Triggers
	matrixCommandTrigger = "matrix"

	// Main command usage
	matrixCommandUsage = "Usage: /matrix [test|create|join|map|unmap|list|status|migrate] [room_name|room_alias|room_id]"

	// Subcommand descriptions for autocomplete
	testCommandDesc    = "Test Matrix server connection and configuration"
	createCommandDesc  = "Create a new Matrix room and map to current channel (uses channel name if room name not provided)"
	createCommandHint  = "[room_name] [publish=true|false]"
	joinCommandDesc    = "Join an existing Matrix room with the bridge bot"
	joinCommandHint    = "[room_alias|room_id] [create_channel=true|false]"
	mapCommandDesc     = "Map current channel to Matrix room (prefer #alias:server.com)"
	mapCommandHint     = "[room_alias|room_id]"
	unmapCommandDesc   = "Remove mapping between current channel and Matrix room, and uninvite plugin from shared channel"
	unmapCommandHint   = ""
	listCommandDesc    = "List all channel-to-room mappings"
	statusCommandDesc  = "Show bridge status"
	migrateCommandDesc = "Reset and re-run KV store migrations to fix missing room mappings"

	// Map command usage and validation
	mapCommandUsage     = "Usage: /matrix map [room_alias|room_id]\nExample: /matrix map #test-sync:synapse-mydomain.com"
	joinCommandUsage    = "Usage: /matrix join [room_alias|room_id] [create_channel=true|false]\nExample: /matrix join #community:matrix.org\nExample: /matrix join #community:matrix.org create_channel=true"
	roomIdentifierError = "Invalid room identifier format. Use either:\n• Room alias: `#roomname:server.com` (preferred for joining)\n• Room ID: `!roomid:server.com`"

	// Error messages
	matrixClientNotConfigured = "❌ Matrix client not configured. Please configure Matrix settings in System Console."
	unknownSubcommandError    = "Unknown subcommand. Use: test, create, map, unmap, list, status, or migrate"

	// Status messages
	autoJoinSuccess     = "\n\n✅ **Auto-joined** Matrix room successfully!"
	autoJoinWithUser    = "\n\n✅ **Auto-joined** Matrix room successfully!"
	autoJoinFailed      = "\n\n⚠️ **Note:** Could not auto-join Matrix room. You may need to manually invite the bridge user or make the room public in Matrix."
	matrixClientMissing = "\n\n⚠️ **Note:** Matrix client not configured. Please configure Matrix settings and manually invite the bridge user."

	// Room creation status messages
	roomCreatorJoined        = "\n\nMatrix room created and configured for bridging."
	roomCreatorWithUserReady = "\n\nMatrix room created and you are connected to it."
	roomMemberSyncFailed     = "\n\n⚠️ **Matrix room created, but failed to sync channel members.** Check plugin logs for details. You may need to manually invite users to the Matrix room."

	// Sharing status messages
	channelSharingEnabled = "\n\n✅ **Channel sharing enabled** - Messages will now sync to Matrix!"
	channelSharingFailed  = "\n\n⚠️ **Note:** Failed to automatically enable channel sharing. You may need to manually enable shared channels for this channel to start syncing."

	// Directory status messages
	publishedToDirectory    = "\n**Directory:** Published to public directory"
	notPublishedToDirectory = "\n**Directory:** Not published (private room)"

	// Common help text for commands
	getStartedHelp = "**Get Started:**\n" +
		"• `/matrix create` - Create new Matrix room using channel name and map to current channel\n" +
		"• `/matrix create [room_name]` - Create new Matrix room with custom name and map to current channel\n" +
		"• `/matrix join [room_alias]` - Join an existing Matrix room with the bridge bot\n" +
		"• `/matrix map [room_alias|room_id]` - Map current channel to existing Matrix room\n"

	commandsHelp = "**Commands:**\n" +
		"• `/matrix join [room_alias|room_id]` - Join existing Matrix room with bridge bot\n" +
		"• `/matrix map [room_alias|room_id]` - Map current channel to Matrix room\n" +
		"• `/matrix create` - Create new Matrix room using channel name and map to current channel\n" +
		"• `/matrix create [room_name]` - Create new Matrix room with custom name and map to current channel\n" +
		"• `/matrix status` - Check bridge status\n"

	// Status command response
	statusCommandResponse = "Matrix Bridge Status:\n- Plugin: Active\n- Configuration: Check System Console → Plugins → Matrix Bridge\n- Logs: Check plugin logs for connection status"

	// Test command next steps
	testCommandNextSteps = "\n📋 **Next Steps:**\n" +
		"   • Use `/matrix create \"Room Name\"` to create a Matrix room\n" +
		"   • The channel will be automatically configured for syncing\n"

	// Map User messages
	mapUserCommandDesc   = "Map a local Mattermost user to a specific Matrix user ID (e.g. for admins)"
	mapUserCommandHint   = "[mattermost_username] [matrix_user_id]"
	mapUserCommandUsage  = "Usage: /matrix map_user @username @user:server.com"
	mapUserSuccess       = "✅ Successfully mapped Mattermost user `@%s` to Matrix user `%s`"
	mapUserFailed        = "❌ Failed to map user: %s"
	mapUserInvalidMatrix = "❌ Invalid Matrix User ID. Must look like `@user:server.com`"

	// Backfill messages
	backfillCommandDesc  = "Backfill existing data to Matrix (users, teams, channels)"
	backfillCommandHint  = "[users|teams|channels|all]"
	backfillCommandUsage = "Usage: /matrix backfill [users|teams|channels|all]"
	backfillStarted      = "🔄 **Backfill started** for: %s. This may take a while. Check server logs for progress."
	backfillInvalidMode  = "❌ Invalid backfill mode. Use: users, teams, channels, or all"
)

// NewCommandHandler creates and registers all slash commands for the Matrix Bridge plugin.
func NewCommandHandler(plugin PluginAccessor) Command {
	// Cache frequently used services for reduced verbosity
	client := plugin.GetPluginAPIClient()
	kvstore := plugin.GetKVStore()
	pluginAPI := plugin.GetPluginAPI()

	matrixData := model.NewAutocompleteData(matrixCommandTrigger, "[subcommand]", "Matrix bridge commands")
	matrixData.AddCommand(model.NewAutocompleteData("test", "", testCommandDesc))

	// Create command with argument completion
	createCmd := model.NewAutocompleteData("create", createCommandHint, createCommandDesc)
	createCmd.AddTextArgument("Optional room name (defaults to channel name)", "[room_name]", "")
	createCmd.AddTextArgument("Optional publish flag", "[publish=true|false]", "")
	matrixData.AddCommand(createCmd)

	// Join command with argument completion
	joinCmd := model.NewAutocompleteData("join", joinCommandHint, joinCommandDesc)
	joinCmd.AddTextArgument("Matrix room alias or room ID", "[room_alias|room_id]", "")
	joinCmd.AddTextArgument("Optional create channel flag", "[create_channel=true|false]", "")
	matrixData.AddCommand(joinCmd)

	// Map command with argument completion
	mapCmd := model.NewAutocompleteData("map", mapCommandHint, mapCommandDesc)
	mapCmd.AddTextArgument("Matrix room alias or room ID", "[room_alias|room_id]", "")
	matrixData.AddCommand(mapCmd)

	// Map User command
	mapUserCmd := model.NewAutocompleteData("map_user", mapUserCommandHint, mapUserCommandDesc)
	mapUserCmd.AddTextArgument("Mattermost username", "[username]", "")
	mapUserCmd.AddTextArgument("Matrix User ID", "[matrix_id]", "")
	matrixData.AddCommand(mapUserCmd)

	// Backfill command
	backfillCmd := model.NewAutocompleteData("backfill", backfillCommandHint, backfillCommandDesc)
	backfillCmd.AddTextArgument("Mode", "[users|teams|channels|all]", "")
	matrixData.AddCommand(backfillCmd)

	// Unmap command
	matrixData.AddCommand(model.NewAutocompleteData("unmap", unmapCommandHint, unmapCommandDesc))

	matrixData.AddCommand(model.NewAutocompleteData("list", "", listCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("status", "", statusCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("migrate", "", migrateCommandDesc))

	err := client.SlashCommand.Register(&model.Command{
		Trigger:          matrixCommandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Matrix bridge commands",
		AutoCompleteHint: "[subcommand]",
		AutocompleteData: matrixData,
	})
	if err != nil {
		client.Log.Error("Failed to register matrix command", "error", err)
	}

	return &Handler{
		plugin:    plugin,
		client:    client,
		kvstore:   kvstore,
		pluginAPI: pluginAPI,
	}
}

// Handle processes slash commands registered by the Matrix Bridge plugin.
func (c *Handler) Handle(args *model.CommandArgs) (*model.CommandResponse, error) {
	trigger := strings.TrimPrefix(strings.Fields(args.Command)[0], "/")
	switch trigger {
	case matrixCommandTrigger:
		return c.executeMatrixCommand(args), nil
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("Unknown command: %s", args.Command),
		}, nil
	}
}

// getMatrixClientOrError gets the current Matrix client or returns an error response if not configured
func (c *Handler) getMatrixClientOrError() (*matrix.Client, *model.CommandResponse) {
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		return nil, &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         matrixClientNotConfigured,
		}
	}
	return matrixClient, nil
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

func (c *Handler) executeJoinCommand(args *model.CommandArgs, roomIdentifier string, createChannel bool) *model.CommandResponse {
	// Get current Matrix client and fail fast if not configured
	matrixClient, errResponse := c.getMatrixClientOrError()
	if errResponse != nil {
		return errResponse
	}

	// Validate room identifier format (should start with ! or # and contain a colon)
	if (!strings.HasPrefix(roomIdentifier, "!") && !strings.HasPrefix(roomIdentifier, "#")) || !strings.Contains(roomIdentifier, ":") {
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
			DisplayName: strings.Title(strings.ReplaceAll(channelName, "-", " ")),
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

			c.client.Log.Info("Created and mapped channel for Matrix room", "channel_id", createdChannel.Id, "channel_name", createdChannel.Name, "room_identifier", roomIdentifier)

			responseText.WriteString(fmt.Sprintf("✅ **Created Mattermost channel** ~%s\n\n", createdChannel.Name))
			responseText.WriteString(fmt.Sprintf("Messages between the Matrix room and Mattermost channel will now sync automatically."))

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
	if (!strings.HasPrefix(roomIdentifier, "!") && !strings.HasPrefix(roomIdentifier, "#")) || !strings.Contains(roomIdentifier, ":") {
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
	if err != nil {
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

	// Create the Matrix room
	// Extract server domain from Matrix server URL
	serverDomain := c.extractServerDomain()
	roomID, err := matrixClient.CreateRoom(roomName, topic, serverDomain, publish, args.ChannelId)
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

func (c *Handler) executeMatrixCommand(args *model.CommandArgs) *model.CommandResponse {
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         matrixCommandUsage,
		}
	}

	subcommand := fields[1]
	switch subcommand {
	case "test":
		return c.executeTestCommand(args)
	case "join":
		if len(fields) < 3 {
			return &model.CommandResponse{
				ResponseType: model.CommandResponseTypeEphemeral,
				Text:         joinCommandUsage,
			}
		}
		roomIdentifier := fields[2]
		createChannel := false
		if len(fields) >= 4 {
			arg := fields[3]
			if strings.HasPrefix(arg, "create_channel=") {
				createChannelValue := strings.TrimPrefix(arg, "create_channel=")
				createChannel = createChannelValue == "true"
			} else if arg == "true" || arg == "false" {
				createChannel = arg == "true"
			}
		}
		return c.executeJoinCommand(args, roomIdentifier, createChannel)
	case "create":
		// Parse room name and optional publish parameter
		var roomName string
		publish := false // don't publish rooms unless user explicitly requests it

		// Handle different argument patterns:
		// /matrix create
		// /matrix create true/false
		// /matrix create publish=true/false
		// /matrix create "room name"
		// /matrix create "room name" true/false
		// /matrix create "room name" publish=true/false

		if len(fields) == 2 {
			// Just "/matrix create" - use channel name, no publish
			roomName = ""
		} else if len(fields) == 3 {
			// Check if it's a publish parameter or room name
			arg := fields[2]
			if arg == "true" || arg == "false" || strings.HasPrefix(arg, "publish=") {
				// It's a publish parameter, use channel name for room
				roomName = ""
				if strings.HasPrefix(arg, "publish=") {
					publishValue := strings.TrimPrefix(arg, "publish=")
					publish = publishValue == "true"
				} else {
					publish = arg == "true"
				}
			} else {
				// It's a room name
				roomName = arg
			}
		} else {
			// Multiple arguments - check if last is publish parameter
			lastField := fields[len(fields)-1]
			if lastField == "true" || lastField == "false" || strings.HasPrefix(lastField, "publish=") {
				if strings.HasPrefix(lastField, "publish=") {
					publishValue := strings.TrimPrefix(lastField, "publish=")
					publish = publishValue == "true"
				} else {
					publish = lastField == "true"
				}
				// Room name is everything except the last field
				roomName = strings.Join(fields[2:len(fields)-1], " ")
			} else {
				// No publish parameter, room name is everything after "create"
				roomName = strings.Join(fields[2:], " ")
			}
		}

		return c.executeCreateRoomCommand(args, roomName, publish)
	case "map":
		if len(fields) < 3 {
			return &model.CommandResponse{
				ResponseType: model.CommandResponseTypeEphemeral,
				Text:         mapCommandUsage,
			}
		}
		roomID := fields[2]
		return c.executeMapCommand(args, roomID)
	case "map_user":
		if len(fields) < 4 {
			return &model.CommandResponse{
				ResponseType: model.CommandResponseTypeEphemeral,
				Text:         mapUserCommandUsage,
			}
		}
		username := fields[2]
		matrixUserID := fields[3]
		return c.executeMapUserCommand(args, username, matrixUserID)
	case "backfill":
		if len(fields) < 3 {
			return &model.CommandResponse{
				ResponseType: model.CommandResponseTypeEphemeral,
				Text:         backfillCommandUsage,
			}
		}
		mode := fields[2]
		return c.executeBackfillCommand(args, mode)
	case "unmap":
		return c.executeUnmapCommand(args)
	case "list":
		return c.executeListMappingsCommand(args)
	case "status":
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         statusCommandResponse,
		}
	case "migrate":
		return c.executeMigrateCommand(args)
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         unknownSubcommandError,
		}
	}
}

// shareChannelAndInvitePlugin shares a channel and invites this plugin to receive sync messages
func (c *Handler) shareChannelAndInvitePlugin(args *model.CommandArgs, channelName, purpose string) string {
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

func (c *Handler) executeTestCommand(_ *model.CommandArgs) *model.CommandResponse {
	var responseText strings.Builder
	responseText.WriteString("🔍 **Matrix Connection Test**\n\n")

	// Check basic configuration
	config := c.plugin.GetConfiguration()
	if config == nil {
		responseText.WriteString("❌ **Configuration:** Plugin configuration not available\n")
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         responseText.String(),
		}
	}

	serverURL := config.GetMatrixServerURL()
	if serverURL == "" {
		responseText.WriteString("❌ **Configuration:** Matrix server URL not set\n")
		responseText.WriteString("📝 **Action:** Go to System Console → Plugins → Matrix Bridge and set your Matrix server URL\n")
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         responseText.String(),
		}
	}

	responseText.WriteString(fmt.Sprintf("✅ **Server URL:** %s\n", serverURL))

	// Get current Matrix client and check if configured
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		responseText.WriteString("❌ **Matrix Client:** Not initialized\n")
		responseText.WriteString("📝 **Action:** Check that Application Service and Homeserver tokens are generated\n")
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         responseText.String(),
		}
	}

	responseText.WriteString("✅ **Matrix Client:** Initialized\n")

	// Test Matrix server connection
	err := matrixClient.TestConnection()
	if err != nil {
		responseText.WriteString("❌ **Connection:** Failed to connect to Matrix server\n")
		responseText.WriteString(fmt.Sprintf("🔍 **Error:** %s\n", err.Error()))
		responseText.WriteString("📝 **Actions:**\n")
		responseText.WriteString("   • Verify Matrix server URL is correct and reachable\n")
		responseText.WriteString("   • Check that Application Service registration file is installed\n")
		responseText.WriteString("   • Ensure Matrix homeserver is running\n")
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         responseText.String(),
		}
	}

	responseText.WriteString("✅ **Connection:** Successfully connected to Matrix server\n")

	// Try to get server information (name and version)
	serverInfo, infoErr := matrixClient.GetServerInfo()
	if infoErr == nil && serverInfo != nil {
		if serverInfo.Name != "Matrix Server" || serverInfo.Version != "Unknown" {
			responseText.WriteString(fmt.Sprintf("📊 **Matrix Server:** %s", serverInfo.Name))
			if serverInfo.Version != "Unknown" {
				responseText.WriteString(fmt.Sprintf(" v%s", serverInfo.Version))
			}
			responseText.WriteString("\n")
		}
	}

	// Test Application Service permissions without making invasive changes
	asErr := matrixClient.TestApplicationServicePermissions()
	if asErr != nil {
		responseText.WriteString("❌ **Application Service:** Permission test failed\n")
		responseText.WriteString(fmt.Sprintf("🔍 **Error:** %s\n", asErr.Error()))
		responseText.WriteString("📝 **Actions:**\n")
		responseText.WriteString("   • Verify Application Service registration file is properly installed\n")
		responseText.WriteString("   • Check that homeserver and AS tokens match the registration file\n")
		responseText.WriteString("   • Restart Matrix homeserver if registration file was recently added\n")
	} else {
		responseText.WriteString("✅ **Application Service:** Permissions verified (can query namespace)\n")
	}

	// Test shared channels registration
	responseText.WriteString(testCommandNextSteps)

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         responseText.String(),
	}
}

func (c *Handler) executeBackfillCommand(args *model.CommandArgs, mode string) *model.CommandResponse {
	if mode != "users" && mode != "teams" && mode != "channels" && mode != "all" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Invalid mode. Available modes: users, teams, channels, all",
		}
	}

	go func() {
		if mode == "users" || mode == "all" {
			c.backfillUsers()
		}
		if mode == "teams" || mode == "all" {
			c.backfillTeams()
		}
		if mode == "channels" || mode == "all" {
			c.backfillChannels()
		}
	}()

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("Backfill started for mode: %s. This process runs in the background. Check server logs for progress/errors.", mode),
	}
}

func (c *Handler) backfillUsers() {
	page := 0
	perPage := 100
	for {
		users, err := c.client.User.List(&model.UserGetOptions{Page: page, PerPage: perPage})
		if err != nil {
			c.plugin.GetPluginAPI().LogError("Failed to list users for backfill", "error", err)
			return
		}
		if len(users) == 0 {
			break
		}

		for _, u := range users {
			if u.DeleteAt != 0 || u.IsBot {
				continue
			}
			if err := c.plugin.SyncUser(u.Id); err != nil {
				c.plugin.GetPluginAPI().LogError("Failed to sync user", "user_id", u.Id, "error", err)
			}
		}
		page++
	}
}

func (c *Handler) backfillTeams() {
	// GetTeams returns all teams in the system
	teams, err := c.plugin.GetPluginAPI().GetTeams()
	if err != nil {
		c.plugin.GetPluginAPI().LogError("Failed to list teams for backfill", "error", err)
		return
	}

	for _, t := range teams {
		if t.DeleteAt != 0 {
			continue
		}
		if err := c.plugin.SyncTeam(t.Id); err != nil {
			c.plugin.GetPluginAPI().LogError("Failed to sync team", "team_id", t.Id, "error", err)
		}
	}
}

func (c *Handler) backfillChannels() {
	// GetTeams returns all teams in the system
	teams, err := c.plugin.GetPluginAPI().GetTeams()
	if err != nil {
		c.plugin.GetPluginAPI().LogError("Failed to list teams for backfill channels", "error", err)
		return
	}

	for _, t := range teams {
		c.backfillChannelsForTeam(t.Id)
	}
}

func (c *Handler) backfillChannelsForTeam(teamID string) {
	page := 0
	perPage := 100
	for {
		channels, err := c.plugin.GetPluginAPI().GetPublicChannelsForTeam(teamID, page, perPage)
		if err != nil {
			c.plugin.GetPluginAPI().LogError("Failed to list channels for backfill", "team_id", teamID, "error", err)
			break
		}
		if len(channels) == 0 {
			break
		}

		for _, ch := range channels {
			if ch.DeleteAt != 0 {
				continue
			}
			if err := c.plugin.SyncChannel(ch.Id); err != nil {
				c.plugin.GetPluginAPI().LogError("Failed to sync channel", "channel_id", ch.Id, "error", err)
			}
		}
		page++
	}
}

func (c *Handler) executeMigrateCommand(_ *model.CommandArgs) *model.CommandResponse {
	// Get current version before reset
	kvstorage := c.plugin.GetKVStore()
	versionBytes, _ := kvstorage.Get(kvstore.KeyStoreVersion)
	currentVersion := "0"
	if len(versionBytes) > 0 {
		currentVersion = string(versionBytes)
	}

	// Reset KV store version to 0 to force re-migration
	if err := kvstorage.Set(kvstore.KeyStoreVersion, []byte("0")); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Failed to reset migration version: %v", err),
		}
	}

	// Run migrations and get detailed results
	migrationResult, err := c.plugin.RunKVStoreMigrationsWithResults()
	if err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("❌ Migration failed: %v", err),
		}
	}

	// Get the results from migration
	userMappingsAdded := migrationResult.UserMappingsCreated
	channelMappingsAdded := migrationResult.ChannelMappingsCreated
	roomMappingsAdded := migrationResult.RoomMappingsCreated
	dmMappingsAdded := migrationResult.DMMappingsCreated
	reverseDMMappingsAdded := migrationResult.ReverseDMMappingsCreated

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: fmt.Sprintf("✅ **Migration completed successfully!**\n\n"+
			"**Migration Results:**\n"+
			"   • Reset version: %s → %d\n"+
			"   • User reverse mappings created/updated: %d\n"+
			"   • Channel reverse mappings created/updated: %d\n"+
			"   • Room ID mappings created/updated: %d\n"+
			"   • DM mappings migrated: %d\n"+
			"   • DM reverse mappings created: %d\n\n"+
			"This should have resolved any missing or incorrect mappings.\n"+
			"Check the plugin logs for detailed migration information.",
			currentVersion, kvstore.CurrentKVStoreVersion,
			userMappingsAdded, channelMappingsAdded, roomMappingsAdded,
			dmMappingsAdded, reverseDMMappingsAdded),
	}
}
