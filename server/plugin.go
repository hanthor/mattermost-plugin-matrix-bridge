package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/logr/v2"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/command"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/pkg/errors"
)

const (
	// DefaultMaxProfileImageSize is the default maximum size for profile images (6MB)
	DefaultMaxProfileImageSize = 6 * 1024 * 1024
	// DefaultMaxFileSize is the default maximum size for file attachments (50MB)
	DefaultMaxFileSize = 50 * 1024 * 1024
)

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// kvstore is the client used to read/write KV records for this plugin.
	kvstore kvstore.KVStore

	// client is the Mattermost server API client.
	client *pluginapi.Client

	// commandClient is the client used to register and execute slash commands.
	commandClient command.Command

	// matrixClient is the client used to communicate with Matrix servers.
	matrixClient *matrix.Client

	// postTracker tracks post creation timestamps to detect redundant edits
	postTracker *PostTracker

	// pendingFiles tracks uploaded files awaiting their posts
	pendingFiles *PendingFileTracker

	// metrics collects telemetry and performance data
	metrics *Metrics

	// remoteID is the identifier returned by RegisterPluginForSharedChannels
	remoteID string

	backgroundJob *cluster.Job

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	// Logr instance specifically for logging Matrix transactions.
	transactionLogger logr.Logger

	// logger is the main logger for the plugin
	logger Logger

	// maxProfileImageSize is the maximum size for profile images in bytes
	maxProfileImageSize int64

	// maxFileSize is the maximum size for file attachments in bytes
	maxFileSize int64

	// Bridge components for dependency injection architecture
	mattermostToMatrixBridge *MattermostToMatrixBridge
	matrixToMattermostBridge *MatrixToMattermostBridge
}

// OnActivate is invoked when the plugin is activated. If an error is returned, the plugin will be deactivated.
func (p *Plugin) OnActivate() error {
	var err error
	p.transactionLogger, err = CreateTransactionLogger()
	if err != nil {
		return errors.Wrap(err, "failed to create transaction logger")
	}

	p.client = pluginapi.NewClient(p.API, p.Driver)

	// Initialize the logger
	p.logger = NewPluginAPILogger(p.API)

	p.kvstore = kvstore.NewKVStore(p.client)

	p.postTracker = NewPostTracker(DefaultPostTrackerMaxEntries)
	p.pendingFiles = NewPendingFileTracker()
	p.metrics = NewMetrics()

	// Initialize file size limits with default values
	p.maxProfileImageSize = DefaultMaxProfileImageSize
	p.maxFileSize = DefaultMaxFileSize

	p.initMatrixClient()

	// Run KV store migrations before initializing bridges
	if err := p.runKVStoreMigrations(); err != nil {
		return errors.Wrap(err, "failed to run KV store migrations")
	}

	// Register for shared channels first to get remote ID
	if err := p.registerForSharedChannels(); err != nil {
		p.logger.LogWarn("Failed to register for shared channels", "error", err)
	}

	// Initialize bridge components after getting remote ID
	p.initBridges()

	p.commandClient = command.NewCommandHandler(p)

	job, err := cluster.Schedule(
		p.API,
		"BackgroundJob",
		cluster.MakeWaitForRoundedInterval(1*time.Hour),
		p.runJob,
	)
	if err != nil {
		return errors.Wrap(err, "failed to schedule background job")
	}

	p.backgroundJob = job

	return nil
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	if p.backgroundJob != nil {
		if err := p.backgroundJob.Close(); err != nil {
			p.logger.LogError("Failed to close background job", "err", err)
		}
	}
	return nil
}

// ExecuteCommand executes the commands that were registered in the NewCommandHandler function.
func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response, err := p.commandClient.Handle(args)
	if err != nil {
		return nil, model.NewAppError("ExecuteCommand", "plugin.command.execute_command.app_error", nil, err.Error(), http.StatusInternalServerError)
	}
	return response, nil
}

