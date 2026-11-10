package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// Delivery from Matrix to Mattermost must not lose events. The homeserver
// treats a 200 as "delivered" and never sends the transaction again, so every
// event is written to the key-value store (the journal) before the 200 and
// removed once its handler has finished. Events that fail every retry move to
// a dead-letter list that an administrator can replay with /matrix retry.
// Whatever is still in the journal after a restart or a crash is replayed.

const (
	inboxKeyPrefix      = "inbox_"
	deadLetterKeyPrefix = "deadletter_"
	// The key-value store limits value sizes; a bigger event is handled
	// without a journal entry.
	inboxMaxBytes    = 40_000
	deadLetterMax    = 500
	inboxReplayEvery = 30 * time.Second
	inboxReplayAfter = 20 * time.Second
	inboxAttempts    = 3
)

type inboxEntry struct {
	Event    MatrixEvent `json:"event"`
	Received int64       `json:"received"`
	Error    string      `json:"error,omitempty"`
	FailedAt int64       `json:"failed_at,omitempty"`
}

type inboxState struct {
	inflight sync.Map // journal key -> struct{}, for events queued or running
	stop     chan struct{}
	done     chan struct{}
}

func inboxKey(prefix, eventID string) string {
	if len(eventID) > 100 {
		sum := sha256.Sum256([]byte(eventID))
		return prefix + "h_" + hex.EncodeToString(sum[:])
	}
	return prefix + eventID
}

// dispatchInbound journals an event (when journal is true) and queues it. It
// reports whether the event is safe in the journal; an event that is neither
// journaled nor queued is lost unless the caller makes the homeserver retry.
func (p *Plugin) dispatchInbound(e MatrixEvent, journal bool) (journaled bool, err error) {
	key := inboxKey(inboxKeyPrefix, e.EventID)
	if journal && e.EventID != "" && p.kvstore != nil {
		if data, mErr := json.Marshal(inboxEntry{Event: e, Received: time.Now().Unix()}); mErr == nil && len(data) <= inboxMaxBytes {
			if sErr := p.kvstore.Set(key, data); sErr == nil {
				journaled = true
			} else {
				p.logger.LogWarn("Could not journal a Matrix event", "error", sErr, "event_id", e.EventID)
			}
		}
	} else if !journal {
		journaled = true // replaying: it is in the journal already
	}

	p.inbox.inflight.Store(key, struct{}{})
	err = p.eventQueue.Enqueue(Task{
		Key:     e.RoomID,
		ID:      "matrix-event-" + e.EventID,
		Type:    "matrix-event-" + e.Type,
		Payload: e,
		Execute: func(ctx context.Context) error {
			if processErr := p.processMatrixEvent(e); processErr != nil {
				return processErr
			}
			p.completeInbound(e)
			return nil
		},
	})
	if err != nil {
		p.inbox.inflight.Delete(key)
	}
	return journaled, err
}

// completeInbound forgets an event whose handler is done.
func (p *Plugin) completeInbound(e MatrixEvent) {
	key := inboxKey(inboxKeyPrefix, e.EventID)
	p.inbox.inflight.Delete(key)
	if p.kvstore != nil && e.EventID != "" {
		_ = p.kvstore.Delete(key)
	}
}

// onTaskFailed is the queue's last resort: an inbound event that failed every
// attempt is kept on the dead-letter list.
func (p *Plugin) onTaskFailed(task Task, err error) {
	switch payload := task.Payload.(type) {
	case MatrixEvent:
		p.deadLetter(payload, err)
		p.completeInbound(payload)
	case outboxJob:
		p.onOutboundFailed(payload, err)
	}
}

func (p *Plugin) deadLetter(e MatrixEvent, cause error) {
	if p.kvstore == nil || e.EventID == "" {
		return
	}
	if keys, err := p.kvstore.ListKeysWithPrefix(0, deadLetterMax+1, deadLetterKeyPrefix); err == nil && len(keys) > deadLetterMax {
		p.logger.LogError("Dead-letter list is full; dropping an event", "event_id", e.EventID)
		return
	}
	data, err := json.Marshal(inboxEntry{Event: e, Received: time.Now().Unix(), Error: cause.Error(), FailedAt: time.Now().Unix()})
	if err != nil || len(data) > inboxMaxBytes {
		p.logger.LogError("Could not keep a failed Matrix event", "event_id", e.EventID, "error", cause)
		return
	}
	if err := p.kvstore.Set(inboxKey(deadLetterKeyPrefix, e.EventID), data); err != nil {
		p.logger.LogError("Could not keep a failed Matrix event", "event_id", e.EventID, "error", err)
		return
	}
	p.logger.LogError("A Matrix event could not be delivered to Mattermost and was set aside; /matrix retry replays it", "event_id", e.EventID, "room_id", e.RoomID, "error", cause)
	p.tellRoomNotDelivered(e)
}

