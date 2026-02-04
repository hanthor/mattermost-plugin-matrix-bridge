package command

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/matrix"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestExecuteJoinCommand(t *testing.T) {
	t.Run("join existing room without creating channel", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		kvStore := kvstore.NewMemoryKVStore()
		
		// Mock Matrix client
		mockMatrixClient := &matrix.MockClient{}
		mockMatrixClient.On("JoinRoom", "#test:matrix.org").Return(nil)
		mockMatrixClient.On("InviteAndJoinGhostUser", "#test:matrix.org", mock.Anything).Return(nil)
		
		// Mock plugin accessor
		mockPlugin := &MockPluginAccessor{}
		mockPlugin.On("GetMatrixClient").Return(mockMatrixClient)
		mockPlugin.On("GetKVStore").Return(kvStore)
		mockPlugin.On("GetPluginAPI").Return(api)
		mockPlugin.On("GetPluginAPIClient").Return(pluginapi.NewClient(api, &plugintest.Driver{}))
		mockPlugin.On("CreateOrGetGhostUser", "user123").Return("@mattermost_user123:matrix.local", nil)
		
		// Mock user
		api.On("GetUser", "user123").Return(&model.User{
			Id:       "user123",
			Username: "testuser",
		}, nil)
		
		handler := &Handler{
			plugin:    mockPlugin,
			client:    pluginapi.NewClient(api, &plugintest.Driver{}),
			kvstore:   kvStore,
			pluginAPI: api,
		}
		
		args := &model.CommandArgs{
			UserId:    "user123",
			ChannelId: "channel123",
			TeamId:    "team123",
		}
		
		// Execute
		response := handler.executeJoinCommand(args, "#test:matrix.org", false)
		
		// Assert
		assert.NotNil(t, response)
		assert.Equal(t, model.CommandResponseTypeEphemeral, response.ResponseType)
		assert.Contains(t, response.Text, "Successfully joined Matrix room")
		assert.Contains(t, response.Text, "#test:matrix.org")
		assert.Contains(t, response.Text, "Next Steps")
		
		mockMatrixClient.AssertExpectations(t)
	})
	
	t.Run("join room with invalid identifier", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		kvStore := kvstore.NewMemoryKVStore()
		
		mockMatrixClient := &matrix.MockClient{}
		
		mockPlugin := &MockPluginAccessor{}
		mockPlugin.On("GetMatrixClient").Return(mockMatrixClient)
		mockPlugin.On("GetKVStore").Return(kvStore)
		mockPlugin.On("GetPluginAPI").Return(api)
		mockPlugin.On("GetPluginAPIClient").Return(pluginapi.NewClient(api, &plugintest.Driver{}))
		
		handler := &Handler{
			plugin:    mockPlugin,
			client:    pluginapi.NewClient(api, &plugintest.Driver{}),
			kvstore:   kvStore,
			pluginAPI: api,
		}
		
		args := &model.CommandArgs{
			UserId:    "user123",
			ChannelId: "channel123",
			TeamId:    "team123",
		}
		
		// Execute with invalid room identifier
		response := handler.executeJoinCommand(args, "invalid-room", false)
		
		// Assert
		assert.NotNil(t, response)
		assert.Contains(t, response.Text, "Invalid room identifier format")
	})
	
	t.Run("join room and create channel", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		kvStore := kvstore.NewMemoryKVStore()
		
		// Mock Matrix client
		mockMatrixClient := &matrix.MockClient{}
		mockMatrixClient.On("JoinRoom", "#community:matrix.org").Return(nil)
		mockMatrixClient.On("InviteAndJoinGhostUser", "#community:matrix.org", mock.Anything).Return(nil)
		mockMatrixClient.On("ResolveRoomAlias", "#community:matrix.org").Return("!abc123:matrix.org", nil)
		
		// Mock plugin accessor
		mockPlugin := &MockPluginAccessor{}
		mockPlugin.On("GetMatrixClient").Return(mockMatrixClient)
		mockPlugin.On("GetKVStore").Return(kvStore)
		mockPlugin.On("GetPluginAPI").Return(api)
		mockPlugin.On("GetPluginAPIClient").Return(pluginapi.NewClient(api, &plugintest.Driver{}))
		mockPlugin.On("CreateOrGetGhostUser", "user123").Return("@mattermost_user123:matrix.local", nil)
		
		// Mock user
		api.On("GetUser", "user123").Return(&model.User{
			Id:       "user123",
			Username: "testuser",
		}, nil)
		
		// Mock channel creation
		createdChannel := &model.Channel{
			Id:          "newchannel123",
			TeamId:      "team123",
			Type:        model.ChannelTypeOpen,
			Name:        "community",
			DisplayName: "Community",
		}
		api.On("CreateChannel", mock.AnythingOfType("*model.Channel")).Return(createdChannel, nil)
		api.On("AddChannelMember", "newchannel123", "user123").Return(&model.ChannelMember{}, nil)
		
		handler := &Handler{
			plugin:    mockPlugin,
			client:    pluginapi.NewClient(api, &plugintest.Driver{}),
			kvstore:   kvStore,
			pluginAPI: api,
		}
		
		args := &model.CommandArgs{
			UserId:    "user123",
			ChannelId: "channel123",
			TeamId:    "team123",
		}
		
		// Execute
		response := handler.executeJoinCommand(args, "#community:matrix.org", true)
		
		// Assert
		assert.NotNil(t, response)
		assert.Equal(t, model.CommandResponseTypeEphemeral, response.ResponseType)
		assert.Contains(t, response.Text, "Successfully joined Matrix room")
		assert.Contains(t, response.Text, "Created Mattermost channel")
		assert.Contains(t, response.Text, "~community")
		
		// Verify mappings were saved
		channelMapping, err := kvStore.Get(kvstore.BuildChannelMappingKey("newchannel123"))
		assert.NoError(t, err)
		assert.Equal(t, "#community:matrix.org", string(channelMapping))
		
		roomMapping, err := kvStore.Get(kvstore.BuildRoomMappingKey("#community:matrix.org"))
		assert.NoError(t, err)
		assert.Equal(t, "newchannel123", string(roomMapping))
		
		mockMatrixClient.AssertExpectations(t)
	})
}
