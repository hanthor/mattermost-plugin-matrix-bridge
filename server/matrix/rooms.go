package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// JoinRoom joins a Matrix room using either a room ID or room alias.
func (c *Client) JoinRoom(roomIdentifier string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	// Apply rate limiting for room join operations (rc_joins)
	if err := c.waitForRateLimit(c.joinLimiter, "Room join"); err != nil {
		return err
	}

	// Use the unified join endpoint that supports both room IDs and aliases
	encodedIdentifier := url.PathEscape(roomIdentifier)
	requestURL := c.serverURL + "/_matrix/client/v3/join/" + encodedIdentifier

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return errors.Wrap(err, "failed to create join request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send join request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read join response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to join room: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// JoinRoomAsUser joins a room as a specific user (using application service impersonation)
func (c *Client) JoinRoomAsUser(roomIdentifier, userID string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	// Apply rate limiting for room join operations (rc_joins)
	if err := c.waitForRateLimit(c.joinLimiter, "Room join"); err != nil {
		return err
	}

	// Use the unified join endpoint that supports both room IDs and aliases
	encodedIdentifier := url.PathEscape(roomIdentifier)
	requestURL := c.serverURL + "/_matrix/client/v3/join/" + encodedIdentifier

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return errors.Wrap(err, "failed to create join request")
	}

	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send join request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read join response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to join room as user: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// InviteUserToRoom invites a user to a Matrix room
func (c *Client) InviteUserToRoom(roomID, userID string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	// Apply rate limiting for room invite operations
	if err := c.waitForRateLimit(c.inviteLimiter, "Room invite"); err != nil {
		return err
	}

	c.logger.LogDebug("Inviting user to Matrix room", "room_id", roomID, "user_id", userID)

	// Prepare invite request body
	inviteData := map[string]string{
		"user_id": userID,
	}

	jsonData, err := json.Marshal(inviteData)
	if err != nil {
		return errors.Wrap(err, "failed to marshal invite data")
	}

	// Use room invite endpoint
	encodedRoomID := url.PathEscape(roomID)
	requestURL := c.serverURL + "/_matrix/client/v3/rooms/" + encodedRoomID + "/invite"

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create invite request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send invite request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read invite response")
	}

	if resp.StatusCode != http.StatusOK {
		// Parse Matrix error response for better error handling
		matrixErr := parseMatrixError(resp.StatusCode, body)

		// Check for common "already joined" error cases to make this idempotent
		if matrixErr.IsAlreadyJoined() {
			c.logger.LogDebug("User already joined or invited to Matrix room, skipping", "room_id", roomID, "user_id", userID, "matrix_error", matrixErr.ErrCode)
			return nil // Not an error - user is already in the room
		}

		// Homeservers word this refusal differently ("already joined",
		// "cannot invite user that is joined or banned"), so a forbidden invite is
		// settled by asking what the person's membership actually is.
		if matrixErr.ErrCode == "M_FORBIDDEN" {
			if state, err := c.membershipOf(roomID, userID); err == nil && (state == "join" || state == "invite") {
				c.logger.LogDebug("User already joined or invited to Matrix room, skipping", "room_id", roomID, "user_id", userID, "membership", state)
				return nil
			}
		}

		c.logger.LogWarn("Failed to invite user to Matrix room", "status_code", resp.StatusCode, "matrix_error", matrixErr.ErrCode, "error_message", matrixErr.ErrMsg, "room_id", roomID, "user_id", userID)
		return matrixErr
	}

	c.logger.LogDebug("Successfully invited user to Matrix room", "room_id", roomID, "user_id", userID)
	return nil
}

// GetRoomJoinRule fetches the join rule for a Matrix room
func (c *Client) GetRoomJoinRule(roomID string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	// Use the room state endpoint to get join rules
	encodedRoomID := url.PathEscape(roomID)
	requestURL := c.serverURL + "/_matrix/client/v3/rooms/" + encodedRoomID + "/state/m.room.join_rules/"

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create join rules request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send join rules request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read join rules response")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get join rules: %d %s", resp.StatusCode, string(body))
	}

	var joinRuleEvent struct {
		JoinRule string `json:"join_rule"`
	}
	if err := json.Unmarshal(body, &joinRuleEvent); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal join rules response")
	}

	return joinRuleEvent.JoinRule, nil
}

