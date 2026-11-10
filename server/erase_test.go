package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestEraseUser(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	p, api := newMirrorTestBridge(t, hs, ada, bob)
	require.NoError(t, newMirrorStore(p.kvstore).save(&mirrorAccount{MattermostUserID: "mm-ada", MatrixUserID: "@ada:example.com", Localpart: "ada"}))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan1"), []byte("!room1:example.com")))

	key := "matrix_event_id_" + extractServerDomain(p.API, hs.server.URL)
	post := func(id, user, event string) *model.Post {
		pt := &model.Post{Id: id, UserId: user, ChannelId: "chan1"}
		if event != "" {
			pt.AddProp(key, event)
		}
		return pt
	}
	posts := &model.PostList{Order: []string{"p1", "p2", "p3", "p4"}, Posts: map[string]*model.Post{
		"p1": post("p1", "mm-ada", "$ev1"),
		"p2": post("p2", "mm-bob", "$ev2"),
		"p3": post("p3", "mm-ada", "$ev3"),
		"p4": post("p4", "mm-ada", ""), // never reached Matrix
	}}
	api.On("HasPermissionTo", "admin", model.PermissionManageSystem).Return(true).Maybe()
	api.On("HasPermissionTo", "member", model.PermissionManageSystem).Return(false).Maybe()
	api.On("GetPostsForChannel", "chan1", 0, eraseScanPageSize).Return(posts, nil).Maybe()
	api.On("GetTeamsForUser", mock.Anything).Return([]*model.Team{}, nil).Maybe()

	t.Run("only administrators can run it", func(t *testing.T) {
		_, err := p.EraseUser("member", "ada", false)
		require.Error(t, err)
	})
	t.Run("the preview counts what would be redacted and changes nothing", func(t *testing.T) {
		text, err := p.EraseUser("admin", "@ada", false)
		require.NoError(t, err)
		assert.Contains(t, text, "2 messages")
		assert.Contains(t, text, "@ada:example.com")
		assert.Empty(t, hs.redactions)
		assert.Empty(t, hs.deactivated)
	})
	t.Run("confirming redacts only that person's bridged messages and erases the account", func(t *testing.T) {
		text, err := p.EraseUser("admin", "ada", true)
		require.NoError(t, err)
		assert.Contains(t, text, "2 messages redacted")
		assert.Len(t, hs.redactions, 2)
		assert.Contains(t, hs.deactivated, "@ada:example.com")
	})
	t.Run("a person without a bridge account has nothing to erase", func(t *testing.T) {
		text, err := p.EraseUser("admin", "bob", true)
		require.NoError(t, err)
		assert.Contains(t, text, "no bridge account")
	})
}
