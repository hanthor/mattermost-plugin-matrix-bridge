// Package command implements slash command handlers for the Matrix Bridge plugin.
package command

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	GetEnableMirrorMode() bool
}

// MetricsSummary holds a snapshot of all metrics at a point in time.
// This type is defined in the main server package to avoid circular dependencies.
type MetricsSummary = struct {
	MessagesToMatrix    uint64            `json:"messages_to_matrix"`
	MessagesFromMatrix  uint64            `json:"messages_from_matrix"`
	MessageSyncErrors   uint64            `json:"message_sync_errors"`
	MessageEditsSynced  uint64            `json:"message_edits_synced"`
	ReactionsAdded      uint64            `json:"reactions_added"`
	ReactionsRemoved    uint64            `json:"reactions_removed"`
	GhostUsersCreated   uint64            `json:"ghost_users_created"`
	RoomsCreated        uint64            `json:"rooms_created"`
	ChannelsCreated     uint64            `json:"channels_created"`
	UsersSynced         uint64            `json:"users_synced"`
	MatrixAPICalls      uint64            `json:"matrix_api_calls"`
	MatrixAPIErrors     uint64            `json:"matrix_api_errors"`
	MatrixAPIRetries    uint64            `json:"matrix_api_retries"`
	MessageLatencyAvgMs float64           `json:"message_latency_avg_ms"`
	MessageLatencyMaxMs uint64            `json:"message_latency_max_ms"`
	APILatencyAvgMs     float64           `json:"api_latency_avg_ms"`
	APILatencyMaxMs     uint64            `json:"api_latency_max_ms"`
	ErrorsByType        map[string]uint64 `json:"errors_by_type"`
	UptimeSeconds       float64           `json:"uptime_seconds"`
}

// MetricsAccessor interface for accessing plugin metrics
type MetricsAccessor interface {
	GetSummary() MetricsSummary
}

// MigrationResult holds the results of a migration operation
type MigrationResult struct {
	UserMappingsCreated      int
	ChannelMappingsCreated   int
	RoomMappingsCreated      int
	DMMappingsCreated        int
	ReverseDMMappingsCreated int
}

// MetricsProvider exposes the read-only metrics snapshot used by the
// /matrix metrics command. The main package's *Metrics satisfies it.
type MetricsProvider interface {
	GetMessagesToMatrix() uint64
	GetMessagesFromMatrix() uint64
	GetMessageSyncErrors() uint64
	GetMessageEditsSynced() uint64
	GetReactionsAdded() uint64
	GetReactionsRemoved() uint64
	GetGhostUsersCreated() uint64
	GetRoomsCreated() uint64
	GetChannelsCreated() uint64
	GetUsersSynced() uint64
	GetMatrixAPICalls() uint64
	GetMatrixAPIErrors() uint64
	GetMatrixAPIRetries() uint64
	GetMessageLatencyAvg() float64
	GetMessageLatencyMax() uint64
	GetAPILatencyAvg() float64
	GetAPILatencyMax() uint64
	GetErrorsByType() map[string]uint64
	GetUptime() time.Duration
}

// MirrorStatus describes the account mode of the bridge for /matrix status.
type MirrorStatus struct {
	// Mode is "ghost" or "mirror".
	Mode string
	// Pinned is set when the mode was fixed by existing accounts and the
	// setting asks for a different one.
	Pinned bool
	// ConfiguredMirror is what the setting asks for.
	ConfiguredMirror bool
	Accounts         int
	Claimed          int
	Collisions       int
	// ClaimSupported is true when the bridge can set a password for a
	// mirrored account (an admin token is configured).
	ClaimSupported bool
	// Cutover is set when accounts and rooms are frozen.
	Cutover bool
}

// GhostMigrationReport is the outcome of /matrix migrate ghosts-to-mirror.
type GhostMigrationReport struct {
	DryRun   bool
	Users    int
	Rooms    int
	Moved    int
	Skipped  int
	Failed   int
	Errors   []string
	Switched bool
}

