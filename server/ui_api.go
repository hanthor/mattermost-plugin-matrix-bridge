package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
)

// The webapp's panel talks to the plugin through this small API. Every route
// needs a signed-in Mattermost user (the server sets Mattermost-User-Id) and
// answers only about that user and the channels they can read.

const uiAPIPrefix = "/api/v1"

// registerUIRoutes adds the webapp routes to the router.
func (p *Plugin) registerUIRoutes(router *mux.Router) {
	ui := router.PathPrefix(uiAPIPrefix).Subrouter()
	ui.Use(p.mattermostUserRequired)
	ui.HandleFunc("/me", p.handleUIMe).Methods(http.MethodGet)
	ui.HandleFunc("/me/claim", p.handleUIClaim).Methods(http.MethodPost)
	ui.HandleFunc("/me/link", p.handleUILinkStart).Methods(http.MethodPost)
	ui.HandleFunc("/me/link", p.handleUIUnlink).Methods(http.MethodDelete)
	ui.HandleFunc("/channels/{channelId}", p.handleUIChannel).Methods(http.MethodGet)
	ui.HandleFunc("/channels/{channelId}/call", p.handleUICall).Methods(http.MethodPost)
}

type ctxKey string

const userIDKey ctxKey = "mattermost_user_id"

func (p *Plugin) mattermostUserRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("Mattermost-User-Id")
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
			return
		}
		next.ServeHTTP(w, r.WithContext(contextWithUser(r, userID)))
	})
}

type uiMe struct {
	Enabled bool `json:"enabled"`
	// Mode is "ghost" or "mirror": how the person appears in Matrix.
	Mode string `json:"mode"`
	// MatrixUserID is who the person is in Matrix: their mirror account, the
	// account they linked, or their ghost.
	MatrixUserID string `json:"matrix_user_id"`
	Linked       bool   `json:"linked"`
	Puppeted     bool   `json:"puppeted"`
	// Claimed is set for a mirror account the person has taken over.
	Claimed bool `json:"claimed"`
	// CanClaim is true when mirror mode is on and the account is not claimed.
	CanClaim bool `json:"can_claim"`
	// ClaimGivesPassword is true when claiming sets a one-time password.
	ClaimGivesPassword bool   `json:"claim_gives_password"`
	BotUser            string `json:"bot_user"`
	Cutover            bool   `json:"cutover"`
	IsAdmin            bool   `json:"is_admin"`
}

func (p *Plugin) uiMe(userID string) uiMe {
	cfg := p.getConfiguration()
	st := p.MirrorStatus()
	me := uiMe{
		Enabled:            cfg.EnableSync && p.matrixClient != nil,
		Mode:               st.Mode,
		ClaimGivesPassword: st.ClaimSupported,
		BotUser:            p.BridgeBotUserID(),
		Cutover:            st.Cutover,
		IsAdmin:            p.API.HasPermissionTo(userID, model.PermissionManageSystem),
	}
	if matrixID, puppeted, ok := p.GetIdentityLink(userID); ok {
		me.Linked, me.Puppeted, me.MatrixUserID = true, puppeted, matrixID
	}
	if st.Mode == accountModeMirror {
		if acct, ok := newMirrorStore(p.kvstore).Account(userID); ok {
			me.Claimed = acct.Claimed
			if me.MatrixUserID == "" {
				me.MatrixUserID = acct.MatrixUserID
			}
		}
		me.CanClaim = !me.Claimed && !st.Cutover
	}
	if me.MatrixUserID == "" && p.mattermostToMatrixBridge != nil && me.Enabled {
		if id, ok := p.mattermostToMatrixBridge.getGhostUser(userID); ok {
			me.MatrixUserID = id
		}
	}
	return me
}

func (p *Plugin) handleUIMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.uiMe(userFromContext(r)))
}

func (p *Plugin) handleUIClaim(w http.ResponseWriter, r *http.Request) {
	userID := userFromContext(r)
	if p.MirrorStatus().Cutover {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Cut-over mode is on: accounts are frozen."})
		return
	}
	matrixUserID, password, err := p.ClaimMirrorAccount(userID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"matrix_user_id": matrixUserID, "one_time_password": password})
}

