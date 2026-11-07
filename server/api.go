// Package main implements the Mattermost Matrix Bridge plugin server component.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// ServeHTTP exposes the Matrix Application Service API implemented by the bridge.
//
// The homeserver reaches the bridge at
// <siteURL>/plugins/<plugin id>/_matrix/app/v1/... (the URL written into the
// appservice registration). The legacy unprefixed paths used by older
// homeservers are served as well.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()

	for _, prefix := range []string{"/_matrix/app/v1", ""} {
		asRouter := router.PathPrefix(prefix).Subrouter()
		asRouter.Use(p.MatrixAuthorizationRequired)
		asRouter.HandleFunc("/transactions/{txnId}", p.handleMatrixTransaction).Methods(http.MethodPut)
		asRouter.HandleFunc("/users/{userId}", p.handleAppServiceUserQuery).Methods(http.MethodGet)
		asRouter.HandleFunc("/rooms/{roomAlias}", p.handleAppServiceRoomQuery).Methods(http.MethodGet)
		asRouter.HandleFunc("/ping", p.handleAppServicePing).Methods(http.MethodPost)
	}

	router.ServeHTTP(w, r)
}

// writeMatrixError writes a Matrix-style JSON error body.
func writeMatrixError(w http.ResponseWriter, status int, errcode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"errcode": errcode, "error": message})
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// extractMatrixAccessToken returns the credential the homeserver presented:
// the Authorization bearer token (spec v1.4+) or the legacy access_token
// query parameter.
func extractMatrixAccessToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		return ""
	}
	return r.URL.Query().Get("access_token")
}

// MatrixAuthorizationRequired is a middleware that requires a valid hs_token.
//
// Following the Application Service API: a request without credentials is
// answered with 401 M_UNAUTHORIZED, a request with the wrong credentials with
// 403 M_FORBIDDEN.
func (p *Plugin) MatrixAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		config := p.getConfiguration()

		if !config.EnableSync {
			p.logger.LogDebug("Matrix appservice request received but sync is disabled")
			writeMatrixError(w, http.StatusServiceUnavailable, "M_UNKNOWN", "Matrix sync is disabled")
			return
		}

		if config.MatrixHSToken == "" {
			p.logger.LogWarn("Matrix appservice request received but hs_token not configured")
			writeMatrixError(w, http.StatusServiceUnavailable, "M_UNKNOWN", "Matrix homeserver token not configured")
			return
		}

		token := extractMatrixAccessToken(r)
		if token == "" {
			writeMatrixError(w, http.StatusUnauthorized, "M_UNAUTHORIZED", "Missing access token")
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(config.MatrixHSToken)) != 1 {
			p.logger.LogWarn("Matrix appservice authentication failed - hs_token mismatch")
			writeMatrixError(w, http.StatusForbidden, "M_FORBIDDEN", "Invalid access token")
			return
		}

		next.ServeHTTP(w, r)
	})
}
