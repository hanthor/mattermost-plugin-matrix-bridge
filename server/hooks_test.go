package main

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestReactionHasBeenAdded tests the ReactionHasBeenAdded hook
func TestReactionHasBeenAdded(t *testing.T) {
	t.Run("successful reaction sync", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{
			remoteID: "test-remote-id",
		}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.kvstore = NewMemoryKVStore()
		plugin.pendingFiles = NewPendingFileTracker()
		plugin.postTracker = NewPostTracker(DefaultPostTrackerMaxEntries)
		plugin.configuration = &configuration{
			EnableSync: true,
		}

		// Create a test matrix client
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", plugin.remoteID)
		plugin.initBridges()

		// Test data
		postID := model.NewId()
		userID := model.NewId()
		channelID := model.NewId()
		eventID := "!event123:matrix.org"

		post := &model.Post{
			Id:        postID,
			UserId:    userID,
			ChannelId: channelID,
			Message:   "Test message",
			Props: map[string]any{
				"matrix_event_id_localhost": eventID,
			},
		}

		reaction := &model.Reaction{
			UserId:    userID,
			PostId:    postID,
			EmojiName: "thumbsup",
			CreateAt:  time.Now().UnixMilli(),
			DeleteAt:  0,
		}

		user := &model.User{
			Id:       userID,
			Username: "testuser",
		}

		// Mock expectations
		api.On("GetPost", postID).Return(post, nil)
		api.On("GetUser", userID).Return(user, nil)
		api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
		api.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute
		plugin.ReactionHasBeenAdded(nil, reaction)

		// Assert - verify GetPost was called
		api.AssertCalled(t, "GetPost", postID)
	})

	t.Run("sync disabled", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: false, // Sync disabled
		}
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", "test-remote")

		reaction := &model.Reaction{
			UserId:    model.NewId(),
			PostId:    model.NewId(),
			EmojiName: "thumbsup",
		}

		// Execute
		plugin.ReactionHasBeenAdded(nil, reaction)

		// Assert - GetPost should not be called since sync is disabled
		api.AssertNotCalled(t, "GetPost", mock.Anything)
	})

	t.Run("matrix client not initialized", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: true,
		}
		plugin.matrixClient = nil // Client not initialized

		reaction := &model.Reaction{
			UserId:    model.NewId(),
			PostId:    model.NewId(),
			EmojiName: "thumbsup",
		}

		// Execute
		plugin.ReactionHasBeenAdded(nil, reaction)

		// Assert - should exit early, GetPost not called
		api.AssertNotCalled(t, "GetPost", mock.Anything)
	})

	t.Run("post not found", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{
			remoteID: "test-remote-id",
		}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: true,
		}
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", plugin.remoteID)
		plugin.kvstore = NewMemoryKVStore()
		plugin.initBridges()

		postID := model.NewId()
		reaction := &model.Reaction{
			UserId:    model.NewId(),
			PostId:    postID,
			EmojiName: "thumbsup",
		}

		// Mock expectations - post not found
		appError := model.NewAppError("test", "app.post.get.app_error", nil, "", 404)
		api.On("GetPost", postID).Return(nil, appError)

		// Execute
		plugin.ReactionHasBeenAdded(nil, reaction)

		// Assert - should handle error gracefully
		api.AssertCalled(t, "GetPost", postID)
	})
}

// TestReactionHasBeenRemoved tests the ReactionHasBeenRemoved hook
func TestReactionHasBeenRemoved(t *testing.T) {
	t.Run("successful reaction removal sync", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{
			remoteID: "test-remote-id",
		}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.kvstore = NewMemoryKVStore()
		plugin.pendingFiles = NewPendingFileTracker()
		plugin.postTracker = NewPostTracker(DefaultPostTrackerMaxEntries)
		plugin.configuration = &configuration{
			EnableSync: true,
		}

		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", plugin.remoteID)
		plugin.initBridges()

		// Test data
		postID := model.NewId()
		userID := model.NewId()
		channelID := model.NewId()

		post := &model.Post{
			Id:        postID,
			UserId:    userID,
			ChannelId: channelID,
			Message:   "Test message",
		}

		reaction := &model.Reaction{
			UserId:    userID,
			PostId:    postID,
			EmojiName: "thumbsup",
			CreateAt:  time.Now().UnixMilli(),
			DeleteAt:  time.Now().UnixMilli(), // Marked as deleted
		}

		// Mock expectations
		api.On("GetPost", postID).Return(post, nil)
		api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()

		// Execute
		plugin.ReactionHasBeenRemoved(nil, reaction)

		// Assert
		api.AssertCalled(t, "GetPost", postID)
	})

	t.Run("sync disabled", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: false,
		}
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", "test-remote")

		reaction := &model.Reaction{
			UserId:    model.NewId(),
			PostId:    model.NewId(),
			EmojiName: "thumbsup",
			DeleteAt:  time.Now().UnixMilli(),
		}

		// Execute
		plugin.ReactionHasBeenRemoved(nil, reaction)

		// Assert
		api.AssertNotCalled(t, "GetPost", mock.Anything)
	})
}

