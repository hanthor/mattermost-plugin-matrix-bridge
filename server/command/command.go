// Package command implements slash command handlers for the Matrix Bridge plugin.
package command

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost/server/public/model"
)

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
	unknownSubcommandError    = "Unknown subcommand. Use: test, create, map, unmap, invite, list, status, or migrate"

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
	backfillCommandDesc  = "Backfill existing data to Matrix (users, teams, channels, room structure)"
	backfillCommandHint  = "[users|teams|channels|structure|all]"
	backfillCommandUsage = "Usage: /matrix backfill [users|teams|channels|structure|all]"
	backfillStarted      = "🔄 **Backfill started** for: %s. This may take a while. Check server logs for progress."
	backfillInvalidMode  = "❌ Invalid backfill mode. Use: users, teams, channels, structure, or all"
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
	backfillCmd.AddTextArgument("Mode", "[users|teams|channels|structure|all]", "")
	matrixData.AddCommand(backfillCmd)

	// Unmap command
	matrixData.AddCommand(model.NewAutocompleteData("unmap", unmapCommandHint, unmapCommandDesc))

	matrixData.AddCommand(model.NewAutocompleteData("list", "", listCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("status", "", statusCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("migrate", "", migrateCommandDesc))
	matrixData.AddCommand(model.NewAutocompleteData("repair", "", "Make this channel's room match the channel's members"))
	invite := model.NewAutocompleteData("invite", "@user:server", "Invite a Matrix user to this channel's room")
	invite.AddTextArgument("The Matrix user ID, for example @ada:example.org", "@user:server", "")
	matrixData.AddCommand(invite)
	matrixData.AddCommand(model.NewAutocompleteData("encryption-backup-key", "", "Show the key that protects the bridge's encryption key backup, to keep elsewhere"))
	matrixData.AddCommand(model.NewAutocompleteData("encryption-verify", "[confirm]", "Verify the bridge's encryption device so clients stop warning about it (shows a recovery key once)"))
	matrixData.AddCommand(model.NewAutocompleteData("doctor", "", "Check the bridge setup and this channel, with what to fix"))
	matrixData.AddCommand(model.NewAutocompleteData("retry", "", "Replay changes that could not be delivered across the bridge"))
	dmCmd := model.NewAutocompleteData("dm", "[on|off|status]", "Allow your direct messages to be bridged to Matrix (only if everyone in the conversation agrees)")
	dmCmd.AddStaticListArgument("action", false, []model.AutocompleteListItem{
		{Item: "on", HelpText: "Agree to bridge your direct and group messages"},
		{Item: "off", HelpText: "Stop bridging them"},
		{Item: "status", HelpText: "Show your setting"},
	})
	matrixData.AddCommand(dmCmd)
	matrixData.AddCommand(model.NewAutocompleteData("erase", "<username> [confirm]", "Redact a person's bridged messages in Matrix and remove their bridge account"))
	matrixData.AddCommand(model.NewAutocompleteData("welcome", "[confirm]", "Tell people with an unclaimed Matrix account their Matrix ID and bring over their direct messages and favourites"))
	teamCmd := model.NewAutocompleteData("migrate-team", "report|readonly|writable|archive [confirm]", "Cut a team over to Matrix: report, make channels read-only, archive")
	teamCmd.AddStaticListArgument("action", true, []model.AutocompleteListItem{
		{Item: "report", HelpText: "Compare members and messages per channel"},
		{Item: "readonly", HelpText: "Make bridged channels read-only with a pinned link to the room"},
		{Item: "writable", HelpText: "Undo readonly"},
		{Item: "archive", HelpText: "Archive the read-only channels"},
	})
	teamCmd.AddStaticListArgument("confirm", false, []model.AutocompleteListItem{{Item: "confirm", HelpText: "Apply the change"}})
	matrixData.AddCommand(teamCmd)
	historyCmd := model.NewAutocompleteData("history", "[from-matrix] [confirm] [limit=N]", "Import past messages: this channel into its room, or with from-matrix the room into this channel")
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
	case "erase":
		fields := strings.Fields(args.Command)
		if len(fields) < 3 {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "Usage: `/matrix erase <username> [confirm]`"}
		}
		text, err := c.plugin.EraseUser(args.UserId, fields[2], len(fields) > 3 && fields[3] == "confirm")
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	case "welcome":
		text, err := c.plugin.WelcomeAccounts(args.UserId, strings.HasSuffix(strings.TrimSpace(args.Command), " confirm"))
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	case "repair":
		text, err := c.plugin.RepairMembership(args.ChannelId)
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	case "invite":
		if len(fields) < 3 {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "Usage: `/matrix invite @user:server`"}
		}
		text, err := c.plugin.InviteMatrixUser(args.ChannelId, fields[2])
		if err != nil {
			return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: "❌ " + err.Error()}
		}
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	case "doctor":
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: c.plugin.Doctor(args.ChannelId)}
	case "encryption-verify":
		return c.executeEncryptionVerify(args)
	case "encryption-backup-key":
		return c.executeEncryptionBackupKey(args)
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
	case "map_user", "backfill", "migrate", "metrics", "history", "migrate-team", "welcome", "erase", "retry", "doctor", "encryption-verify", "encryption-backup-key":
		if !isSystemAdmin() {
			return deny("❌ Only system administrators can run `/matrix " + subcommand + "`.")
		}
	case "create", "map", "unmap", "join", "repair", "invite":
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
