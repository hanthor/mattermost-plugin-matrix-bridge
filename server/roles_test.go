package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestChannelAdminsBecomeRoomModerators(t *testing.T) {
	p, hs, api := realtimeFixture(t)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Type: model.ChannelTypeOpen}, nil).Maybe()
	api.On("GetChannelMembers", "ch1", 0, 200).Return(model.ChannelMembers{
		{UserId: "mm-ada", SchemeAdmin: true},
	}, nil)

	p.syncChannelRoles("ch1")
	require.Len(t, hs.states, 1)
	assert.Contains(t, hs.states[0], "m.room.power_levels")
	assert.Contains(t, hs.states[0], `"@ada:example.com":50`)

	// Already a moderator: nothing more to send.
	hs.levels = map[string]int{"@ada:example.com": 50}
	p.syncChannelRoles("ch1")
	assert.Len(t, hs.states, 1)
}

func TestARoleThatWasRemovedLowersThePowerLevel(t *testing.T) {
	p, hs, api := realtimeFixture(t)
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", Type: model.ChannelTypeOpen}, nil).Maybe()
	api.On("GetChannelMembers", "ch1", 0, 200).Return(model.ChannelMembers{{UserId: "mm-ada", SchemeAdmin: false}}, nil)
	hs.levels = map[string]int{"@ada:example.com": 50}

	p.syncChannelRoles("ch1")
	require.Len(t, hs.states, 1)
	assert.NotContains(t, hs.states[0], "@ada:example.com")
}

func TestAMatrixModeratorBecomesAChannelAdmin(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixUserKey("@bob:other.org"), []byte("mm-bob")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildMattermostUserKey("mm-bob"), []byte("@bob:other.org")))
	api.On("GetChannelMembers", "ch1", 0, 200).Return(model.ChannelMembers{{UserId: "mm-bob"}}, nil)
	api.On("GetChannelMember", "ch1", "mm-bob").Return(&model.ChannelMember{UserId: "mm-bob", SchemeAdmin: false}, nil)
	var roles string
	api.On("UpdateChannelMemberRoles", "ch1", "mm-bob", mock.Anything).Run(func(args mock.Arguments) {
		roles = args.String(2)
	}).Return(&model.ChannelMember{}, nil)

	err := p.syncMatrixPowerLevels(MatrixEvent{Content: map[string]any{"users": map[string]any{"@bob:other.org": float64(50)}}}, "ch1")
	require.NoError(t, err)
	assert.Equal(t, channelAdminRoles, roles)
}
