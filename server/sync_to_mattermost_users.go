package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// getOrCreateMattermostUser gets or creates a Mattermost user for a Matrix user
// If channelID is provided, ensures the user is added to the team associated with that channel
func (b *MatrixToMattermostBridge) getOrCreateMattermostUser(matrixUserID string, channelID string) (string, error) {
	// Check if we already have a mapping for this Matrix user
	userMapKey := kvstore.BuildMatrixUserKey(matrixUserID)
	userIDBytes, err := b.kvstore.Get(userMapKey)
	if err == nil && len(userIDBytes) > 0 {
		mattermostUserID := string(userIDBytes)

		// Verify the user still exists
		if existingUser, appErr := b.API.GetUser(mattermostUserID); appErr == nil {
			// User exists, ensure they're in the team for this channel
			if channelID != "" {
				if err := b.addUserToChannelTeam(mattermostUserID, channelID); err != nil {
					b.logger.LogWarn("Failed to add existing Matrix user to team", "error", err, "user_id", mattermostUserID, "channel_id", channelID, "matrix_user_id", matrixUserID)
				}
			}

			// Bridge-created remote users mirror the Matrix profile; linked real
			// accounts keep their own Mattermost profile.
			if existingUser.IsRemote() {
				context := &ProfileUpdateContext{
					EventID: "",
					Source:  "api",
				}
				b.updateMattermostUserProfile(existingUser, matrixUserID, context)
			}
			return mattermostUserID, nil
		}

		// User no longer exists, remove the mapping
		if err := b.kvstore.Delete(userMapKey); err != nil {
			b.logger.LogWarn("Failed to delete user mapping", "error", err, "key", userMapKey)
		}
	}

	if b.getConfiguration().CutoverMode {
		return "", errors.New("cut-over mode is on: no new Mattermost users are created for Matrix users")
	}

	// The same person on both sides, by verified email: link, do not duplicate.
	if linkedID, ok := b.autoLinkByEmail(matrixUserID); ok {
		if channelID != "" {
			if err := b.addUserToChannelTeam(linkedID, channelID); err != nil {
				b.logger.LogWarn("Failed to add linked user to team", "error", err, "user_id", linkedID, "channel_id", channelID)
			}
		}
		return linkedID, nil
	}

	// Extract username from Matrix user ID (@username:server.com -> username)
	username := b.extractUsernameFromMatrixUserID(matrixUserID)
	if username == "" {
		return "", errors.New("failed to extract username from Matrix user ID")
	}

	// Create a unique Mattermost username
	mattermostUsername := b.generateMattermostUsername(username)

	// Get real display name and avatar from Matrix profile
	var displayName string
	var firstName, lastName string
	var avatarData []byte

	if b.matrixClient != nil {
		profile, err := b.matrixClient.GetUserProfile(matrixUserID)
		if err != nil {
			b.logger.LogWarn("Failed to get Matrix user profile", "error", err, "user_id", matrixUserID)
			displayName = username // Fallback to username
		} else {
			if profile.DisplayName != "" {
				displayName = profile.DisplayName
				// Try to parse first/last name from display name
				firstName, lastName = parseDisplayName(profile.DisplayName)
			} else {
				displayName = username // Fallback to username if no display name set
			}

			// Download avatar if available
			if profile.AvatarURL != "" {
				avatarData, err = b.downloadMatrixAvatar(profile.AvatarURL)
				if err != nil {
					b.logger.LogWarn("Failed to download Matrix user avatar", "error", err, "user_id", matrixUserID, "avatar_url", profile.AvatarURL)
				}
			}
		}
	} else {
		displayName = username // Fallback if no Matrix client
	}

	// Create the user in Mattermost
	// Generate valid email by replacing colon with underscore (email local part cannot contain colons)
	emailUsername := strings.ReplaceAll(mattermostUsername, ":", "_")

	user := &model.User{
		Username:    mattermostUsername,
		Email:       fmt.Sprintf("%s@matrix.bridge", emailUsername), // Placeholder email with valid format
		Password:    model.NewId(),                                  // Generate random password (user won't use it for login)
		Nickname:    displayName,                                    // Use real Matrix display name
		FirstName:   firstName,                                      // Parsed from display name if possible
		LastName:    lastName,                                       // Parsed from display name if possible
		AuthData:    nil,
		AuthService: "",
		RemoteId:    &b.remoteID, // Attribute to Matrix remote
	}

	b.logger.LogDebug("Creating Mattermost user with remote details",
		"username", mattermostUsername,
		"email", user.Email,
		"remote_id", b.remoteID,
		"remote_id_ptr", user.RemoteId,
		"matrix_user_id", matrixUserID)

	createdUser, appErr := b.API.CreateUser(user)
	if appErr != nil {
		b.logger.LogError("Failed to create Mattermost user",
			"error", appErr.Error(),
			"username", mattermostUsername,
			"remote_id", b.remoteID,
			"remote_id_empty", b.remoteID == "",
			"matrix_user_id", matrixUserID)
		return "", errors.Wrap(appErr, "failed to create Mattermost user")
	}

	// Set avatar if we downloaded one
	if len(avatarData) > 0 {
		appErr = b.API.SetProfileImage(createdUser.Id, avatarData)
		if appErr != nil {
			b.logger.LogWarn("Failed to set Matrix user avatar", "error", appErr, "user_id", createdUser.Id, "matrix_user_id", matrixUserID)
		} else {
			b.logger.LogDebug("Successfully set avatar for Matrix user", "user_id", createdUser.Id, "matrix_user_id", matrixUserID)
		}
	}

	// Add user to team if channelID is provided
	if channelID != "" {
		if err := b.addUserToChannelTeam(createdUser.Id, channelID); err != nil {
			b.logger.LogWarn("Failed to add new Matrix user to team", "error", err, "user_id", createdUser.Id, "channel_id", channelID, "matrix_user_id", matrixUserID)
		}
	}

	// Store both directions of the mapping
	err = b.kvstore.Set(userMapKey, []byte(createdUser.Id))
	if err != nil {
		b.logger.LogWarn("Failed to store Matrix user mapping", "error", err, "matrix_user_id", matrixUserID, "mattermost_user_id", createdUser.Id)
	}

	// Store reverse mapping: mattermost_user_<mattermostUserID> -> matrixUserID
	// This mapping is critical for user lookups - treat failure as a serious issue
	mattermostUserKey := kvstore.BuildMattermostUserKey(createdUser.Id)
	err = b.kvstore.Set(mattermostUserKey, []byte(matrixUserID))
	if err != nil {
		b.logger.LogError("Failed to store critical reverse user mapping", "error", err, "mattermost_user_id", createdUser.Id, "matrix_user_id", matrixUserID)
		// Continue execution but this could cause lookup issues later
	}

	b.logger.LogDebug("Created Mattermost user for Matrix user", "matrix_user_id", matrixUserID, "mattermost_user_id", createdUser.Id, "username", mattermostUsername)
	return createdUser.Id, nil
}

