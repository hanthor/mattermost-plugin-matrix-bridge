package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

// SendNoticeAsUser sends an m.notice as the given user and returns the event
// ID. A notice does not trigger bot loops or notification sounds in most
// clients, which suits status messages such as call announcements.
func (c *Client) SendNoticeAsUser(roomID, body, htmlBody, userID string) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}
	if err := c.waitForRateLimit(c.messageLimiter, "Notice"); err != nil {
		return nil, err
	}
	return c.sendEventAsUser(roomID, "m.room.message", noticeContent(body, htmlBody), userID)
}

// EditNoticeAsUser replaces a notice this user sent earlier.
func (c *Client) EditNoticeAsUser(roomID, eventID, body, htmlBody, userID string) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}
	if err := c.waitForRateLimit(c.messageLimiter, "Notice edit"); err != nil {
		return nil, err
	}
	content := noticeContent(" * "+body, "")
	content["m.new_content"] = noticeContent(body, htmlBody)
	content["m.relates_to"] = map[string]any{"rel_type": "m.replace", "event_id": eventID}
	return c.sendEventAsUser(roomID, "m.room.message", content, userID)
}

func noticeContent(body, htmlBody string) map[string]any {
	content := map[string]any{"msgtype": "m.notice", "body": body}
	if htmlBody != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = htmlBody
	}
	return content
}

// SetRoomState sets a state event in a room as the given user (the
// appservice impersonates them) and returns the event ID.
func (c *Client) SetRoomState(roomID, eventType, stateKey string, content any, userID string) (*SendEventResponse, error) {
	if c.asToken == "" {
		return nil, errors.New("application service token not configured")
	}
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "state", eventType, stateKey)
	if err != nil {
		return nil, errors.Wrap(err, "invalid room ID, event type or state key")
	}
	payload, err := json.Marshal(content)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal state content")
	}
	req, err := http.NewRequest(http.MethodPut, c.serverURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req, userID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("matrix state API error: %d %s", resp.StatusCode, string(body))
	}
	var out SendEventResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.Wrap(err, "failed to parse response")
	}
	c.sent.record(out.EventID)
	return &out, nil
}
