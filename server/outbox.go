package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// Delivery from Mattermost to Matrix mirrors the inbound journal: what a
// person did in Mattermost (post, edit, delete, react) is written to the
// key-value store before it is sent and removed once the homeserver has it.
// Sends go through the event queue, one lane per channel, so a channel's
// changes reach the room in the order they were made. When the homeserver is
// down the entry stays; a background pass sends it again later with a growing
// delay, and after a day it is set aside for /matrix retry.

const (
	outboxKeyPrefix     = "outbox_"
	outboxFailedPrefix  = "outboxfail_"
	outboxEvery         = 15 * time.Second
	outboxMaxAge        = 24 * time.Hour
	outboxMaxDelay      = 5 * time.Minute
	outboxOpPost        = "post"
	outboxOpDelete      = "delete"
	outboxOpReaction    = "reaction"
	outboxFailedMaxKept = 500
)

type outboxEntry struct {
	Op        string          `json:"op"`
	Post      *model.Post     `json:"post,omitempty"`
	Reaction  *model.Reaction `json:"reaction,omitempty"`
	ChannelID string          `json:"channel_id"`
	Created   int64           `json:"created"`
	Attempts  int             `json:"attempts"`
	NextAt    int64           `json:"next_at"`
	Error     string          `json:"error,omitempty"`
}

type outboxJob struct {
	key   string
	entry outboxEntry
}

type outboxState struct {
	inflight sync.Map
	seq      uint64
	mu       sync.Mutex
	stop     chan struct{}
	done     chan struct{}
}

func (p *Plugin) nextOutboxKey(subject string) string {
	p.outbox.mu.Lock()
	defer p.outbox.mu.Unlock()
	p.outbox.seq++
	return inboxKey(outboxKeyPrefix, fmt.Sprintf("%020d_%06d_%s", time.Now().UnixNano(), p.outbox.seq%1_000_000, subject))
}

// sendOutbound sends a change to Matrix reliably. Without a running queue (unit
// tests, before activation) it just sends.
func (p *Plugin) sendOutbound(op string, post *model.Post, reaction *model.Reaction, channelID string) {
	entry := outboxEntry{Op: op, Post: post, Reaction: reaction, ChannelID: channelID, Created: time.Now().Unix()}
	if p.eventQueue == nil {
		if err := p.runOutbound(entry); err != nil {
			p.logger.LogError("Failed to sync to Matrix", "error", err, "op", op, "channel_id", channelID)
		}
		return
	}
	subject := channelID
	if post != nil {
		subject = post.Id
	}
	job := outboxJob{key: p.nextOutboxKey(subject), entry: entry}
	if data, err := json.Marshal(entry); err == nil && len(data) <= inboxMaxBytes && p.kvstore != nil {
		if err := p.kvstore.Set(job.key, data); err != nil {
			p.logger.LogWarn("Could not journal an outgoing change", "error", err, "op", op)
		}
	}
	if err := p.queueOutbound(job); err != nil {
		// The entry is journaled; the background pass sends it shortly.
		p.logger.LogWarn("Event queue full; the change stays in the outbox and is sent shortly", "op", op, "channel_id", channelID)
	}
}

func (p *Plugin) queueOutbound(job outboxJob) error {
	p.outbox.inflight.Store(job.key, struct{}{})
	err := p.eventQueue.Enqueue(Task{
		Key:     job.entry.ChannelID,
		ID:      "outbound-" + job.key,
		Type:    "mattermost-" + job.entry.Op,
		Payload: job,
		Execute: func(ctx context.Context) error {
			if err := p.runOutbound(job.entry); err != nil {
				return err
			}
			p.finishOutbound(job.key)
			return nil
		},
	})
	if err != nil {
		p.outbox.inflight.Delete(job.key)
	}
	return err
}

func (p *Plugin) finishOutbound(key string) {
	p.outbox.inflight.Delete(key)
	if p.kvstore != nil {
		_ = p.kvstore.Delete(key)
	}
}

// runOutbound performs one change against Matrix.
func (p *Plugin) runOutbound(e outboxEntry) error {
	bridge := p.mattermostToMatrixBridge
	if bridge == nil {
		return errors.New("the bridge is not ready")
	}
	switch e.Op {
	case outboxOpPost:
		post := e.Post
		if post == nil {
			return nil
		}
		if e.Attempts > 0 {
			// The post may have gained its Matrix event ID (the earlier try got
			// through but its answer was lost) or been deleted since.
			fresh, appErr := p.API.GetPost(post.Id)
			if appErr != nil || fresh == nil || fresh.DeleteAt != 0 {
				return nil
			}
			post = fresh
		}
		p.stageAttachmentsForPost(post)
		return bridge.SyncPostToMatrix(post, e.ChannelID)
	case outboxOpDelete:
		if e.Post == nil {
			return nil
		}
		return bridge.deletePostFromMatrix(e.Post, e.ChannelID)
	case outboxOpReaction:
		if e.Reaction == nil {
			return nil
		}
		return bridge.SyncReactionToMatrix(e.Reaction, e.ChannelID)
	}
	return nil
}

