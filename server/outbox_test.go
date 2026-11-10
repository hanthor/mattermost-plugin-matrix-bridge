package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// outboxFixture is a bridged channel with a running queue.
func outboxFixture(t *testing.T, posts ...*model.Post) (*Plugin, *fakeSynapse) {
	t.Helper()
	p, hs, api := historyFixture(t, posts...)
	p.configuration.MatrixServerURL = "https://example.com"
	api.On("GetProfileImage", mock.Anything).Return([]byte{}, nil).Maybe()
	p.eventQueue = NewEventQueue(2, 50, p.logger, nil)
	p.eventQueue.MaxAttempts = 2
	p.eventQueue.RetryBackoff = []time.Duration{time.Millisecond}
	p.eventQueue.OnFinalFailure = p.onTaskFailed
	p.eventQueue.Start()
	t.Cleanup(p.eventQueue.Stop)
	return p, hs
}

func TestAPostGoesToMatrixAndLeavesNothingInTheOutbox(t *testing.T) {
	post := oldPost("p1", "mm-ada", "hello from the outbox", 0)
	p, hs := outboxFixture(t, post)

	p.sendOutbound(outboxOpPost, post, nil, historyChannel)

	require.Eventually(t, func() bool { return len(hs.messages()) == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return p.OutboundPending() == 0 }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, "hello from the outbox", hs.messages()[0].Body)
}

func TestAPostSentWhileTheHomeserverIsDownIsKeptAndSentLater(t *testing.T) {
	post := oldPost("p1", "mm-ada", "sent while down", 0)
	p, hs := outboxFixture(t, post)
	hs.setDown(true)

	p.sendOutbound(outboxOpPost, post, nil, historyChannel)

	// After the queue's own retries the change stays in the outbox, due later.
	require.Eventually(t, func() bool {
		keys, _ := p.kvstore.ListKeysWithPrefix(0, 10, outboxKeyPrefix)
		if len(keys) != 1 {
			return false
		}
		data, _ := p.kvstore.Get(keys[0])
		var e outboxEntry
		_ = json.Unmarshal(data, &e)
		return e.Attempts == 1 && e.NextAt > time.Now().Unix()
	}, 3*time.Second, 10*time.Millisecond, "the change waits for a later attempt")
	assert.Empty(t, hs.messages())

	// The homeserver comes back; the background pass sends it.
	hs.setDown(false)
	keys, _ := p.kvstore.ListKeysWithPrefix(0, 10, outboxKeyPrefix)
	data, _ := p.kvstore.Get(keys[0])
	var e outboxEntry
	require.NoError(t, json.Unmarshal(data, &e))
	e.NextAt = 0
	out, _ := json.Marshal(e)
	require.NoError(t, p.kvstore.Set(keys[0], out))

	p.drainOutbox(false)

	require.Eventually(t, func() bool { return len(hs.messages()) == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return p.OutboundPending() == 0 }, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, "sent while down", hs.messages()[0].Body)
}

func TestAChangeThatNeverGetsThroughIsSetAsideAndCanBeRetried(t *testing.T) {
	post := oldPost("p1", "mm-ada", "never delivered", 0)
	p, hs := outboxFixture(t, post)
	hs.setDown(true)
	job := outboxJob{key: p.nextOutboxKey("p1"), entry: outboxEntry{
		Op: outboxOpPost, Post: post, ChannelID: historyChannel,
		Created: time.Now().Add(-48 * time.Hour).Unix(), Attempts: 9,
	}}

	p.onOutboundFailed(job, assert.AnError)

	assert.Equal(t, 0, p.OutboundPending())
	assert.Equal(t, 1, p.OutboundFailed())
	assert.Equal(t, 1, p.DeadLetters(), "it counts with the other set-aside changes")

	hs.setDown(false)
	n, err := p.RetryDeadLetters()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 0, p.OutboundFailed())
	assert.Equal(t, 1, p.OutboundPending(), "back in the outbox, due at once")

	p.drainOutbox(true)
	require.Eventually(t, func() bool { return len(hs.messages()) == 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestAnOutboundChangeForADeletedPostIsDroppedOnRetry(t *testing.T) {
	post := oldPost("p1", "mm-ada", "gone", 0)
	p, hs, api := historyFixture(t, post)
	_ = api
	p.configuration.MatrixServerURL = "https://example.com"
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetPost")
	api.On("GetPost", "p1").Return(nil, &model.AppError{Message: "not found"})

	err := p.runOutbound(outboxEntry{Op: outboxOpPost, Post: post, ChannelID: historyChannel, Attempts: 1})

	require.NoError(t, err)
	assert.Empty(t, hs.messages())
}