func (p *Plugin) initMatrixClient() {
	config := p.getConfiguration()
	rateLimitMode := matrix.ParseRateLimitingMode(config.RateLimitingMode)
	rateLimitConfig := matrix.GetRateLimitConfigByMode(rateLimitMode)
	p.matrixClient = matrix.NewClientWithRateLimit(config.MatrixServerURL, config.MatrixASToken, p.remoteID, p.API, rateLimitConfig)
}

func (p *Plugin) initBridges() {
	// Create shared utilities
	sharedUtils := NewBridgeUtils(BridgeUtilsConfig{
		Logger:              p.logger,
		API:                 p.API,
		KVStore:             p.kvstore,
		MatrixClient:        p.matrixClient,
		RemoteID:            p.remoteID,
		MaxProfileImageSize: p.maxProfileImageSize,
		MaxFileSize:         p.maxFileSize,
		ConfigGetter:        p,
		Metrics:             p.metrics,
	})

	// Create bridge instances
	p.mattermostToMatrixBridge = NewMattermostToMatrixBridge(sharedUtils, p.pendingFiles, p.postTracker)
	p.matrixToMattermostBridge = NewMatrixToMattermostBridge(sharedUtils)
}

func (p *Plugin) registerForSharedChannels() error {
	// Get the bot user ID or use a system admin
	botUser, err := p.API.GetUserByUsername("mattermost-bridge")
	var creatorID string
	if err != nil {
		// Fallback to getting any system admin
		users, err2 := p.API.GetUsers(&model.UserGetOptions{
			Page:    0,
			PerPage: 1,
		})
		if err2 != nil || len(users) == 0 {
			return errors.New("failed to find a valid creator user")
		}
		creatorID = users[0].Id
	} else {
		creatorID = botUser.Id
	}

	opts := model.RegisterPluginOpts{
		Displayname:  "Matrix_Bridge",
		PluginID:     "com.mattermost.plugin-matrix-bridge",
		CreatorID:    creatorID,
		AutoShareDMs: false,
		AutoInvited:  false,
	}

	remoteID, appErr := p.API.RegisterPluginForSharedChannels(opts)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to register plugin for shared channels")
	}

	// Store the remote ID for use in sync operations
	p.remoteID = remoteID

	p.logger.LogInfo("Successfully registered plugin for shared channels", "remote_id", remoteID)
	return nil
}

// PluginAccessor interface implementation for command handlers

// GetMatrixClient returns the Matrix client instance
func (p *Plugin) GetMatrixClient() *matrix.Client {
	return p.matrixClient
}

// GetKVStore returns the KV store instance
func (p *Plugin) GetKVStore() kvstore.KVStore {
	return p.kvstore
}

// GetConfiguration returns the plugin configuration
func (p *Plugin) GetConfiguration() command.Configuration {
	return p.getConfiguration()
}

// GetMetrics returns the metrics collector wrapped to satisfy the command interface
func (p *Plugin) GetMetrics() interface {
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
} {
	return p.metrics
}

// CreateOrGetGhostUser gets an existing ghost user or creates a new one for a Mattermost user
func (p *Plugin) CreateOrGetGhostUser(mattermostUserID string) (string, error) {
	return p.mattermostToMatrixBridge.CreateOrGetGhostUser(mattermostUserID)
}

// GetMatrixUserIDFromMattermostUser looks up the original Matrix user ID for a remote Mattermost user
func (p *Plugin) GetMatrixUserIDFromMattermostUser(mattermostUserID string) (string, error) {
	return p.mattermostToMatrixBridge.GetMatrixUserIDFromMattermostUser(mattermostUserID)
}

// GetPluginAPI returns the Mattermost plugin API
func (p *Plugin) GetPluginAPI() plugin.API {
	return p.API
}

// GetPluginAPIClient returns the pluginapi client
func (p *Plugin) GetPluginAPIClient() *pluginapi.Client {
	return p.client
}