// onOutboundFailed runs when the queue has tried an outgoing change as often
// as it will for now: it stays in the outbox with a later time.
func (p *Plugin) onOutboundFailed(job outboxJob, cause error) {
	p.outbox.inflight.Delete(job.key)
	e := job.entry
	e.Attempts++
	e.Error = cause.Error()
	delay := time.Duration(5*(1<<min(e.Attempts, 6))) * time.Second
	if delay > outboxMaxDelay {
		delay = outboxMaxDelay
	}
	e.NextAt = time.Now().Add(delay).Unix()

	if time.Since(time.Unix(e.Created, 0)) > outboxMaxAge {
		p.setAsideOutbound(job.key, e)
		return
	}
	data, err := json.Marshal(e)
	if err != nil || len(data) > inboxMaxBytes || p.kvstore == nil {
		p.logger.LogError("Could not keep an outgoing change for a later retry", "op", e.Op, "error", cause)
		return
	}
	if err := p.kvstore.Set(job.key, data); err != nil {
		p.logger.LogError("Could not keep an outgoing change for a later retry", "op", e.Op, "error", err)
		return
	}
	p.logger.LogWarn("Could not reach Matrix; the change stays in the outbox", "op", e.Op, "channel_id", e.ChannelID, "attempts", e.Attempts, "retry_in", delay.String(), "error", cause)
}

// setAsideOutbound moves a change that never got through to the failed list.
func (p *Plugin) setAsideOutbound(key string, e outboxEntry) {
	_ = p.kvstore.Delete(key)
	if keys, err := p.kvstore.ListKeysWithPrefix(0, outboxFailedMaxKept+1, outboxFailedPrefix); err == nil && len(keys) > outboxFailedMaxKept {
		p.logger.LogError("Failed-delivery list is full; dropping a change", "op", e.Op, "channel_id", e.ChannelID)
		return
	}
	if data, err := json.Marshal(e); err == nil && len(data) <= inboxMaxBytes {
		_ = p.kvstore.Set(outboxFailedPrefix+key[len(outboxKeyPrefix):], data)
	}
	p.logger.LogError("A Mattermost change never reached Matrix and was set aside; /matrix retry sends it again", "op", e.Op, "channel_id", e.ChannelID, "error", e.Error)
}

// OutboundFailed is how many changes were set aside.
func (p *Plugin) OutboundFailed() int { return p.countKeys(outboxFailedPrefix) }

// OutboundPending is how many changes wait in the outbox.
func (p *Plugin) OutboundPending() int { return p.countKeys(outboxKeyPrefix) }

func (p *Plugin) countKeys(prefix string) int {
	if p.kvstore == nil {
		return 0
	}
	n := 0
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, prefix)
		if err != nil {
			return n
		}
		n += len(keys)
		if len(keys) < 200 {
			return n
		}
	}
}

// retryOutboundFailed puts set-aside changes back in the outbox, due at once.
func (p *Plugin) retryOutboundFailed() int {
	n := 0
	for {
		keys, err := p.kvstore.ListKeysWithPrefix(0, 200, outboxFailedPrefix)
		if err != nil || len(keys) == 0 {
			return n
		}
		for _, key := range keys {
			data, err := p.kvstore.Get(key)
			_ = p.kvstore.Delete(key)
			if err != nil || len(data) == 0 {
				continue
			}
			var e outboxEntry
			if json.Unmarshal(data, &e) != nil {
				continue
			}
			e.Created, e.Attempts, e.NextAt, e.Error = time.Now().Unix(), 0, 0, ""
			if out, err := json.Marshal(e); err == nil {
				_ = p.kvstore.Set(outboxKeyPrefix+key[len(outboxFailedPrefix):], out)
				n++
			}
		}
		if len(keys) < 200 {
			return n
		}
	}
}

// startOutbox begins the background pass that sends what is left in the outbox.
func (p *Plugin) startOutbox() {
	p.outbox.stop = make(chan struct{})
	p.outbox.done = make(chan struct{})
	go func() {
		defer close(p.outbox.done)
		p.drainOutbox(true)
		t := time.NewTicker(outboxEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.drainOutbox(false)
			case <-p.outbox.stop:
				return
			}
		}
	}()
}

func (p *Plugin) stopOutbox() {
	if p.outbox.stop == nil {
		return
	}
	close(p.outbox.stop)
	<-p.outbox.done
	p.outbox.stop = nil
}

// drainOutbox queues entries that are due, oldest first. At start everything
// is due: nothing is in flight in a fresh process.
func (p *Plugin) drainOutbox(atStart bool) {
	if p.kvstore == nil || p.eventQueue == nil {
		return
	}
	now := time.Now().Unix()
	var due []outboxJob
	for page := 0; ; page++ {
		keys, err := p.kvstore.ListKeysWithPrefix(page, 200, outboxKeyPrefix)
		if err != nil {
			return
		}
		for _, key := range keys {
			if _, busy := p.outbox.inflight.Load(key); busy {
				continue
			}
			data, err := p.kvstore.Get(key)
			if err != nil || len(data) == 0 {
				continue
			}
			var e outboxEntry
			if json.Unmarshal(data, &e) != nil {
				_ = p.kvstore.Delete(key)
				continue
			}
			if !atStart && e.NextAt > now {
				continue
			}
			if !atStart && e.Attempts == 0 && now-e.Created < int64(inboxReplayAfter/time.Second) {
				continue // just written; the queue has it
			}
			due = append(due, outboxJob{key: key, entry: e})
		}
		if len(keys) < 200 {
			break
		}
	}
	// Keys start with the time they were made, so the list is nearly in order
	// already; the queue keeps each channel's changes in the order queued.
	sortOutboxJobs(due)
	for _, job := range due {
		if err := p.queueOutbound(job); err != nil {
			return
		}
	}
	if len(due) > 0 {
		p.logger.LogInfo("Sending changes from the outbox", "count", len(due))
	}
}

func sortOutboxJobs(jobs []outboxJob) {
	for i := 1; i < len(jobs); i++ {
		for j := i; j > 0 && jobs[j].key < jobs[j-1].key; j-- {
			jobs[j], jobs[j-1] = jobs[j-1], jobs[j]
		}
	}
}
