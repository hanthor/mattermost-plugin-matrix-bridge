package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockKVStore
type MockKVStore struct {
	mock.Mock
}

func (m *MockKVStore) Get(key string) ([]byte, error) {
	args := m.Called(key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockKVStore) Set(key string, value []byte) error {
	args := m.Called(key, value)
	return args.Error(0)
}

func (m *MockKVStore) GetTemplateData(userID string) (string, error) {
	args := m.Called(userID)
	return args.String(0), args.Error(1)
}

func (m *MockKVStore) Delete(key string) error {
	args := m.Called(key)
	return args.Error(0)
}

func (m *MockKVStore) ListKeys(page, perPage int) ([]string, error) {
	args := m.Called(page, perPage)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockKVStore) ListKeysWithPrefix(page, perPage int, prefix string) ([]string, error) {
	args := m.Called(page, perPage, prefix)
	return args.Get(0).([]string), args.Error(1)
}

// ConfigGetterMock
type ConfigGetterMock struct {
	config *configuration
}

func (c *ConfigGetterMock) getConfiguration() *configuration {
	return c.config
}

// localTestLogger implements Logger interface for testing
type localTestLogger struct {
	t *testing.T
}

func (l *localTestLogger) LogDebug(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[DEBUG] %s %v", message, keyValuePairs)
	}
}

func (l *localTestLogger) LogInfo(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[INFO] %s %v", message, keyValuePairs)
	}
}

func (l *localTestLogger) LogWarn(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[WARN] %s %v", message, keyValuePairs)
	}
}

func (l *localTestLogger) LogError(message string, keyValuePairs ...any) {
	if l.t != nil {
		l.t.Logf("[ERROR] %s %v", message, keyValuePairs)
	}
}

func TestUserSyncFeature(t *testing.T) {
	var uploadCalled, setAvatarCalled bool
	
	// Setup Matrix Mock Server
	matrixServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock UploadMedia
		if r.Method == "POST" && r.URL.Path == "/_matrix/media/v3/upload" {
			uploadCalled = true
			response := map[string]string{
				"content_uri": "mxc://example.com/avatar123",
			}
			json.NewEncoder(w).Encode(response)
			return
		}

		// Mock SetAvatarURL (Profile API)
		// /_matrix/client/v3/profile/{userID}/avatar_url
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/avatar_url") {
			setAvatarCalled = true
			response := map[string]interface{}{}
			json.NewEncoder(w).Encode(response)
			return
		}

		// Mock SetDisplayName (Profile API)
		// /_matrix/client/v3/profile/{userID}/displayname
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/displayname") {
			response := map[string]interface{}{}
			json.NewEncoder(w).Encode(response)
			return
		}
		
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}))
	defer matrixServer.Close()

	// Setup KVStore Mock
	mockKV := &MockKVStore{}
	mmUserID := "mm_user_id"
	ghostUserID := "@ghost:matrix.org"
	ghostUserKey := kvstore.BuildGhostUserKey(mmUserID)
	mockKV.On("Get", ghostUserKey).Return([]byte(ghostUserID), nil)

	// Setup API Mock
	api := &plugintest.API{}
	// Allow any logging
	api.On("LogDebug", mock.Anything, mock.Anything).Maybe()
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Maybe()
	
	// Return some bytes for profile image
	api.On("GetProfileImage", mmUserID).Return([]byte("fake_image_data"), nil)

	// Create Matrix Client
	testLogger := matrix.NewTestLogger(t)
	matrixClient := matrix.NewClientWithLoggerAndRateLimit(matrixServer.URL, "dummy_token", "dummy_bot", testLogger, matrix.TestRateLimitConfig())

	// Create Plugin
	plugin := &Plugin{}
	plugin.SetAPI(api)
	plugin.logger = &localTestLogger{t: t}
	
	// Config
	config := &configuration{
		EnableSync: true,
	}
	plugin.configuration = config

	bridgeUtilsConfig := BridgeUtilsConfig{
		Logger:       matrix.NewTestLogger(t), // Pass matrix logger which satisfies interface
		API:          api,
		KVStore:      mockKV,
		MatrixClient: matrixClient,
		RemoteID:     "test_remote",
		ConfigGetter: &ConfigGetterMock{config: config},
	}
	bridgeUtils := NewBridgeUtils(bridgeUtilsConfig)
	plugin.mattermostToMatrixBridge = NewMattermostToMatrixBridge(bridgeUtils, nil, nil)

	// Set matrixClient on plugin as well because hooks check `p.matrixClient`
	plugin.matrixClient = matrixClient

	t.Run("Avatar Syncing", func(t *testing.T) {
		uploadCalled = false
		setAvatarCalled = false
		
		user := &model.User{
			Id:        mmUserID,
			Username:  "testuser",
			FirstName: "Test",
			LastName:  "User",
		}

		err := plugin.mattermostToMatrixBridge.SyncUserToMatrix(user)
		assert.NoError(t, err)

		assert.True(t, uploadCalled, "UploadAvatarFromData should be called")
		assert.True(t, setAvatarCalled, "SetAvatarURL should be called")
	})

	t.Run("Hook Integration", func(t *testing.T) {
		uploadCalled = false
		setAvatarCalled = false

		user := &model.User{
			Id:        mmUserID,
			Username:  "testuser",
			FirstName: "Test",
			LastName:  "User",
		}

		// UserHasUpdated calls SyncUserToMatrix
		// Pass user as both previous and new to ensure no panic regardless of which argument is used
		plugin.UserHasUpdated(nil, user, user)

		assert.True(t, uploadCalled, "UploadAvatarFromData should be called via Hook")
		assert.True(t, setAvatarCalled, "SetAvatarURL should be called via Hook")
	})
}
