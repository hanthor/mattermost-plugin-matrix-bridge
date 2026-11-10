package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

const linkPostID = "abcdefghijklmnopqrstuvwxyz"

func linkFixture(t *testing.T) *Plugin {
	t.Helper()
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	site := "https://chat.example.com"
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetConfig")
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &site}}).Maybe()
	key := "matrix_event_id_" + extractServerDomain(p.logger, p.configuration.MatrixServerURL)
	api.On("GetPost", linkPostID).Return(&model.Post{Id: linkPostID, ChannelId: "ch1", Props: model.StringInterface{key: "$ev1"}}, nil).Maybe()
	api.On("GetPost", "zzzzzzzzzzzzzzzzzzzzzzzzzz").Return(&model.Post{Id: "zzzzzzzzzzzzzzzzzzzzzzzzzz", ChannelId: "ch1"}, nil).Maybe()
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "t1", Name: "town-square", DisplayName: "Town Square"}, nil).Maybe()
	api.On("GetChannel", "ch2").Return(&model.Channel{Id: "ch2", TeamId: "t1", Name: "ops", DisplayName: "Ops"}, nil).Maybe()
	api.On("GetChannelByName", "t1", "ops", false).Return(&model.Channel{Id: "ch2", TeamId: "t1", Name: "ops", DisplayName: "Ops"}, nil).Maybe()
	api.On("GetChannelByName", "t1", "nowhere", false).Return(nil, &model.AppError{Message: "not found"}).Maybe()
	api.On("GetTeam", "t1").Return(&model.Team{Id: "t1", Name: "main"}, nil).Maybe()
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room1:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch2"), []byte("!room2:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey("!room1:example.com"), []byte("ch1")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildRoomMappingKey("!room2:example.com"), []byte("ch2")))
	return p
}

func TestMattermostPermalinksBecomeMatrixEventLinks(t *testing.T) {
	p := linkFixture(t)
	b := p.mattermostToMatrixBridge

	got := b.rewriteLinksToMatrix("see https://chat.example.com/main/pl/"+linkPostID+" for context", "ch1")
	assert.Equal(t, "see https://matrix.to/#/!room1:example.com/$ev1 for context", got)

	p.configuration.ElementURL = "https://element.example.com"
	got = b.rewriteLinksToMatrix("https://chat.example.com/main/pl/"+linkPostID, "ch1")
	assert.Equal(t, "https://element.example.com/#/room/!room1:example.com/$ev1", got)
}

func TestLinksToThingsThatAreNotBridgedAreLeftAlone(t *testing.T) {
	p := linkFixture(t)
	b := p.mattermostToMatrixBridge
	other := "https://other.example.com/main/pl/" + linkPostID
	assert.Equal(t, other, b.rewriteLinksToMatrix(other, "ch1"), "a link to another server")
	notSynced := "https://chat.example.com/main/pl/zzzzzzzzzzzzzzzzzzzzzzzzzz"
	assert.Equal(t, notSynced, b.rewriteLinksToMatrix(notSynced, "ch1"), "a post that has no Matrix event")
	assert.Equal(t, "plain text", b.rewriteLinksToMatrix("plain text", "ch1"))
}

func TestChannelMentionsBecomeRoomLinks(t *testing.T) {
	p := linkFixture(t)
	b := p.mattermostToMatrixBridge
	assert.Equal(t, "ask in [#Ops](https://matrix.to/#/!room2:example.com) please", b.rewriteLinksToMatrix("ask in ~ops please", "ch1"))
	assert.Equal(t, "ask in ~nowhere", b.rewriteLinksToMatrix("ask in ~nowhere", "ch1"), "a channel that is not bridged")
}

func TestMatrixLinksBecomeMattermostLinks(t *testing.T) {
	p := linkFixture(t)
	b := p.matrixToMattermostBridge
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$ev9"), []byte(linkPostID)))

	got := b.rewriteLinksToMattermost("look at https://matrix.to/#/!room1:example.com/$ev9?via=example.com thanks")
	assert.Equal(t, "look at https://chat.example.com/main/pl/"+linkPostID+" thanks", got)

	assert.Equal(t, "join ~ops", b.rewriteLinksToMattermost("join https://matrix.to/#/!room2:example.com"))

	unknownRoom := "https://matrix.to/#/!elsewhere:example.com/$x"
	assert.Equal(t, unknownRoom, b.rewriteLinksToMattermost(unknownRoom))
	user := "https://matrix.to/#/@ada:example.com"
	assert.Equal(t, user, b.rewriteLinksToMattermost(user), "user links are not touched")
}

func TestElementLinksAreRewrittenToo(t *testing.T) {
	p := linkFixture(t)
	p.configuration.ElementURL = "https://element.example.com"
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$ev9"), []byte(linkPostID)))
	got := p.matrixToMattermostBridge.rewriteLinksToMattermost("https://element.example.com/#/room/!room1:example.com/$ev9")
	assert.Equal(t, "https://chat.example.com/main/pl/"+linkPostID, got)
}