// TestMessageHasBeenUpdated tests the MessageHasBeenUpdated hook
func TestMessageHasBeenUpdated(t *testing.T) {
	t.Run("successful message edit sync", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{
			remoteID: "test-remote-id",
		}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.kvstore = NewMemoryKVStore()
		plugin.pendingFiles = NewPendingFileTracker()
		plugin.postTracker = NewPostTracker(DefaultPostTrackerMaxEntries)
		plugin.configuration = &configuration{
			EnableSync:      true,
			MatrixServerURL: "https://matrix.example.com",
		}

		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", plugin.remoteID)
		plugin.initBridges()

		// Test data
		postID := model.NewId()
		userID := model.NewId()
		channelID := model.NewId()
		now := time.Now().UnixMilli()

		oldPost := &model.Post{
			Id:        postID,
			UserId:    userID,
			ChannelId: channelID,
			Message:   "Original message",
			CreateAt:  now,
			UpdateAt:  now,
		}

		newPost := &model.Post{
			Id:        postID,
			UserId:    userID,
			ChannelId: channelID,
			Message:   "Edited message",
			CreateAt:  now,
			UpdateAt:  now + 1000,
		}

		user := &model.User{
			Id:       userID,
			Username: "testuser",
		}

		channel := &model.Channel{
			Id:   channelID,
			Type: model.ChannelTypeOpen,
		}

		// Mock expectations
		api.On("GetUser", userID).Return(user, nil)
		api.On("GetChannel", channelID).Return(channel, nil)
		api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
		api.On("KVSet", mock.Anything, mock.Anything).Return(nil).Maybe()

		// Execute
		plugin.MessageHasBeenUpdated(nil, newPost, oldPost)

		// Verify the hook was executed (sync method would be called internally)
		// The actual sync logic is tested in sync_to_matrix_integration_test.go
		assert.NotNil(t, plugin.mattermostToMatrixBridge)
	})

	t.Run("sync disabled", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: false,
		}
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", "test-remote")

		newPost := &model.Post{Id: model.NewId()}
		oldPost := &model.Post{Id: model.NewId()}

		// Execute
		plugin.MessageHasBeenUpdated(nil, newPost, oldPost)

		// Assert - no API calls should be made
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("matrix client not initialized", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		plugin := &Plugin{}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: true,
		}
		plugin.matrixClient = nil

		newPost := &model.Post{Id: model.NewId()}
		oldPost := &model.Post{Id: model.NewId()}

		// Execute
		plugin.MessageHasBeenUpdated(nil, newPost, oldPost)

		// Assert
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})

	t.Run("skip matrix-originated posts", func(t *testing.T) {
		// Setup
		api := &plugintest.API{}
		remoteID := "matrix-remote-id"
		plugin := &Plugin{
			remoteID: remoteID,
		}
		plugin.SetAPI(api)
		plugin.logger = &testLogger{t: t}
		plugin.configuration = &configuration{
			EnableSync: true,
		}
		plugin.matrixClient = createMatrixClientWithTestLogger(t, "https://matrix.example.com", "test_token", remoteID)
		plugin.kvstore = NewMemoryKVStore()
		plugin.initBridges()

		// Post originated from Matrix (has matching remote ID)
		newPost := &model.Post{
			Id:       model.NewId(),
			RemoteId: &remoteID,
		}
		oldPost := &model.Post{Id: newPost.Id}

		// Execute
		plugin.MessageHasBeenUpdated(nil, newPost, oldPost)

		// Assert - should skip without calling sync
		api.AssertNotCalled(t, "GetUser", mock.Anything)
	})
}


