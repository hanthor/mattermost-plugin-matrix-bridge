package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestPresenceIsMapped(t *testing.T) {
	assert.Equal(t, "online", presenceFor(model.StatusOnline))
	assert.Equal(t, "unavailable", presenceFor(model.StatusAway))
	assert.Equal(t, "unavailable", presenceFor(model.StatusDnd))
	assert.Equal(t, "offline", presenceFor(model.StatusOffline))
	assert.Equal(t, "offline", presenceFor("anything else"))
}

func TestCustomStatusIsRead(t *testing.T) {
	user := &model.User{Props: model.StringMap{model.UserPropsKeyCustomStatus: `{"emoji":"palm_tree","text":"On holiday","expires_at":""}`}}
	assert.Contains(t, customStatusText(user), "On holiday")
	assert.Equal(t, "", customStatusText(&model.User{}))
	expired := &model.User{Props: model.StringMap{model.UserPropsKeyCustomStatus: `{"emoji":"","text":"Gone","expires_at":"2001-01-01T00:00:00Z"}`}}
	assert.Equal(t, "", customStatusText(expired))
}

func realtimeFixture(t *testing.T) (*Plugin, *fakeSynapse, *plugintest.API) {
	t.Helper()
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-ada")
	require.NoError(t, err)
	return p, hs, api
}

func TestTypingReachesTheRoom(t *testing.T) {
	p, hs, _ := realtimeFixture(t)
	p.relayTyping("ch1", "mm-ada")
	require.Len(t, hs.ephemeral, 1)
	assert.Contains(t, hs.ephemeral[0], "typing @ada:example.com")
	assert.Contains(t, hs.ephemeral[0], `"typing":true`)

	p.relayTyping("unmapped", "mm-ada")
	assert.Len(t, hs.ephemeral, 1, "an unmapped channel sends nothing")
}

func TestPresenceIsSentOnlyWhenItChanges(t *testing.T) {
	p, hs, api := realtimeFixture(t)
	status := &model.Status{UserId: "mm-ada", Status: model.StatusOnline}
	api.On("GetUserStatusesByIds", []string{"mm-ada"}).Return([]*model.Status{status}, nil)
	api.On("GetUsersByIds", []string{"mm-ada"}).Return([]*model.User{{Id: "mm-ada", Username: "ada"}}, nil)

	memory := newRealtimeMemory()
	p.pollPresence(memory)
	require.Len(t, hs.ephemeral, 1)
	assert.Contains(t, hs.ephemeral[0], `"presence":"online"`)

	p.pollPresence(memory)
	assert.Len(t, hs.ephemeral, 1, "unchanged")

	status.Status = model.StatusAway
	p.pollPresence(memory)
	require.Len(t, hs.ephemeral, 2)
	assert.Contains(t, hs.ephemeral[1], `"presence":"unavailable"`)
}

func TestReadReceiptsFollowViewing(t *testing.T) {
	p, hs, api := realtimeFixture(t)
	key := p.historyEventKey()
	api.On("GetPostsForChannel", "ch1", 0, 20).Return(&model.PostList{Order: []string{"p2", "p1"}, Posts: map[string]*model.Post{
		"p2": {Id: "p2", CreateAt: 2000, Props: model.StringInterface{key: "$ev2"}},
		"p1": {Id: "p1", CreateAt: 1000, Props: model.StringInterface{key: "$ev1"}},
	}}, nil)
	api.On("GetChannelMembers", "ch1", 0, 200).Return(model.ChannelMembers{
		{UserId: "mm-ada", LastViewedAt: 3000},
		{UserId: "mm-bob", LastViewedAt: 1500}, // saw only the first message
	}, nil)

	memory := newRealtimeMemory()
	p.receiptsForChannel(&model.Channel{Id: "ch1"}, key, memory)
	require.Len(t, hs.ephemeral, 1)
	assert.Contains(t, hs.ephemeral[0], "receipt @ada:example.com")
	assert.Contains(t, hs.ephemeral[0], "$ev2")

	p.receiptsForChannel(&model.Channel{Id: "ch1"}, key, memory)
	assert.Len(t, hs.ephemeral, 1, "marked once")
}
