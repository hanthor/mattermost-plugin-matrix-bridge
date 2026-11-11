package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestWelcomeAccounts(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada"}
	bob := &model.User{Id: "mm-bob", Username: "bob"}
	cy := &model.User{Id: "mm-cy", Username: "cy"}
	p, api := newMirrorTestBridge(t, hs, ada, bob, cy)

	store := newMirrorStore(p.kvstore)
	require.NoError(t, store.save(&mirrorAccount{MattermostUserID: "mm-ada", MatrixUserID: "@ada:example.com", Localpart: "ada"}))
	require.NoError(t, store.save(&mirrorAccount{MattermostUserID: "mm-bob", MatrixUserID: "@bob:example.com", Localpart: "bob"}))
	require.NoError(t, store.save(&mirrorAccount{MattermostUserID: "mm-cy", MatrixUserID: "@cy:example.com", Localpart: "cy", Claimed: true}))

	// Ada and Bob have a bridged direct message; Ada has a favourite channel.
	require.NoError(t, p.kvstore.Set(dmBridgedKeyPrefix+"dm1", []byte("1")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("dm1"), []byte("!dm:example.com")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("chan1"), []byte("!chan1:example.com")))
	api.On("HasPermissionTo", "admin", model.PermissionManageSystem).Return(true).Maybe()
	api.On("HasPermissionTo", "member", model.PermissionManageSystem).Return(false).Maybe()
	api.On("GetChannelMembers", "dm1", 0, dmRoomMembersLimit).Return(model.ChannelMembers{{UserId: "mm-ada"}, {UserId: "mm-bob"}}, nil).Maybe()
	api.On("GetChannelMembers", mock.Anything, mock.Anything, mock.Anything).Return(model.ChannelMembers{}, nil).Maybe()
	api.On("GetPreferencesForUser", "mm-ada").Return([]model.Preference{{UserId: "mm-ada", Category: model.PreferenceCategoryFavoriteChannel, Name: "chan1", Value: "true"}}, nil).Maybe()
	api.On("GetPreferencesForUser", mock.Anything).Return([]model.Preference{}, nil).Maybe()
	api.On("EnsureBotUser", mock.Anything).Return("bot1", nil).Maybe()
	api.On("GetDirectChannel", "bot1", mock.Anything).Return(&model.Channel{Id: "dmbot"}, nil).Maybe()
	var notices []string
	api.On("CreatePost", mock.Anything).Run(func(a mock.Arguments) {
		notices = append(notices, a.Get(0).(*model.Post).Message)
	}).Return(&model.Post{}, nil).Maybe()

	t.Run("only administrators can run it", func(t *testing.T) {
		_, err := p.WelcomeAccounts("member", false)
		require.Error(t, err)
	})
	t.Run("the preview changes nothing and skips claimed accounts", func(t *testing.T) {
		text, err := p.WelcomeAccounts("admin", false)
		require.NoError(t, err)
		assert.Contains(t, text, "2 people")
		assert.Empty(t, hs.accountData)
		assert.Empty(t, notices)
	})
	t.Run("confirming brings the rooms over and tells people once", func(t *testing.T) {
		text, err := p.WelcomeAccounts("admin", true)
		require.NoError(t, err)
		assert.Contains(t, text, "Welcomed 2 people")
		joined := strings.Join(hs.accountData, "\n")
		assert.Contains(t, joined, "@ada:example.com")
		assert.Contains(t, joined, "m.direct")
		assert.Contains(t, joined, "@bob:example.com")
		assert.Contains(t, joined, "!dm:example.com")
		assert.Contains(t, joined, "m.favourite")
		assert.Contains(t, joined, "!chan1:example.com")
		require.Len(t, notices, 2)
		assert.Contains(t, notices[0]+notices[1], "@ada:example.com")

		// Repeating it adds no second message.
		_, err = p.WelcomeAccounts("admin", true)
		require.NoError(t, err)
		assert.Len(t, notices, 2)
	})
}

func TestWelcomeEmail(t *testing.T) {
	hs := newFakeSynapse(t)
	ada := &model.User{Id: "mm-ada", Username: "ada", Email: "ada@example.com"}
	p, api := newMirrorTestBridge(t, hs, ada)
	cfg := p.getConfiguration().Clone()
	cfg.WelcomeEmail = true
	p.setConfiguration(cfg)

	var body string
	api.On("SendMail", "ada@example.com", "Your Matrix account", mock.Anything).Run(func(a mock.Arguments) {
		body = a.String(2)
	}).Return((*model.AppError)(nil)).Once()
	p.emailWelcome(welcomePlan{userID: "mm-ada", matrixID: "@ada:example.com"})
	assert.Contains(t, body, "@ada:example.com")

	// A mail failure is not an error for the caller.
	api.On("SendMail", mock.Anything, mock.Anything, mock.Anything).Return(model.NewAppError("SendMail", "x", nil, "smtp off", 500)).Once()
	p.emailWelcome(welcomePlan{userID: "mm-ada", matrixID: "@ada:example.com"})
}