// HistoryReport describes importing a channel's past messages into its room.
type HistoryReport struct {
	// Pending is how many messages are still to be imported.
	Pending int
	// Imported, Skipped and Failed count what a run did so far.
	Imported int
	Skipped  int
	Failed   int
	// Files counts the attachments that come with the pending messages.
	Files int
	// Reactions counts the reactions imported so far.
	Reactions int
	// Running is set while an import is in progress.
	Running bool
	// Truncated is set when the channel has more history than was examined.
	Truncated bool
	Oldest    int64 // time of the oldest pending message, ms
	Newest    int64 // time of the newest pending message, ms
	Error     string
}

// PluginAccessor defines the interface for plugin functionality needed by command handlers
type PluginAccessor interface {
	// Matrix client access
	GetMatrixClient() *matrix.Client

	// Storage access
	GetKVStore() kvstore.KVStore

	// Configuration access
	GetConfiguration() Configuration

	// Metrics access
	GetMetrics() MetricsProvider

	// Ghost user management
	CreateOrGetGhostUser(mattermostUserID string) (string, error)

	// Matrix user mapping access
	GetMatrixUserIDFromMattermostUser(mattermostUserID string) (string, error)

	// Mattermost API access
	GetPluginAPI() plugin.API
	GetPluginAPIClient() *pluginapi.Client

	// Shared channel access
	GetRemoteID() string

	// InvalidateChannelMapping drops cached channel → room lookups after a
	// mapping changed.
	InvalidateChannelMapping(channelID string)

	// Migration access
	RunKVStoreMigrations() error
	RunKVStoreMigrationsWithResults() (*MigrationResult, error)

	// MigrateGhostsToMirror moves ghost users to mirror accounts; without
	// confirm it only reports what it would do.
	MigrateGhostsToMirror(confirm bool) (GhostMigrationReport, error)

	// StartCall starts a call in the channel that both Mattermost and Matrix
	// users can join, and returns its join address.
	StartCall(mattermostUserID, channelID, title string) (string, error)

	// EncryptionSupported reports whether the bridge can read and send into
	// end-to-end encrypted rooms (it is set up for it and running).
	EncryptionSupported() bool

	// Mirror mode: account mode and claiming (see docs/CALLS_AND_MIRROR_MODE.md)
	MirrorStatus() MirrorStatus
	// ClaimMirrorAccount hands a mirrored account to its person. When the
	// bridge can set a password it returns a one-time password to show once.
	ClaimMirrorAccount(mattermostUserID string) (matrixUserID, oneTimePassword string, err error)

	// History import (see docs/COMMANDS.md). PlanChannelHistory only counts;
	// StartChannelHistory imports in the background, oldest first, as the
	// authors and with the original times; ChannelHistoryStatus reports the
	// last or current run.
	PlanChannelHistory(channelID string, limit int) (HistoryReport, error)
	StartChannelHistory(channelID string, limit int) error
	ChannelHistoryStatus(channelID string) HistoryReport

	// MigrateTeam runs a cut-over step for the bridged channels of a team:
	// report, readonly, writable or archive (see docs/COMMANDS.md). The
	// returned text is shown to the administrator.
	MigrateTeam(userID, teamID, action string, confirm bool) (string, error)

	// DirectMessages handles /matrix dm: action is on, off or status.
	DirectMessages(userID, action string) (string, error)

	// DeadLetters is how many Matrix events failed delivery and were set
	// aside; RetryDeadLetters queues them again and returns how many.
	DeadLetters() int
	RetryDeadLetters() (int, error)

	// RepairMembership makes the channel's room match the channel's members
	// (for the bridge's own accounts) and describes what it changed.
	RepairMembership(channelID string) (string, error)

	// Doctor runs the setup checks (and the channel's checks) and returns the report.
	Doctor(channelID string) string

	// Identity linking (one person on both networks)
	CreateIdentityLinkCode(mattermostUserID string) (string, error)
	GetIdentityLink(mattermostUserID string) (matrixUserID string, puppeted bool, ok bool)
	UnlinkIdentity(mattermostUserID string) error

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
	matrixCommandUsage = "Usage: /matrix [test|create|join|map|unmap|list|status|link|unlink|claim|call|history|migrate] [room_name|room_alias|room_id]"
	linkCommandDesc    = "Link your Matrix account so you are one identity on both networks"
	unlinkCommandDesc  = "Remove the link to your Matrix account"
	claimCommandDesc   = "Mirror mode: take over your mirrored Matrix account"
	callCommandDesc    = "Start a call in this bridged channel that Mattermost and Matrix users can both join"

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
	// channelSharingUnavailable is shown when Shared Channels is off on the
	// server; the bridge then syncs through plugin hooks, which needs no setup.
	channelSharingUnavailable = "\n\nℹ️ Shared Channels is not enabled on this server; the bridge syncs this channel through plugin hooks."

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

// buildMatrixAutocompleteData describes the /matrix command tree used for
// slash command registration. Tests reuse it so the expected registration
// never drifts from what the plugin actually registers.
func buildMatrixAutocompleteData() *model.AutocompleteData {
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
	matrixData.AddCommand(model.NewAutocompleteData("repair", "", "Make this channel's room match the channel's members"))
	matrixData.AddCommand(model.NewAutocompleteData("doctor", "", "Check the bridge setup and this channel, with what to fix"))
	matrixData.AddCommand(model.NewAutocompleteData("retry", "", "Replay changes that could not be delivered across the bridge"))
	dmCmd := model.NewAutocompleteData("dm", "[on|off|status]", "Allow your direct messages to be bridged to Matrix (only if everyone in the conversation agrees)")
	dmCmd.AddStaticListArgument("action", false, []model.AutocompleteListItem{
		{Item: "on", HelpText: "Agree to bridge your direct and group messages"},
		{Item: "off", HelpText: "Stop bridging them"},
		{Item: "status", HelpText: "Show your setting"},
	})
	matrixData.AddCommand(dmCmd)
	teamCmd := model.NewAutocompleteData("migrate-team", "report|readonly|writable|archive [confirm]", "Cut a team over to Matrix: report, make channels read-only, archive")
	teamCmd.AddStaticListArgument("action", true, []model.AutocompleteListItem{
		{Item: "report", HelpText: "Compare members and messages per channel"},
		{Item: "readonly", HelpText: "Make bridged channels read-only with a pinned link to the room"},
		{Item: "writable", HelpText: "Undo readonly"},
		{Item: "archive", HelpText: "Archive the read-only channels"},
	})
	teamCmd.AddStaticListArgument("confirm", false, []model.AutocompleteListItem{{Item: "confirm", HelpText: "Apply the change"}})
	matrixData.AddCommand(teamCmd)
	historyCmd := model.NewAutocompleteData("history", "[confirm] [limit=N]", "Import this channel's past messages into its Matrix room")
	historyCmd.AddTextArgument("confirm to start; omit to preview", "[confirm] [limit=N]", "")
	matrixData.AddCommand(historyCmd)
	matrixData.AddCommand(model.NewAutocompleteData("link", "", linkCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("unlink", "", unlinkCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("claim", "", claimCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("call", "[title]", callCommandDesc))

	return matrixData
}

// NewCommandHandler creates and registers all slash commands for the Matrix Bridge plugin.
func NewCommandHandler(plugin PluginAccessor) Command {
	// Cache frequently used services for reduced verbosity
	client := plugin.GetPluginAPIClient()
	kvstore := plugin.GetKVStore()
	pluginAPI := plugin.GetPluginAPI()

	matrixData := buildMatrixAutocompleteData()

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

func (c *Handler) executeMatrixCommand(args *model.CommandArgs) *model.CommandResponse {
	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         matrixCommandUsage,
		}
	}

	subcommand := fields[1]

	if denied := c.checkCommandPermission(args, subcommand); denied != nil {
		return denied
	}

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
			Text:         statusCommandResponse + c.accountModeStatus() + c.channelEncryptionStatus(args.ChannelId) + c.deliveryStatus(),
		}
	case "claim":
		return c.executeClaimCommand(args)
	case "call":
		title := ""
		if len(fields) > 2 {
			title = strings.Join(fields[2:], " ")
		}
		return c.executeCallCommand(args, title)
	case "metrics":
		return c.executeMetricsCommand(args)
	case "link":
		return c.executeLinkCommand(args)
	case "unlink":
		return c.executeUnlinkCommand(args)
	case "migrate":
		return c.executeMigrateCommand(args)
	case "history":
		return c.executeHistoryCommand(args)
	case "migrate-team":
		return c.executeMigrateTeamCommand(args)
	case "repair":
		text, err := c.plugin.RepairMembership(args.ChannelId)
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	case "doctor":
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: c.plugin.Doctor(args.ChannelId)}
	case "retry":
		n, err := c.plugin.RetryDeadLetters()
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		if n == 0 {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "Nothing is waiting to be retried."}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: fmt.Sprintf("🔁 %d changes were queued again. They are posted in the background; run `/matrix status` to see whether any remain.", n)}
	case "dm":
		fields := strings.Fields(args.Command)
		action := ""
		if len(fields) > 2 {
			action = fields[2]
		}
		text, err := c.plugin.DirectMessages(args.UserId, action)
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	default:
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         unknownSubcommandError,
		}
	}
}

