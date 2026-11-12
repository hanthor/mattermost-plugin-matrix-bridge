package main

import (
	"reflect"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/pkg/errors"
)

// DefaultMatrixUsernamePrefix is the default username prefix for Matrix-originated users
const DefaultMatrixUsernamePrefix = "matrix"

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	MatrixServerURL           string `json:"matrix_server_url"`
	MatrixASToken             string `json:"matrix_as_token"`
	MatrixHSToken             string `json:"matrix_hs_token"`
	MetricsToken              string `json:"metrics_token"` // read-only credential for /metrics; falls back to the homeserver token when empty
	EnableSync                bool   `json:"enable_sync"`
	EnableDirectMessages      bool   `json:"enable_direct_messages"`       // Bridge direct and group messages whose participants all agreed
	EnableEncryption          bool   `json:"enable_encryption"`            // Bridge end-to-end encrypted Matrix rooms (bridge bot holds the keys)
	EncryptDirectRooms        bool   `json:"encrypt_direct_rooms"`         // Create bridged direct and group message rooms encrypted (needs EnableEncryption)
	EnableRealtime            bool   `json:"enable_realtime"`              // Send typing, presence, custom status and read receipts to Matrix
	InviteCreatesChannelsTeam string `json:"invite_creates_channels_team"` // Team in which inviting the bot to a room creates a channel; empty = off
	RealtimePollSeconds       int    `json:"realtime_poll_seconds"`        // How often status and read state are checked (default 30)
	MatrixUsernamePrefix      string `json:"matrix_username_prefix"`
	RateLimitingMode          string `json:"rate_limiting_mode"`
	EnableMirrorMode          bool   `json:"enable_mirror_mode"`   // Hijack Matrix server to mirror Mattermost
	MatrixServerDomain        string `json:"matrix_server_domain"` // e.g. "synapse" for @user:synapse
	MirrorModePassword        string `json:"mirror_mode_password"` // Legacy: one shared password for every mirrored account
	HistoryIncludeBots        bool   `json:"history_include_bots"` // /matrix history also imports what bots and integrations posted
	WelcomeEmail              bool   `json:"welcome_email"`        // /matrix welcome also emails the person their Matrix ID
	AutoLinkByEmail           bool   `json:"auto_link_by_email"`   // Link a Matrix person to the Mattermost account with the same verified email
	MatrixAdminToken          string `json:"matrix_admin_token"`   // Synapse admin token; lets /matrix claim set a one-time password
	// Matrix Authentication Service: when the homeserver delegates sign-in to
	// MAS, mirror accounts are also created there and linked to the person's
	// identity at the shared OpenID Connect provider.
	MASURL                string `json:"mas_url"`
	MASAdminClientID      string `json:"mas_admin_client_id"`
	MASAdminClientSecret  string `json:"mas_admin_client_secret"`
	MASUpstreamProviderID string `json:"mas_upstream_provider_id"`
	CallsMode             string `json:"calls_mode"`         // off, presence (default) or element_call
	ElementCallURL        string `json:"element_call_url"`   // join URL template for calls both sides can join
	ElementURL            string `json:"element_url"`        // Element Web address used for "join in Element" links
	CutoverMode           bool   `json:"cutover_mode"`       // Freeze accounts and rooms: no new ones are created
	SyncUserProfiles      bool   `json:"sync_user_profiles"` // Sync display names and avatars

	// accountModePinned is set when EnableMirrorMode was overridden because
	// accounts already exist in the other mode.
	accountModePinned bool
}

