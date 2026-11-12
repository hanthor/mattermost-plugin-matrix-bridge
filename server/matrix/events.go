package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"
)

// SendReactionAsGhost sends a reaction to a message as a ghost user
func (c *Client) SendReactionAsGhost(roomID, eventID, emoji, ghostUserID string) (*SendEventResponse, error) {
	return c.SendReactionAsGhostAt(roomID, eventID, emoji, ghostUserID, 0)
}

// SendReactionAsGhostAt is SendReactionAsGhost with the event's time set to
// timestampMs (milliseconds since the epoch) when it is not zero, for history
// import.
func (c *Client) SendReactionAsGhostAt(roomID, eventID, emoji, ghostUserID string, timestampMs int64) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	// Apply rate limiting for reaction sending
	if err := c.waitForRateLimit(c.messageLimiter, "Reaction sending"); err != nil {
		return nil, err
	}

	// Matrix reaction content structure
	content := map[string]any{
		"m.relates_to": map[string]any{
			"rel_type": "m.annotation",
			"event_id": eventID,
			"key":      emoji,
		},
	}

	return c.sendEventAsUserAt(roomID, "m.reaction", content, ghostUserID, timestampMs)
}

// RedactEventAsGhost redacts (removes) an event as a ghost user
func (c *Client) RedactEventAsGhost(roomID, eventID, ghostUserID string) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	// Apply rate limiting for redaction operations
	if err := c.waitForRateLimit(c.messageLimiter, "Event redaction"); err != nil {
		return nil, err
	}

	// Empty content for redaction
	content := map[string]any{}

	txnID := uuid.New().String()
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "redact", eventID, txnID)
	if err != nil {
		return nil, errors.Wrap(err, "invalid room or event ID")
	}
	reqURL := c.serverURL + endpoint

	jsonData, err := json.Marshal(content)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal redaction content")
	}

	req, err := http.NewRequest("PUT", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redaction request")
	}

	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, ghostUserID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send redaction request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read redaction response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("matrix redaction API error: %d %s", resp.StatusCode, string(body))
	}

	var response SendEventResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal redaction response")
	}
	c.sent.record(response.EventID)

	return &response, nil
}

// GetEvent retrieves a single Matrix event by ID
func (c *Client) GetEvent(roomID, eventID string) (map[string]any, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "event", eventID)
	if err != nil {
		return nil, errors.Wrap(err, "invalid room or event ID")
	}
	reqURL := c.serverURL + endpoint

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create get event request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send get event request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read get event response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get event: %d %s", resp.StatusCode, string(body))
	}

	var event map[string]any
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, errors.Wrap(err, "failed to parse get event response")
	}

	return event, nil
}

