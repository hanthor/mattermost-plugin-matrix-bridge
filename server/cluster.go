package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

// A high-availability Mattermost runs the plugin on every node. What the
// plugin keeps in memory is per node, so three things are shared through the
// key-value store or cluster events: the record of events the bridge sent
// (to recognise echoes), the history-import lock, and cache invalidation.

const (
	sentEventKeyPrefix   = "sent_event_"
	historyLockKeyPrefix = "history_lock_"
	historyLockTTL       = 15 * 60 // seconds; refreshed while an import runs

	clusterEventInvalidateChannel = "invalidate_channel_mapping"
)

// sharedSentStore keeps sent event IDs in the key-value store with an expiry.
type sharedSentStore struct{ p *Plugin }

func (s sharedSentStore) MarkSent(eventID string) {
	if s.p.kvstore == nil {
		return
	}
	expires := time.Now().Add(10 * time.Minute).Unix()
	_ = s.p.kvstore.Set(sentEventKeyPrefix+eventID, []byte(strconv.FormatInt(expires, 10)))
}

func (s sharedSentStore) WasSent(eventID string) bool {
	if s.p.kvstore == nil {
		return false
	}
	data, err := s.p.kvstore.Get(sentEventKeyPrefix + eventID)
	if err != nil || len(data) == 0 {
		return false
	}
	expires, err := strconv.ParseInt(string(data), 10, 64)
	return err == nil && time.Now().Unix() <= expires
}

// sweepSentEvents deletes expired records; it runs with the background job.
func (p *Plugin) sweepSentEvents() {
	if p.kvstore == nil {
		return
	}
	keys, err := p.kvstore.ListKeysWithPrefix(0, 200, sentEventKeyPrefix)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, key := range keys {
		data, err := p.kvstore.Get(key)
		if err != nil {
			continue
		}
		if expires, perr := strconv.ParseInt(string(data), 10, 64); perr != nil || now > expires {
			_ = p.kvstore.Delete(key)
		}
	}
}

// acquireHistoryLock takes the cross-node lock for a channel's history import.
// It returns false when another node (or run) holds it.
func (p *Plugin) acquireHistoryLock(channelID string) bool {
	ok, appErr := p.API.KVSetWithOptions(historyLockKeyPrefix+channelID, []byte("1"), model.PluginKVSetOptions{
		Atomic: true, OldValue: nil, ExpireInSeconds: historyLockTTL,
	})
	return appErr == nil && ok
}

// refreshHistoryLock keeps the lock while an import is making progress.
func (p *Plugin) refreshHistoryLock(channelID string) {
	_, _ = p.API.KVSetWithOptions(historyLockKeyPrefix+channelID, []byte("1"), model.PluginKVSetOptions{ExpireInSeconds: historyLockTTL})
}

func (p *Plugin) releaseHistoryLock(channelID string) {
	_ = p.API.KVDelete(historyLockKeyPrefix + channelID)
}

func (p *Plugin) historyLockHeld(channelID string) bool {
	data, appErr := p.API.KVGet(historyLockKeyPrefix + channelID)
	return appErr == nil && len(data) > 0
}

// publishChannelInvalidation tells the other nodes that a channel's mapping
// changed. It never fails the caller: the caches expire on their own anyway.
func (p *Plugin) publishChannelInvalidation(channelID string) {
	defer func() { _ = recover() }()
	if err := p.API.PublishPluginClusterEvent(
		model.PluginClusterEvent{Id: clusterEventInvalidateChannel, Data: []byte(channelID)},
		model.PluginClusterEventSendOptions{SendType: model.PluginClusterEventSendTypeReliable},
	); err != nil {
		p.logger.LogDebug("Could not tell the other nodes about a mapping change", "error", fmt.Sprint(err))
	}
}

// OnPluginClusterEvent receives what another node published.
func (p *Plugin) OnPluginClusterEvent(_ *plugin.Context, ev model.PluginClusterEvent) {
	if ev.Id == clusterEventInvalidateChannel && p.roomMappingCache != nil {
		p.roomMappingCache.Delete(kvstore.BuildChannelMappingKey(string(ev.Data)))
	}
}