// masConfigured reports whether the bridge can reach the MAS admin API.
func (c *configuration) masConfigured() bool {
	return c.MASURL != "" && c.MASAdminClientID != "" && c.MASAdminClientSecret != ""
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *configuration) Clone() *configuration {
	var clone = *c
	return &clone
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var configuration = new(configuration)

	// Shared Channels (Connected Workspaces) lets the bridge receive file
	// attachments and profile images through the sync framework. Without it the
	// bridge still works through the regular plugin hooks, so only warn.
	if cfg := p.API.GetConfig(); cfg != nil {
		enableSharedChannels := cfg.ConnectedWorkspacesSettings.EnableSharedChannels
		if enableSharedChannels == nil || !*enableSharedChannels {
			p.API.LogWarn("Shared Channels is not enabled; the Matrix bridge will run in hooks-only mode")
		}
	}

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	// Log non-secret mirror mode settings; the mirror mode password must never be logged.
	p.API.LogInfo("Configuration loaded",
		"enable_mirror_mode", configuration.EnableMirrorMode,
		"sync_user_profiles", configuration.SyncUserProfiles,
		"mirror_mode_password_set", configuration.MirrorModePassword != "",
		"admin_token_set", configuration.MatrixAdminToken != "",
		"mas_configured", configuration.masConfigured(),
		"matrix_server_domain", configuration.MatrixServerDomain,
	)

	// Validate required configuration
	if err := p.validateConfiguration(configuration); err != nil {
		return errors.Wrap(err, "invalid plugin configuration")
	}

	p.applyAccountModeLock(configuration)

	// Mattermost reports every server configuration change, not only this
	// plugin's. When no bridge setting changed, keep the client, bridges and
	// encryption device as they are.
	unchanged := p.matrixClient != nil && p.mattermostToMatrixBridge != nil && reflect.DeepEqual(p.getConfiguration(), configuration)
	p.setConfiguration(configuration)
	if unchanged {
		return nil
	}

	p.initMatrixClient()
	// The bridges hold the client they were built with; give them the new one
	// so a changed server URL or token takes effect and every part of the
	// plugin shares one client (and what it has learned about rooms).
	if p.mattermostToMatrixBridge != nil {
		p.initBridges()
		p.startEncryption()
		p.startRealtime()
	}

	return nil
}

// applyAccountModeLock keeps the account mode the bridge started with.
// Switching between ghosts and mirror accounts once accounts exist would give
// every user two Matrix identities and two sets of room memberships, so the
// mode recorded with the first account wins over the setting.
func (p *Plugin) applyAccountModeLock(cfg *configuration) {
	if p.kvstore == nil {
		return
	}
	recorded := effectiveAccountMode(p.kvstore)
	if recorded == "" {
		return
	}
	wantMirror := recorded == accountModeMirror
	if cfg.EnableMirrorMode == wantMirror {
		return
	}
	p.API.LogError("The mirror mode setting differs from the mode the existing Matrix accounts were created in; keeping the existing mode. Use /matrix migrate ghosts-to-mirror to change modes",
		"setting_enable_mirror_mode", cfg.EnableMirrorMode, "account_mode", recorded)
	cfg.EnableMirrorMode = wantMirror
	cfg.accountModePinned = true
}

// validateConfiguration checks that required configuration fields are present
func (p *Plugin) validateConfiguration(config *configuration) error {
	if config.EnableSync {
		if config.MatrixServerURL == "" {
			return errors.New("Matrix Server URL is required when sync is enabled")
		}
		if config.MatrixASToken == "" {
			return errors.New("Matrix Application Service Token is required when sync is enabled")
		}
		if config.MatrixHSToken == "" {
			return errors.New("Matrix Homeserver Token is required when sync is enabled")
		}
	}

	// Validate and normalize rate limiting mode using matrix package
	parsedMode := matrix.ParseRateLimitingMode(config.RateLimitingMode)
	config.RateLimitingMode = string(parsedMode)

	return nil
}

// GetMatrixServerURL implements the Configuration interface for command package
func (c *configuration) GetMatrixServerURL() string {
	return c.MatrixServerURL
}

// GetMatrixUsernamePrefix returns the username prefix to use for Matrix-originated users
func (c *configuration) GetMatrixUsernamePrefix() string {
	if c.MatrixUsernamePrefix == "" {
		return DefaultMatrixUsernamePrefix
	}
	return c.MatrixUsernamePrefix
}

// GetMatrixUsernamePrefixForServer returns the username prefix for a specific Matrix server
// This allows for future extensibility to support different prefixes per server
func (c *configuration) GetMatrixUsernamePrefixForServer(_ string) string {
	// For now, return the global prefix
	// In the future, this could check a map of server-specific prefixes
	return c.GetMatrixUsernamePrefix()
}

// GetEnableMirrorMode returns whether Mirror Mode is enabled
func (c *configuration) GetEnableMirrorMode() bool {
	return c.EnableMirrorMode
}
