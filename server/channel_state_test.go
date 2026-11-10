package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestChannelRenameHeaderAndArchiveReachTheRoom(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	channel := &model.Channel{Id: "ch1", DisplayName: "Town Square", Header: "Welcome", Type: model.ChannelTypeOpen}

	p.syncChannelState(channel) // the baseline sends nothing
	assert.Empty(t, hs.states)

	channel.DisplayName = "Main Hall"
	channel.Header = "New header"
	p.syncChannelState(channel)
	require.Len(t, hs.states, 2)
	assert.Contains(t, hs.states[0], "m.room.name")
	assert.Contains(t, hs.states[0], "Main Hall")
	assert.Contains(t, hs.states[1], "m.room.topic")
	assert.Contains(t, hs.states[1], "New header")

	p.syncChannelState(channel) // unchanged: nothing more
	assert.Len(t, hs.states, 2)

	channel.DeleteAt = 1234
	p.syncChannelState(channel)
	sent := hs.messages()
	require.NotEmpty(t, sent)
	assert.Contains(t, sent[len(sent)-1].Body, "archived")
}

func TestAChangeFromMatrixDoesNotBounceBack(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	channel := &model.Channel{Id: "ch1", DisplayName: "Old", Type: model.ChannelTypeOpen}
	p.syncChannelState(channel)

	renamed := &model.Channel{Id: "ch1", DisplayName: "Renamed in Matrix", Type: model.ChannelTypeOpen}
	p.noteChannelStateFromMatrix(renamed)
	p.syncChannelState(renamed)
	assert.Empty(t, hs.states, "the room already has that name")
}

func TestDirectMessagesAreLeftAlone(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	p.syncChannelState(&model.Channel{Id: "dm", Type: model.ChannelTypeDirect})
	_, known := p.loadChannelState("dm")
	assert.False(t, known)
}
