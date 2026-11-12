package matrix

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/pkg/errors"
)

// ServerVersionResponse represents the response from the Matrix server version endpoint
type ServerVersionResponse struct {
	Server map[string]any `json:"server,omitempty"`
}

// ServerInfo contains server name and version information
type ServerInfo struct {
	Name    string
	Version string
}

// GetServerVersion retrieves the Matrix server version information
func (c *Client) GetServerVersion() (string, error) {
	if c.serverURL == "" {
		return "", errors.New("matrix server URL not configured")
	}

	requestURL := c.serverURL + "/_matrix/federation/v1/version"

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create version request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send version request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read version response")
	}

	if resp.StatusCode != http.StatusOK {
		// Fall back to trying the client API endpoint if federation API is not available
		return c.getServerVersionFromClient()
	}

	var versionResp ServerVersionResponse
	if err := json.Unmarshal(body, &versionResp); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal version response")
	}

	// Extract version information from the server field
	if versionResp.Server != nil {
		if version, exists := versionResp.Server["version"].(string); exists && version != "" {
			return version, nil
		}
		if name, exists := versionResp.Server["name"].(string); exists && name != "" {
			return name, nil
		}
	}

	return "Unknown", nil
}

// getServerVersionFromClient tries to get version info from client API endpoints
func (c *Client) getServerVersionFromClient() (string, error) {
	// Try the client versions endpoint
	requestURL := c.serverURL + "/_matrix/client/versions"

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create client versions request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send client versions request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return "Matrix Server (version info not available)", nil
	}

	return "", errors.New("unable to determine server version")
}

// TestApplicationServicePermissions tests AS permissions without making invasive changes
func (c *Client) TestApplicationServicePermissions() error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}

	// Test 1: Try to query a user that should be in our namespace but doesn't exist
	// This tests if we have permission to query users in our namespace
	testUserID := "@_mattermost_nonexistent_test_user:" + c.extractServerDomainForTest()

	requestURL := c.serverURL + "/_matrix/client/v3/profile/" + url.PathEscape(testUserID)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create AS permission test request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send AS permission test request")
	}
	defer func() { _ = resp.Body.Close() }()

	// We expect either:
	// - 404: User doesn't exist (good, we have permission to query our namespace)
	// - 403: Forbidden (bad, AS not properly configured)
	// - 401: Unauthorized (bad, token invalid)

	if resp.StatusCode == http.StatusNotFound {
		// This is expected - user doesn't exist but we have permission to query
		return nil
	}

	if resp.StatusCode == http.StatusForbidden {
		return errors.New("application service lacks permission to query users in its namespace")
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("application service token is invalid or not recognized")
	}

	// Any other status code is also acceptable (user might exist, etc.)
	return nil
}

// GetServerInfo retrieves both server name and version information
func (c *Client) GetServerInfo() (*ServerInfo, error) {
	if c.serverURL == "" {
		return nil, errors.New("matrix server URL not configured")
	}

	requestURL := c.serverURL + "/_matrix/federation/v1/version"

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create version request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send version request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read version response")
	}

	if resp.StatusCode != http.StatusOK {
		// Fall back to basic info if federation API is not available
		return &ServerInfo{
			Name:    "Matrix Server",
			Version: "Unknown",
		}, nil
	}

	var versionResp ServerVersionResponse
	if err := json.Unmarshal(body, &versionResp); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal version response")
	}

	serverInfo := &ServerInfo{
		Name:    "Matrix Server",
		Version: "Unknown",
	}

	// Extract server information from the response
	if versionResp.Server != nil {
		if name, exists := versionResp.Server["name"].(string); exists && name != "" {
			serverInfo.Name = name
		}
		if version, exists := versionResp.Server["version"].(string); exists && version != "" {
			serverInfo.Version = version
		}
	}

	return serverInfo, nil
}
