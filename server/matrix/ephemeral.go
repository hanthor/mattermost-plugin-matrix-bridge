package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

// putJSONAsUser PUTs or POSTs a small JSON body as the given user.
func (c *Client) sendJSONAsUser(method, path string, body any, userID string) error {
	if c.asToken == "" {
		return errors.New("application service token not configured")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "failed to marshal the request")
	}
	req, err := http.NewRequest(method, c.serverURL+path, bytes.NewReader(payload))
	if err != nil {
		return errors.Wrap(err, "failed to create the request")
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send the request")
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("matrix API error: %d %s", resp.StatusCode, string(data))
	}
	return nil
}

// SetTyping shows or clears the typing indicator of a user in a room.
func (c *Client) SetTyping(roomID, userID string, typing bool, timeoutMs int) error {
	path, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "typing", userID)
	if err != nil {
		return errors.Wrap(err, "invalid room or user ID")
	}
	body := map[string]any{"typing": typing}
	if typing {
		body["timeout"] = timeoutMs
	}
	return c.sendJSONAsUser(http.MethodPut, path, body, userID)
}

// SetPresence sets a user's presence ("online", "unavailable" or "offline")
// and an optional status message.
func (c *Client) SetPresence(userID, presence, statusMsg string) error {
	path, err := BuildSecureURL("/_matrix/client/v3/presence/", userID, "status")
	if err != nil {
		return errors.Wrap(err, "invalid user ID")
	}
	body := map[string]any{"presence": presence}
	if statusMsg != "" {
		body["status_msg"] = statusMsg
	}
	return c.sendJSONAsUser(http.MethodPut, path, body, userID)
}

// SendReadReceipt marks an event as read by a user.
func (c *Client) SendReadReceipt(roomID, eventID, userID string) error {
	path, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "receipt", "m.read", eventID)
	if err != nil {
		return errors.Wrap(err, "invalid room or event ID")
	}
	return c.sendJSONAsUser(http.MethodPost, path, map[string]any{}, userID)
}