// InviteAndJoinGhostUser invites a ghost user to a room (via application service) and then joins them
// This checks the room's join rules first to determine if invitation is required
func (c *Client) InviteAndJoinGhostUser(roomIdentifier, ghostUserID string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	c.logger.LogDebug("Attempting to join ghost user to Matrix room", "room_identifier", roomIdentifier, "ghost_user_id", ghostUserID)

	// Always resolve room alias to room ID for consistency
	roomID, err := c.ResolveRoomAlias(roomIdentifier)
	if err != nil {
		return errors.Wrap(err, "failed to resolve room identifier")
	}

	c.logger.LogDebug("Resolved room identifier to room ID", "room_identifier", roomIdentifier, "room_id", roomID, "ghost_user_id", ghostUserID)

	// Check the room's join rules first
	joinRule, err := c.GetRoomJoinRule(roomID)
	if err != nil {
		c.logger.LogWarn("Failed to get room join rules, falling back to try-join approach", "room_id", roomID, "error", err)
		// Fall back to the old approach if we can't determine join rules
		return c.joinGhostUserWithFallback(roomID, ghostUserID)
	}

	c.logger.LogDebug("Determined room join rule", "room_id", roomID, "join_rule", joinRule)

	// Handle based on join rule
	switch joinRule {
	case "public":
		// Public room - can join directly
		err := c.JoinRoomAsUser(roomID, ghostUserID)
		if err != nil {
			return errors.Wrap(err, "failed to join ghost user to public room")
		}
		c.logger.LogDebug("Successfully joined ghost user to public room", "room_id", roomID, "ghost_user_id", ghostUserID)
		return nil

	case "invite":
		// Private/invite-only room - need to invite first
		c.logger.LogDebug("Room requires invitation, inviting ghost user first", "room_id", roomID, "ghost_user_id", ghostUserID)

		// Invite the ghost user to the room using application service bot
		inviteErr := c.InviteUserToRoom(roomID, ghostUserID)
		if inviteErr != nil {
			return errors.Wrap(inviteErr, "failed to invite ghost user to private room")
		}

		// Now join after invitation
		joinErr := c.JoinRoomAsUser(roomID, ghostUserID)
		if joinErr != nil {
			return errors.Wrap(joinErr, "failed to join ghost user to room after invitation")
		}

		c.logger.LogDebug("Successfully invited and joined ghost user to private room", "room_id", roomID, "ghost_user_id", ghostUserID)
		return nil

	default:
		// Unknown or unsupported join rule (e.g., "knock", "restricted")
		c.logger.LogWarn("Unknown join rule, falling back to try-join approach", "room_id", roomID, "join_rule", joinRule)
		return c.joinGhostUserWithFallback(roomID, ghostUserID)
	}
}

// joinGhostUserWithFallback is the fallback method that tries join first, then invite+join
func (c *Client) joinGhostUserWithFallback(roomID, ghostUserID string) error {
	// First try to join directly
	err := c.JoinRoomAsUser(roomID, ghostUserID)
	if err == nil {
		c.logger.LogDebug("Successfully joined ghost user to room directly", "room_id", roomID, "ghost_user_id", ghostUserID)
		return nil
	}

	// If join failed with a forbidden error, try invitation first
	if isForbiddenJoinError(err) {
		var matrixErr *Error
		if errors.As(err, &matrixErr) {
			c.logger.LogDebug("Direct join failed with forbidden error, attempting invitation first", "room_id", roomID, "ghost_user_id", ghostUserID, "matrix_error", matrixErr.ErrCode)
		} else {
			// Fallback for legacy error types that aren't structured
			c.logger.LogDebug("Direct join failed with forbidden error (legacy error), attempting invitation first", "room_id", roomID, "ghost_user_id", ghostUserID, "error", err.Error())
		}
	} else {
		// For other errors, return the original join error
		return errors.Wrap(err, "failed to join ghost user to room")
	}

	// Common invitation and join logic for both structured and legacy errors
	// Invite the ghost user to the room using application service bot
	inviteErr := c.InviteUserToRoom(roomID, ghostUserID)
	if inviteErr != nil {
		return errors.Wrap(inviteErr, "failed to invite ghost user to private room")
	}

	// Now try to join again after invitation
	joinErr := c.JoinRoomAsUser(roomID, ghostUserID)
	if joinErr != nil {
		return errors.Wrap(joinErr, "failed to join ghost user to room after invitation")
	}

	c.logger.LogDebug("Successfully invited and joined ghost user to private room via fallback", "room_id", roomID, "ghost_user_id", ghostUserID)
	return nil
}

