package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// sentEventTTL is how long the client remembers the events it sent. The
// homeserver echoes every event back to the appservice within moments, so ten
// minutes covers retries and slow transactions.
const sentEventTTL = 10 * time.Minute

// sentEvents remembers the IDs of events this client sent so the inbound
// side can tell its own echoes from events a person sent from their own
// client with the same account (a claimed mirror account).
type sentEvents struct {
	mu     sync.Mutex
	events map[string]time.Time
	shared SharedSentStore
}

// SharedSentStore keeps the record of sent events where every node of a
// cluster can see it, so an echo that reaches another node than the one that
// sent the event is still recognised.
type SharedSentStore interface {
	MarkSent(eventID string)
	WasSent(eventID string) bool
}

func newSentEvents() *sentEvents {
	return &sentEvents{events: make(map[string]time.Time)}
}

func (s *sentEvents) record(eventID string) {
	if s == nil || eventID == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shared != nil {
		go s.shared.MarkSent(eventID)
	}
	s.events[eventID] = now.Add(sentEventTTL)
	if len(s.events) > 4096 {
		for id, expires := range s.events {
			if now.After(expires) {
				delete(s.events, id)
			}
		}
	}
}

func (s *sentEvents) has(eventID string) bool {
	if s == nil || eventID == "" {
		return false
	}
	s.mu.Lock()
	expires, ok := s.events[eventID]
	if ok && time.Now().After(expires) {
		delete(s.events, eventID)
		ok = false
	}
	shared := s.shared
	s.mu.Unlock()
	if !ok && shared != nil {
		return shared.WasSent(eventID)
	}
	return ok
}

// SetSharedSentStore makes the record of sent events visible to other nodes.
func (c *Client) SetSharedSentStore(store SharedSentStore) {
	if c.sent == nil {
		return
	}
	c.sent.mu.Lock()
	c.sent.shared = store
	c.sent.mu.Unlock()
}

// RecordSentEvent notes an event ID that the bridge sent.
func (c *Client) RecordSentEvent(eventID string) {
	c.sent.record(eventID)
}

// SentByBridge reports whether the bridge sent the event (recently), so an
// echo of it must not be relayed back.
func (c *Client) SentByBridge(eventID string) bool {
	return c.sent.has(eventID)
}

// RegisterMirrorUser creates the mirror-mode account for a localpart through
// the appservice registration flow. Without a password the account has no
// credentials at all (inhibit_login), which is the default: a person gets
// into it by claiming it, not through a shared or guessable password.
//
// created is false when the homeserver says the user already exists; the
// caller decides whether that account is theirs. The old behaviour of
// treating M_USER_IN_USE as success silently adopted accounts the bridge did
// not own.
func (c *Client) RegisterMirrorUser(localpart, password string) (userID string, created bool, err error) {
	if c.asToken == "" {
		return "", false, errors.New("application service token not configured")
	}
	if err := c.waitForRateLimit(c.registrationLimiter, "Mirror mode user registration"); err != nil {
		return "", false, err
	}

	data := map[string]any{
		"type":     "m.login.application_service",
		"username": localpart,
	}
	if password != "" {
		data["password"] = password
	} else {
		data["inhibit_login"] = true
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to marshal registration data")
	}

	req, err := http.NewRequest(http.MethodPost, c.serverURL+"/_matrix/client/v3/register", bytes.NewReader(payload))
	if err != nil {
		return "", false, errors.Wrap(err, "failed to create registration request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.asToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to send registration request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, errors.Wrap(err, "failed to read registration response")
	}

	if resp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			ErrCode string `json:"errcode"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.ErrCode == "M_USER_IN_USE" {
			return fmt.Sprintf("@%s:%s", localpart, c.getServerName()), false, nil
		}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", false, errors.Errorf("registration failed: HTTP %d - %s", resp.StatusCode, string(body))
	}

	var out struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", false, errors.Wrap(err, "failed to parse registration response")
	}
	if out.UserID == "" {
		out.UserID = fmt.Sprintf("@%s:%s", localpart, c.getServerName())
	}
	return out.UserID, true, nil
}

// SetUserPasswordAsAdmin sets a password for a local account through the
// Synapse admin API, without logging out existing sessions. It is the
// fallback for handing a mirrored account to a person on deployments that
// cannot run shared login.
func (c *Client) SetUserPasswordAsAdmin(adminToken, userID, password string) error {
	if adminToken == "" {
		return errors.New("no admin token configured")
	}
	payload, err := json.Marshal(map[string]any{"password": password, "logout_devices": false})
	if err != nil {
		return errors.Wrap(err, "failed to marshal request")
	}
	endpoint := c.serverURL + "/_synapse/admin/v2/users/" + url.PathEscape(userID)
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return errors.Errorf("admin API returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// DeactivateUserAsAdmin deactivates a homeserver account through the Synapse
// admin API. The account keeps its history; it can no longer be used.
func (c *Client) DeactivateUserAsAdmin(adminToken, userID string) error {
	if adminToken == "" {
		return errors.New("no admin token configured")
	}
	endpoint := c.serverURL + "/_synapse/admin/v1/deactivate/" + url.PathEscape(userID)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader([]byte(`{"erase":false}`)))
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("admin API returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// VerifiedEmailsAsAdmin returns the validated email addresses the homeserver
// holds for an account, through the Synapse admin API.
func (c *Client) VerifiedEmailsAsAdmin(adminToken, userID string) ([]string, error) {
	if adminToken == "" {
		return nil, errors.New("no admin token configured")
	}
	req, err := http.NewRequest(http.MethodGet, c.serverURL+"/_synapse/admin/v2/users/"+url.PathEscape(userID), nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("admin API returned %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		ThreePIDs []struct {
			Medium      string `json:"medium"`
			Address     string `json:"address"`
			ValidatedAt int64  `json:"validated_at"`
		} `json:"threepids"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, errors.Wrap(err, "failed to parse the account")
	}
	var emails []string
	for _, t := range out.ThreePIDs {
		if t.Medium == "email" && t.ValidatedAt > 0 && t.Address != "" {
			emails = append(emails, t.Address)
		}
	}
	return emails, nil
}
