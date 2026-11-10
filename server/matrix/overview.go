package matrix

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/pkg/errors"
)

// RoomOverview is what the Mattermost panel shows about a bridged room.
type RoomOverview struct {
	Name    string
	Members int
}

// GetRoomOverview returns a room's name and how many accounts have joined,
// as seen by the bridge bot. A missing name is not an error.
func (c *Client) GetRoomOverview(roomID string) (RoomOverview, error) {
	var out RoomOverview
	if c.serverURL == "" || c.asToken == "" {
		return out, errors.New("matrix client not configured")
	}
	base := c.serverURL + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID)

	var joined struct {
		Joined map[string]json.RawMessage `json:"joined"`
	}
	status, err := c.getJSON(base+"/joined_members", &joined)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("could not list room members: status %d", status)
	}
	out.Members = len(joined.Joined)

	var name struct {
		Name string `json:"name"`
	}
	if status, err := c.getJSON(base+"/state/m.room.name/", &name); err == nil && status == http.StatusOK {
		out.Name = name.Name
	}
	return out, nil
}

// getJSON GETs a client API URL as the bridge bot and decodes a 200 response.
func (c *Client) getJSON(requestURL string, into any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, errors.Wrap(err, "failed to create request")
	}
	req.Header.Set("Authorization", "Bearer "+c.asToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, errors.Wrap(err, "failed to read response")
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, into); err != nil {
			return resp.StatusCode, errors.Wrap(err, "failed to decode response")
		}
	}
	return resp.StatusCode, nil
}

// JoinedMembers lists the accounts that have joined a room, as seen by the
// bridge bot.
func (c *Client) JoinedMembers(roomID string) ([]string, error) {
	if c.serverURL == "" || c.asToken == "" {
		return nil, errors.New("matrix client not configured")
	}
	var joined struct {
		Joined map[string]json.RawMessage `json:"joined"`
	}
	status, err := c.getJSON(c.serverURL+"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/joined_members", &joined)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("could not list room members: status %d", status)
	}
	out := make([]string, 0, len(joined.Joined))
	for id := range joined.Joined {
		out = append(out, id)
	}
	return out, nil
}