// LeaveRoomAsUser makes the given (ghost) user leave a room.
func (c *Client) LeaveRoomAsUser(roomID, userID string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "leave")
	if err != nil {
		return errors.Wrap(err, "invalid room ID")
	}
	requestURL := c.serverURL + endpoint

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer([]byte("{}")))
	if err != nil {
		return errors.Wrap(err, "failed to create leave request")
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send leave request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to leave room: HTTP %d - %s", resp.StatusCode, string(body))
	}
	return nil
}

// CreateRoom creates a new Matrix room with the specified name, topic, and settings.
// Returns the room ID or alias on success.
// If skipPrefix is true, no _mattermost_ prefix is added (for Mirror Mode)
func (c *Client) CreateRoom(name, topic, serverDomain string, publish bool, mattermostChannelID string, skipPrefix bool, teamName string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	// Apply rate limiting for room creation
	if err := c.waitForRateLimit(c.roomCreationLimiter, "Room creation"); err != nil {
		return "", err
	}

	c.logger.LogDebug("Creating Matrix room", "name", name, "topic", topic, "server_domain", serverDomain, "skip_prefix", skipPrefix, "team_name", teamName)

	// Create room alias - include team name to avoid collisions across teams
	alias := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	alias = strings.ReplaceAll(alias, "_", "-")

	// Prepend team name if available to avoid alias collisions
	// (e.g., multiple teams can have "town-square" channels)
	if teamName != "" {
		teamSlug := strings.ToLower(strings.ReplaceAll(teamName, " ", "-"))
		teamSlug = strings.ReplaceAll(teamSlug, "_", "-")
		alias = teamSlug + "-" + alias
	}

	// In Mirror Mode (skipPrefix=true), use clean aliases without prefix
	// Otherwise use _mattermost_ prefix for namespace reservation
	roomAlias := ""
	if serverDomain != "" {
		if skipPrefix {
			roomAlias = "#" + alias + ":" + serverDomain
			c.logger.LogInfo("Mirror Mode: Creating room with clean alias", "room_alias", roomAlias, "skip_prefix", skipPrefix)
		} else {
			roomAlias = "#_mattermost_" + alias + ":" + serverDomain
			c.logger.LogInfo("Standard Mode: Creating room with _mattermost_ prefix", "room_alias", roomAlias, "skip_prefix", skipPrefix)
		}
	}

	// Set room visibility and rules based on publish parameter
	var preset, visibility, joinRule, historyVisibility, guestAccess string
	if publish {
		preset = "public_chat"
		visibility = "public"
		joinRule = "public"
		historyVisibility = "world_readable"
		guestAccess = "can_join"
	} else {
		preset = "private_chat"
		visibility = "private"
		joinRule = "invite"
		historyVisibility = "invited"
		guestAccess = "forbidden"
	}

	roomData := map[string]any{
		"name":         name,
		"topic":        topic,
		"preset":       preset,
		"visibility":   visibility,
		"is_direct":    false, // Explicitly mark as not a direct message room
		"room_version": "10",  // Explicitly set room version and directory visibility
		"initial_state": []map[string]any{
			{
				"type":      "m.room.guest_access",
				"state_key": "",
				"content": map[string]any{
					"guest_access": guestAccess,
				},
			},
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]any{
					"history_visibility": historyVisibility,
				},
			},
			{
				"type":      "m.room.join_rules",
				"state_key": "",
				"content": map[string]any{
					"join_rule": joinRule,
				},
			},
			{
				"type":      "com.mattermost.bridge.channel",
				"state_key": "",
				"content": map[string]any{
					"mattermost_channel_id": mattermostChannelID,
					"created_at":            time.Now().Unix(),
				},
			},
		},
		"creation_content": map[string]any{
			"m.federate": true,
		},
	}

	// Add room alias if we have a server domain
	if roomAlias != "" {
		if skipPrefix {
			roomData["room_alias_name"] = alias // Clean alias in Mirror Mode
		} else {
			roomData["room_alias_name"] = "_mattermost_" + alias // Namespace prefix for normal mode
		}
	}

	resp, body, err := c.postCreateRoom(roomData)
	if err != nil {
		return "", err
	}

	// A homeserver whose registration does not reserve the alias namespace
	// rejects the alias with M_EXCLUSIVE. The room itself is more important
	// than its alias, so retry without one and keep going.
	if resp.StatusCode == http.StatusBadRequest && roomData["room_alias_name"] != nil && isAliasNotReservedError(body) {
		c.logger.LogWarn("Homeserver refused the room alias because the appservice registration does not reserve it; creating the room without an alias", "room_alias", roomAlias)
		delete(roomData, "room_alias_name")
		roomAlias = ""
		resp, body, err = c.postCreateRoom(roomData)
		if err != nil {
			return "", err
		}
	}

	if resp.StatusCode != http.StatusOK {
		matrixErr := parseMatrixError(resp.StatusCode, body)

		// Handle 429 Too Many Requests with specific logging
		if IsRateLimitError(matrixErr) {
			c.logger.LogWarn("Matrix room creation rate limited, server rejected request",
				"status_code", resp.StatusCode,
				"response", string(body),
				"room_name", name,
				"error_code", matrixErr.ErrCode)
			return "", matrixErr
		}

		c.logger.LogError("Matrix room creation failed", "status_code", resp.StatusCode, "response", string(body), "room_name", name)
		return "", matrixErr
	}

	var response struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal room creation response")
	}

	// Join the application service bot to the room immediately after creation
	// This is required for the bot to query room state, invite users, etc.
	if err := c.JoinRoom(response.RoomID); err != nil {
		c.logger.LogWarn("Failed to join application service bot to created room", "room_id", response.RoomID, "error", err)
		// Continue - the room was created successfully, but bot operations may fail
	} else {
		c.logger.LogDebug("Application service bot joined room", "room_id", response.RoomID)
	}

	// Publish to directory based on the publish parameter
	if publish {
		c.logger.LogDebug("Publishing room to public directory", "room_id", response.RoomID)
		if err := c.PublishRoomToDirectory(response.RoomID, true); err != nil {
			// Log warning but don't fail room creation - the room was created successfully
			c.logger.LogWarn("Failed to publish room to public directory", "room_id", response.RoomID, "error", err)
			c.logger.LogDebug("Room created but not published to directory", "room_id", response.RoomID, "room_alias", roomAlias)
		} else {
			c.logger.LogDebug("Room created and published to directory", "room_id", response.RoomID, "room_alias", roomAlias)
		}
	} else {
		c.logger.LogDebug("Room created (not published to directory)", "room_id", response.RoomID, "room_alias", roomAlias)
	}

	// Log successful room creation
	returnValue := response.RoomID
	if roomAlias != "" {
		returnValue = roomAlias
	}
	c.logger.LogInfo("Matrix room created successfully", "room_id", response.RoomID, "room_alias", roomAlias, "return_value", returnValue)

	// Add bridge alias for Matrix Application Service filtering
	if roomAlias != "" {
		// Create bridge alias with mattermost-bridge- prefix
		bridgeAlias := "#mattermost-bridge-" + alias + ":" + serverDomain
		err = c.AddRoomAlias(response.RoomID, bridgeAlias)
		if err != nil {
			c.logger.LogWarn("Failed to add bridge filtering alias", "error", err, "bridge_alias", bridgeAlias, "room_id", response.RoomID)
			// Continue - user alias still works, bridge filtering just won't work for this room
		} else {
			c.logger.LogDebug("Successfully added bridge filtering alias", "room_id", response.RoomID, "bridge_alias", bridgeAlias, "user_alias", roomAlias)
		}
	}

	// Return the room alias if we created one, otherwise return the room ID
	if roomAlias != "" {
		return roomAlias, nil
	}
	return response.RoomID, nil
}

