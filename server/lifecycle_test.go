package main

import (
	"net/url"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// noTeams answers the membership lookups for a user who is in no team.
func noTeams(api *plugintest.API) {
	api.On("GetTeamsForUser", mock.Anything).Return([]*model.Team{}, nil).Maybe()
}

// mirrorInRooms creates a user's mirror account and records it in rooms.
func mirrorInRooms(t *testing.T, p *Plugin, userID string, rooms ...string) string {
	t.Helper()
	id, err := p.mattermostToMatrixBridge.ensureAccount(userID)
	require.NoError(t, err)
	for _, r := range rooms {
		require.NoError(t, p.kvstore.Set(kvstoreGhostRoomKey(userID, r), []byte("joined")))
	}
	return id
}

func TestDeactivatedUnclaimedMirrorAccountLeavesAndIsDeactivated(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	noTeams(api)
	id := mirrorInRooms(t, p, "mm-ada", "!r1:example.com", "!r2:example.com")

	p.deactivateBridgeAccount("mm-ada")

	_, left := hs.joinsAndLeaves()
	assert.ElementsMatch(t, []string{id + " from !r1:example.com", id + " from !r2:example.com"}, left)
	assert.Equal(t, []string{url.PathEscape(id)}, hs.deactivated)
	assert.Empty(t, p.ghostRooms("mm-ada"), "the rooms are forgotten")
}

func TestDeactivationWithoutAdminTokenOnlyLeavesRooms(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	noTeams(api)
	p.configuration.MatrixAdminToken = ""
	mirrorInRooms(t, p, "mm-ada", "!r1:example.com")

	p.deactivateBridgeAccount("mm-ada")

	_, left := hs.joinsAndLeaves()
	assert.Len(t, left, 1)
	assert.Empty(t, hs.deactivated)
}

func TestDeactivationLeavesAClaimedAccountAlone(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	noTeams(api)
	mirrorInRooms(t, p, "mm-ada", "!r1:example.com")
	_, err := newMirrorStore(p.kvstore).Claim("mm-ada")
	require.NoError(t, err)

	p.deactivateBridgeAccount("mm-ada")

	_, left := hs.joinsAndLeaves()
	assert.Empty(t, left, "a claimed account is the person's own")
	assert.Empty(t, hs.deactivated)
	assert.Len(t, p.ghostRooms("mm-ada"), 1)
}

func TestDeactivatedGhostLeavesItsRooms(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	noTeams(api)
	p.configuration.EnableMirrorMode = false
	ghost := seedGhostWithRooms(t, p, "mm-bob", "!r1:example.com")

	p.deactivateBridgeAccount("mm-bob")

	_, left := hs.joinsAndLeaves()
	assert.Equal(t, []string{ghost + " from !r1:example.com"}, left)
	assert.Empty(t, hs.deactivated, "ghosts are not real homeserver accounts to deactivate")
}

func TestDeactivationStopsActingAsALinkedAccount(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	noTeams(api)
	require.NoError(t, p.identity().save(&IdentityLink{MattermostUserID: "mm-carol", MatrixUserID: "@carol:other.org", AccessToken: "syt_x"}))

	p.deactivateBridgeAccount("mm-carol")

	link, ok := p.identity().Link("mm-carol")
	require.True(t, ok)
	assert.False(t, link.Puppeted())
	assert.Equal(t, "@carol:other.org", link.MatrixUserID, "the link itself stays")
}

func TestLinkingANativeAccountRetiresTheMirrorAccount(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	noTeams(api)
	mirror := mirrorInRooms(t, p, "mm-ada", "!r1:example.com", "!r2:example.com")

	p.retireMirrorAccount("mm-ada", "@ada:matrix.org")

	_, left := hs.joinsAndLeaves()
	assert.ElementsMatch(t, []string{mirror + " from !r1:example.com", mirror + " from !r2:example.com"}, left)
	assert.ElementsMatch(t, []string{"@ada:matrix.org to !r1:example.com", "@ada:matrix.org to !r2:example.com"}, hs.invites)

	store := newMirrorStore(p.kvstore)
	_, still := store.Account("mm-ada")
	assert.False(t, still, "the mirror account is no longer in use")
	_, byMatrix := store.AccountForMatrixUser(mirror)
	assert.False(t, byMatrix)
	assert.Equal(t, "mm-ada", store.LocalpartOwner("ada"), "the name stays reserved for the same person")
	replaced, ok := p.retiredMirrorAccount("mm-ada")
	assert.True(t, ok)
	assert.Equal(t, "@ada:matrix.org", replaced)
}

func TestRetiringAClaimedMirrorAccountKeepsItsRooms(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	noTeams(api)
	mirrorInRooms(t, p, "mm-ada", "!r1:example.com")
	_, err := newMirrorStore(p.kvstore).Claim("mm-ada")
	require.NoError(t, err)

	p.retireMirrorAccount("mm-ada", "@ada:matrix.org")

	_, left := hs.joinsAndLeaves()
	assert.Empty(t, left)
	assert.Len(t, hs.invites, 1)
}

func TestRetiringDoesNothingWithoutAMirrorAccount(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	noTeams(api)
	p.retireMirrorAccount("mm-nobody", "@x:matrix.org")
	assert.Empty(t, hs.invites)
	_, ok := p.retiredMirrorAccount("mm-nobody")
	assert.False(t, ok)
}

func TestRoomsForUserIncludeMappedChannelsTheyAreIn(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	p, api := newMirrorTestBridge(t, hs, ada)
	api.On("GetTeamsForUser", "mm-ada").Return([]*model.Team{{Id: "team1"}}, nil)
	api.On("GetChannelMembersForUser", "team1", "mm-ada", 0, 200).Return([]*model.ChannelMember{
		{ChannelId: "chan-mapped"}, {ChannelId: "chan-unmapped"},
	}, nil)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-mapped"), []byte("!mapped:example.com")))
	require.NoError(t, p.kvstore.Set(kvstoreGhostRoomKey("mm-ada", "!recorded:example.com"), []byte("joined")))

	assert.ElementsMatch(t, []string{"!recorded:example.com", "!mapped:example.com"}, p.roomsForUser("mm-ada"))
}
