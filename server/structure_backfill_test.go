package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestBackfillStructureGoesThroughEveryBridgedChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	site := "https://chat.example.com"
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetConfig")
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &site}}).Maybe()
	api.On("GetChannel", mock.Anything).Return(func(id string) *model.Channel {
		return &model.Channel{Id: id, TeamId: "t1", Name: id, DisplayName: "Channel " + id, Type: model.ChannelTypeOpen}
	}, nil).Maybe()
	api.On("GetTeam", "t1").Return(&model.Team{Id: "t1", Name: "main", DisplayName: "Main"}, nil).Maybe()
	api.On("EnsureBotUser", mock.Anything).Return("bot1", nil).Maybe()
	api.On("CreatePost", mock.Anything).Return(&model.Post{}, nil).Maybe()
	p.configuration.EnableSync = true

	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room1:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch2"), []byte("!room2:example.com")))

	assert.Equal(t, 2, p.BackfillStructure())

	joined := ""
	for _, s := range hs.states {
		joined += s + "\n"
	}
	for _, room := range []string{"!room1:example.com", "!room2:example.com"} {
		assert.Contains(t, joined, room+" m.bridge/")
		assert.Contains(t, joined, room+" im.vector.modular.widgets/")
	}
}
