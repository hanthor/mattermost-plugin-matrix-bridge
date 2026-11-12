package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/pkg/errors"
)

// BridgeBotLocalpart is the sender_localpart in the appservice registration.
const BridgeBotLocalpart = "_mattermost_bridge"

// EnsureBotRegistered registers the appservice bot user if the homeserver has
// not created it yet. Some homeservers only create the sender on first use,
// others require an explicit registration before the bot can join rooms.
func (c *Client) EnsureBotRegistered() error {
	if c.serverURL == "" || c.asToken == "" {
		return errors.New("matrix client not configured")
	}
	_, err := c.RegisterUserWithASToken(BridgeBotLocalpart, "")
	return err
}

// Ghost user management functions

// GhostUser represents a Matrix user created by the application service to represent a Mattermost user.
type GhostUser struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

// CreateGhostUser creates a ghost user for a Mattermost user with display name and avatar data
func (c *Client) CreateGhostUser(mattermostUserID, displayName string, avatarData []byte, avatarContentType string) (*GhostUser, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	// Apply rate limiting for user creation operations
	if err := c.waitForRateLimit(c.inviteLimiter, "Ghost user creation"); err != nil {
		return nil, err
	}

	// Extract server domain from serverURL
	serverDomain, err := c.extractServerDomain()
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract server domain")
	}

	// Generate ghost user ID following the namespace pattern
	ghostUsername := fmt.Sprintf("_mattermost_%s", mattermostUserID)
	ghostUserID := fmt.Sprintf("@%s:%s", ghostUsername, serverDomain)

	// Registration data
	regData := map[string]any{
		"type":     "m.login.application_service",
		"username": ghostUsername,
	}

	jsonData, err := json.Marshal(regData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal registration data")
	}

	url := c.serverURL + "/_matrix/client/v3/register"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create registration request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send registration request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read registration response")
	}

	// 200 = created, 400 with M_USER_IN_USE = already exists (both are fine)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return nil, fmt.Errorf("failed to create ghost user: %d %s", resp.StatusCode, string(body))
	}

	// Check if it's a "user already exists" error
	if resp.StatusCode == http.StatusBadRequest {
		var errorResp struct {
			Errcode string `json:"errcode"`
		}
		if err := json.Unmarshal(body, &errorResp); err != nil || errorResp.Errcode != "M_USER_IN_USE" {
			return nil, fmt.Errorf("failed to create ghost user: %d %s", resp.StatusCode, string(body))
		}
		// User already exists, that's fine - continue with profile setup
	}

	// Set display name for the ghost user if provided
	if displayName != "" {
		err = c.SetDisplayName(ghostUserID, displayName)
		if err != nil {
			// Don't fail user creation if display name setting fails
			// Return the error info in a way the caller can log it
			return &GhostUser{
				UserID:   ghostUserID,
				Username: ghostUsername,
			}, errors.Wrap(err, "ghost user created but failed to set display name")
		}
	}

	// Upload and set avatar for the ghost user if provided
	if len(avatarData) > 0 {
		// Upload avatar to Matrix
		mxcURI, err := c.UploadAvatarFromData(avatarData, avatarContentType)
		if err != nil {
			// Don't fail user creation if avatar upload fails
			return &GhostUser{
				UserID:   ghostUserID,
				Username: ghostUsername,
			}, errors.Wrap(err, "ghost user created but failed to upload avatar")
		}

		// Set the uploaded avatar
		err = c.SetAvatarURL(ghostUserID, mxcURI)
		if err != nil {
			// Don't fail user creation if avatar setting fails
			return &GhostUser{
				UserID:   ghostUserID,
				Username: ghostUsername,
			}, errors.Wrap(err, "ghost user created but failed to set avatar")
		}
	}

	return &GhostUser{
		UserID:   ghostUserID,
		Username: ghostUsername,
	}, nil
}

// SetDisplayName sets the display name for a user (using application service impersonation)
func (c *Client) SetDisplayName(userID, displayName string) error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}

	// Apply rate limiting for profile operations
	if err := c.waitForRateLimit(c.inviteLimiter, "Display name setting"); err != nil {
		return err
	}

	// Content for the display name event
	content := map[string]any{
		"displayname": displayName,
	}

	jsonData, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal display name content")
	}

	// Use the profile API endpoint with user impersonation
	requestURL := c.serverURL + "/_matrix/client/v3/profile/" + url.PathEscape(userID) + "/displayname"
	if userID != "" {
		requestURL += "?user_id=" + url.QueryEscape(userID)
	}

	req, err := http.NewRequest("PUT", requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create display name request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send display name request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read display name response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set display name: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetAvatarURL sets the avatar URL for a user (using application service impersonation)
func (c *Client) SetAvatarURL(userID, avatarURL string) error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}

	// Apply rate limiting for profile operations
	if err := c.waitForRateLimit(c.inviteLimiter, "Avatar URL setting"); err != nil {
		return err
	}

	// Content for the avatar URL event
	content := map[string]any{
		"avatar_url": avatarURL,
	}

	jsonData, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal avatar URL content")
	}

	// Use the profile API endpoint with user impersonation
	requestURL := c.serverURL + "/_matrix/client/v3/profile/" + url.PathEscape(userID) + "/avatar_url"
	if userID != "" {
		requestURL += "?user_id=" + url.QueryEscape(userID)
	}

	req, err := http.NewRequest("PUT", requestURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create avatar URL request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send avatar URL request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read avatar URL response")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set avatar URL: %d %s", resp.StatusCode, string(body))
	}

	return nil
}

