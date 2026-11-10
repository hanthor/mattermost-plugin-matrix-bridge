package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestAdminStatus(t *testing.T) {
	p, _ := doctorFixture(t)
	api := p.API.(*plugintest.API)
	api.On("HasPermissionTo", "admin", model.PermissionManageSystem).Return(true).Maybe()
	api.On("HasPermissionTo", "member", model.PermissionManageSystem).Return(false).Maybe()
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-1"), []byte("!room1:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan-2"), []byte("!room2:example.com")))

	t.Run("only system administrators see it", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, uiRequest(t, p, http.MethodGet, "/api/v1/admin/status", "").Code)
		assert.Equal(t, http.StatusForbidden, uiRequest(t, p, http.MethodGet, "/api/v1/admin/status", "member").Code)
	})
	t.Run("an administrator gets health, mappings and counts", func(t *testing.T) {
		rec := uiRequest(t, p, http.MethodGet, "/api/v1/admin/status", "admin")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var status adminStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
		assert.True(t, status.Enabled)
		assert.Equal(t, "ghost", status.Mode)
		assert.Equal(t, "ok", status.Worst, "%v", status.Health)
		assert.NotEmpty(t, status.Health)
		assert.Equal(t, 2, status.MappedChannels)
		require.Len(t, status.Mappings, 2)
		assert.Equal(t, "!room1:example.com", status.Mappings[0].Room)
		assert.Equal(t, 0, status.DeadLetters)
	})
}
