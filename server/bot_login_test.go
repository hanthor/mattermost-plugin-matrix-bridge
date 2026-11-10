package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loginFixture(t *testing.T, redactFails bool) (*Plugin, *fakeSynapse, MatrixEvent) {
	t.Helper()
	hs := newFakeSynapse(t)
	hs.whoami = "@alice:example.com"
	hs.redactFails = redactFails
	p, api := newMirrorTestBridge(t, hs)
	api.On("GetUser", "mm-alice").Return(nil, nil).Maybe()
	store := p.identity()
	code, err := store.CreateCode("mm-alice")
	require.NoError(t, err)
	_, err = store.Redeem(code, "@alice:example.com")
	require.NoError(t, err)
	return p, hs, MatrixEvent{
		Type: "m.room.message", RoomID: "!dm:example.com", EventID: "$login", Sender: "@alice:example.com",
		Content: map[string]any{"msgtype": "m.text", "body": "!mattermost login syt_token"},
	}
}

func TestLoginMessageIsRemovedFromTheRoom(t *testing.T) {
	p, hs, event := loginFixture(t, false)
	p.handleBotCommand(event)

	assert.Equal(t, []string{"!dm:example.com"}, hs.redactions, "the message holding the token is redacted")
	sent := hs.messages()
	require.NotEmpty(t, sent)
	assert.Contains(t, sent[len(sent)-1].Body, "removed your message")
}

func TestLoginMessageThatCannotBeRemovedTellsThePersonToDeleteIt(t *testing.T) {
	p, hs, event := loginFixture(t, true)
	p.handleBotCommand(event)

	assert.Empty(t, hs.redactions)
	sent := hs.messages()
	require.NotEmpty(t, sent)
	assert.True(t, strings.Contains(sent[len(sent)-1].Body, "delete it yourself"), sent[len(sent)-1].Body)
}