// checkCommandPermission enforces who may run each subcommand. Commands that
// change bridge-wide state (user mappings, backfill, migrations, metrics)
// require system admin. Commands that bridge a channel require the right to
// manage that channel. Read-only commands are open to everyone.
func (c *Handler) checkCommandPermission(args *model.CommandArgs, subcommand string) *model.CommandResponse {
	deny := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}

	isSystemAdmin := func() bool {
		return c.pluginAPI.HasPermissionTo(args.UserId, model.PermissionManageSystem)
	}

	switch subcommand {
	case "create", "map", "join":
		if c.plugin.MirrorStatus().Cutover {
			return deny("❌ Cut-over mode is on: no new rooms are bridged.")
		}
	}

	switch subcommand {
	case "map_user", "backfill", "migrate", "metrics", "history", "migrate-team", "retry", "doctor":
		if !isSystemAdmin() {
			return deny("❌ Only system administrators can run `/matrix " + subcommand + "`.")
		}
	case "create", "map", "unmap", "join", "repair":
		if isSystemAdmin() {
			return nil
		}
		canManage := c.pluginAPI.HasPermissionToChannel(args.UserId, args.ChannelId, model.PermissionManagePublicChannelProperties) ||
			c.pluginAPI.HasPermissionToChannel(args.UserId, args.ChannelId, model.PermissionManagePrivateChannelProperties)
		if !canManage {
			return deny("❌ You need permission to manage this channel to run `/matrix " + subcommand + "`.")
		}
	}
	return nil
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