// GetRemoteID returns the plugin's remote ID for shared channel operations
func (p *Plugin) GetRemoteID() string {
	return p.remoteID
}

// RunKVStoreMigrations exposes migration functionality to command handlers
func (p *Plugin) RunKVStoreMigrations() error {
	return p.runKVStoreMigrations()
}

// RunKVStoreMigrationsWithResults exposes migration functionality to command handlers and returns detailed results
func (p *Plugin) RunKVStoreMigrationsWithResults() (*command.MigrationResult, error) {
	result, err := p.runKVStoreMigrationsWithResults()
	if err != nil {
		return nil, err
	}

	// Convert from internal MigrationResult to command.MigrationResult
	return &command.MigrationResult{
		UserMappingsCreated:      result.UserMappingsCreated,
		ChannelMappingsCreated:   result.ChannelMappingsCreated,
		RoomMappingsCreated:      result.RoomMappingsCreated,
		DMMappingsCreated:        result.DMMappingsCreated,
		ReverseDMMappingsCreated: result.ReverseDMMappingsCreated,
	}, nil
}

// UserHasJoinedChannel is called when a user joins or is added to a channel
func (p *Plugin) UserHasJoinedChannel(_ *plugin.Context, channelMember *model.ChannelMember, actor *model.User) {
	config := p.getConfiguration()
	if !config.EnableSync {
		return
	}

	if p.matrixClient == nil {
		p.logger.LogError("Matrix client not initialized")
		return
	}

	// First check if this channel is bridged to Matrix
	matrixRoomID, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelMember.ChannelId)
	if err != nil || matrixRoomID == "" {
		// Channel is not bridged to Matrix, nothing to do
		p.logger.LogDebug("Channel not bridged to Matrix, skipping user join sync", "channel_id", channelMember.ChannelId)
		return
	}

	// Get the user who joined the channel
	// If the actor is the same as the user who joined, use the provided actor to avoid API call
	var user *model.User
	if actor != nil && actor.Id == channelMember.UserId {
		user = actor
	} else {
		var appErr *model.AppError
		user, appErr = p.API.GetUser(channelMember.UserId)
		if appErr != nil {
			// Log the failure with context about both fallback methods
			if actor == nil {
				p.logger.LogError("Failed to get user who joined channel - no actor provided and GetUser API call failed",
					"error", appErr,
					"user_id", channelMember.UserId,
					"channel_id", channelMember.ChannelId,
					"troubleshooting", "both actor parameter and GetUser API call failed")
			} else {
				p.logger.LogError("Failed to get user who joined channel - actor provided but user ID mismatch, GetUser API call also failed",
					"error", appErr,
					"user_id", channelMember.UserId,
					"actor_id", actor.Id,
					"channel_id", channelMember.ChannelId,
					"troubleshooting", "actor user ID did not match channel member user ID, and GetUser API call failed")
			}
			return
		}
	}

	p.logger.LogDebug("User joined bridged channel",
		"user_id", user.Id,
		"username", user.Username,
		"channel_id", channelMember.ChannelId,
		"matrix_room_id", matrixRoomID,
		"is_remote", user.IsRemote())

	// If this is a Matrix-originated user (remote), invite them to the corresponding Matrix room
	if user.IsRemote() {
		if err := p.inviteRemoteUserToMatrixRoom(user, channelMember.ChannelId); err != nil {
			p.logger.LogError("Failed to invite remote user to Matrix room", "error", err, "user_id", user.Id, "username", user.Username, "channel_id", channelMember.ChannelId)
		}
	} else {
		// This is a local Mattermost user - create ghost user and join them to the Matrix room
		ghostUserID, err := p.CreateOrGetGhostUser(user.Id)
		if err != nil {
			p.logger.LogError("Failed to create or get ghost user", "error", err, "user_id", user.Id, "username", user.Username)
			return
		}

		// Resolve room alias to room ID if needed
		resolvedRoomID, err := p.matrixClient.ResolveRoomAlias(matrixRoomID)
		if err != nil {
			p.logger.LogError("Failed to resolve Matrix room identifier", "error", err, "room_identifier", matrixRoomID)
			return
		}

		// Try to join the ghost user to the Matrix room (handles both public and private rooms)
		if err := p.matrixClient.InviteAndJoinGhostUser(resolvedRoomID, ghostUserID); err != nil {
			p.logger.LogError("Failed to join ghost user to Matrix room", "error", err, "ghost_user_id", ghostUserID, "room_id", resolvedRoomID, "mattermost_user_id", user.Id)
		} else {
			p.logger.LogInfo("Successfully joined ghost user to Matrix room", "ghost_user_id", ghostUserID, "room_id", resolvedRoomID, "mattermost_user_id", user.Id, "username", user.Username)
		}
	}
}