// postCreateRoom sends a createRoom request and returns the response with its
// body already read.
func (c *Client) postCreateRoom(roomData map[string]any) (*http.Response, []byte, error) {
	jsonData, err := json.Marshal(roomData)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to marshal room creation data")
	}

	req, err := http.NewRequest("POST", c.serverURL+"/_matrix/client/v3/createRoom", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create room creation request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to send room creation request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to read room creation response")
	}
	return resp, body, nil
}

// isAliasNotReservedError reports whether a createRoom failure is Synapse
// refusing an alias outside the appservice's reserved namespaces.
func isAliasNotReservedError(body []byte) bool {
	mErr := parseMatrixError(http.StatusBadRequest, body)
	return mErr.ErrCode == "M_EXCLUSIVE" || strings.Contains(strings.ToLower(mErr.ErrMsg), "not reserved")
}

// CreateDirectRoom creates a Matrix DM room and invites the specified ghost users
func (c *Client) CreateDirectRoom(ghostUserIDs []string, roomName string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	if len(ghostUserIDs) < 2 {
		return "", errors.New("direct room requires at least 2 users")
	}

	// Apply rate limiting for room creation operations
	if err := c.waitForRateLimit(c.roomCreationLimiter, "Direct room creation"); err != nil {
		return "", err
	}

	c.logger.LogDebug("Creating Matrix DM room", "users", ghostUserIDs)

	roomData := map[string]any{
		"preset":    "private_chat",
		"is_direct": true,
		"invite":    ghostUserIDs,
		"creation_content": map[string]any{
			"m.federate": true,
		},
		"initial_state": []map[string]any{
			{
				"type":      "m.room.history_visibility",
				"state_key": "",
				"content": map[string]any{
					"history_visibility": "shared",
				},
			},
		},
	}

	// Set room name for better identification
	if roomName != "" {
		roomData["name"] = roomName
	}

	// Start encrypted when the bridge is set up for it, so the homeserver
	// never holds these conversations in the clear.
	if c.encryptDirectRooms != nil && c.encryptDirectRooms() {
		state, _ := roomData["initial_state"].([]map[string]any)
		roomData["initial_state"] = append(state, map[string]any{
			"type":      "m.room.encryption",
			"state_key": "",
			"content":   map[string]any{"algorithm": "m.megolm.v1.aes-sha2"},
		})
	}

	// For group DMs (more than 2 users), adjust settings
	if len(ghostUserIDs) > 2 {
		if roomName == "" {
			roomData["name"] = "Group Chat"
		}
		roomData["preset"] = "private_chat"
		// Group DMs are not considered "direct" in Matrix spec
		roomData["is_direct"] = false
	}

	jsonData, err := json.Marshal(roomData)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal DM room creation data")
	}

	url := c.serverURL + "/_matrix/client/v3/createRoom"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", errors.Wrap(err, "failed to create DM room creation request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send DM room creation request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read DM room creation response")
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.LogError("Matrix DM room creation failed", "status_code", resp.StatusCode, "response", string(body), "users", ghostUserIDs)
		return "", fmt.Errorf("failed to create DM room: %d %s", resp.StatusCode, string(body))
	}

	var response struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal DM room creation response")
	}

	c.logger.LogInfo("Matrix DM room created successfully", "room_id", response.RoomID, "users", ghostUserIDs)
	return response.RoomID, nil
}

