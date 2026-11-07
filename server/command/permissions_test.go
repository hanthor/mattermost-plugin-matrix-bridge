package command

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func newPermissionTestHandler(api *plugintest.API) *Handler {
	return &Handler{
		plugin:    &mockPlugin{},
		client:    pluginapi.NewClient(api, &plugintest.Driver{}),
		pluginAPI: api,
	}
}

func TestCommandPermissions(t *testing.T) {
	args := &model.CommandArgs{UserId: "user1", ChannelId: "chan1", TeamId: "team1"}

	t.Run("admin-only commands are denied to regular users", func(t *testing.T) {
		api := &plugintest.API{}
		api.On("HasPermissionTo", "user1", model.PermissionManageSystem).Return(false)
		h := newPermissionTestHandler(api)

		for _, sub := range []string{"map_user", "backfill", "migrate", "metrics"} {
			resp := h.checkCommandPermission(args, sub)
			if assert.NotNil(t, resp, sub) {
				assert.Contains(t, resp.Text, "system administrators")
			}
		}
	})

	t.Run("channel commands require channel management rights", func(t *testing.T) {
		api := &plugintest.API{}
		api.On("HasPermissionTo", "user1", model.PermissionManageSystem).Return(false)
		api.On("HasPermissionToChannel", "user1", "chan1", mock.Anything).Return(false)
		h := newPermissionTestHandler(api)

		for _, sub := range []string{"create", "map", "unmap", "join"} {
			resp := h.checkCommandPermission(args, sub)
			if assert.NotNil(t, resp, sub) {
				assert.Contains(t, resp.Text, "manage this channel")
			}
		}
	})

	t.Run("channel admins may bridge their channel", func(t *testing.T) {
		api := &plugintest.API{}
		api.On("HasPermissionTo", "user1", model.PermissionManageSystem).Return(false)
		api.On("HasPermissionToChannel", "user1", "chan1", model.PermissionManagePublicChannelProperties).Return(true)
		h := newPermissionTestHandler(api)

		assert.Nil(t, h.checkCommandPermission(args, "create"))
	})

	t.Run("system admins may run everything", func(t *testing.T) {
		api := &plugintest.API{}
		api.On("HasPermissionTo", "user1", model.PermissionManageSystem).Return(true)
		h := newPermissionTestHandler(api)

		for _, sub := range []string{"map_user", "backfill", "migrate", "metrics", "create", "map", "unmap", "join"} {
			assert.Nil(t, h.checkCommandPermission(args, sub), sub)
		}
	})

	t.Run("read-only commands are open to everyone", func(t *testing.T) {
		api := &plugintest.API{}
		h := newPermissionTestHandler(api)

		for _, sub := range []string{"test", "list", "status"} {
			assert.Nil(t, h.checkCommandPermission(args, sub), sub)
		}
	})
}
