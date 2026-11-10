package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestBridgeInfoIsSetWhenMappedAndClearedWhenUnmapped(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	site := "https://chat.example.com"
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetConfig")
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &site}}).Maybe()
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "t1", Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen}, nil).Maybe()
	api.On("GetTeam", "t1").Return(&model.Team{Id: "t1", Name: "main", DisplayName: "Main"}, nil).Maybe()
	p.configuration.EnableSync = true

	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	p.syncBridgeInfo("ch1")

	require.Len(t, hs.states, 2, "both the stable and the unstable event types are set")
	assert.Contains(t, hs.states[0], "!room:example.com")
	assert.Contains(t, hs.states[0], `"id":"mattermost"`)
	assert.Contains(t, hs.states[0], "https://chat.example.com/main/channels/town-square")
	assert.Contains(t, hs.states[1], "uk.half-shot.bridge")

	// Unmapped: the event is emptied in the room it was set in.
	require.NoError(t, p.kvstore.Delete(kvstore.BuildChannelMappingKey("ch1")))
	p.syncBridgeInfo("ch1")
	require.Len(t, hs.states, 4)
	assert.Contains(t, hs.states[2], "!room:example.com")
	assert.Contains(t, hs.states[2], " {}")
}