// CreateSpace creates a new Matrix space.
// It returns the space RoomID.
func (c *Client) CreateSpace(name, topic, aliasLocalPart string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	// Apply rate limiting for room creation
	if err := c.waitForRateLimit(c.roomCreationLimiter, "Space creation"); err != nil {
		return "", err
	}

	c.logger.LogDebug("Creating Matrix space", "name", name, "alias_local_part", aliasLocalPart)

	roomData := map[string]any{
		"name":  name,
		"topic": topic,
		"creation_content": map[string]any{
			"type": "m.space",
		},
		"preset":     "public_chat",
		"visibility": "public",
		"power_level_content_override": map[string]any{
			"invite": 0,
		},
	}

	if aliasLocalPart != "" {
		// Try to extract server domain to construct full alias
		domain, err := c.extractServerDomain()
		if err == nil && domain != "" {
			roomData["room_alias_name"] = aliasLocalPart
		}
	}

	resp, body, err := c.postCreateRoom(roomData)
	if err != nil {
		return "", err
	}

	// Same fallback as rooms: an alias outside the reserved namespace must not
	// prevent the space from being created.
	if resp.StatusCode == http.StatusBadRequest && roomData["room_alias_name"] != nil && isAliasNotReservedError(body) {
		c.logger.LogWarn("Homeserver refused the space alias because the appservice registration does not reserve it; creating the space without an alias", "alias_local_part", aliasLocalPart)
		delete(roomData, "room_alias_name")
		resp, body, err = c.postCreateRoom(roomData)
		if err != nil {
			return "", err
		}
	}

	if resp.StatusCode != http.StatusOK {
		matrixErr := parseMatrixError(resp.StatusCode, body)
		if IsRateLimitError(matrixErr) {
			c.logger.LogWarn("Matrix space creation rate limited", "status_code", resp.StatusCode)
			return "", matrixErr
		}
		c.logger.LogError("Matrix space creation failed", "status_code", resp.StatusCode, "response", string(body))
		return "", matrixErr
	}

	var response struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal space creation response")
	}

	c.logger.LogInfo("Matrix space created successfully", "room_id", response.RoomID, "name", name)

	// Join the application service bot to the space
	if err := c.JoinRoom(response.RoomID); err != nil {
		c.logger.LogWarn("Failed to join application service bot to created space", "room_id", response.RoomID, "error", err)
	}

	return response.RoomID, nil
}