func (c *Handler) executeMetricsCommand(_ *model.CommandArgs) *model.CommandResponse {
	metrics := c.plugin.GetMetrics()

	var responseText strings.Builder
	responseText.WriteString("📊 **Matrix Bridge Metrics**\n\n")

	// Message metrics
	responseText.WriteString("**Messages**\n")
	responseText.WriteString(fmt.Sprintf("• To Matrix: %d\n", metrics.GetMessagesToMatrix()))
	responseText.WriteString(fmt.Sprintf("• From Matrix: %d\n", metrics.GetMessagesFromMatrix()))
	responseText.WriteString(fmt.Sprintf("• Sync Errors: %d\n", metrics.GetMessageSyncErrors()))
	responseText.WriteString(fmt.Sprintf("• Edits Synced: %d\n\n", metrics.GetMessageEditsSynced()))

	// Reaction metrics
	responseText.WriteString("**Reactions**\n")
	responseText.WriteString(fmt.Sprintf("• Added: %d\n", metrics.GetReactionsAdded()))
	responseText.WriteString(fmt.Sprintf("• Removed: %d\n\n", metrics.GetReactionsRemoved()))

	// Entity metrics
	responseText.WriteString("**Entities**\n")
	responseText.WriteString(fmt.Sprintf("• Ghost Users Created: %d\n", metrics.GetGhostUsersCreated()))
	responseText.WriteString(fmt.Sprintf("• Rooms Created: %d\n", metrics.GetRoomsCreated()))
	responseText.WriteString(fmt.Sprintf("• Channels Created: %d\n", metrics.GetChannelsCreated()))
	responseText.WriteString(fmt.Sprintf("• Users Synced: %d\n\n", metrics.GetUsersSynced()))

	// API metrics
	responseText.WriteString("**Matrix API**\n")
	responseText.WriteString(fmt.Sprintf("• API Calls: %d\n", metrics.GetMatrixAPICalls()))
	responseText.WriteString(fmt.Sprintf("• API Errors: %d\n", metrics.GetMatrixAPIErrors()))
	responseText.WriteString(fmt.Sprintf("• API Retries: %d\n\n", metrics.GetMatrixAPIRetries()))

	// Latency metrics
	avgLatency := metrics.GetMessageLatencyAvg()
	if avgLatency > 0 {
		responseText.WriteString("**Latency**\n")
		responseText.WriteString(fmt.Sprintf("• Message Latency (avg): %.1fms\n", avgLatency))
		responseText.WriteString(fmt.Sprintf("• Message Latency (max): %dms\n", metrics.GetMessageLatencyMax()))
		apiAvgLatency := metrics.GetAPILatencyAvg()
		if apiAvgLatency > 0 {
			responseText.WriteString(fmt.Sprintf("• API Latency (avg): %.1fms\n", apiAvgLatency))
			responseText.WriteString(fmt.Sprintf("• API Latency (max): %dms\n", metrics.GetAPILatencyMax()))
		}
		responseText.WriteString("\n")
	}

	// Error breakdown
	errorsByType := metrics.GetErrorsByType()
	if len(errorsByType) > 0 {
		responseText.WriteString("**Errors by Type**\n")
		for errorType, count := range errorsByType {
			responseText.WriteString(fmt.Sprintf("• %s: %d\n", errorType, count))
		}
		responseText.WriteString("\n")
	}

	// Uptime
	responseText.WriteString(fmt.Sprintf("**Uptime:** %.1f seconds\n", metrics.GetUptime().Seconds()))

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

func (c *Handler) executeMigrateCommand(args *model.CommandArgs) *model.CommandResponse {
	if fields := strings.Fields(args.Command); len(fields) > 2 && fields[2] == "ghosts-to-mirror" {
		return c.executeGhostMigration(len(fields) > 3 && fields[3] == "confirm")
	}
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

// executeHistoryCommand previews or starts importing the channel's past
// messages. Without "confirm" it only reports what would be imported.
func (c *Handler) executeHistoryCommand(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	confirm := false
	limit := 0
	for _, f := range strings.Fields(args.Command)[2:] {
		switch {
		case f == "confirm":
			confirm = true
		case strings.HasPrefix(f, "limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(f, "limit="))
			if err != nil || n <= 0 {
				return reply("❌ `limit=` needs a positive number, for example `/matrix history confirm limit=500`.")
			}
			limit = n
		default:
			return reply("Usage: `/matrix history [confirm] [limit=N]`. Without `confirm` it only shows what would be imported.")
		}
	}

	status := c.plugin.ChannelHistoryStatus(args.ChannelId)
	if status.Running {
		return reply(fmt.Sprintf("🔄 **History import is running** for this channel: %d imported, %d skipped, %d failed so far. Run `/matrix history` again later.", status.Imported, status.Skipped, status.Failed))
	}

	plan, err := c.plugin.PlanChannelHistory(args.ChannelId, limit)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	if !confirm {
		return reply(historyPreview(plan, status))
	}
	if plan.Pending == 0 {
		return reply("ℹ️ Nothing to import: every message in this channel is already in the room.")
	}
	if err := c.plugin.StartChannelHistory(args.ChannelId, limit); err != nil {
		return reply("❌ " + err.Error())
	}
	return reply(fmt.Sprintf("🔄 **History import started**: %d messages, oldest first, as their authors and with their original times. It runs in the background and can be repeated if it stops. Run `/matrix history` to see progress.", plan.Pending))
}

func historyPreview(plan, last HistoryReport) string {
	var sb strings.Builder
	sb.WriteString("📜 **History import preview**\n\n")
	if plan.Pending == 0 {
		sb.WriteString("Every message in this channel is already in its Matrix room.")
	} else {
		sb.WriteString(fmt.Sprintf("%d messages would be imported, oldest first (%s to %s), as their authors and with their original times.",
			plan.Pending, time.UnixMilli(plan.Oldest).UTC().Format("2006-01-02"), time.UnixMilli(plan.Newest).UTC().Format("2006-01-02")))
		if plan.Files > 0 {
			sb.WriteString(fmt.Sprintf("\n\n%d attachments are uploaded with them (files above the size limit are skipped), and reactions are replayed with their original times.", plan.Files))
		}
		if plan.Truncated {
			sb.WriteString("\n\nThis channel has more history than one run examines; run it again after it finishes.")
		}
		sb.WriteString("\n\nRun `/matrix history confirm` to start. Add `limit=N` to import only the oldest N.")
	}
	if last.Imported+last.Failed > 0 {
		sb.WriteString(fmt.Sprintf("\n\nLast run: %d imported, %d skipped, %d failed.", last.Imported, last.Skipped, last.Failed))
		if last.Error != "" {
			sb.WriteString(" Stopped: " + last.Error)
		}
	}
	return sb.String()
}

// titleCase upper-cases the first letter of every space-separated word.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
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

// executeLinkCommand issues a one-time code the user redeems with the bridge
// bot in Matrix ("!mattermost link <code>").
func (c *Handler) executeLinkCommand(args *model.CommandArgs) *model.CommandResponse {
	if matrixUserID, puppeted, ok := c.plugin.GetIdentityLink(args.UserId); ok {
		mode := "Your Mattermost messages are sent by a ghost user; send `!mattermost login <access_token>` to the bridge bot to post as your own account."
		if puppeted {
			mode = "Your Mattermost messages are sent as that account."
		}
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         fmt.Sprintf("✅ Already linked to `%s`. %s Use `/matrix unlink` to remove the link.", matrixUserID, mode),
		}
	}

	code, err := c.plugin.CreateIdentityLinkCode(args.UserId)
	if err != nil {
		c.client.Log.Error("Failed to create identity link code", "error", err, "user_id", args.UserId)
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ Could not create a link code. Check plugin logs for details.",
		}
	}

	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text: fmt.Sprintf("🔗 **Link your Matrix account**\n\n"+
			"1. In your Matrix client, start a direct message with the bridge bot `@%s:<your Matrix server>`.\n"+
			"2. Send it: `!mattermost link %s`\n\n"+
			"The code expires in 15 minutes. Afterwards your Matrix messages in bridged rooms post as you here, "+
			"and `!mattermost login <access_token>` lets your Mattermost messages appear from your Matrix account.",
			matrix.BridgeBotLocalpart, code),
	}
}