// UserHasBeenCreated is called when a new user is created in Mattermost
func (p *Plugin) UserHasBeenCreated(_ *plugin.Context, user *model.User) {
	if user.IsBot {
		return
	}

	config := p.getConfiguration()
	if !config.EnableSync {
		return
	}

	p.logger.LogDebug("User created, ensuring Matrix ghost user exists", "user_id", user.Id, "username", user.Username)
	if _, err := p.CreateOrGetGhostUser(user.Id); err != nil {
		p.logger.LogError("Failed to create ghost user for new user", "user_id", user.Id, "username", user.Username, "error", err)
	}
}

// UserHasUpdated is called when a user is updated in Mattermost
func (p *Plugin) UserHasUpdated(_ *plugin.Context, user *model.User, _ *model.User) {
	if user.IsBot {
		return
	}

	config := p.getConfiguration()
	if !config.EnableSync {
		return
	}

	p.logger.LogDebug("User updated, syncing to Matrix", "user_id", user.Id, "username", user.Username)
	if err := p.mattermostToMatrixBridge.SyncUserToMatrix(user); err != nil {
		p.logger.LogError("Failed to sync user update to Matrix", "user_id", user.Id, "username", user.Username, "error", err)
	}
}

// ChannelHasBeenCreated is called when a new channel is created in Mattermost
func (p *Plugin) ChannelHasBeenCreated(_ *plugin.Context, channel *model.Channel) {
	config := p.getConfiguration()
	if !config.EnableSync || p.matrixClient == nil {
		return
	}

	// We support bridging Public (Open) and Private channels
	isPublic := channel.Type == model.ChannelTypeOpen
	if !isPublic && channel.Type != model.ChannelTypePrivate {
		// Ignore DMs, GM, etc. for now, only handle standard channels
		return
	}

	p.logger.LogInfo("New channel created, creating Matrix room", "channel_id", channel.Id, "channel_name", channel.Name, "type", channel.Type)

	// Extract Server Domain from MatrixServerURL
	serverDomain := "example.com"
	if u, err := url.Parse(config.MatrixServerURL); err == nil {
		host := u.Host
		if strings.Contains(host, ":") {
			host = strings.Split(host, ":")[0]
		}
		serverDomain = host
	}

	// Prepare room details
	roomName := channel.DisplayName
	if roomName == "" {
		roomName = channel.Name
	}
	topic := channel.Header
	if topic == "" {
		topic = channel.Purpose
	}

	// Create the Matrix room
	// Note: isPublic flag controls visibility and presets (PublicChat vs PrivateChat)
	roomID, err := p.matrixClient.CreateRoom(roomName, topic, serverDomain, isPublic, channel.Id)
	if err != nil {
		p.logger.LogError("Failed to create Matrix room for new channel", "channel_name", channel.Name, "error", err)
		return
	}

	p.logger.LogInfo("Created Matrix room for new channel", "room_id", roomID, "channel_name", channel.Name)

	// Automatically map the created room to this channel (both directions)
	mappingKey := kvstore.BuildChannelMappingKey(channel.Id)
	if err := p.kvstore.Set(mappingKey, []byte(roomID)); err != nil {
		p.logger.LogError("Failed to save channel mapping", "error", err, "channel_id", channel.Id, "room_id", roomID)
	}

	// Store reverse mapping: room_mapping_<roomID> -> channelID
	roomMappingKey := kvstore.BuildRoomMappingKey(roomID)
	if err := p.kvstore.Set(roomMappingKey, []byte(channel.Id)); err != nil {
		p.logger.LogError("Failed to save reverse room mapping", "error", err, "channel_id", channel.Id, "room_id", roomID)
	}

	// If the channel belongs to a team, add it to the team's Matrix space
	if channel.TeamId != "" {
		// Look up Space ID for the team
		spaceIDBytes, err := p.kvstore.Get(kvstore.BuildTeamMappingKey(channel.TeamId))
		if err == nil && spaceIDBytes != nil {
			spaceID := string(spaceIDBytes)
			if err := p.matrixClient.AddSpaceChild(spaceID, roomID, []string{serverDomain}); err != nil {
				p.logger.LogError("Failed to add room to team space", "error", err, "space_id", spaceID, "room_id", roomID)
			} else {
				p.logger.LogDebug("Added room to team space", "space_id", spaceID, "room_id", roomID)
			}
		}
	}
}

