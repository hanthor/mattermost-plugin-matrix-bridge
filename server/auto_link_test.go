package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAutoLinkByEmail(t *testing.T) {
	ada := &model.User{Id: "mm-ada", Username: "ada", Email: "ada@example.com", EmailVerified: true}
	setup := func(t *testing.T, enabled bool, mutate func(*fakeSynapse, *model.User)) (*Plugin, *fakeSynapse) {
		hs := newFakeSynapse(t)
		hs.threepids = map[string][]map[string]any{
			"@ada:example.com": {{"medium": "email", "address": "Ada@Example.com", "validated_at": 1700000000000}},
		}
		u := *ada
		if mutate != nil {
			mutate(hs, &u)
		}
		p, api := newMirrorTestBridge(t, hs)
		p.configuration.AutoLinkByEmail = enabled
		api.On("GetUserByEmail", "ada@example.com").Return(&u, nil).Maybe()
		api.On("GetUserByEmail", mock.Anything).Return(nil, &model.AppError{Message: "none"}).Maybe()
		return p, hs
	}

	t.Run("the same verified email links the two accounts", func(t *testing.T) {
		p, _ := setup(t, true, nil)
		id, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		require.True(t, ok)
		assert.Equal(t, "mm-ada", id)
		link, linked := p.identity().Link("mm-ada")
		require.True(t, linked)
		assert.Equal(t, "@ada:example.com", link.MatrixUserID)
	})
	t.Run("with MAS the link also gives the bridge the person's own token", func(t *testing.T) {
		p, hs := setup(t, true, nil)
		hs.whoami = "@ada:example.com"
		mas := newFakeMASServer(t)
		withMAS(p, mas)
		p.configuration.MatrixServerDomain = "example.com"
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		require.True(t, ok)
		link, linked := p.identity().Link("mm-ada")
		require.True(t, linked)
		assert.True(t, link.Puppeted(), "the person's own account sends their messages")
	})
	t.Run("a token for the wrong account is not used", func(t *testing.T) {
		p, hs := setup(t, true, nil)
		hs.whoami = "@someone-else:example.com"
		mas := newFakeMASServer(t)
		withMAS(p, mas)
		p.configuration.MatrixServerDomain = "example.com"
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		require.True(t, ok)
		link, _ := p.identity().Link("mm-ada")
		assert.False(t, link.Puppeted())
	})
	t.Run("it does nothing unless switched on", func(t *testing.T) {
		p, _ := setup(t, false, nil)
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		assert.False(t, ok)
	})
	t.Run("an address the homeserver has not verified is not trusted", func(t *testing.T) {
		p, hs := setup(t, true, nil)
		hs.threepids["@ada:example.com"][0]["validated_at"] = 0
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		assert.False(t, ok)
	})
	t.Run("an unverified Mattermost address is not trusted", func(t *testing.T) {
		p, _ := setup(t, true, func(_ *fakeSynapse, u *model.User) { u.EmailVerified = false })
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		assert.False(t, ok)
	})
	t.Run("an account already linked to someone else is left alone", func(t *testing.T) {
		p, _ := setup(t, true, nil)
		_, err := p.identity().linkAccounts("mm-ada", "@someone-else:example.com")
		require.NoError(t, err)
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		assert.False(t, ok)
	})
	t.Run("a deactivated account is not linked", func(t *testing.T) {
		p, _ := setup(t, true, func(_ *fakeSynapse, u *model.User) { u.DeleteAt = 1 })
		_, ok := p.matrixToMattermostBridge.autoLinkByEmail("@ada:example.com")
		assert.False(t, ok)
	})
}