// executeUnlinkCommand removes the user's identity link.
func (c *Handler) executeUnlinkCommand(args *model.CommandArgs) *model.CommandResponse {
	if err := c.plugin.UnlinkIdentity(args.UserId); err != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "❌ " + err.Error(),
		}
	}
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         "✅ Your Matrix account has been unlinked. Your messages will be bridged through a ghost user again.",
	}
}

// channelEncryptionStatus tells whether this channel's room is encrypted and
// what that means for the bridge.
func (c *Handler) channelEncryptionStatus(channelID string) string {
	matrixClient := c.plugin.GetMatrixClient()
	if matrixClient == nil {
		return ""
	}
	room, err := c.kvstore.Get(kvstore.BuildChannelMappingKey(channelID))
	if err != nil || len(room) == 0 {
		return ""
	}
	roomID := string(room)
	if strings.HasPrefix(roomID, "#") {
		resolved, err := matrixClient.ResolveRoomAlias(roomID)
		if err != nil {
			return ""
		}
		roomID = resolved
	}
	if encrypted, err := matrixClient.IsRoomEncrypted(roomID); err != nil || !encrypted {
		return ""
	}
	if c.plugin.EncryptionSupported() {
		return "\n- 🔒 This room is end-to-end encrypted; the bridge encrypts and decrypts its messages (see docs/ENCRYPTION.md)."
	}
	return "\n- 🔒 **This room is end-to-end encrypted, which the bridge cannot read or send into: messages are not synced either way.** See docs/ENCRYPTION.md."
}

