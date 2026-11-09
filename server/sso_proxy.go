package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Mattermost is an OAuth 2.0 provider, not an OpenID Connect one, and its
// token endpoint answers with "scope":"" and "id_token":"". Matrix
// Authentication Service rejects an empty scope, so it cannot use Mattermost's
// token endpoint directly. This route passes the request on to Mattermost
// unchanged and returns its answer without the empty fields, which is all MAS
// needs to sign people in with their Mattermost account (docs/MAS_SETUP.md).
// It adds no capability: it is the same token exchange Mattermost already
// serves publicly, authenticated by the application's own client secret.
const ssoTokenPath = "/sso/mattermost/token"

const ssoTokenMaxBody = 16 << 10

var ssoHTTPClient = &http.Client{Timeout: 15 * time.Second}

func (p *Plugin) handleSSOToken(w http.ResponseWriter, r *http.Request) {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil || *cfg.ServiceSettings.SiteURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the site URL is not configured"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ssoTokenMaxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return
	}
	target := strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/") + "/oauth/access_token"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build the request"})
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Accept", "application/json")
	res, err := ssoHTTPClient.Do(req)
	if err != nil {
		p.logger.LogWarn("SSO token exchange could not reach Mattermost", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach Mattermost"})
		return
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not read Mattermost's answer"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(dropEmptyTokenFields(out))
}

// dropEmptyTokenFields removes "scope" and "id_token" when they are empty
// strings; anything that is not a JSON object is returned as it came.
func dropEmptyTokenFields(body []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	for _, key := range []string{"scope", "id_token"} {
		if string(fields[key]) == `""` {
			delete(fields, key)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return out
}
