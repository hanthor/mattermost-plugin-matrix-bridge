package matrix

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pkg/errors"
)

// RoomMessagesBackwards returns one page of a room's timeline, newest first,
// as the bridge bot. from is the token the previous page returned ("" for the
// newest events); next is "" when the start of the room has been reached.
// Events the bot may not see (history visibility, encrypted content it has no
// keys for) are what the homeserver returns: encrypted events stay encrypted.
func (c *Client) RoomMessagesBackwards(roomID, from string, limit int) (events []map[string]any, next string, err error) {
	if c.asToken == "" {
		return nil, "", errors.New("application service token not configured")
	}
	endpoint, err := BuildSecureURL("/_matrix/client/v3/rooms/", roomID, "messages")
	if err != nil {
		return nil, "", errors.Wrap(err, "invalid room ID")
	}
	query := url.Values{"dir": {"b"}, "limit": {strconv.Itoa(limit)}}
	if from != "" {
		query.Set("from", from)
	}
	req, err := http.NewRequest(http.MethodGet, c.serverURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to create messages request")
	}
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to read the room's history")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to read the history response")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("the homeserver refused to give the room's history: %d %s", resp.StatusCode, string(body))
	}
	var page struct {
		Chunk []map[string]any `json:"chunk"`
		End   string           `json:"end"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", errors.Wrap(err, "failed to parse the history response")
	}
	return page.Chunk, page.End, nil
}
