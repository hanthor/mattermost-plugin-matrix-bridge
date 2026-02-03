package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestRoomMirroring(t *testing.T) {
	tests := []struct {
		name               string
		channelType        model.ChannelType
		channelName        string
		channelDisplayName string
		expectedCalled     bool
		expectedPreset     string
		expectedVisibility string
	}{
		{
			name:               "Public Channel",
			channelType:        model.ChannelTypeOpen,
			channelName:        "pub-channel",
			channelDisplayName: "Public Channel",
			expectedCalled:     true,
			expectedPreset:     "public_chat",
			expectedVisibility: "public",
		},
		{
			name:               "Private Channel",
			channelType:        model.ChannelTypePrivate,
			channelName:        "priv-channel",
			channelDisplayName: "Private Channel",
			expectedCalled:     true,
			expectedPreset:     "private_chat",
			expectedVisibility: "private",
		},
		{
			name:               "DM Channel",
			channelType:        model.ChannelTypeDirect,
			channelName:        "dm_channel",
			channelDisplayName: "",
			expectedCalled:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var createRoomCalled bool
			var requestBody map[string]interface{}

			// Mock Matrix Home Server
			matrixServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && r.URL.Path == "/_matrix/client/v3/createRoom" {
					createRoomCalled = true
					json.NewDecoder(r.Body).Decode(&requestBody)

					response := map[string]string{
						"room_id": "!newroom:example.com",
					}
					json.NewEncoder(w).Encode(response)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer matrixServer.Close()

			mockKV := &MockKVStore{}
			// Expect Set calls only if we expect creation
			if tt.expectedCalled {
				// The plugin calls Set twice: channel->room and room->channel
				// We match arguments loosely or exactly?
				// Loose matching for now to verify Set is called.
				mockKV.On("Set", mock.Anything, mock.Anything).Return(nil).Twice()
			}

			// Create Matrix Client
			testLogger := matrix.NewTestLogger(t)
			matrixClient := matrix.NewClientWithLoggerAndRateLimit(matrixServer.URL, "dummy_token", "dummy_bot", testLogger, matrix.TestRateLimitConfig())
			
			// Setup Plugin
			plugin := &Plugin{
				kvstore:      mockKV,
				matrixClient: matrixClient,
				logger:       &localTestLogger{t: t},
				configuration: &configuration{
					EnableSync:      true,
					MatrixServerURL: matrixServer.URL,
				},
			}

			channel := &model.Channel{
				Id:          "ch_id_" + tt.channelName,
				Type:        tt.channelType,
				Name:        tt.channelName,
				DisplayName: tt.channelDisplayName,
				Header:      "Topic",
				Purpose:     "Purpose",
			}

			plugin.ChannelHasBeenCreated(nil, channel)

			assert.Equal(t, tt.expectedCalled, createRoomCalled, "CreateRoom called mismatch")

			if tt.expectedCalled {
				assert.Equal(t, tt.expectedPreset, requestBody["preset"], "Preset mismatch")
				assert.Equal(t, tt.expectedVisibility, requestBody["visibility"], "Visibility mismatch")

				mockKV.AssertExpectations(t)
			}
		})
	}
}
