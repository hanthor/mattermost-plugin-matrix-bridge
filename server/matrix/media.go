package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"
)

// UploadMedia uploads media content to the Matrix server and returns the mxc:// URI
func (c *Client) UploadMedia(data []byte, filename, contentType string) (string, error) {
	if c.asToken == "" {
		return "", errors.New("application service token not configured")
	}

	// Apply rate limiting for media upload operations
	if err := c.waitForRateLimit(c.messageLimiter, "Media upload"); err != nil {
		return "", err
	}

	// Use the media upload endpoint
	requestURL := c.serverURL + "/_matrix/media/v3/upload"
	if filename != "" {
		requestURL += "?filename=" + url.QueryEscape(filename)
	}

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(data))
	if err != nil {
		return "", errors.Wrap(err, "failed to create media upload request")
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to send media upload request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read media upload response")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to upload media: %d %s", resp.StatusCode, string(body))
	}

	var response struct {
		ContentURI string `json:"content_uri"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal media upload response")
	}

	return response.ContentURI, nil
}

// UploadAvatarFromData uploads avatar image data to Matrix and returns mxc:// URI
func (c *Client) UploadAvatarFromData(imageData []byte, contentType string) (string, error) {
	if len(imageData) == 0 {
		return "", errors.New("image data is empty")
	}

	// Determine content type if not provided
	if contentType == "" {
		contentType = "application/octet-stream" // fallback
	}

	// Generate filename based on content type
	var filename string
	switch contentType {
	case "image/jpeg":
		filename = "avatar.jpg"
	case "image/png":
		filename = "avatar.png"
	case "image/gif":
		filename = "avatar.gif"
	case "image/webp":
		filename = "avatar.webp"
	default:
		filename = "avatar"
	}

	// Upload to Matrix
	mxcURI, err := c.UploadMedia(imageData, filename, contentType)
	if err != nil {
		return "", errors.Wrap(err, "failed to upload avatar to Matrix")
	}

	return mxcURI, nil
}

// DownloadFile downloads file data from a Matrix MXC URI with configurable size limit
func (c *Client) DownloadFile(mxcURI string, maxSize int64, contentTypePrefix string) ([]byte, error) {
	if mxcURI == "" {
		return nil, errors.New("MXC URI is empty")
	}

	// Matrix file URIs are in the format mxc://server/media_id
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return nil, errors.New("invalid Matrix MXC URI format")
	}

	// Extract server and media ID from mxc://server/media_id
	mxcParts := strings.TrimPrefix(mxcURI, "mxc://")
	parts := strings.SplitN(mxcParts, "/", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid Matrix MXC URI format")
	}

	serverName := parts[0]
	mediaID := parts[1]

	// Validate MXC URI components for path traversal attacks
	if err := ValidateMXCComponents(serverName, mediaID); err != nil {
		return nil, errors.Wrap(err, "invalid MXC URI components")
	}

	// Build secure media download URLs with proper escaping
	var downloadURLs []string
	var validationErrors []error

	// NEW: Try client API endpoints first (newer Synapse versions use these)
	url1, err := BuildSecureURL(c.serverURL+"/_matrix/client/v1/media/download/", serverName, mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url1)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "client v1 with server"))
	}

	url2, err := BuildSecureURL(c.serverURL+"/_matrix/client/v1/media/download/", mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url2)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "client v1 media-only"))
	}

	// Standard Matrix media repository API v3
	url3, err := BuildSecureURL(c.serverURL+"/_matrix/media/v3/download/", serverName, mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url3)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "media v3 with server"))
	}

	// Fallback to v1 API (some older servers)
	url4, err := BuildSecureURL(c.serverURL+"/_matrix/media/v1/download/", serverName, mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url4)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "media v1 with server"))
	}

	// Alternative endpoint without server name (some configurations)
	url5, err := BuildSecureURL(c.serverURL+"/_matrix/media/v3/download/", mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url5)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "media v3 media-only"))
	}

	url6, err := BuildSecureURL(c.serverURL+"/_matrix/media/v1/download/", mediaID)
	if err == nil {
		downloadURLs = append(downloadURLs, url6)
	} else {
		validationErrors = append(validationErrors, errors.Wrap(err, "media v1 media-only"))
	}

	if len(downloadURLs) == 0 {
		// Collect all validation error messages for debugging
		var errorMsgs []string
		for _, validationErr := range validationErrors {
			errorMsgs = append(errorMsgs, validationErr.Error())
		}
		return nil, errors.Errorf("failed to construct any valid download URLs for MXC URI %s - validation errors: %s",
			mxcURI, strings.Join(errorMsgs, "; "))
	}

	var lastErr error
	for i, downloadURL := range downloadURLs {
		c.logger.LogDebug("Attempting to download Matrix file", "url", downloadURL, "attempt", i+1, "mxc_uri", mxcURI)

		req, err := http.NewRequest("GET", downloadURL, nil)
		if err != nil {
			c.logger.LogWarn("Failed to create file download request", "error", err, "url", downloadURL)
			lastErr = err
			continue
		}

		// Add authorization header for authenticated download
		req.Header.Set("Authorization", "Bearer "+c.asToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.logger.LogWarn("Failed to download file from URL", "error", err, "url", downloadURL)
			lastErr = err
			continue
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			c.logger.LogWarn("Matrix media endpoint returned error", "url", downloadURL, "status", resp.StatusCode)
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, downloadURL)
			continue
		}

		// Check content type if specified
		contentType := resp.Header.Get("Content-Type")
		if contentTypePrefix != "" && !strings.HasPrefix(contentType, contentTypePrefix) {
			c.logger.LogWarn("Invalid content type", "content_type", contentType, "expected_prefix", contentTypePrefix, "url", downloadURL)
			lastErr = fmt.Errorf("invalid content type: %s (expected prefix: %s)", contentType, contentTypePrefix)
			continue
		}

		// Read the file data
		fileData, err := io.ReadAll(resp.Body)
		if err != nil {
			c.logger.LogWarn("Failed to read file data", "error", err, "url", downloadURL)
			lastErr = err
			continue
		}

		// Check size limit
		if maxSize > 0 && int64(len(fileData)) > maxSize {
			c.logger.LogWarn("File too large", "size", len(fileData), "max", maxSize, "url", downloadURL)
			lastErr = fmt.Errorf("file too large: %d bytes (max %d)", len(fileData), maxSize)
			continue
		}

		c.logger.LogDebug("Successfully downloaded Matrix file", "url", downloadURL, "size", len(fileData), "content_type", contentType, "mxc_uri", mxcURI)
		return fileData, nil
	}

	// If we get here, all attempts failed
	return nil, errors.Wrapf(lastErr, "failed to download file from any endpoint for MXC URI: %s", mxcURI)
}
