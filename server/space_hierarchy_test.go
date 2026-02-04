package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestSpaceHierarchy(t *testing.T) {
	// Common config
	testTeamID := "team_id_1"
	testSpaceID := "!space:example.com"
	testChannelID := "channel_id_1"
	testRoomID := "!room:example.com"

	t.Run("Team Creation => Space Creation", func(t *testing.T) {
		var createRoomCalled bool
		var requestBody map[string]interface{}

		// Mock Matrix Server
		matrixServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/_matrix/client/v3/createRoom" {
				createRoomCalled = true
				_ = json.NewDecoder(r.Body).Decode(&requestBody)

				response := map[string]string{
					"room_id": testSpaceID,
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(response)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer matrixServer.Close()

		mockKV := &MockKVStore{}
		// Expect Set for Team Mapping
		// kvstore.BuildTeamMappingKey(testTeamID) -> testSpaceID
		mockKV.On("Set", kvstore.BuildTeamMappingKey(testTeamID), []byte(testSpaceID)).Return(nil).Once()

		// Setup Plugin
		// matrix.NewClientWithLoggerAndRateLimit requires a logger. Using testLogger from testhelpers_test.go
		testLogger := &testLogger{t: t}
		matrixClient := matrix.NewClientWithLoggerAndRateLimit(matrixServer.URL, "dummy_token", "dummy_bot", testLogger, matrix.TestRateLimitConfig())

		plugin := &Plugin{
			kvstore:      mockKV,
			matrixClient: matrixClient,
			logger:       testLogger,
			configuration: &configuration{
				EnableSync:      true,
				MatrixServerURL: matrixServer.URL,
			},
		}

		// Trigger
		team := &model.Team{
			Id:   testTeamID,
			Name: "test-team",
		}
		plugin.TeamHasBeenCreated(nil, team)

		// Assertions
		assert.True(t, createRoomCalled, "createRoom should be called")
		
		creationContent, ok := requestBody["creation_content"].(map[string]interface{})
		assert.True(t, ok, "creation_content should be present")
		if ok {
			assert.Equal(t, "m.space", creationContent["type"], "Room type should be m.space")
		}
		
		mockKV.AssertExpectations(t)
	})

	t.Run("Room Added to Space", func(t *testing.T) {
		var putSpaceChildCalled bool
		var createRoomCalled bool

		// Expected Alias based on logic #_mattermost_<channel_name>:<domain>
		// Domain is 127.0.0.1 from httptest
		expectedAlias := "#_mattermost_test-channel:127.0.0.1"

		// Mock Matrix Server
		matrixServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Expect Create Room first (ChannelHasBeenCreated creates room first)
			if r.Method == "POST" && r.URL.Path == "/_matrix/client/v3/createRoom" {
				createRoomCalled = true
				response := map[string]string{
					"room_id": testRoomID,
				}
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(response)
				return
			}

			// Allow alias creation calls if any (logic adds aliases)
			if r.Method == "PUT" && len(r.URL.Path) > 28 && r.URL.Path[:28] == "/_matrix/client/v3/directory" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// Expect PUT /_matrix/client/v3/rooms/{spaceID}/state/m.space.child/{childRoomID}
			expectedPath := "/_matrix/client/v3/rooms/" + testSpaceID + "/state/m.space.child/" + expectedAlias
			if r.Method == "PUT" && r.URL.Path == expectedPath {
				putSpaceChildCalled = true
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$event"})
				return
			}
			
			w.WriteHeader(http.StatusOK)
		}))
		defer matrixServer.Close()

		mockKV := &MockKVStore{}
		
		// 1. ChannelHasBeenCreated calls Set(channel->room) with ALIAS as roomID
		mockKV.On("Set", kvstore.BuildChannelMappingKey(testChannelID), []byte(expectedAlias)).Return(nil).Once()
		// 2. ChannelHasBeenCreated calls Set(room->channel) with ALIAS as roomID
		mockKV.On("Set", kvstore.BuildRoomMappingKey(expectedAlias), []byte(testChannelID)).Return(nil).Once()
		
		// 3. ChannelHasBeenCreated calls Get(team_mapping) to find space
		mockKV.On("Get", kvstore.BuildTeamMappingKey(testTeamID)).Return([]byte(testSpaceID), nil).Once()

		// Setup Plugin
		testLogger := &testLogger{t: t}
		matrixClient := matrix.NewClientWithLoggerAndRateLimit(matrixServer.URL, "dummy_token", "dummy_bot", testLogger, matrix.TestRateLimitConfig())

		plugin := &Plugin{
			kvstore:      mockKV,
			matrixClient: matrixClient,
			logger:       testLogger,
			configuration: &configuration{
				EnableSync:      true,
				MatrixServerURL: matrixServer.URL,
			},
		}

		// Trigger
		channel := &model.Channel{
			Id:          testChannelID,
			TeamId:      testTeamID,
			Type:        model.ChannelTypeOpen,
			Name:        "test-channel",
			DisplayName: "Test Channel",
		}
		plugin.ChannelHasBeenCreated(nil, channel)

		// Assertions
		assert.True(t, createRoomCalled, "createRoom should be called")
		assert.True(t, putSpaceChildCalled, "AddSpaceChild should be called")
		
		mockKV.AssertExpectations(t)
	})
}