func (p *Plugin) handleUILinkStart(w http.ResponseWriter, r *http.Request) {
	userID := userFromContext(r)
	if _, _, ok := p.GetIdentityLink(userID); ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already linked"})
		return
	}
	code, err := p.CreateIdentityLinkCode(userID)
	if err != nil {
		p.logger.LogError("Failed to create identity link code", "error", err, "user_id", userID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create a link code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":        code,
		"bot_user":    p.BridgeBotUserID(),
		"command":     "!mattermost link " + code,
		"expires_min": 15,
	})
}

func (p *Plugin) handleUIUnlink(w http.ResponseWriter, r *http.Request) {
	if err := p.UnlinkIdentity(userFromContext(r)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type uiChannel struct {
	Mapped     bool   `json:"mapped"`
	RoomID     string `json:"room_id,omitempty"`
	RoomName   string `json:"room_name,omitempty"`
	ElementURL string `json:"element_url,omitempty"`
	// Direct is set for direct and group messages.
	Direct bool `json:"direct"`
	// CanManage is true for people who may bridge or unbridge the channel.
	CanManage bool `json:"can_manage"`
	// CanCreate is true when the person may bridge this channel now.
	CanCreate    bool   `json:"can_create"`
	CallsMode    string `json:"calls_mode"`
	CanCall      bool   `json:"can_call"`
	Cutover      bool   `json:"cutover"`
	Mode         string `json:"mode"`
	Participants int    `json:"participants"`
}

func (p *Plugin) handleUIChannel(w http.ResponseWriter, r *http.Request) {
	userID := userFromContext(r)
	channelID := mux.Vars(r)["channelId"]
	if !model.IsValidId(channelID) || !p.API.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to this channel"})
		return
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}

	cfg := p.getConfiguration()
	st := p.MirrorStatus()
	out := uiChannel{
		Direct:    channel.IsGroupOrDirect(),
		CallsMode: cfg.callsMode(),
		Cutover:   st.Cutover,
		Mode:      st.Mode,
	}
	admin := p.API.HasPermissionTo(userID, model.PermissionManageSystem)
	out.CanManage = admin ||
		p.API.HasPermissionToChannel(userID, channelID, model.PermissionManagePublicChannelProperties) ||
		p.API.HasPermissionToChannel(userID, channelID, model.PermissionManagePrivateChannelProperties)

	if p.mattermostToMatrixBridge != nil && p.matrixClient != nil && cfg.EnableSync {
		if identifier, err := p.mattermostToMatrixBridge.GetMatrixRoomID(channelID); err == nil && identifier != "" {
			roomID := identifier
			if strings.HasPrefix(identifier, "#") {
				if resolved, err := p.matrixClient.ResolveRoomAlias(identifier); err == nil {
					roomID = resolved
				}
			}
			out.Mapped = true
			out.RoomID = roomID
			out.ElementURL = p.elementRoomURL(roomID)
			if overview, err := p.matrixClient.GetRoomOverview(roomID); err == nil {
				out.RoomName = overview.Name
				out.Participants = overview.Members
			}
		}
	}
	out.CanCreate = !out.Mapped && !out.Direct && out.CanManage && !st.Cutover && cfg.EnableSync
	out.CanCall = out.Mapped && cfg.callsMode() == callsModeElementCall
	writeJSON(w, http.StatusOK, out)
}

func (p *Plugin) handleUICall(w http.ResponseWriter, r *http.Request) {
	userID := userFromContext(r)
	channelID := mux.Vars(r)["channelId"]
	if !model.IsValidId(channelID) || !p.API.HasPermissionToChannel(userID, channelID, model.PermissionCreatePost) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no access to this channel"})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	joinURL, err := p.StartCall(userID, channelID, strings.TrimSpace(body.Title))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"join_url": joinURL})
}

func contextWithUser(r *http.Request, userID string) context.Context {
	return context.WithValue(r.Context(), userIDKey, userID)
}

func userFromContext(r *http.Request) string {
	id, _ := r.Context().Value(userIDKey).(string)
	return id
}