// AddSpaceChild adds a child room to a parent space using m.space.child state event.
func (c *Client) AddSpaceChild(spaceID, childRoomID string, via []string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	if len(via) == 0 {
		// Try to add our own server domain if none provided
		domain, err := c.extractServerDomain()
		if err == nil && domain != "" {
			via = []string{domain}
		}
	}

	c.logger.LogDebug("Adding child to space", "space_id", spaceID, "child_room_id", childRoomID, "via", via)

	content := map[string]any{
		"via": via,
	}

	// Create request for PUT /_matrix/client/v3/rooms/{roomId}/state/{eventType}/{stateKey}
	urlPath := fmt.Sprintf("/_matrix/client/v3/rooms/%s/state/m.space.child/%s", url.PathEscape(spaceID), url.PathEscape(childRoomID))
	fullURL := c.serverURL + urlPath

	jsonData, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal space child content")
	}

	req, err := http.NewRequest("PUT", fullURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create space child request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send space child request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read space child response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to add space child: %d %s", resp.StatusCode, string(body))
	}

	c.logger.LogInfo("Successfully added child to space", "space_id", spaceID, "child_room_id", childRoomID)
	return nil
}

// AddRoomAlias adds an additional alias to an existing Matrix room
func (c *Client) AddRoomAlias(roomID, alias string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	c.logger.LogDebug("Adding room alias", "room_id", roomID, "alias", alias)

	requestBody := map[string]string{
		"room_id": roomID,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return errors.Wrap(err, "failed to marshal alias request")
	}

	// URL encode the alias to handle special characters
	encodedAlias := url.PathEscape(alias)
	requestURL := c.serverURL + "/_matrix/client/v3/directory/room/" + encodedAlias

	req, err := http.NewRequest("PUT", requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create alias request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send alias request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read alias response")
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.LogError("Failed to add room alias", "status_code", resp.StatusCode, "response", string(body), "alias", alias, "room_id", roomID)
		return fmt.Errorf("failed to add room alias: %d %s", resp.StatusCode, string(body))
	}

	c.logger.LogDebug("Successfully added room alias", "room_id", roomID, "alias", alias)
	return nil
}

