package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func timelineEvent(id, sender, evType string, ts int64, content map[string]any) map[string]any {
	return map[string]any{"event_id": id, "sender": sender, "type": evType, "origin_server_ts": float64(ts), "content": content}
}

func text(body string) map[string]any { return map[string]any{"msgtype": "m.text", "body": body} }

func TestMatrixHistoryPlanReadsOldestFirstAndAppliesEdits(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	hs.timeline = []map[string]any{ // newest first, as the homeserver returns it
		timelineEvent("$react", "@bob:other.org", "m.reaction", 5000, map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": "$one", "key": "👍"}}),
		timelineEvent("$edit", "@ada:other.org", "m.room.message", 4000, map[string]any{
			"msgtype": "m.text", "body": "* fixed",
			"m.new_content": map[string]any{"msgtype": "m.text", "body": "fixed"},
			"m.relates_to":  map[string]any{"rel_type": "m.replace", "event_id": "$one"},
		}),
		timelineEvent("$ghost", "@_mattermost_x:example.com", "m.room.message", 3500, text("from the bridge")),
		timelineEvent("$known", "@bob:other.org", "m.room.message", 3000, text("already here")),
		timelineEvent("$file", "@bob:other.org", "m.room.message", 2000, map[string]any{"msgtype": "m.file", "body": "a.pdf", "url": "mxc://other.org/x"}),
		timelineEvent("$one", "@ada:other.org", "m.room.message", 1000, text("typo")),
		timelineEvent("$crypt", "@ada:other.org", "m.room.encrypted", 900, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}),
		{"event_id": "$gone", "sender": "@bob:other.org", "type": "m.room.message", "origin_server_ts": float64(800), "content": map[string]any{}},
	}
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$known"), []byte("post1")))

	plan, roomID, err := p.planMatrixHistory("ch1", 0)
	require.NoError(t, err)
	assert.Equal(t, "!room:example.com", roomID)

	require.Len(t, plan.events, 3, "two messages and a reaction")
	assert.Equal(t, "$one", plan.events[0].EventID)
	assert.Equal(t, "fixed", plan.events[0].Content["body"], "the latest edit is applied")
	assert.Equal(t, "$file", plan.events[1].EventID)
	assert.Equal(t, "$react", plan.events[2].EventID, "reactions come after the messages")
	assert.Equal(t, 1, plan.files)
	assert.Equal(t, 1, plan.edits)
	assert.Equal(t, 1, plan.already)
	assert.Equal(t, 1, plan.byBridge)
	assert.Equal(t, 1, plan.encrypted)
	assert.True(t, plan.reached)
	assert.Contains(t, plan.describe(), "encrypted")
}

func TestMatrixHistoryNeedsABridgedChannel(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	_, _, err := p.planMatrixHistory("unmapped", 0)
	assert.ErrorContains(t, err, "not linked")
}

func TestMatrixHistoryRespectsTheLimit(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	for i := 5; i >= 1; i-- {
		hs.timeline = append(hs.timeline, timelineEvent("$e"+string(rune('0'+i)), "@a:other.org", "m.room.message", int64(i*1000), text("m")))
	}
	plan, _, err := p.planMatrixHistory("ch1", 3)
	require.NoError(t, err)
	assert.Len(t, plan.events, 3)
	assert.False(t, plan.reached, "more history remains")
}

func TestMatrixHistoryWithOnlyReactionsPlansNothing(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	hs.timeline = []map[string]any{
		timelineEvent("$react", "@bob:other.org", "m.reaction", 2000, map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": "$one", "key": "👍"}}),
		timelineEvent("$one", "@ada:other.org", "m.room.message", 1000, text("already imported")),
	}
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$one"), []byte("post1")))

	plan, _, err := p.planMatrixHistory("ch1", 0)
	require.NoError(t, err, "a run with nothing new must not fail")
	assert.Empty(t, plan.events)
	assert.Contains(t, plan.describe(), "Nothing to import")
}
