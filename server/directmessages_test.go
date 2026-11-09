package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// dmFixture is a direct channel between two local people.
func dmFixture(t *testing.T) (*Plugin, *fakeSynapse, *plugintest.API) {
	t.Helper()
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	p, api := newMirrorTestBridge(t, hs, ada, bob)
	p.mattermostToMatrixBridge.fileTracker = NewPendingFileTracker()
	p.mattermostToMatrixBridge.postTracker = NewPostTracker(100)
	api.On("GetChannel", "dm1").Return(&model.Channel{Id: "dm1", Type: model.ChannelTypeDirect}, nil).Maybe()
	api.On("GetChannelMembers", "dm1", 0, mock.Anything).Return(model.ChannelMembers{{UserId: "mm-ada"}, {UserId: "mm-bob"}}, nil).Maybe()
	api.On("GetProfileImage", mock.Anything).Return([]byte{}, nil).Maybe()
	api.On("GetPost", mock.Anything).Return(&model.Post{}, nil).Maybe()
	api.On("UpdatePost", mock.Anything).Return(func(post *model.Post) *model.Post { return post }, nil).Maybe()
	return p, hs, api
}

func dmPost() *model.Post {
	return &model.Post{Id: model.NewId(), UserId: "mm-ada", ChannelId: "dm1", Message: "a private hello"}
}

func TestDirectMessagesAreNotBridgedByDefault(t *testing.T) {
	p, hs, _ := dmFixture(t)

	p.MessageHasBeenPosted(nil, dmPost())

	assert.Empty(t, hs.createdRooms)
	assert.Empty(t, hs.messages())
}

func TestDirectMessagesNeedEveryoneToAgree(t *testing.T) {
	p, hs, _ := dmFixture(t)
	p.configuration.EnableDirectMessages = true

	_, err := p.DirectMessages("mm-ada", "on")
	require.NoError(t, err)
	p.MessageHasBeenPosted(nil, dmPost())
	assert.Empty(t, hs.createdRooms, "bob has not agreed")

	_, err = p.DirectMessages("mm-bob", "on")
	require.NoError(t, err)
	p.MessageHasBeenPosted(nil, dmPost())

	require.Len(t, hs.createdRooms, 1)
	assert.Equal(t, true, hs.createdRooms[0]["is_direct"])
	assert.ElementsMatch(t, []any{"@ada:example.com", "@bob:example.com"}, hs.createdRooms[0]["invite"])
	require.Len(t, hs.messages(), 1)
	assert.Equal(t, "a private hello", hs.messages()[0].Body)
	assert.Equal(t, "@ada:example.com", hs.messages()[0].UserID)

	room, err := p.kvstore.Get(kvstore.BuildChannelMappingKey("dm1"))
	require.NoError(t, err)
	assert.Equal(t, "!dm:example.com", string(room))
	assert.Len(t, p.bridgedDirectChannels("mm-bob"), 1)
}

func TestLeavingStopsTheConversationFromSyncing(t *testing.T) {
	p, hs, _ := dmFixture(t)
	p.configuration.EnableDirectMessages = true
	for _, u := range []string{"mm-ada", "mm-bob"} {
		_, err := p.DirectMessages(u, "on")
		require.NoError(t, err)
	}
	p.MessageHasBeenPosted(nil, dmPost())
	require.Len(t, hs.messages(), 1)

	out, err := p.DirectMessages("mm-bob", "off")
	require.NoError(t, err)
	assert.Contains(t, out, "1 conversations stopped syncing")

	room, _ := p.kvstore.Get(kvstore.BuildChannelMappingKey("dm1"))
	assert.Empty(t, room, "the mapping is gone")
	p.MessageHasBeenPosted(nil, dmPost())
	assert.Len(t, hs.messages(), 1, "nothing more is sent")
	assert.Len(t, hs.createdRooms, 1, "and no new room is made while bob has not agreed")
}

func TestDirectMessageCommandRefusesWhenTheSettingIsOff(t *testing.T) {
	p, _, _ := dmFixture(t)

	_, err := p.DirectMessages("mm-ada", "on")
	require.Error(t, err)

	status, err := p.DirectMessages("mm-ada", "status")
	require.NoError(t, err)
	assert.Contains(t, status, "not bridged")

	_, err = p.DirectMessages("mm-ada", "bogus")
	require.Error(t, err)
}

func TestDirectMessageStatusReflectsAgreement(t *testing.T) {
	p, _, _ := dmFixture(t)
	p.configuration.EnableDirectMessages = true

	status, _ := p.DirectMessages("mm-ada", "status")
	assert.Contains(t, status, "not bridged")
	_, _ = p.DirectMessages("mm-ada", "on")
	status, _ = p.DirectMessages("mm-ada", "status")
	assert.Contains(t, status, "You agreed")
}