// tellRoomNotDelivered says in the room that a message did not reach
// Mattermost, so its sender is not left guessing.
func (p *Plugin) tellRoomNotDelivered(e MatrixEvent) {
	defer func() { _ = recover() }()
	if p.matrixClient == nil || e.RoomID == "" || e.Type != "m.room.message" {
		return
	}
	_ = p.matrixClient.SendNotice(e.RoomID, "⚠️ A message from "+e.Sender+" could not be delivered to Mattermost. An administrator can send it again with /matrix retry.")
}

// DeadLetters implements command.PluginAccessor: how many changes are set
// aside, in either direction.
func (p *Plugin) DeadLetters() int {
	return p.inboundDeadLetters() + p.OutboundFailed()
}

func (p *Plugin) inboundDeadLetters() int {
	if p.kvstore == nil {
		return 0
	}
	n := 0
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, deadLetterKeyPrefix)
		if err != nil {
			return n
		}
		n += len(keys)
		if len(keys) < 200 {
			return n
		}
	}
}

// RetryDeadLetters implements command.PluginAccessor: it queues every set-aside
// event again and returns how many it queued.
func (p *Plugin) RetryDeadLetters() (int, error) {
	if p.kvstore == nil || p.eventQueue == nil {
		return 0, errors.New("the bridge is not ready")
	}
	queued := p.retryOutboundFailed()
	for {
		keys, err := p.kvstore.ListKeysWithPrefix(0, 200, deadLetterKeyPrefix)
		if err != nil {
			return queued, err
		}
		progress := false
		for _, key := range keys {
			data, err := p.kvstore.Get(key)
			if err != nil || len(data) == 0 {
				_ = p.kvstore.Delete(key)
				continue
			}
			var entry inboxEntry
			if json.Unmarshal(data, &entry) != nil {
				_ = p.kvstore.Delete(key)
				continue
			}
			if _, err := p.dispatchInbound(entry.Event, true); err != nil {
				return queued, fmt.Errorf("the event queue is full after %d events; try again shortly", queued)
			}
			_ = p.kvstore.Delete(key)
			queued++
			progress = true
		}
		if len(keys) < 200 || !progress {
			return queued, nil
		}
	}
}

// startInbox replays what an earlier run left in the journal and keeps
// looking for events that were journaled but never queued (a full queue).
func (p *Plugin) startInbox() {
	p.inbox.stop = make(chan struct{})
	p.inbox.done = make(chan struct{})
	go func() {
		defer close(p.inbox.done)
		p.replayInbox(0)
		t := time.NewTicker(inboxReplayEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.replayInbox(inboxReplayAfter)
			case <-p.inbox.stop:
				return
			}
		}
	}()
}

func (p *Plugin) stopInbox() {
	if p.inbox.stop == nil {
		return
	}
	close(p.inbox.stop)
	<-p.inbox.done
	p.inbox.stop = nil
}

// replayInbox queues journal entries that are not already queued or running
// and are older than minAge.
func (p *Plugin) replayInbox(minAge time.Duration) {
	if p.kvstore == nil || p.eventQueue == nil {
		return
	}
	replayed := 0
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, inboxKeyPrefix)
		if err != nil {
			return
		}
		for _, key := range keys {
			if _, busy := p.inbox.inflight.Load(key); busy {
				continue
			}
			data, err := p.kvstore.Get(key)
			if err != nil || len(data) == 0 {
				continue
			}
			var entry inboxEntry
			if json.Unmarshal(data, &entry) != nil {
				_ = p.kvstore.Delete(key)
				continue
			}
			if minAge > 0 && time.Since(time.Unix(entry.Received, 0)) < minAge {
				continue
			}
			if _, err := p.dispatchInbound(entry.Event, false); err != nil {
				return // queue full; the next pass tries again
			}
			replayed++
		}
		if len(keys) < 200 {
			break
		}
	}
	if replayed > 0 {
		p.logger.LogInfo("Replayed Matrix events from the journal", "count", replayed)
	}
}

// pendingInbound is how many events are journaled and not yet handled.
func (p *Plugin) pendingInbound() int {
	if p.kvstore == nil {
		return 0
	}
	n := 0
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, inboxKeyPrefix)
		if err != nil {
			return n
		}
		n += len(keys)
		if len(keys) < 200 {
			return n
		}
	}
}
