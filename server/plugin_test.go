package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

const (
	testHSToken  = "hs-token-for-tests"
	testServerID = "https://matrix.example.com"
)

// newAppServiceTestPlugin builds a plugin with just enough wiring to exercise
// the Application Service HTTP API without a Matrix server.
func newAppServiceTestPlugin(t *testing.T, enableSync bool) (*Plugin, *plugintest.API) {
	t.Helper()
	api := &plugintest.API{}
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	p := &Plugin{}
	p.SetAPI(api)
	p.logger = &testLogger{t: t}
	p.kvstore = NewMemoryKVStore()
	p.configuration = &configuration{
		EnableSync:      enableSync,
		MatrixServerURL: testServerID,
		MatrixASToken:   "as-token",
		MatrixHSToken:   testHSToken,
	}
	var err error
	p.transactionLogger, err = CreateTransactionLogger()
	require.NoError(t, err)
	return p, api
}

func doAppServiceRequest(p *Plugin, method, path, auth string, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	p.ServeHTTP(nil, w, r)
	return w
}

func errcodeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	return body["errcode"]
}

func TestAppServiceAuthentication(t *testing.T) {
	t.Run("sync disabled returns 503", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, false)
		w := doAppServiceRequest(p, http.MethodPost, "/_matrix/app/v1/ping", "Bearer "+testHSToken, "{}")
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("missing token returns 401 M_UNAUTHORIZED", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodPost, "/_matrix/app/v1/ping", "", "{}")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "M_UNAUTHORIZED", errcodeOf(t, w))
	})

	t.Run("wrong token returns 403 M_FORBIDDEN", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodPost, "/_matrix/app/v1/ping", "Bearer nope", "{}")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "M_FORBIDDEN", errcodeOf(t, w))
	})

	t.Run("bearer token is accepted", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodPost, "/_matrix/app/v1/ping", "Bearer "+testHSToken, "{}")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, "{}", w.Body.String())
	})

	t.Run("legacy access_token query parameter and unprefixed path are accepted", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodPost, "/ping?access_token="+testHSToken, "", "{}")
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestAppServiceUserQuery(t *testing.T) {
	t.Run("user outside the bridge namespace is not found", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodGet, "/_matrix/app/v1/users/@alice:matrix.example.com", "Bearer "+testHSToken, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "M_NOT_FOUND", errcodeOf(t, w))
	})

	t.Run("ghost of an unknown Mattermost user is not found", func(t *testing.T) {
		p, api := newAppServiceTestPlugin(t, true)
		api.On("GetUser", "nouser").Return(nil, model.NewAppError("GetUser", "not found", nil, "", http.StatusNotFound))
		w := doAppServiceRequest(p, http.MethodGet, "/_matrix/app/v1/users/@_mattermost_nouser:matrix.example.com", "Bearer "+testHSToken, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("room alias query is not found", func(t *testing.T) {
		p, _ := newAppServiceTestPlugin(t, true)
		w := doAppServiceRequest(p, http.MethodGet, "/_matrix/app/v1/rooms/%23something:matrix.example.com", "Bearer "+testHSToken, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestAppServiceTransactionEphemeralTyping(t *testing.T) {
	p, api := newAppServiceTestPlugin(t, true)

	roomID := "!room:matrix.example.com"
	channelID := model.NewId()
	mmUserID := model.NewId()
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey(roomID), []byte(channelID)))
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixUserKey("@alice:matrix.example.com"), []byte(mmUserID)))

	// Alice is mapped and should produce a typing event; the ghost user and
	// the unknown user must be ignored.
	api.On("PublishUserTyping", mmUserID, channelID, "").Return(nil).Once()

	txn := map[string]any{
		"events": []any{},
		"ephemeral": []map[string]any{{
			"type":    "m.typing",
			"room_id": roomID,
			"content": map[string]any{
				"user_ids": []string{
					"@alice:matrix.example.com",
					"@_mattermost_" + mmUserID + ":matrix.example.com",
					"@stranger:matrix.example.com",
				},
			},
		}},
	}
	body, _ := json.Marshal(txn)

	w := doAppServiceRequest(p, http.MethodPut, "/_matrix/app/v1/transactions/txn-1", "Bearer "+testHSToken, string(body))
	assert.Equal(t, http.StatusOK, w.Code)
	api.AssertExpectations(t)

	// The same transaction ID is idempotent and must not publish again.
	w = doAppServiceRequest(p, http.MethodPut, "/_matrix/app/v1/transactions/txn-1", "Bearer "+testHSToken, string(body))
	assert.Equal(t, http.StatusOK, w.Code)
	api.AssertNumberOfCalls(t, "PublishUserTyping", 1)
}

func TestAppServiceTransactionEphemeralPresence(t *testing.T) {
	p, api := newAppServiceTestPlugin(t, true)
	mmUserID := model.NewId()
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixUserKey("@alice:matrix.example.com"), []byte(mmUserID)))

	api.On("UpdateUserStatus", mmUserID, model.StatusAway).Return(&model.Status{}, nil).Once()

	txn := map[string]any{
		"events": []any{},
		"de.sorunome.msc2409.ephemeral": []map[string]any{{
			"type":    "m.presence",
			"sender":  "@alice:matrix.example.com",
			"content": map[string]any{"presence": "unavailable"},
		}},
	}
	body, _ := json.Marshal(txn)
	w := doAppServiceRequest(p, http.MethodPut, "/_matrix/app/v1/transactions/txn-presence", "Bearer "+testHSToken, string(body))
	assert.Equal(t, http.StatusOK, w.Code)
	api.AssertExpectations(t)
}

func TestMatrixServerDomain(t *testing.T) {
	p := &Plugin{}
	p.configuration = &configuration{MatrixServerURL: "https://matrix.example.com:8448"}
	assert.Equal(t, "matrix.example.com", p.matrixServerDomain())

	p.configuration = &configuration{MatrixServerURL: "https://matrix.example.com", MatrixServerDomain: "example.com"}
	assert.Equal(t, "example.com", p.matrixServerDomain())
	assert.True(t, p.isGhostUser("@_mattermost_abc:example.com"))
	assert.False(t, p.isGhostUser("@_mattermost_abc:matrix.example.com"))
	assert.False(t, p.isGhostUser("@alice:example.com"))
}

func TestPostOriginatedFromMatrix(t *testing.T) {
	assert.False(t, postOriginatedFromMatrix(&model.Post{}))
	assert.True(t, postOriginatedFromMatrix(&model.Post{Props: model.StringInterface{"from_matrix": true}}))
	assert.True(t, postOriginatedFromMatrix(&model.Post{Props: model.StringInterface{"from_matrix": "true"}}))
	assert.False(t, postOriginatedFromMatrix(&model.Post{Props: model.StringInterface{"from_matrix": false}}))
}
