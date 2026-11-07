package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSyncMatrixRoomNameAndTopic(t *testing.T) {
	p, api := newAppServiceTestPlugin(t, true)
	channelID := model.NewId()

	t.Run("room name updates channel display name", func(t *testing.T) {
		channel := &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Old", Header: "hdr"}
		api.On("GetChannel", channelID).Return(channel, nil).Once()
		api.On("UpdateChannel", mock.MatchedBy(func(c *model.Channel) bool {
			return c.Id == channelID && c.DisplayName == "New Name" && c.Header == "hdr"
		})).Return(channel, nil).Once()

		err := p.syncMatrixRoomNameToMattermost(MatrixEvent{Type: "m.room.name", Content: map[string]any{"name": "New Name"}}, channelID)
		require.NoError(t, err)
		api.AssertExpectations(t)
	})

	t.Run("unchanged name is a no-op", func(t *testing.T) {
		channel := &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DisplayName: "Same"}
		api.On("GetChannel", channelID).Return(channel, nil).Once()

		err := p.syncMatrixRoomNameToMattermost(MatrixEvent{Type: "m.room.name", Content: map[string]any{"name": "Same"}}, channelID)
		require.NoError(t, err)
		api.AssertNotCalled(t, "UpdateChannel", mock.MatchedBy(func(c *model.Channel) bool { return c.DisplayName == "Same" }))
	})

	t.Run("room topic updates channel header", func(t *testing.T) {
		channel := &model.Channel{Id: channelID, Type: model.ChannelTypePrivate, DisplayName: "Name", Header: ""}
		api.On("GetChannel", channelID).Return(channel, nil).Once()
		api.On("UpdateChannel", mock.MatchedBy(func(c *model.Channel) bool {
			return c.Id == channelID && c.Header == "Welcome!"
		})).Return(channel, nil).Once()

		err := p.syncMatrixRoomTopicToMattermost(MatrixEvent{Type: "m.room.topic", Content: map[string]any{"topic": "Welcome!"}}, channelID)
		require.NoError(t, err)
		api.AssertExpectations(t)
	})

	t.Run("direct channels are never renamed", func(t *testing.T) {
		channel := &model.Channel{Id: channelID, Type: model.ChannelTypeDirect, DisplayName: ""}
		api.On("GetChannel", channelID).Return(channel, nil).Once()

		err := p.syncMatrixRoomNameToMattermost(MatrixEvent{Type: "m.room.name", Content: map[string]any{"name": "Nope"}}, channelID)
		assert.NoError(t, err)
	})
}
