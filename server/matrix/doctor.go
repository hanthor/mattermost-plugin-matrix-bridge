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

// ServerVersions returns the Matrix spec versions the homeserver supports.
func (c *Client) ServerVersions() ([]string, error) {
	var out struct {
		Versions []string `json:"versions"`
	}
	status, err := c.getPublicJSON(c.serverURL+"/_matrix/client/versions", &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("the homeserver answered %d", status)
	}
	return out.Versions, nil
}

func (c *Client) getPublicJSON(requestURL string, into any) (int, error) {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK {
		_ = json.Unmarshal(body, into)
	}
	return resp.StatusCode, nil
}

// WhoAmIAsBridge asks the homeserver who the appservice token belongs to.
func (c *Client) WhoAmIAsBridge() (string, error) {
	if c.asToken == "" {
		return "", errors.New("the appservice token is not set")
	}
	return c.WhoAmI(c.asToken)
}

// PingAppservice makes the homeserver call the bridge back through the
// registered URL, which proves the URL, the network path and the homeserver
// token in one go. It returns the round-trip time in milliseconds. The
// registration ID is the id: in the registration file.
func (c *Client) PingAppservice(registrationID string) (int64, error) {
	body, _ := json.Marshal(map[string]string{"transaction_id": "matrix-bridge-doctor"})
	endpoint := c.serverURL + "/_matrix/client/v1/appservice/" + url.PathEscape(registrationID) + "/ping"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return 0, parseMatrixError(resp.StatusCode, data)
	}
	var out struct {
		DurationMs int64 `json:"duration_ms"`
	}
	_ = json.Unmarshal(data, &out)
	return out.DurationMs, nil
}

// BotPowerLevel returns the power level of a user in a room, as the bridge bot sees it.
func (c *Client) BotPowerLevel(roomID, userID string) (int, error) {
	if creator, err := c.isRoomCreator(roomID, userID); err == nil && creator {
		return creatorPowerLevel, nil
	}
	var levels struct {
		Users        map[string]int `json:"users"`
		UsersDefault int            `json:"users_default"`
	}
	status, err := c.getJSON(c.serverURL+"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/m.room.power_levels/", &levels)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("could not read the room's power levels: status %d", status)
	}
	if level, ok := levels.Users[userID]; ok {
		return level, nil
	}
	return levels.UsersDefault, nil
}

// BotIsInRoom reports whether the bridge bot has joined a room.
func (c *Client) BotIsInRoom(roomID string) (bool, error) {
	var joined struct {
		JoinedRooms []string `json:"joined_rooms"`
	}
	status, err := c.getJSON(c.serverURL+"/_matrix/client/v3/joined_rooms", &joined)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("could not list the bot's rooms: status %d", status)
	}
	for _, r := range joined.JoinedRooms {
		if r == roomID {
			return true, nil
		}
	}
	return false, nil
}

// creatorPowerLevel stands for the unlimited power room creators have in room
// version 12 and later.
const creatorPowerLevel = 1 << 53

// isRoomCreator reports whether the user created the room (or was named an
// additional creator) in a room version where creators hold unlimited power
// and are not listed in the power levels.
func (c *Client) isRoomCreator(roomID, userID string) (bool, error) {
	var state []struct {
		Type    string `json:"type"`
		Sender  string `json:"sender"`
		Content struct {
			RoomVersion       string   `json:"room_version"`
			AdditionalCreator []string `json:"additional_creators"`
		} `json:"content"`
	}
	status, err := c.getJSON(c.serverURL+"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state", &state)
	if err != nil || status != http.StatusOK {
		return false, err
	}
	for _, ev := range state {
		if ev.Type != "m.room.create" {
			continue
		}
		version := ev.Content.RoomVersion
		if version == "" || version == "1" || version == "2" || version == "3" || version == "4" || version == "5" ||
			version == "6" || version == "7" || version == "8" || version == "9" || version == "10" || version == "11" {
			return false, nil // creators are ordinary entries in the power levels
		}
		if ev.Sender == userID {
			return true, nil
		}
		for _, extra := range ev.Content.AdditionalCreator {
			if extra == userID {
				return true, nil
			}
		}
	}
	return false, nil
}

// GetPowerLevels returns a room's power levels content.
func (c *Client) GetPowerLevels(roomID string) (map[string]any, error) {
	var levels map[string]any
	status, err := c.getJSON(c.serverURL+"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/m.room.power_levels/", &levels)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("could not read the room's power levels: status %d", status)
	}
	return levels, nil
}