// UpdateGhostUserAvatar uploads new avatar data and updates the ghost user's avatar
func (c *Client) UpdateGhostUserAvatar(userID string, avatarData []byte, avatarContentType string) error {
	if len(avatarData) == 0 {
		return errors.New("avatar data is empty")
	}

	// Upload avatar to Matrix
	mxcURI, err := c.UploadAvatarFromData(avatarData, avatarContentType)
	if err != nil {
		return errors.Wrap(err, "failed to upload avatar")
	}

	// Set the uploaded avatar
	err = c.SetAvatarURL(userID, mxcURI)
	if err != nil {
		return errors.Wrap(err, "failed to set avatar URL")
	}

	return nil
}

// UserProfile represents a Matrix user's profile information
type UserProfile struct {
	DisplayName string `json:"displayname,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// GetUserProfile retrieves the profile information for a Matrix user
func (c *Client) GetUserProfile(userID string) (*UserProfile, error) {
	if c.serverURL == "" || c.asToken == "" {
		return nil, errors.New("matrix client not configured")
	}

	// URL encode the user ID to handle special characters
	encodedUserID := url.PathEscape(userID)
	requestURL := c.serverURL + "/_matrix/client/v3/profile/" + encodedUserID

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create profile request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send profile request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read profile response")
	}

	if resp.StatusCode != http.StatusOK {
		c.logger.LogWarn("Failed to get Matrix user profile", "status_code", resp.StatusCode, "response", string(body), "user_id", userID)
		// Return empty profile rather than error - user might not have set a display name
		return &UserProfile{}, nil
	}

	var profile UserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal profile response")
	}

	c.logger.LogDebug("Successfully retrieved Matrix user profile", "user_id", userID, "display_name", profile.DisplayName)
	return &profile, nil
}

// RegisterUser creates a regular Matrix user (not a ghost user) with rate limiting
func (c *Client) RegisterUser(username, password string) (*RegisterResponse, error) {
	// Apply rate limiting for user registration operations (rc_registration)
	if err := c.waitForRateLimit(c.registrationLimiter, "User registration"); err != nil {
		return nil, err
	}

	// First, try to get registration flows
	_, err := c.makeMatrixRequest("POST", "/_matrix/client/v3/register", map[string]any{}, "")
	if err != nil {
		// If we get an error, it might contain flow information - this is expected
		c.logger.LogDebug("Registration flow error (expected)", "error", err)
	}

	// Try registration with dummy auth
	userData := map[string]any{
		"username": username,
		"password": password,
		"auth": map[string]any{
			"type": "m.login.dummy",
		},
	}

	result, err := c.makeMatrixRequest("POST", "/_matrix/client/v3/register", userData, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to register user")
	}

	var response RegisterResponse
	responseBytes, err := json.Marshal(result)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal registration response")
	}

	err = json.Unmarshal(responseBytes, &response)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal registration response")
	}

	return &response, nil
}

// RegisterResponse represents the response from Matrix user registration
type RegisterResponse struct {
	UserID      string `json:"user_id"`
	AccessToken string `json:"access_token"`
	HomeServer  string `json:"home_server"`
	DeviceID    string `json:"device_id"`
}

// RegisterUserWithASToken creates a Matrix user using Application Service authentication
// This bypasses normal registration restrictions and allows creating users in the AS namespace
func (c *Client) RegisterUserWithASToken(username, password string) (*RegisterResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("Application Service token not configured")
	}

	// Apply rate limiting
	if err := c.waitForRateLimit(c.registrationLimiter, "Mirror mode user registration"); err != nil {
		return nil, err
	}

	// Register user with AS token authentication
	userData := map[string]any{
		"type":     "m.login.application_service",
		"username": username,
	}

	if password != "" {
		userData["password"] = password
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/register?access_token=%s", c.serverURL, url.QueryEscape(c.asToken))

	jsonData, err := json.Marshal(userData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal registration data")
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create registration request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send registration request")
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read registration response")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Check if user already exists
		if resp.StatusCode == http.StatusBadRequest {
			var errResp map[string]any
			if json.Unmarshal(bodyBytes, &errResp) == nil {
				if errCode, ok := errResp["errcode"].(string); ok && errCode == "M_USER_IN_USE" {
					// User already exists, return a success-like response
					userID := fmt.Sprintf("@%s:%s", username, c.getServerName())
					return &RegisterResponse{
						UserID: userID,
					}, nil
				}
			}
		}
		return nil, errors.Errorf("registration failed: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var response RegisterResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal registration response")
	}

	return &response, nil
}

// SetUserDisplayName sets the display name for a Matrix user using AS authentication
func (c *Client) SetUserDisplayName(userID, displayName string) error {
	if c.asToken == "" {
		return errors.New("Application Service token not configured")
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/profile/%s/displayname?user_id=%s&access_token=%s",
		c.serverURL,
		url.PathEscape(userID),
		url.QueryEscape(userID),
		url.QueryEscape(c.asToken))

	data := map[string]string{
		"displayname": displayName,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "failed to marshal display name data")
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create display name request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send display name request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return errors.Errorf("failed to set display name: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// SetUserAvatar sets the avatar for a Matrix user using AS authentication
func (c *Client) SetUserAvatar(userID, avatarURL string) error {
	if c.asToken == "" {
		return errors.New("Application Service token not configured")
	}

	url := fmt.Sprintf("%s/_matrix/client/v3/profile/%s/avatar_url?user_id=%s&access_token=%s",
		c.serverURL,
		url.PathEscape(userID),
		url.QueryEscape(userID),
		url.QueryEscape(c.asToken))

	data := map[string]string{
		"avatar_url": avatarURL,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return errors.Wrap(err, "failed to marshal avatar data")
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create avatar request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send avatar request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return errors.Errorf("failed to set avatar: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