// accountModeStatus reports how Mattermost users appear in Matrix.
func (c *Handler) accountModeStatus() string {
	st := c.plugin.MirrorStatus()
	var sb strings.Builder
	if st.Mode == "mirror" {
		sb.WriteString(fmt.Sprintf("\n- Accounts: **mirror mode**, %d mirrored account(s), %d claimed by their people", st.Accounts, st.Claimed))
		if st.Collisions > 0 {
			sb.WriteString(fmt.Sprintf("\n- ⚠️ %d account(s) use a suffixed name because the natural name was already taken on the homeserver", st.Collisions))
		}
		if !st.ClaimSupported {
			sb.WriteString("\n- People can claim an account through shared login; set an admin token to also let `/matrix claim` hand out a one-time password")
		}
	} else {
		sb.WriteString("\n- Accounts: **ghost users** (`@_mattermost_<id>`)")
	}
	if st.Cutover {
		sb.WriteString("\n- 🔒 **Cut-over mode**: accounts and rooms are frozen; no new ones are created")
	}
	if st.Pinned {
		want := "ghost users"
		if st.ConfiguredMirror {
			want = "mirror mode"
		}
		sb.WriteString(fmt.Sprintf("\n- ⚠️ The setting asks for %s, but accounts already exist in the other mode, so it is ignored. Use `/matrix migrate ghosts-to-mirror` to change modes", want))
	}
	return sb.String()
}