// ResolveRoomAlias resolves a Matrix room alias to its room ID.
func (c *Client) ResolveRoomAlias(roomAlias string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	if !strings.HasPrefix(roomAlias, "#") {
		// If it's already a room ID, return as-is
		return roomAlias, nil
	}

	// URL encode the room alias
	encodedAlias := url.QueryEscape(roomAlias)
	requestURL := c.serverURL + "/_matrix/client/v3/directory/room/" + encodedAlias

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create alias resolution request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send alias resolution request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read alias resolution response")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to resolve room alias: %d %s", resp.StatusCode, string(body))
	}

	var response struct {
		RoomID string `json:"room_id"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal alias resolution response")
	}

	return response.RoomID, nil
}

// PublishRoomToDirectory explicitly publishes a room to the public directory
func (c *Client) PublishRoomToDirectory(roomID string, publish bool) error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}

	requestURL := c.serverURL + "/_matrix/client/v3/directory/list/room/" + url.PathEscape(roomID)

	content := map[string]any{
		"visibility": "public",
	}
	if !publish {
		content["visibility"] = "private"
	}

	jsonData, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal directory visibility content")
	}

	req, err := http.NewRequest("PUT", requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create directory visibility request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send directory visibility request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read directory visibility response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set directory visibility: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetMattermostChannelID retrieves the Mattermost channel ID from the Matrix room's custom state.
// Returns the channel ID if found, or empty string if not set.
func (c *Client) GetMattermostChannelID(roomID string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}

	url := c.serverURL + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/state/com.mattermost.bridge.channel/"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create room state request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send room state request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// State event doesn't exist - this is normal for non-Mattermost rooms
		return "", nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get room state: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read room state response")
	}

	var stateContent struct {
		MattermostChannelID string `json:"mattermost_channel_id"`
		CreatedAt           int64  `json:"created_at"`
	}
	if err := json.Unmarshal(body, &stateContent); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal room state response")
	}

	return stateContent.MattermostChannelID, nil
}

// RemoveMattermostChannelID removes the Mattermost channel ID from the Matrix room's custom state.
// This prevents the room from being found during fallback lookups after unmapping.
func (c *Client) RemoveMattermostChannelID(roomID string) error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}

	url := c.serverURL + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/state/com.mattermost.bridge.channel/"

	// A room the bridge did not create (one mapped with /matrix map) carries no
	// such state, and the bot may not be allowed to write it: nothing to clear.
	var current map[string]any
	if status, err := c.getJSON(url, &current); err == nil && (status == http.StatusNotFound || (status == http.StatusOK && len(current) == 0)) {
		return nil
	}

	// Send an empty object to clear the state
	body := strings.NewReader("{}")
	req, err := http.NewRequest("PUT", url, body)
	if err != nil {
		return errors.Wrap(err, "failed to create room state removal request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send room state removal request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("failed to remove room state: HTTP %d", resp.StatusCode)
	}

	return nil
}

// SetEncryptDirectRooms installs the check CreateDirectRoom makes to decide
// whether a new direct or group room starts with end-to-end encryption.
func (c *Client) SetEncryptDirectRooms(fn func() bool) {
	c.encryptDirectRooms = fn
}

// membershipOf returns a person's membership of a room ("join", "invite",
// "leave", "ban", ...) as the bridge bot can see it, or "" when they have none.
func (c *Client) membershipOf(roomID, userID string) (string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return "", errors.New("matrix client not configured")
	}
	requestURL := c.serverURL + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/state/m.room.member/" + url.PathEscape(userID)
	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create membership request")
	}
	req.Header.Set("Authorization", "Bearer "+c.asToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send membership request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read membership response")
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get membership: %d %s", resp.StatusCode, string(body))
	}
	var ev struct {
		Membership string `json:"membership"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", errors.Wrap(err, "failed to parse membership")
	}
	return ev.Membership, nil
}
