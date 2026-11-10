package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func reconcileFixture(t *testing.T, channelMembers ...string) (*Plugin, *fakeSynapse) {
	t.Helper()
	hs := newFakeSynapse(t)
	var users []*model.User
	for _, id := range []string{"mm-ada", "mm-bob", "mm-carol", "mm-dave"} {
		users = append(users, &model.User{Id: id, Username: id[3:]})
	}
	p, api := newMirrorTestBridge(t, hs, users...)
	p.configuration.EnableMirrorMode = false
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room:example.com")))
	var members []*model.ChannelMember
	for _, id := range channelMembers {
		members = append(members, &model.ChannelMember{UserId: id})
	}
	api.On("GetChannelMembers", "chan-1", 0, 200).Return(model.ChannelMembers(func() model.ChannelMembers {
		out := model.ChannelMembers{}
		for _, m := range members {
			out = append(out, *m)
		}
		return out
	}()), nil)
	api.On("GetProfileImage", mock.Anything).Return([]byte{}, nil).Maybe()
	return p, hs
}

func ghostOf(id string) string { return "@_mattermost_" + id + ":example.com" }

func TestReconcileAddsMissingPeopleAndRemovesThoseWhoLeft(t *testing.T) {
	p, hs := reconcileFixture(t, "mm-ada", "mm-bob")
	seedGhostWithRooms(t, p, "mm-ada")
	seedGhostWithRooms(t, p, "mm-bob")
	seedGhostWithRooms(t, p, "mm-carol", "!room:example.com") // left the channel, still in the room
	// Ada is in the room; Bob is not; Carol is.
	hs.members = []string{ghostOf("mm-ada"), ghostOf("mm-carol"), "@_mattermost_bridge:example.com", "@alice:example.com"}

	res, err := p.reconcileChannel("chan-1", true)
	require.NoError(t, err)

	assert.Equal(t, []string{ghostOf("mm-bob")}, res.Joined)
	assert.Equal(t, []string{ghostOf("mm-carol")}, res.Left)
	assert.Empty(t, res.CannotFix)
	joins, leaves := hs.joinsAndLeaves()
	assert.Contains(t, joins, ghostOf("mm-bob")+" in !room:example.com")
	assert.Equal(t, []string{ghostOf("mm-carol") + " from !room:example.com"}, leaves, "the bot and the real Matrix person are never touched")
	assert.Empty(t, p.ghostRooms("mm-carol"), "the record of her membership is dropped")
}

func TestReconcileDryRunChangesNothing(t *testing.T) {
	p, hs := reconcileFixture(t, "mm-ada")
	seedGhostWithRooms(t, p, "mm-ada")
	seedGhostWithRooms(t, p, "mm-carol")
	hs.members = []string{ghostOf("mm-carol")}

	res, err := p.reconcileChannel("chan-1", false)
	require.NoError(t, err)

	assert.Equal(t, 2, res.outOfStep())
	joins, leaves := hs.joinsAndLeaves()
	assert.Empty(t, joins)
	assert.Empty(t, leaves)
}

func TestReconcileLeavesAClaimedAccountAndALinkedPersonAlone(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	p, api := newMirrorTestBridge(t, hs, ada, bob)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room:example.com")))
	api.On("GetChannelMembers", "chan-1", 0, 200).Return(model.ChannelMembers{{UserId: "mm-ada"}}, nil)
	api.On("GetProfileImage", mock.Anything).Return([]byte{}, nil).Maybe()

	// Bob's mirror account is claimed and he left the channel; he is still in the room.
	_, err := p.mattermostToMatrixBridge.ensureAccount("mm-bob")
	require.NoError(t, err)
	_, err = newMirrorStore(p.kvstore).Claim("mm-bob")
	require.NoError(t, err)
	// Ada uses her own Matrix account and is not in the room yet.
	require.NoError(t, p.identity().save(&IdentityLink{MattermostUserID: "mm-ada", MatrixUserID: "@ada:matrix.org", AccessToken: "syt_x"}))
	hs.members = []string{"@bob:example.com"}

	res, err := p.reconcileChannel("chan-1", true)
	require.NoError(t, err)

	assert.Empty(t, res.Left, "a claimed account belongs to its person")
	assert.Equal(t, []string{"@ada:matrix.org"}, res.CannotFix, "a person with their own account must join it themselves")
	_, leaves := hs.joinsAndLeaves()
	assert.Empty(t, leaves)
}

func TestReconcileSkipsRemoteUsersAndBots(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs, &model.User{Id: "mm-ada", Username: "ada"})
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room:example.com")))
	api.On("GetChannelMembers", "chan-1", 0, 200).Return(model.ChannelMembers{{UserId: "remote"}, {UserId: "bot"}}, nil)
	api.On("GetUser", "remote").Return(&model.User{Id: "remote", RemoteId: model.NewPointer("r")}, nil)
	api.On("GetUser", "bot").Return(&model.User{Id: "bot", IsBot: true}, nil)

	res, err := p.reconcileChannel("chan-1", true)
	require.NoError(t, err)
	assert.Zero(t, res.outOfStep())
}

func TestRepairReportsWhatItDid(t *testing.T) {
	p, hs := reconcileFixture(t, "mm-ada")
	seedGhostWithRooms(t, p, "mm-ada")
	hs.members = nil

	out, err := p.RepairMembership("chan-1")
	require.NoError(t, err)
	assert.Contains(t, out, "1 people were missing from the room and were added")

	hs.members = []string{ghostOf("mm-ada")}
	out, err = p.RepairMembership("chan-1")
	require.NoError(t, err)
	assert.Contains(t, out, "same people")
}

func TestRepairRefusesAnUnbridgedChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	_, err := p.RepairMembership("nope")
	require.Error(t, err)
}

func TestWhichMattermostUserAnAccountStandsFor(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	assert.Equal(t, "mm-ada", p.mattermostUserForBridgeAccount(ghostOf("mm-ada")))
	assert.Empty(t, p.mattermostUserForBridgeAccount("@alice:example.com"))
	assert.Empty(t, p.mattermostUserForBridgeAccount(p.BridgeBotUserID()))
}
