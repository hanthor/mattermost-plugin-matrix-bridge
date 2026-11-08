package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func uiRequest(t *testing.T, p *Plugin, method, path, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if userID != "" {
		req.Header.Set("Mattermost-User-Id", userID)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, req)
	return rec
}

func TestUIRoutesNeedASignedInUser(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	assert.Equal(t, http.StatusUnauthorized, uiRequest(t, p, http.MethodGet, "/api/v1/me", "").Code)
	assert.Equal(t, http.StatusUnauthorized, uiRequest(t, p, http.MethodPost, "/api/v1/me/claim", "").Code)
}

func TestUIMeAndClaim(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	api.On("HasPermissionTo", "mm-ada", model.PermissionManageSystem).Return(false).Maybe()

	rec := uiRequest(t, p, http.MethodGet, "/api/v1/me", "mm-ada")
	require.Equal(t, http.StatusOK, rec.Code)
	var me uiMe
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.Equal(t, "mirror", me.Mode)
	assert.True(t, me.CanClaim)
	assert.False(t, me.Claimed)
	assert.True(t, me.ClaimGivesPassword)
	assert.Equal(t, "@_mattermost_bridge:example.com", me.BotUser)

	rec = uiRequest(t, p, http.MethodPost, "/api/v1/me/claim", "mm-ada")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var claim map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &claim))
	assert.Equal(t, "@ada:example.com", claim["matrix_user_id"])
	assert.Len(t, claim["one_time_password"], 20)

	rec = uiRequest(t, p, http.MethodGet, "/api/v1/me", "mm-ada")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.True(t, me.Claimed)
	assert.False(t, me.CanClaim)
	assert.Equal(t, "@ada:example.com", me.MatrixUserID)
}

func TestUIClaimIsRefusedDuringCutover(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	api.On("HasPermissionTo", "mm-ada", model.PermissionManageSystem).Return(false).Maybe()
	p.configuration.CutoverMode = true

	assert.Equal(t, http.StatusConflict, uiRequest(t, p, http.MethodPost, "/api/v1/me/claim", "mm-ada").Code)
	rec := uiRequest(t, p, http.MethodGet, "/api/v1/me", "mm-ada")
	var me uiMe
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &me))
	assert.True(t, me.Cutover)
	assert.False(t, me.CanClaim)
}

func TestUILinkCodeAndUnlink(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, _ := newMirrorTestBridge(t, hs, ada)

	rec := uiRequest(t, p, http.MethodPost, "/api/v1/me/link", "mm-ada")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Code    string `json:"code"`
		Command string `json:"command"`
		BotUser string `json:"bot_user"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Code, 8)
	assert.Equal(t, "!mattermost link "+out.Code, out.Command)
	assert.Equal(t, "@_mattermost_bridge:example.com", out.BotUser)

	_, err := p.identity().Redeem(out.Code, "@ada-native:example.com")
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, uiRequest(t, p, http.MethodPost, "/api/v1/me/link", "mm-ada").Code, "already linked")
	assert.Equal(t, http.StatusOK, uiRequest(t, p, http.MethodDelete, "/api/v1/me/link", "mm-ada").Code)
	_, _, linked := p.GetIdentityLink("mm-ada")
	assert.False(t, linked)
}

func TestUIChannelNeedsAccessToTheChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	channelID := model.NewId()
	api.On("HasPermissionToChannel", "mm-ada", channelID, model.PermissionReadChannel).Return(false)

	assert.Equal(t, http.StatusForbidden, uiRequest(t, p, http.MethodGet, "/api/v1/channels/"+channelID, "mm-ada").Code)
	assert.Equal(t, http.StatusForbidden, uiRequest(t, p, http.MethodGet, "/api/v1/channels/not-an-id", "mm-ada").Code)
	api.On("HasPermissionToChannel", mock.Anything, channelID, model.PermissionCreatePost).Return(false)
	assert.Equal(t, http.StatusForbidden, uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+channelID+"/call", "mm-ada").Code)
}

func TestUIChannelReportsAnUnmappedChannelAndWhoCanBridgeIt(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	channelID := model.NewId()
	api.On("HasPermissionToChannel", "mm-ada", channelID, model.PermissionReadChannel).Return(true)
	api.On("HasPermissionTo", "mm-ada", model.PermissionManageSystem).Return(true)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)

	rec := uiRequest(t, p, http.MethodGet, "/api/v1/channels/"+channelID, "mm-ada")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var ch uiChannel
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ch))
	assert.False(t, ch.Mapped)
	assert.True(t, ch.CanManage)
	assert.True(t, ch.CanCreate)
	assert.False(t, ch.CanCall)
}

func TestUIBridgeRunsMatrixCreateAsThePerson(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	channelID := model.NewId()
	api.On("HasPermissionToChannel", "mm-ada", channelID, model.PermissionReadChannel).Return(true)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, TeamId: "team1", Type: model.ChannelTypeOpen}, nil)
	api.On("ExecuteSlashCommand", mock.MatchedBy(func(a *model.CommandArgs) bool {
		return a.UserId == "mm-ada" && a.ChannelId == channelID && a.TeamId == "team1" && a.Command == "/matrix create"
	})).Return(&model.CommandResponse{Text: "✅ **Matrix Room Created**"}, nil).Once()

	rec := uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+channelID+"/bridge", "mm-ada")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Matrix Room Created")
}

func TestUIBridgeReportsTheCommandsRefusal(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	channelID := model.NewId()
	api.On("HasPermissionToChannel", "mm-ada", channelID, model.PermissionReadChannel).Return(true)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)
	api.On("ExecuteSlashCommand", mock.Anything).Return(&model.CommandResponse{Text: "❌ You need permission to manage this channel to run `/matrix create`."}, nil)

	rec := uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+channelID+"/bridge", "mm-ada")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "permission")
}

func TestUIBridgeRefusesDirectMessagesCutoverAndOutsiders(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	dm, open, hidden := model.NewId(), model.NewId(), model.NewId()
	api.On("HasPermissionToChannel", "mm-ada", dm, model.PermissionReadChannel).Return(true)
	api.On("HasPermissionToChannel", "mm-ada", open, model.PermissionReadChannel).Return(true)
	api.On("HasPermissionToChannel", "mm-ada", hidden, model.PermissionReadChannel).Return(false)
	api.On("GetChannel", dm).Return(&model.Channel{Id: dm, Type: model.ChannelTypeDirect}, nil)
	api.On("GetChannel", open).Return(&model.Channel{Id: open, Type: model.ChannelTypeOpen}, nil)

	assert.Equal(t, http.StatusBadRequest, uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+dm+"/bridge", "mm-ada").Code)
	assert.Equal(t, http.StatusForbidden, uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+hidden+"/bridge", "mm-ada").Code)
	p.configuration.CutoverMode = true
	assert.Equal(t, http.StatusConflict, uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+open+"/bridge", "mm-ada").Code)
}

func TestUIBridgeRefusesAChannelThatIsAlreadyBridged(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	channelID := model.NewId()
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey(channelID), []byte("!room:example.com")))
	api.On("HasPermissionToChannel", "mm-ada", channelID, model.PermissionReadChannel).Return(true)
	api.On("GetChannel", channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil)

	rec := uiRequest(t, p, http.MethodPost, "/api/v1/channels/"+channelID+"/bridge", "mm-ada")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already bridged")
	api.AssertNotCalled(t, "ExecuteSlashCommand", mock.Anything)
}
