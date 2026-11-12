package main

import (
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/secretbox"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// MattermostToMatrix-specific utility methods

// getGhostUser returns the bridge-controlled Matrix account of a Mattermost
// user if one exists: the mirror account in mirror mode, otherwise the ghost.
func (b *MattermostToMatrixBridge) getGhostUser(userID string) (string, bool) {
	if acct, ok := newMirrorStore(b.kvstore).Account(userID); ok {
		return acct.MatrixUserID, true
	}
	ghostUserKey := kvstore.BuildGhostUserKey(userID)

	// Check cache first
	if b.ghostUserCache != nil {
		if ghostUserID, found := b.ghostUserCache.Get(ghostUserKey); found {
			return ghostUserID, true
		}
	}

	// Fall back to KV store
	ghostUserIDBytes, err := b.kvstore.Get(ghostUserKey)
	if err == nil && len(ghostUserIDBytes) > 0 {
		ghostUserID := string(ghostUserIDBytes)
		// Populate cache
		if b.ghostUserCache != nil {
			b.ghostUserCache.Set(ghostUserKey, ghostUserID)
		}
		return ghostUserID, true
	}
	return "", false
}

// resolveSender decides which Matrix identity sends on behalf of a Mattermost
// user. A linked account with a stored token is used directly (double
// puppeting); otherwise the ghost (or mirror-mode account) is used through the
// appservice token.
func (b *MattermostToMatrixBridge) resolveSender(userID string) (matrixUserID string, client *matrix.Client, puppet bool, err error) {
	if link, ok := newIdentityStore(b.kvstore, func() *secretbox.Box {
		return secretbox.New(secretFor(b.API, b.configGetter.getConfiguration()), "identity-token")
	}).Link(userID); ok && link.Puppeted() {
		return link.MatrixUserID, b.matrixClient.AsPuppet(link.AccessToken), true, nil
	}
	matrixUserID, err = b.GetMatrixUserID(userID)
	if err != nil {
		return "", nil, false, err
	}
	return matrixUserID, b.matrixClient, false, nil
}

// ensureSenderInRoom makes sure the sending identity is in the room: puppets
// join with their own token, ghosts are invited and joined by the bot.
func (b *MattermostToMatrixBridge) ensureSenderInRoom(client *matrix.Client, matrixUserID, roomID, userID string) error {
	if !client.IsPuppet() {
		return b.ensureGhostUserInRoom(matrixUserID, roomID, userID)
	}
	// Kept apart from the ghost's key: unlinking must not make the ghost look
	// as though it were still in the room the linked account joined.
	key := "puppet:" + kvstore.BuildGhostRoomKey(userID, roomID)
	if b.roomMembershipCache != nil {
		if m, found := b.roomMembershipCache.Get(key); found && m == "joined" {
			return nil
		}
	}
	if err := client.JoinRoomAsUser(roomID, matrixUserID); err != nil {
		return errors.Wrap(err, "failed to join room as linked user")
	}
	if b.roomMembershipCache != nil {
		b.roomMembershipCache.Set(key, "joined")
	}
	return nil
}

// GetMatrixUserID returns the bridge-controlled Matrix account that acts for a
// Mattermost user, creating it on first use: a ghost, or in mirror mode the
// user's mirror account.
func (b *MattermostToMatrixBridge) GetMatrixUserID(userID string) (string, error) {
	return b.ensureAccount(userID)
}

// CreateOrGetGhostUser creates a new Matrix ghost user for a Mattermost user, or returns existing one
func (b *MattermostToMatrixBridge) CreateOrGetGhostUser(userID string) (string, error) {
	// First check if ghost user already exists
	if ghostUserID, exists := b.getGhostUser(userID); exists {
		return ghostUserID, nil
	}

	// Ghost user doesn't exist, create a new one
	// Get the Mattermost user to fetch display name and avatar
	user, appErr := b.API.GetUser(userID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to get Mattermost user for ghost user creation")
	}

	// Get display name
	displayName := user.GetDisplayName(model.ShowFullName)

	// Get user's avatar image data
	var avatarData []byte
	var avatarContentType string
	if imageData, appErr := b.API.GetProfileImage(userID); appErr == nil {
		avatarData = imageData
		avatarContentType = "image/png" // Mattermost typically returns PNG
	}

	// Create new ghost user with display name and avatar
	ghostUser, err := b.matrixClient.CreateGhostUser(userID, displayName, avatarData, avatarContentType)
	if err != nil {
		// Check if this is a display name error (user was created but display name failed)
		if ghostUser != nil && ghostUser.UserID != "" {
			b.logger.LogWarn("Ghost user created but display name setting failed", "error", err, "ghost_user_id", ghostUser.UserID, "display_name", displayName)
			// Continue with caching - user creation was successful
		} else {
			return "", errors.Wrap(err, "failed to create ghost user")
		}
	}

	// Cache the ghost user ID
	ghostUserKey := kvstore.BuildGhostUserKey(userID)
	err = b.kvstore.Set(ghostUserKey, []byte(ghostUser.UserID))
	if err != nil {
		b.logger.LogWarn("Failed to cache ghost user ID", "error", err, "ghost_user_id", ghostUser.UserID)
		// Continue anyway, the ghost user was created successfully
	}

	// Also cache in memory
	if b.ghostUserCache != nil {
		b.ghostUserCache.Set(ghostUserKey, ghostUser.UserID)
	}

	// The ghost carries this profile now; remember it so the next activity
	// does not resync an unchanged profile.
	if err := b.kvstore.Set(profileFingerprintKeyPrefix+userID, []byte(profileFingerprint(user))); err != nil {
		b.logger.LogDebug("Failed to store profile fingerprint for new ghost", "error", err, "user_id", userID)
	}

	if displayName != "" {
		b.logger.LogDebug("Created new ghost user with display name", "mattermost_user_id", userID, "ghost_user_id", ghostUser.UserID, "display_name", displayName)
	} else {
		b.logger.LogDebug("Created new ghost user", "mattermost_user_id", userID, "ghost_user_id", ghostUser.UserID)
	}

	// Record ghost user creation
	b.metrics.RecordGhostUserCreated()

	return ghostUser.UserID, nil
}

func (b *MattermostToMatrixBridge) ensureGhostUserInRoom(ghostUserID, roomID, userID string) error {
	// Check if we've already confirmed this ghost user is in this room
	roomMembershipKey := kvstore.BuildGhostRoomKey(userID, roomID)

	// Check cache first
	if b.roomMembershipCache != nil {
		if membership, found := b.roomMembershipCache.Get(roomMembershipKey); found && membership == "joined" {
			return nil
		}
	}

	// Fall back to KV store
	membershipBytes, err := b.kvstore.Get(roomMembershipKey)
	if err == nil && len(membershipBytes) > 0 && string(membershipBytes) == "joined" {
		// Already confirmed this user is in the room, update cache
		if b.roomMembershipCache != nil {
			b.roomMembershipCache.Set(roomMembershipKey, "joined")
		}
		return nil
	}

	// Try to join the ghost user to the room (handles both public and private rooms)
	err = b.matrixClient.InviteAndJoinGhostUser(roomID, ghostUserID)
	if err != nil {
		return errors.Wrap(err, "failed to join ghost user to room")
	}

	// Cache the successful room join
	err = b.kvstore.Set(roomMembershipKey, []byte("joined"))
	if err != nil {
		b.logger.LogWarn("Failed to cache room membership", "error", err, "ghost_user_id", ghostUserID, "room_id", roomID)
		// Continue anyway, the join was successful
	}

	// Also cache in memory
	if b.roomMembershipCache != nil {
		b.roomMembershipCache.Set(roomMembershipKey, "joined")
	}

	b.logger.LogDebug("Ghost user joined room successfully", "ghost_user_id", ghostUserID, "room_id", roomID)
	return nil
}

// SyncUserToMatrix handles syncing user changes (like display name) to Matrix ghost users
func (b *MattermostToMatrixBridge) SyncUserToMatrix(user *model.User) error {
	b.logger.LogDebug("Syncing user to Matrix", "user_id", user.Id, "username", user.Username)

	// Check if we have a ghost user for this Mattermost user
	ghostUserID, exists := b.getGhostUser(user.Id)
	if !exists {
		b.logger.LogInfo("No ghost user found for user sync", "user_id", user.Id, "username", user.Username)
		return nil // No ghost user exists yet, nothing to update
	}

	b.logger.LogDebug("Found ghost user for user sync", "user_id", user.Id, "ghost_user_id", ghostUserID)

	// Only accounts the bridge manages get their profile synced: ghosts and
	// unclaimed mirror accounts. A mapped real Matrix user or a claimed
	// account has a profile of its own.
	if !b.ownsAccount(ghostUserID) {
		b.logger.LogDebug("Skipping profile sync for an account the bridge does not manage", "user_id", user.Id, "matrix_user_id", ghostUserID)
		return nil
	}

	// Update display name
	displayName := user.GetDisplayName(model.ShowFullName)
	if displayName != "" {
		err := b.matrixClient.SetDisplayName(ghostUserID, displayName)
		if err != nil {
			b.logger.LogError("Failed to update ghost user display name", "error", err, "user_id", user.Id, "ghost_user_id", ghostUserID, "display_name", displayName)
			// Continue to avatar update even if display name fails
		} else {
			b.logger.LogInfo("Updated ghost user display name", "user_id", user.Id, "ghost_user_id", ghostUserID, "display_name", displayName)
		}
	}

	// Update avatar
	if imageData, appErr := b.API.GetProfileImage(user.Id); appErr == nil && len(imageData) > 0 {
		// Upload avatar to Matrix
		mxcURI, err := b.matrixClient.UploadAvatarFromData(imageData, "image/png")
		if err != nil {
			b.logger.LogError("Failed to upload avatar for user update", "error", err, "user_id", user.Id)
		} else {
			if err := b.matrixClient.SetAvatarURL(ghostUserID, mxcURI); err != nil {
				b.logger.LogError("Failed to set avatar URL for user update", "error", err, "user_id", user.Id)
			} else {
				b.logger.LogDebug("Updated ghost user avatar", "user_id", user.Id, "ghost_user_id", ghostUserID)
			}
		}
	}

	return nil
}

// getOrCreateDMRoom gets an existing DM room mapping or creates a new Matrix DM room
func (b *MattermostToMatrixBridge) getOrCreateDMRoom(channelID string, userIDs []string, initiatingUserID string) (string, error) {
	// First check if we already have a room mapping (unified for all channels)
	existingRoomID, err := b.GetMatrixRoomID(channelID)
	if err == nil && existingRoomID != "" {
		b.logger.LogDebug("Found existing DM room mapping", "channel_id", channelID, "matrix_room_id", existingRoomID)
		return existingRoomID, nil
	}

	b.logger.LogInfo("Creating new Matrix DM room", "channel_id", channelID, "user_count", len(userIDs))

	// For DMs, handle both local and remote users appropriately
	var matrixUserIDs []string
	for _, userID := range userIDs {
		user, appErr := b.API.GetUser(userID)
		if appErr != nil {
			b.logger.LogWarn("Failed to get user for DM room creation", "user_id", userID, "error", appErr)
			continue
		}

		if user.IsRemote() {
			// This is a Matrix user that appears as remote in Mattermost
			// Look up their original Matrix user ID via reverse mapping
			matrixUserID, err := b.GetMatrixUserIDFromMattermostUser(userID)
			if err == nil && matrixUserID != "" {
				b.logger.LogDebug("Using existing Matrix user ID for DM", "mattermost_user_id", userID, "matrix_user_id", matrixUserID)
				matrixUserIDs = append(matrixUserIDs, matrixUserID)
			} else {
				b.logger.LogWarn("Could not find Matrix user ID for remote user", "user_id", userID, "username", user.Username, "error", err)
			}
		} else {
			// This is a local Mattermost user - create or get ghost user
			ghostUserID, err := b.ensureAccount(userID)
			if err != nil {
				b.logger.LogWarn("Failed to create ghost user for DM", "user_id", userID, "error", err)
				continue
			}
			b.logger.LogDebug("Created ghost user for DM", "mattermost_user_id", userID, "ghost_user_id", ghostUserID)
			matrixUserIDs = append(matrixUserIDs, ghostUserID)
		}
	}

	if len(matrixUserIDs) < 2 {
		return "", errors.New("insufficient users for DM room creation")
	}

	// Get the initiating user's display name for the room name
	roomName := "Direct Message"
	if initiatingUser, appErr := b.API.GetUser(initiatingUserID); appErr == nil {
		roomName = "DM with " + initiatingUser.GetDisplayName(model.ShowFullName)
	}

	// Create the Matrix DM room
	matrixRoomID, err := b.matrixClient.CreateDirectRoom(matrixUserIDs, roomName)
	if err != nil {
		return "", errors.Wrap(err, "failed to create Matrix DM room")
	}

	// Everyone in the conversation is part of the room from the start.
	for _, mxid := range matrixUserIDs {
		if joinErr := b.matrixClient.InviteAndJoinGhostUser(matrixRoomID, mxid); joinErr != nil {
			b.logger.LogDebug("Could not join a participant to the DM room", "matrix_user", mxid, "error", joinErr)
		}
	}

	// Store the mapping using unified channel mapping
	err = b.setChannelRoomMapping(channelID, matrixRoomID)
	if err == nil {
		b.markDirectChannelBridged(channelID)
	}
	if err != nil {
		b.logger.LogError("Failed to store DM room mapping", "error", err, "channel_id", channelID, "matrix_room_id", matrixRoomID)
		// Continue anyway - the room was created successfully
	}

	b.logger.LogInfo("Successfully created Matrix DM room", "channel_id", channelID, "matrix_room_id", matrixRoomID, "matrix_users", matrixUserIDs)
	return matrixRoomID, nil
}

// GetMatrixUserIDFromMattermostUser looks up the original Matrix user ID for a remote Mattermost user
// If KV lookup fails, attempts to reconstruct the Matrix user ID from the username
func (b *MattermostToMatrixBridge) GetMatrixUserIDFromMattermostUser(mattermostUserID string) (string, error) {
	// Use Mattermost user ID as key: mattermost_user_<mattermostUserID> -> matrixUserID
	mattermostUserKey := kvstore.BuildMattermostUserKey(mattermostUserID)
	matrixUserIDBytes, err := b.kvstore.Get(mattermostUserKey)
	if err == nil && len(matrixUserIDBytes) > 0 {
		return string(matrixUserIDBytes), nil
	}

	// KV lookup failed - try to reconstruct from username as fallback
	b.logger.LogWarn("KV store lookup failed for Matrix user ID, attempting fallback reconstruction", "user_id", mattermostUserID, "error", err)

	user, appErr := b.API.GetUser(mattermostUserID)
	if appErr != nil {
		return "", errors.Wrap(appErr, "failed to get user for Matrix ID reconstruction")
	}

	reconstructedID := b.reconstructMatrixUserIDFromUsername(user.Username)
	if reconstructedID != "" {
		b.logger.LogWarn("Successfully reconstructed Matrix user ID from username (KV mapping missing)", "user_id", mattermostUserID, "username", user.Username, "matrix_user_id", reconstructedID)
		return reconstructedID, nil
	}

	// Both KV lookup and reconstruction failed
	if err != nil {
		return "", errors.Wrap(err, "no Matrix user ID mapping found for Mattermost user and reconstruction failed")
	}
	return "", errors.New("empty Matrix user ID mapping found and reconstruction failed")
}
