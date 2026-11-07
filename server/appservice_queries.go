package main

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// handleAppServicePing answers the homeserver's appservice ping (MSC2659 /
// spec v1.7). It lets administrators verify from the homeserver side that the
// registration URL and tokens are correct.
func (p *Plugin) handleAppServicePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleAppServiceUserQuery answers the homeserver's user query. The homeserver
// asks before it lets anyone interact with a user in our namespace that does
// not exist yet. Ghost users are provisioned lazily for existing Mattermost
// users so that Matrix users can, for example, start a DM with a Mattermost
// user who has not posted anything yet.
func (p *Plugin) handleAppServiceUserQuery(w http.ResponseWriter, r *http.Request) {
	matrixUserID := mux.Vars(r)["userId"]

	mattermostUserID := p.extractMattermostUserIDFromGhost(matrixUserID)
	if mattermostUserID == "" || !strings.HasSuffix(matrixUserID, ":"+p.matrixServerDomain()) {
		writeMatrixError(w, http.StatusNotFound, "M_NOT_FOUND", "User is not managed by this bridge")
		return
	}

	if _, appErr := p.API.GetUser(mattermostUserID); appErr != nil {
		writeMatrixError(w, http.StatusNotFound, "M_NOT_FOUND", "No such Mattermost user")
		return
	}

	if p.mattermostToMatrixBridge == nil {
		writeMatrixError(w, http.StatusServiceUnavailable, "M_UNKNOWN", "Bridge not initialised")
		return
	}

	if _, err := p.mattermostToMatrixBridge.CreateOrGetGhostUser(mattermostUserID); err != nil {
		p.logger.LogWarn("Failed to provision ghost user for appservice query", "error", err, "matrix_user_id", matrixUserID)
		writeMatrixError(w, http.StatusInternalServerError, "M_UNKNOWN", "Failed to provision user")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleAppServiceRoomQuery answers the homeserver's room alias query. Rooms
// are created eagerly by the bridge (with their aliases), so an alias the
// homeserver cannot resolve is one we do not know about either.
func (p *Plugin) handleAppServiceRoomQuery(w http.ResponseWriter, _ *http.Request) {
	writeMatrixError(w, http.StatusNotFound, "M_NOT_FOUND", "Room alias is not managed by this bridge")
}
