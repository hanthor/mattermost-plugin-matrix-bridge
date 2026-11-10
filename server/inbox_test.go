package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTasksWithOneKeyRunInOrder(t *testing.T) {
	q := NewEventQueue(4, 200, &mockQueueLogger{t: t}, nil)
	q.Start()
	defer q.Stop()

	var mu sync.Mutex
	var seen []int
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		require.NoError(t, q.Enqueue(Task{Key: "!room:hs", ID: "t", Execute: func(context.Context) error {
			defer wg.Done()
			if i%7 == 0 {
				time.Sleep(2 * time.Millisecond) // a slow one must not let later ones overtake
			}
			mu.Lock()
			seen = append(seen, i)
			mu.Unlock()
			return nil
		}}))
	}
	wg.Wait()
	for i := range seen {
		require.Equal(t, i, seen[i], "tasks for one room keep their order")
	}
}

func TestADifferentKeyIsNotHeldUpBySlowWork(t *testing.T) {
	q := NewEventQueue(4, 20, &mockQueueLogger{t: t}, nil)
	q.Start()
	defer q.Stop()

	release := make(chan struct{})
	started := make(chan struct{})
	require.NoError(t, q.Enqueue(Task{Key: "slow-room", Execute: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}))
	<-started
	// Find a key that lands on another lane.
	var other string
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		if q.lane(k) != q.lane("slow-room") {
			other = k
			break
		}
	}
	require.NotEmpty(t, other)
	done := make(chan struct{})
	require.NoError(t, q.Enqueue(Task{Key: other, Execute: func(context.Context) error { close(done); return nil }}))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("work for another room waited behind a slow one")
	}
	close(release)
}

func TestAFailingTaskIsRetriedThenSetAside(t *testing.T) {
	q := NewEventQueue(1, 10, &mockQueueLogger{t: t}, nil)
	q.MaxAttempts = 3
	q.RetryBackoff = []time.Duration{time.Millisecond}
	var attempts atomic.Int32
	failed := make(chan error, 1)
	q.OnFinalFailure = func(_ Task, err error) { failed <- err }
	q.Start()
	defer q.Stop()

	require.NoError(t, q.Enqueue(Task{Execute: func(context.Context) error { attempts.Add(1); return errors.New("down") }}))
	select {
	case err := <-failed:
		assert.EqualError(t, err, "down")
	case <-time.After(2 * time.Second):
		t.Fatal("no final failure")
	}
	assert.Equal(t, int32(3), attempts.Load())
}

func TestATaskThatRecoversIsNotSetAside(t *testing.T) {
	q := NewEventQueue(1, 10, &mockQueueLogger{t: t}, nil)
	q.MaxAttempts = 3
	q.RetryBackoff = []time.Duration{time.Millisecond}
	var attempts atomic.Int32
	done := make(chan struct{})
	q.OnFinalFailure = func(Task, error) { t.Error("must not be set aside") }
	q.Start()
	defer q.Stop()

	require.NoError(t, q.Enqueue(Task{Execute: func(context.Context) error {
		if attempts.Add(1) < 2 {
			return errors.New("blip")
		}
		close(done)
		return nil
	}}))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("never retried")
	}
}

func inboxPlugin(t *testing.T) *Plugin {
	t.Helper()
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	p.eventQueue = NewEventQueue(2, 50, p.logger, nil)
	p.eventQueue.MaxAttempts = 2
	p.eventQueue.RetryBackoff = []time.Duration{time.Millisecond}
	p.eventQueue.OnFinalFailure = p.onTaskFailed
	return p
}

func TestAnEventIsJournaledUntilItsHandlerFinishes(t *testing.T) {
	p := inboxPlugin(t)
	// Not started: the event waits in the queue, journaled.
	e := MatrixEvent{EventID: "$e1", RoomID: "!r:hs", Type: "m.room.name", Sender: "@a:hs", Content: map[string]any{}}
	journaled, err := p.dispatchInbound(e, true)
	require.NoError(t, err)
	assert.True(t, journaled)
	assert.Equal(t, 1, p.pendingInbound(), "journaled before anything runs")

	p.completeInbound(e)
	assert.Equal(t, 0, p.pendingInbound(), "gone once handled")
}

func TestAnEventThatKeepsFailingGoesToTheDeadLetters(t *testing.T) {
	p := inboxPlugin(t)
	e := MatrixEvent{EventID: "$bad", RoomID: "!r:hs", Type: "m.room.message", Sender: "@a:hs"}
	p.deadLetter(e, errors.New("mattermost was down"))
	p.completeInbound(e)

	assert.Equal(t, 1, p.DeadLetters())
	assert.Equal(t, 0, p.pendingInbound())
}

func TestRetryingDeadLettersQueuesThemAgain(t *testing.T) {
	p := inboxPlugin(t)
	for _, id := range []string{"$a", "$b"} {
		p.deadLetter(MatrixEvent{EventID: id, RoomID: "!r:hs", Type: "m.room.name", Sender: "@a:hs"}, errors.New("boom"))
	}
	require.Equal(t, 2, p.DeadLetters())

	n, err := p.RetryDeadLetters()
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 0, p.DeadLetters())
	assert.Equal(t, 2, p.pendingInbound(), "they are journaled and queued again")
}

func TestWhatAnEarlierRunLeftInTheJournalIsReplayed(t *testing.T) {
	p := inboxPlugin(t)
	e := MatrixEvent{EventID: "$left-behind", RoomID: "!r:hs", Type: "m.room.name", Sender: "@a:hs"}
	_, err := p.dispatchInbound(e, true)
	require.NoError(t, err)
	// A restart: the queue and the in-flight record are gone, the journal is not.
	p.inbox.inflight.Range(func(k, _ any) bool { p.inbox.inflight.Delete(k); return true })
	p.eventQueue = NewEventQueue(2, 50, p.logger, nil)
	p.eventQueue.Start()
	defer p.eventQueue.Stop()

	p.replayInbox(0)
	require.Eventually(t, func() bool { return p.pendingInbound() == 0 }, 3*time.Second, 10*time.Millisecond,
		"the event is handled and leaves the journal")
}

func TestARecentJournalEntryIsLeftToTheQueue(t *testing.T) {
	p := inboxPlugin(t)
	e := MatrixEvent{EventID: "$fresh", RoomID: "!r:hs", Type: "m.room.name", Sender: "@a:hs"}
	_, err := p.dispatchInbound(e, true)
	require.NoError(t, err)
	p.inbox.inflight.Delete(inboxKey(inboxKeyPrefix, "$fresh"))
	before := p.eventQueue.Depth()

	p.replayInbox(time.Minute)
	assert.Equal(t, before, p.eventQueue.Depth(), "an entry younger than the grace period is not queued twice")
}

func TestLongEventIDsStayWithinTheKeyLimit(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	assert.LessOrEqual(t, len(inboxKey(inboxKeyPrefix, string(long))), 150)
	assert.Equal(t, "inbox_$abc", inboxKey(inboxKeyPrefix, "$abc"))
}
