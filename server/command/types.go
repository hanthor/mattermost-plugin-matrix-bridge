package command

import (
	"time"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
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
	// CrossSignBridgeDevice verifies the bridge bot's device and returns the
	// recovery key, to be shown once and not stored.
	CrossSignBridgeDevice(userID string) (string, error)
	// EncryptionBackupKey returns the key that protects the bridge's key
	// backup on the homeserver, for an administrator to keep elsewhere.
	EncryptionBackupKey(userID string) (string, error)

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
	// ImportMatrixHistory is the other direction: it copies the room's past
	// messages into the channel (preview, or with confirm start in the
	// background) and returns what to tell the person.
	ImportMatrixHistory(userID, channelID string, limit int, confirm bool) (string, error)
	StartChannelHistory(channelID string, limit int) error
	ChannelHistoryStatus(channelID string) HistoryReport

	// MigrateTeam runs a cut-over step for the bridged channels of a team:
	// report, readonly, writable or archive (see docs/COMMANDS.md). The
	// returned text is shown to the administrator.
	MigrateTeam(userID, teamID, action string, confirm bool) (string, error)

	// EraseUser previews, or with confirm carries out, redacting a person's
	// bridged messages and removing their bridge account.
	EraseUser(adminID, username string, confirm bool) (string, error)

	// WelcomeAccounts tells people with an unclaimed mirror account their
	// Matrix ID and brings their direct-message and favourite rooms over.
	WelcomeAccounts(userID string, confirm bool) (string, error)

	// DirectMessages handles /matrix dm: action is on, off or status.
	DirectMessages(userID, action string) (string, error)

	// DeadLetters is how many Matrix events failed delivery and were set
	// aside; RetryDeadLetters queues them again and returns how many.
	DeadLetters() int
	RetryDeadLetters() (int, error)

	// RepairMembership makes the channel's room match the channel's members
	// (for the bridge's own accounts) and describes what it changed.
	RepairMembership(channelID string) (string, error)
	// InviteMatrixUser invites a Matrix user to the channel's room.
	InviteMatrixUser(channelID, matrixUserID string) (string, error)

	// Doctor runs the setup checks (and the channel's checks) and returns the report.
	Doctor(channelID string) string

	// Identity linking (one person on both networks)
	CreateIdentityLinkCode(mattermostUserID string) (string, error)
	GetIdentityLink(mattermostUserID string) (matrixUserID string, puppeted bool, ok bool)
	UnlinkIdentity(mattermostUserID string) error

	// BackfillStructure brings the structure of every bridged channel's room
	// up to date and returns how many channels it went through.
	BackfillStructure() int

	// Backfill synchronization
	SyncUser(userID string) error
	SyncTeam(teamID string) error
	SyncChannel(channelID string) error
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
