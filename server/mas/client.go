// Package mas talks to the Matrix Authentication Service admin API.
//
// When a homeserver delegates sign-in to MAS, accounts the bridge registers
// in Synapse are unknown to MAS and MAS refuses to create a user for a name
// Synapse already has. The bridge therefore tells MAS about each mirror
// account (without the homeserver check, which MAS offers for exactly this)
// and, when it can, links the account to the person's identity at the shared
// OpenID Connect provider, so signing in through that provider lands on the
// account that already exists.
package mas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// Client is an authenticated MAS admin API client. It is safe for concurrent
// use.
type Client struct {
	base         string
	clientID     string
	clientSecret string
	http         *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// New returns a client for the MAS at baseURL, authenticating as the given
// client with the client credentials grant.
func New(baseURL, clientID, clientSecret string) *Client {
	return &Client{
		base:         strings.TrimRight(baseURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 20 * time.Second},
	}
}

// Configured reports whether every setting needed to talk to MAS is present.
func Configured(baseURL, clientID, clientSecret string) bool {
	return baseURL != "" && clientID != "" && clientSecret != ""
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"urn:mas:admin"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.Wrap(err, "failed to build the MAS token request")
	}
	req.SetBasicAuth(url.QueryEscape(c.clientID), url.QueryEscape(c.clientSecret))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to reach MAS for a token")
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("MAS refused the admin client credentials: %d %s", resp.StatusCode, truncate(body))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", errors.New("MAS returned no access token")
	}
	c.token = tok.AccessToken
	lifetime := time.Duration(tok.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 5 * time.Minute
	}
	c.expires = time.Now().Add(lifetime - 30*time.Second)
	return c.token, nil
}

// do sends an authenticated JSON request and returns the status and body.
func (c *Client) do(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, errors.Wrap(err, "failed to encode the MAS request")
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, nil, errors.Wrap(err, "failed to build the MAS request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.Wrap(err, "failed to reach MAS")
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		// The token may have been revoked: forget it so the next call asks again.
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
	}
	return resp.StatusCode, data, nil
}

type singleResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// EnsureUser makes sure MAS has a user with this localpart and returns its
// MAS ID. The homeserver check is skipped: the account already exists in
// Synapse because the bridge registered it there.
func (c *Client) EnsureUser(ctx context.Context, localpart, displayName string) (id string, created bool, err error) {
	payload := map[string]any{"username": localpart, "skip_homeserver_check": true}
	if displayName != "" {
		payload["displayname"] = displayName
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api/admin/v1/users", payload)
	if err != nil {
		return "", false, err
	}
	switch status {
	case http.StatusCreated, http.StatusOK:
		var r singleResponse
		if err := json.Unmarshal(body, &r); err != nil || r.Data.ID == "" {
			return "", false, errors.New("MAS created the user but returned no ID")
		}
		return r.Data.ID, true, nil
	case http.StatusConflict:
		// Already known to MAS: find it.
		status, body, err := c.do(ctx, http.MethodGet, "/api/admin/v1/users/by-username/"+url.PathEscape(localpart), nil)
		if err != nil {
			return "", false, err
		}
		var r singleResponse
		if status != http.StatusOK || json.Unmarshal(body, &r) != nil || r.Data.ID == "" {
			return "", false, fmt.Errorf("MAS reports %q exists but could not return it: %d %s", localpart, status, truncate(body))
		}
		return r.Data.ID, false, nil
	default:
		return "", false, fmt.Errorf("MAS could not create the user %q: %d %s", localpart, status, truncate(body))
	}
}

// LinkUpstream links the MAS user to a person at an upstream OpenID Connect
// provider, identified by the provider's subject for them. Signing in through
// that provider as that person then lands on this account. Linking the same
// subject to the same user again is not an error; a subject that belongs to
// another user is.
func (c *Client) LinkUpstream(ctx context.Context, userID, providerID, subject, accountName string) error {
	payload := map[string]any{"user_id": userID, "provider_id": providerID, "subject": subject}
	if accountName != "" {
		payload["human_account_name"] = accountName
	}
	status, body, err := c.do(ctx, http.MethodPost, "/api/admin/v1/upstream-oauth-links", payload)
	if err != nil {
		return err
	}
	if status == http.StatusOK || status == http.StatusCreated {
		return nil
	}
	if status == http.StatusConflict {
		// The subject is already linked: fine when it is linked to this user,
		// a real conflict when it belongs to someone else.
		q := url.Values{"filter[user]": {userID}, "filter[provider]": {providerID}, "filter[subject]": {subject}}
		listStatus, listBody, err := c.do(ctx, http.MethodGet, "/api/admin/v1/upstream-oauth-links?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		var list struct {
			Data []struct {
				Attributes struct {
					UserID string `json:"user_id"`
				} `json:"attributes"`
			} `json:"data"`
		}
		if listStatus == http.StatusOK && json.Unmarshal(listBody, &list) == nil && len(list.Data) > 0 {
			return nil
		}
	}
	return fmt.Errorf("MAS could not link the account to the identity provider: %d %s", status, truncate(body))
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

var (
	sharedMu sync.Mutex
	shared   = map[string]*Client{}
)

// Shared returns one client per set of settings, so the access token is reused
// across calls.
func Shared(baseURL, clientID, clientSecret string) *Client {
	key := baseURL + "\x00" + clientID + "\x00" + clientSecret
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if c, ok := shared[key]; ok {
		return c
	}
	c := New(baseURL, clientID, clientSecret)
	shared[key] = c
	return c
}

// Check proves the admin client credentials work by obtaining a token.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.accessToken(ctx)
	return err
}