// executeClaimCommand lets a person take over their mirrored account.
func (c *Handler) executeClaimCommand(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	if c.plugin.MirrorStatus().Mode != "mirror" {
		return reply("ℹ️ Claiming applies to mirror mode, where every Mattermost user has a Matrix account of their own. Use `/matrix link` to connect an existing Matrix account instead.")
	}
	matrixUserID, password, err := c.plugin.ClaimMirrorAccount(args.UserId)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	if password == "" {
		return reply(fmt.Sprintf("✅ `%s` is now yours. Log in to Matrix with your single sign-on account; the bridge keeps posting as you from Mattermost.", matrixUserID))
	}
	return reply(fmt.Sprintf("✅ `%s` is now yours.\n\nOne-time password (shown once, change it after your first login): `%s`\n\nThe bridge keeps posting as you from Mattermost.", matrixUserID, password))
}

// executeCallCommand starts a call that people on both sides can join.
func (c *Handler) executeCallCommand(args *model.CommandArgs, title string) *model.CommandResponse {
	joinURL, err := c.plugin.StartCall(args.UserId, args.ChannelId, title)
	if err != nil {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
	}
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         fmt.Sprintf("📞 The call is announced in the channel and in the Matrix room. Join: %s", joinURL),
	}
}

// executeGhostMigration moves ghost users to mirror accounts.
func (c *Handler) executeGhostMigration(confirm bool) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	r, err := c.plugin.MigrateGhostsToMirror(confirm)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	if r.DryRun {
		return reply(fmt.Sprintf("**Dry run.** %d user(s) with a ghost would move to mirror accounts and %d room membership(s) would be re-created; %d user(s) post as their own linked account and stay as they are.\n\n"+
			"Run `/matrix migrate ghosts-to-mirror confirm` to do it. It can be repeated if something fails.", r.Users-r.Skipped, r.Rooms, r.Skipped))
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Migration finished:** %d moved, %d skipped, %d failed.", r.Moved, r.Skipped, r.Failed))
	for _, e := range r.Errors {
		sb.WriteString("\n- " + e)
	}
	if r.Switched {
		sb.WriteString("\n\n✅ The bridge is now in mirror mode.")
	} else {
		sb.WriteString("\n\n⚠️ Some users were not moved, so the mode was not switched yet. Run the command again after fixing the cause.")
	}
	return reply(sb.String())
}

// executeMigrateTeamCommand runs one cut-over step for the channels of the
// current team. Changes only happen with "confirm".
func (c *Handler) executeMigrateTeamCommand(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return reply("Usage: `/matrix migrate-team report|readonly|writable|archive [confirm]`. Without `confirm` it only shows what would change.")
	}
	confirm := len(fields) > 3 && fields[3] == "confirm"
	text, err := c.plugin.MigrateTeam(args.UserId, args.TeamId, fields[2], confirm)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	return reply(text)
}

// deliveryStatus warns when Matrix events were set aside after every retry.
func (c *Handler) deliveryStatus() string {
	n := c.plugin.DeadLetters()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("\n- ⚠️ **%d changes could not be delivered across the bridge** and were set aside. Run `/matrix retry` to try them again; the log says why they failed.", n)
}
