package main

import (
	"errors"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestAMatrixMessageThatCannotBeDeliveredIsReportedInTheRoom(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)

	p.deadLetter(MatrixEvent{Type: "m.room.message", EventID: "$lost", RoomID: "!room:example.com", Sender: "@ada:other.org"}, errors.New("boom"))
	sent := hs.messages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Body, "could not be delivered to Mattermost")
	assert.Contains(t, sent[0].Body, "@ada:other.org")

	// Only messages: a failed member event is not worth a notice.
	p.deadLetter(MatrixEvent{Type: "m.room.member", EventID: "$m", RoomID: "!room:example.com", Sender: "@ada:other.org"}, errors.New("boom"))
	assert.Len(t, hs.messages(), 1)
}

func TestTheAuthorIsToldWhenAPostKeepsFailingToReachMatrix(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	var told []string
	api.On("SendEphemeralPost", "mm-ada", mock.Anything).Run(func(args mock.Arguments) {
		told = append(told, args.Get(1).(*model.Post).Message)
	}).Return(&model.Post{})

	post := &model.Post{Id: "p1", UserId: "mm-ada", ChannelId: "ch1"}
	job := outboxJob{key: outboxKeyPrefix + "k", entry: outboxEntry{Op: outboxOpPost, Post: post, ChannelID: "ch1", Created: 0, Attempts: outboxTellAuthorAt - 1}}
	job.entry.Created = time.Now().Unix()
	p.onOutboundFailed(job, errors.New("homeserver down"))
	require.Len(t, told, 1)
	assert.Contains(t, told[0], "has not reached Matrix yet")

	// Later failures do not repeat the notice.
	job.entry.Attempts = outboxTellAuthorAt
	p.onOutboundFailed(job, errors.New("homeserver down"))
	assert.Len(t, told, 1)
}