// GetEventRelationsAsUser retrieves events related to a specific event (like reactions) as a specific user
func (c *Client) GetEventRelationsAsUser(roomID, eventID, userID string) ([]map[string]any, error) {
	if c.serverURL == "" || c.asToken == "" {
		return nil, errors.New("matrix client not configured")
	}

	requestURL := c.serverURL + "/_matrix/client/v1/rooms/" + url.PathEscape(roomID) + "/relations/" + url.PathEscape(eventID)

	// Add user_id query parameter for impersonation
	if userID != "" {
		requestURL += "?user_id=" + url.QueryEscape(userID)
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create relations request")
	}

	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send relations request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read relations response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get event relations: %d %s", resp.StatusCode, string(body))
	}

	var response struct {
		Chunk []map[string]any `json:"chunk"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal relations response")
	}

	return response.Chunk, nil
}

// SendNotice sends an m.notice as the appservice bot.
func (c *Client) SendNotice(roomID, text string) error {
	_, err := c.sendEventAsUser(roomID, "m.room.message", map[string]any{
		"msgtype": "m.notice",
		"body":    text,
	}, "")
	return err
}

// EditMessageAsGhost edits an existing message as a ghost user, with optional HTML formatting
func (c *Client) EditMessageAsGhost(roomID, eventID, newMessage, htmlMessage, ghostUserID string) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	// Apply rate limiting for message edit operations
	if err := c.waitForRateLimit(c.messageLimiter, "Message edit"); err != nil {
		return nil, err
	}

	// Matrix edit event content structure
	newContent := map[string]any{
		"msgtype": "m.text",
		"body":    newMessage,
	}

	// Add HTML formatting to new content if provided
	if htmlMessage != "" {
		newContent["format"] = "org.matrix.custom.html"
		newContent["formatted_body"] = htmlMessage
	}

	content := map[string]any{
		"msgtype":       "m.text",
		"body":          " * " + newMessage, // Fallback for clients that don't support edits
		"m.new_content": newContent,
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": eventID,
		},
	}

	return c.sendEventAsUser(roomID, "m.room.message", content, ghostUserID)
}

// SendMessage sends a message as a ghost user with all optional parameters consolidated into a single request.
func (c *Client) SendMessage(req MessageRequest) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}

	// Apply rate limiting for message sending
	if err := c.waitForRateLimit(c.messageLimiter, "Message sending"); err != nil {
		return nil, err
	}

	// Validate required fields
	if req.RoomID == "" {
		return nil, errors.New("room_id is required")
	}
	if req.GhostUserID == "" {
		return nil, errors.New("ghost_user_id is required")
	}

	c.logger.LogDebug("Sending message as ghost user", "room_id", req.RoomID, "ghost_user_id", req.GhostUserID, "file_count", len(req.Files), "has_text", req.Message != "" || req.HTMLMessage != "")

	// Simplified logic: send text (if any) and files (if any) as separate top-level messages
	// All messages from one Mattermost post will be linked via m.relates_to

	if req.Message == "" && req.HTMLMessage == "" && len(req.Files) == 0 {
		return nil, errors.New("no message content or files to send")
	}

	return c.sendMattermostPost(req)
}

// sendMattermostPost sends all content from a Mattermost post as separate Matrix messages
// Text (if any) and each file become separate top-level messages, linked via m.relates_to
func (c *Client) sendMattermostPost(req MessageRequest) (*SendEventResponse, error) {
	var primaryResponse *SendEventResponse
	var rootEventID string

	// Send text message first if present
	if req.Message != "" || req.HTMLMessage != "" {
		textResponse, err := c.sendTextMessage(req, "")
		if err != nil {
			return nil, errors.Wrap(err, "failed to send text message")
		}
		primaryResponse = textResponse
		rootEventID = textResponse.EventID
		c.logger.LogDebug("Sent text message", "event_id", rootEventID)
	}

	// Send each file as separate top-level message
	for _, file := range req.Files {
		fileResponse, err := c.sendFileMessage(req, file, rootEventID)
		if err != nil {
			// Log error but continue with other files
			c.logger.LogWarn("Failed to send file message", "filename", file.Filename, "error", err)
			continue
		}

		// If this is the first message (no text), use it as primary and root
		if primaryResponse == nil {
			primaryResponse = fileResponse
			rootEventID = fileResponse.EventID
			c.logger.LogDebug("Sent first file message as root", "event_id", rootEventID, "filename", file.Filename)
		} else {
			c.logger.LogDebug("Sent file message linked to root", "event_id", fileResponse.EventID, "filename", file.Filename, "root_event_id", rootEventID)
		}
	}

	if primaryResponse == nil {
		return nil, errors.New("failed to send any content")
	}

	c.logger.LogDebug("Successfully sent Mattermost post", "primary_event_id", primaryResponse.EventID, "text_present", req.Message != "" || req.HTMLMessage != "", "file_count", len(req.Files))
	return primaryResponse, nil
}

// sendTextMessage sends a text message with optional relation to root event
func (c *Client) sendTextMessage(req MessageRequest, rootEventID string) (*SendEventResponse, error) {
	content := make(map[string]any)

	// Text message content
	content["msgtype"] = "m.text"
	content["body"] = req.Message

	// Add HTML formatting if provided
	if req.HTMLMessage != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = req.HTMLMessage
	}

	for k, v := range req.ExtraContent {
		if _, set := content[k]; !set {
			content[k] = v
		}
	}

	// Add mentions if provided
	if req.Mentions != nil {
		content["m.mentions"] = req.Mentions
	}

	// Add threading if provided (takes priority over post grouping)
	if req.ThreadEventID != "" {
		// Clients without thread support show the message as a reply to the
		// thread's root (the fallback in the Matrix spec).
		content["m.relates_to"] = map[string]any{
			"rel_type":        "m.thread",
			"event_id":        req.ThreadEventID,
			"is_falling_back": true,
			"m.in_reply_to":   map[string]any{"event_id": req.ThreadEventID},
		}
	} else if rootEventID != "" {
		// Add relation to root event if no threading (for post grouping)
		content["m.relates_to"] = map[string]any{
			"rel_type": "m.mattermost.post",
			"event_id": rootEventID,
		}
	}

	// Add Mattermost metadata - ALWAYS include for deletion tracking
	if req.PostID != "" {
		content["mattermost_post_id"] = req.PostID
	}
	if c.remoteID != "" {
		content["mattermost_remote_id"] = c.remoteID
	}

	return c.sendEventAsUserAt(req.RoomID, "m.room.message", content, req.GhostUserID, req.Timestamp)
}

// sendFileMessage sends a file message with optional relation to root event
func (c *Client) sendFileMessage(req MessageRequest, file FileAttachment, rootEventID string) (*SendEventResponse, error) {
	content := make(map[string]any)

	// Determine message type based on MIME type
	switch {
	case strings.HasPrefix(file.MimeType, "image/"):
		content["msgtype"] = "m.image"
	case strings.HasPrefix(file.MimeType, "video/"):
		content["msgtype"] = "m.video"
	case strings.HasPrefix(file.MimeType, "audio/"):
		content["msgtype"] = "m.audio"
	default:
		content["msgtype"] = "m.file"
	}

	// File content
	content["body"] = file.Filename
	if file.File != nil {
		content["file"] = file.File
	} else {
		content["url"] = file.MxcURI
	}
	info := map[string]any{
		"size":     file.Size,
		"mimetype": file.MimeType,
	}
	if file.Width > 0 && file.Height > 0 {
		info["w"] = file.Width
		info["h"] = file.Height
	}
	content["info"] = info

	// Add threading if provided (takes priority over post grouping)
	if req.ThreadEventID != "" {
		// Clients without thread support show the message as a reply to the
		// thread's root (the fallback in the Matrix spec).
		content["m.relates_to"] = map[string]any{
			"rel_type":        "m.thread",
			"event_id":        req.ThreadEventID,
			"is_falling_back": true,
			"m.in_reply_to":   map[string]any{"event_id": req.ThreadEventID},
		}
	} else if rootEventID != "" {
		// Add relation to root event if no threading (for post grouping)
		content["m.relates_to"] = map[string]any{
			"rel_type": "m.mattermost.post",
			"event_id": rootEventID,
		}
	}

	// Add Mattermost metadata - ALWAYS include for deletion tracking
	if req.PostID != "" {
		content["mattermost_post_id"] = req.PostID
	}
	if c.remoteID != "" {
		content["mattermost_remote_id"] = c.remoteID
	}

	return c.sendEventAsUserAt(req.RoomID, "m.room.message", content, req.GhostUserID, req.Timestamp)
}

// sendEventAsUser sends an event as a specific user (using application service impersonation)
func (c *Client) sendEventAsUser(roomID, eventType string, content any, userID string) (*SendEventResponse, error) {
	return c.sendEventAsUserAt(roomID, eventType, content, userID, 0)
}

// sendEventAsUserAt is sendEventAsUser with the event's timestamp set to
// timestampMs (milliseconds since the epoch) when it is not zero. Synapse
// accepts this "timestamp massaging" from application services only.
func (c *Client) sendEventAsUserAt(roomID, eventType string, content any, userID string, timestampMs int64) (*SendEventResponse, error) {
	var err error
	if eventType, content, err = c.prepareEvent(roomID, eventType, content); err != nil {
		return nil, err
	}
	txnID := uuid.New().String()
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "send", eventType, txnID)
	if err != nil {
		return nil, errors.Wrap(err, "invalid room ID or event type")
	}
	reqURL := c.serverURL + endpoint

	jsonData, err := json.Marshal(content)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal event content")
	}

	req, err := http.NewRequest("PUT", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)
	if timestampMs > 0 {
		q := req.URL.Query()
		q.Set("ts", strconv.FormatInt(timestampMs, 10))
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("matrix API error: %d %s", resp.StatusCode, string(body))
	}

	var response SendEventResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal response")
	}
	c.sent.record(response.EventID)

	return &response, nil
}

// AddFileMetadataToMessage adds custom metadata to a message containing file attachment event IDs
func (c *Client) AddFileMetadataToMessage(roomID, messageEventID string, fileEventIDs []string, ghostUserID string) error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}

	// Create a custom event to store the file attachment metadata
	// We'll use a custom event type that won't be displayed by Matrix clients
	content := map[string]any{
		"file_attachments":   fileEventIDs,
		"relates_to_message": messageEventID,
		// Add proper Matrix relation so it's returned by the relations API
		"m.relates_to": map[string]any{
			"rel_type": "m.mattermost.file_metadata",
			"event_id": messageEventID,
		},
	}

	// Send as a custom event type that Matrix clients will ignore
	err := c.sendCustomEventAsUser(roomID, "m.mattermost.file_metadata", content, ghostUserID)
	if err != nil {
		return errors.Wrapf(err, "failed to send file metadata event for message %s with %d files", messageEventID, len(fileEventIDs))
	}

	// Log successful metadata creation
	c.logger.LogDebug("Successfully created file metadata event", "message_event_id", messageEventID, "file_count", len(fileEventIDs), "file_event_ids", fileEventIDs)
	return nil
}

// sendCustomEventAsUser sends a custom event type as a specific user
func (c *Client) sendCustomEventAsUser(roomID, eventType string, content any, userID string) error {
	txnID := uuid.New().String()
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "send", eventType, txnID)
	if err != nil {
		return errors.Wrap(err, "invalid room ID or event type")
	}
	reqURL := c.serverURL + endpoint

	jsonData, err := json.Marshal(content)
	if err != nil {
		return errors.Wrap(err, "failed to marshal event content")
	}

	req, err := http.NewRequest("PUT", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("matrix API error: %d %s", resp.StatusCode, string(body))
	}

	return nil
}
