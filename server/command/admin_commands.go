package command

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
)

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
	if mode != "users" && mode != "teams" && mode != "channels" && mode != "structure" && mode != "all" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Invalid mode. Available modes: users, teams, channels, structure, all",
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
		if mode == "structure" || mode == "all" {
			n := c.plugin.BackfillStructure()
			c.plugin.GetPluginAPI().LogInfo("Structure backfill finished", "channels", n)
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
