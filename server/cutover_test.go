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

func newCutoverPlugin(t *testing.T) (*Plugin, *plugintest.API) {
	t.Helper()
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	api.On("HasPermissionTo", "admin", model.PermissionManageSystem).Return(true).Maybe()
	api.On("HasPermissionTo", "member", model.PermissionManageSystem).Return(false).Maybe()
	for _, id := range []string{"c1", "c2"} {
		ch := &model.Channel{Id: id, TeamId: "team", Name: "chan-" + id, Type: model.ChannelTypeOpen}
		api.On("GetChannel", id).Return(ch, nil).Maybe()
		require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey(id), []byte("!room-"+id+":example.com")))
	}
	// A channel of another team and a direct channel are never touched.
	api.On("GetChannel", "other").Return(&model.Channel{Id: "other", TeamId: "team2", Type: model.ChannelTypeOpen}, nil).Maybe()
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("other"), []byte("!other:example.com")))
	return p, api
}

func TestReadOnlyNeedsConfirmAndChangesOnlyThisTeam(t *testing.T) {
	p, api := newCutoverPlugin(t)

	out, err := p.MigrateTeam("admin", "team", "readonly", false)
	require.NoError(t, err)
	assert.Contains(t, out, "2 channels would become read-only")
	assert.False(t, p.channelReadOnly("c1"))

	var posted []*model.Post
	api.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		posted = append(posted, args.Get(0).(*model.Post))
	}).Return(func(post *model.Post) *model.Post { post.Id = "notice-" + post.ChannelId; return post }, nil)
	var pinned []*model.Post
	api.On("UpdatePost", mock.Anything).Run(func(args mock.Arguments) {
		pinned = append(pinned, args.Get(0).(*model.Post))
	}).Return(func(post *model.Post) *model.Post { return post }, nil)

	out, err = p.MigrateTeam("admin", "team", "readonly", true)
	require.NoError(t, err)
	assert.Contains(t, out, "2 channels are now read-only")
	assert.True(t, p.channelReadOnly("c1"))
	assert.True(t, p.channelReadOnly("c2"))
	assert.False(t, p.channelReadOnly("other"), "another team's channel is untouched")

	require.Len(t, posted, 2)
	assert.Contains(t, posted[0].Message, "https://matrix.to/#/!room-c1:example.com")
	require.Len(t, pinned, 2)
	assert.True(t, pinned[0].IsPinned)

	again, err := p.MigrateTeam("admin", "team", "readonly", true)
	require.NoError(t, err)
	assert.Contains(t, again, "already read-only")
}

func TestReadOnlyChannelsRejectPeopleButNotMatrixOrSystemPosts(t *testing.T) {
	p, api := newCutoverPlugin(t)
	require.NoError(t, p.kvstore.Set(cutoverReadOnlyKeyPrefix+"c1", []byte("1")))
	api.On("GetUser", "alice").Return(&model.User{Id: "alice"}, nil).Maybe()
	api.On("GetUser", "remote").Return(&model.User{Id: "remote", RemoteId: model.NewPointer("r1")}, nil).Maybe()

	rejected, reason := p.MessageWillBePosted(nil, &model.Post{ChannelId: "c1", UserId: "alice", Message: "hello"})
	assert.Nil(t, rejected)
	assert.Contains(t, reason, "moved to Matrix")

	allowed := []*model.Post{
		{ChannelId: "c1", UserId: "alice", Type: model.PostTypeJoinChannel},
		{ChannelId: "c1", UserId: "alice", Props: model.StringInterface{"from_matrix": true}},
		{ChannelId: "c1", UserId: "alice", Props: model.StringInterface{cutoverNoticeProp: true}},
		{ChannelId: "c1", UserId: "remote"},
		{ChannelId: "c2", UserId: "alice", Message: "other channel"},
	}
	for _, post := range allowed {
		got, reason := p.MessageWillBePosted(nil, post)
		assert.NotNil(t, got, post)
		assert.Empty(t, reason)
	}
}

func TestWritableUndoesReadOnly(t *testing.T) {
	p, _ := newCutoverPlugin(t)
	require.NoError(t, p.kvstore.Set(cutoverReadOnlyKeyPrefix+"c1", []byte("1")))

	out, err := p.MigrateTeam("admin", "team", "writable", false)
	require.NoError(t, err)
	assert.Contains(t, out, "1 read-only channels would accept")
	assert.True(t, p.channelReadOnly("c1"))

	_, err = p.MigrateTeam("admin", "team", "writable", true)
	require.NoError(t, err)
	assert.False(t, p.channelReadOnly("c1"))
}

func TestArchiveOnlyTouchesReadOnlyChannelsAndNeedsConfirm(t *testing.T) {
	p, api := newCutoverPlugin(t)

	out, err := p.MigrateTeam("admin", "team", "archive", true)
	require.NoError(t, err)
	assert.Contains(t, out, "Nothing to archive")

	require.NoError(t, p.kvstore.Set(cutoverReadOnlyKeyPrefix+"c1", []byte("1")))
	out, err = p.MigrateTeam("admin", "team", "archive", false)
	require.NoError(t, err)
	assert.Contains(t, out, "1 read-only channels would be archived")
	assert.Contains(t, out, "1 channels that are not read-only are left alone")

	api.On("DeleteChannel", "c1").Return(nil).Once()
	out, err = p.MigrateTeam("admin", "team", "archive", true)
	require.NoError(t, err)
	assert.Contains(t, out, "1 channels archived")
	api.AssertNotCalled(t, "DeleteChannel", "c2")
}

func TestOnlySystemAdminsCanCutOver(t *testing.T) {
	p, _ := newCutoverPlugin(t)
	_, err := p.MigrateTeam("member", "team", "readonly", true)
	require.Error(t, err)
	assert.False(t, p.channelReadOnly("c1"))
}

func TestCutoverReportComparesBothSides(t *testing.T) {
	p, api := newCutoverPlugin(t)
	api.On("GetChannelStats", mock.Anything).Return(&model.ChannelStats{MemberCount: 5}, nil)
	post := &model.Post{Id: "p1", ChannelId: "c1", CreateAt: 10, Message: "not yet in the room"}
	api.On("GetPostsForChannel", "c1", 0, historyPageSize).Return(&model.PostList{Order: []string{"p1"}, Posts: map[string]*model.Post{"p1": post}}, nil)
	api.On("GetPostsForChannel", "c1", 1, historyPageSize).Return(&model.PostList{}, nil)
	api.On("GetPostsForChannel", "c2", 0, historyPageSize).Return(&model.PostList{}, nil)

	out, err := p.MigrateTeam("admin", "team", "report", false)
	require.NoError(t, err)
	assert.Contains(t, out, "| ~chan-c1 | no | 5 / 0 | 1 |")
	assert.Contains(t, out, "| ~chan-c2 | no | 5 / 0 | 0 ✅ |")
	assert.Contains(t, out, "1 of 2 channels have every message")
}