// addUserToChannelTeam adds a user to the team that owns the specified channel
func (b *MatrixToMattermostBridge) addUserToChannelTeam(userID, channelID string) error {
	// Get the channel to find its team ID
	channel, appErr := b.API.GetChannel(channelID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to get channel")
	}

	// DM and group DM channels don't belong to teams
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		b.logger.LogDebug("Skipping team membership for DM channel", "user_id", userID, "channel_id", channelID, "channel_type", channel.Type)
		return nil
	}

	// Skip if team ID is empty
	if channel.TeamId == "" {
		b.logger.LogDebug("Skipping team membership for channel with no team", "user_id", userID, "channel_id", channelID)
		return nil
	}

	// Check if user is already a member of the team
	_, appErr = b.API.GetTeamMember(channel.TeamId, userID)
	if appErr == nil {
		// User is already a team member
		b.logger.LogDebug("User already member of team", "user_id", userID, "team_id", channel.TeamId, "channel_id", channelID)
		return nil
	}

	// Add user to the team
	_, appErr = b.API.CreateTeamMember(channel.TeamId, userID)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to add user to team")
	}

	b.logger.LogDebug("Added Matrix user to team", "user_id", userID, "team_id", channel.TeamId, "channel_id", channelID)
	return nil
}

// extractUsernameFromMatrixUserID extracts username from Matrix user ID
func (b *MatrixToMattermostBridge) extractUsernameFromMatrixUserID(userID string) string {
	// Matrix user IDs are in format @username:server.com
	if !strings.HasPrefix(userID, "@") {
		return ""
	}

	parts := strings.Split(userID[1:], ":")
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

// generateMattermostUsername creates a unique Mattermost username
func (b *MatrixToMattermostBridge) generateMattermostUsername(baseUsername string) string {
	// Sanitize username for Mattermost (following Shared Channels convention)
	sanitized := strings.ToLower(baseUsername)
	sanitized = regexp.MustCompile(`[^a-z0-9\-_]`).ReplaceAllString(sanitized, "_")

	// Get the configured username prefix for this server
	config := b.getConfiguration()
	prefix := config.GetMatrixUsernamePrefixForServer(config.MatrixServerURL)

	// Follow Shared Channels convention: prefix:username_sanitized
	username := prefix + ":" + sanitized

	// Ensure uniqueness by checking if username exists
	counter := 1
	originalUsername := username

	for {
		if _, appErr := b.API.GetUserByUsername(username); appErr != nil {
			// Username doesn't exist, we can use it
			break
		}

		// Username exists, try with counter
		username = fmt.Sprintf("%s_%d", originalUsername, counter)
		counter++

		// Prevent infinite loop
		if counter > 1000 {
			// Fallback to using counter
			username = fmt.Sprintf("%s:user_%d", prefix, counter)
			break
		}
	}

	return username
}

// downloadMatrixAvatar downloads an avatar image from a Matrix MXC URI
func (b *MatrixToMattermostBridge) downloadMatrixAvatar(avatarURL string) ([]byte, error) {
	if b.matrixClient == nil {
		return nil, errors.New("Matrix client not configured")
	}

	// Use the Matrix client's download method with size limit and image content type validation
	return b.matrixClient.DownloadFile(avatarURL, b.maxProfileImageSize, "image/")
}

// ProfileUpdateContext provides context information for profile updates
type ProfileUpdateContext struct {
	EventID string // Matrix event ID if update is from an event
	Source  string // "api" for proactive checks, "event" for Matrix member events
}

// updateMattermostUserProfile updates an existing Mattermost user's profile from Matrix
// Can be called either proactively (fetching current profile) or reactively (from Matrix events)
func (b *MatrixToMattermostBridge) updateMattermostUserProfile(mattermostUser *model.User, matrixUserID string, context *ProfileUpdateContext, profileData ...*matrix.UserProfile) {
	if b.matrixClient == nil {
		return
	}

	var profile *matrix.UserProfile
	var err error

	// Determine profile data source
	if len(profileData) > 0 && profileData[0] != nil {
		// Use provided profile data (from Matrix event)
		profile = profileData[0]
	} else {
		// Fetch current Matrix profile (proactive check)
		profile, err = b.matrixClient.GetUserProfile(matrixUserID)
		if err != nil {
			b.logger.LogWarn("Failed to get Matrix user profile for update", "error", err, "user_id", matrixUserID)
			return
		}
	}

	var needsUpdate bool
	updatedUser := *mattermostUser // Create a copy

	// Check display name updates
	if profile.DisplayName != "" {
		// Parse first/last name from display name
		firstName, lastName := parseDisplayName(profile.DisplayName)

		// Update nickname (display name)
		if updatedUser.Nickname != profile.DisplayName {
			if context.Source == "event" {
				b.logger.LogDebug("Updating user display name from Matrix event", "user_id", mattermostUser.Id, "old_name", mattermostUser.Nickname, "new_name", profile.DisplayName, "matrix_user_id", matrixUserID, "event_id", context.EventID)
			} else {
				b.logger.LogDebug("Updating display name from proactive check", "user_id", mattermostUser.Id, "old", mattermostUser.Nickname, "new", profile.DisplayName)
			}
			updatedUser.Nickname = profile.DisplayName
			needsUpdate = true
		}

		// Update first/last name if they changed
		if updatedUser.FirstName != firstName {
			updatedUser.FirstName = firstName
			needsUpdate = true
		}
		if updatedUser.LastName != lastName {
			updatedUser.LastName = lastName
			needsUpdate = true
		}
	}

	// Update user profile if needed
	if needsUpdate {
		if _, appErr := b.API.UpdateUser(&updatedUser); appErr != nil {
			if context.Source == "event" {
				b.logger.LogError("Failed to update Mattermost user profile from Matrix event", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "event_id", context.EventID)
			} else {
				b.logger.LogWarn("Failed to update Mattermost user profile from proactive check", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID)
			}
		} else {
			if context.Source == "event" {
				b.logger.LogDebug("Successfully updated Mattermost user profile from Matrix event", "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "display_name", profile.DisplayName, "event_id", context.EventID)
			} else {
				b.logger.LogDebug("Updated Mattermost user profile from proactive check", "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "display_name", profile.DisplayName)
			}
		}
	}

	// Check avatar updates
	if profile.AvatarURL != "" {
		b.updateMattermostUserAvatar(mattermostUser, matrixUserID, profile.AvatarURL, context)
	}
}

// updateMattermostUserAvatar updates a Mattermost user's profile image from Matrix
// Compares current and new image data to avoid unnecessary updates
func (b *MatrixToMattermostBridge) updateMattermostUserAvatar(mattermostUser *model.User, matrixUserID, matrixAvatarURL string, context *ProfileUpdateContext) {
	// Get current Mattermost profile image
	currentAvatarData, appErr := b.API.GetProfileImage(mattermostUser.Id)
	if appErr != nil {
		if context.Source == "event" {
			b.logger.LogWarn("Failed to get current Mattermost profile image for comparison", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "event_id", context.EventID)
		} else {
			b.logger.LogDebug("Failed to get current Mattermost profile image for comparison", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID)
		}
		// Continue with update even if we can't get current image
		currentAvatarData = nil
	}

	// Download Matrix avatar image
	newAvatarData, err := b.downloadMatrixAvatar(matrixAvatarURL)
	if err != nil {
		if context.Source == "event" {
			b.logger.LogWarn("Failed to download Matrix avatar for comparison", "error", err, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "avatar_url", matrixAvatarURL, "event_id", context.EventID)
		} else {
			b.logger.LogDebug("Failed to download Matrix avatar for comparison", "error", err, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "avatar_url", matrixAvatarURL)
		}
		return
	}

	// Compare image data to see if update is needed
	if currentAvatarData != nil && b.compareImageData(currentAvatarData, newAvatarData) {
		b.logger.LogDebug("Matrix avatar unchanged, skipping update", "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "source", context.Source)
		return
	}

	// Images are different, update the profile image
	appErr = b.API.SetProfileImage(mattermostUser.Id, newAvatarData)
	if appErr != nil {
		if context.Source == "event" {
			b.logger.LogError("Failed to update Mattermost user avatar from Matrix event", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "event_id", context.EventID)
		} else {
			b.logger.LogWarn("Failed to update Mattermost user avatar from proactive check", "error", appErr, "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID)
		}
		return
	}

	// Log successful avatar update
	if context.Source == "event" {
		b.logger.LogDebug("Successfully updated Mattermost user avatar from Matrix event", "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "avatar_url", matrixAvatarURL, "event_id", context.EventID, "size_bytes", len(newAvatarData))
	} else {
		b.logger.LogDebug("Updated Mattermost user avatar from proactive check", "user_id", mattermostUser.Id, "matrix_user_id", matrixUserID, "avatar_url", matrixAvatarURL, "size_bytes", len(newAvatarData))
	}
}

// compareImageData compares two image byte arrays to determine if they're the same
// Uses a simple byte comparison for now, could be enhanced with more sophisticated comparison
func (b *MatrixToMattermostBridge) compareImageData(currentData, newData []byte) bool {
	// Quick size check first
	if len(currentData) != len(newData) {
		return false
	}

	// If both are empty, they're the same
	if len(currentData) == 0 {
		return true
	}

	// Compare byte by byte
	for i := range currentData {
		if currentData[i] != newData[i] {
			return false
		}
	}

	return true
}

// updateExistingUserProfile updates an existing user's profile from Matrix event data
func (b *MatrixToMattermostBridge) updateExistingUserProfile(mattermostUserID, matrixUserID, eventID, displayName, avatarURL string) error {
	// Get the existing Mattermost user
	mattermostUser, appErr := b.API.GetUser(mattermostUserID)
	if appErr != nil {
		b.logger.LogWarn("Failed to get Mattermost user for profile update", "error", appErr, "user_id", mattermostUserID, "matrix_user_id", matrixUserID)
		return nil
	}

	b.logger.LogDebug("Found Mattermost user for profile update", "user_id", mattermostUser.Id, "username", mattermostUser.Username, "matrix_user_id", matrixUserID)

	// Create profile data from Matrix event
	eventProfile := &matrix.UserProfile{
		DisplayName: displayName,
		AvatarURL:   avatarURL,
	}

	// Update the user's profile using the unified method
	context := &ProfileUpdateContext{
		EventID: eventID,
		Source:  "event",
	}

	b.updateMattermostUserProfile(mattermostUser, matrixUserID, context, eventProfile)
	return nil
}