// TeamHasBeenCreated is called when a new team is created in Mattermost
func (p *Plugin) TeamHasBeenCreated(_ *plugin.Context, team *model.Team) {
	config := p.getConfiguration()
	if !config.EnableSync || p.matrixClient == nil {
		return
	}

	p.logger.LogInfo("New team created, creating Matrix space", "team_id", team.Id, "team_name", team.Name)

	// Create Matrix Space
	// Note: CreateSpace automatically handles server domain for alias creation
	spaceID, err := p.matrixClient.CreateSpace(team.DisplayName, team.Description, team.Name)
	if err != nil {
		p.logger.LogError("Failed to create Matrix space for team", "team_name", team.Name, "error", err)
		return
	}

	p.logger.LogInfo("Created Matrix space for new team", "space_id", spaceID, "team_name", team.Name)

	// Save mapping
	if err := p.kvstore.Set(kvstore.BuildTeamMappingKey(team.Id), []byte(spaceID)); err != nil {
		p.logger.LogError("Failed to save team mapping", "error", err, "team_id", team.Id, "space_id", spaceID)
	}
}

// SyncUser syncs a Mattermost user to Matrix
func (p *Plugin) SyncUser(userID string) error {
	user, err := p.API.GetUser(userID)
	if err != nil {
		return errors.Wrap(err, "failed to get user")
	}

	if _, err := p.CreateOrGetGhostUser(userID); err != nil {
		return errors.Wrap(err, "failed to ensure ghost user")
	}

	if err := p.mattermostToMatrixBridge.SyncUserToMatrix(user); err != nil {
		return errors.Wrap(err, "failed to sync user profile")
	}

	return nil
}

// SyncTeam syncs a Mattermost team to Matrix (Space)
func (p *Plugin) SyncTeam(teamID string) error {
	team, err := p.API.GetTeam(teamID)
	if err != nil {
		return errors.Wrap(err, "failed to get team")
	}

	p.TeamHasBeenCreated(nil, team)
	return nil
}

// SyncChannel syncs a Mattermost channel to Matrix (Room)
func (p *Plugin) SyncChannel(channelID string) error {
	channel, err := p.API.GetChannel(channelID)
	if err != nil {
		return errors.Wrap(err, "failed to get channel")
	}

	p.ChannelHasBeenCreated(nil, channel)
	return nil
}

// See https://developers.mattermost.com/extend/plugins/server/reference/
