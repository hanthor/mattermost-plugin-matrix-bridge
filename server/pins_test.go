package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestPinningAPostSetsTheRoomsPinnedEvents(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com")))
	key := p.historyEventKey()
	list := &model.PostList{Order: []string{"p1", "p2"}, Posts: map[string]*model.Post{
		"p1": {Id: "p1", IsPinned: true, Props: model.StringInterface{key: "$ev1"}},
		"p2": {Id: "p2", IsPinned: false, Props: model.StringInterface{key: "$ev2"}},
	}}
	api.On("GetPostsForChannel", "ch1", 0, 200).Return(list, nil)

	p.syncPinsToMatrix("ch1")
	require.Len(t, hs.states, 1)
	assert.Contains(t, hs.states[0], "m.room.pinned_events")
	assert.Contains(t, hs.states[0], `"pinned":["$ev1"]`)

	// Nothing changed: no second state event.
	p.syncPinsToMatrix("ch1")
	assert.Len(t, hs.states, 1)

	// Unpinned: the list empties.
	list.Posts["p1"].IsPinned = false
	p.syncPinsToMatrix("ch1")
	require.Len(t, hs.states, 2)
	assert.Contains(t, hs.states[1], `"pinned":[]`)
}

func TestPinningInMatrixPinsAndUnpinsPosts(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$a"), []byte("pa")))
	require.NoError(t, p.kvstore.Set(kvstore.BuildMatrixEventPostKey("$b"), []byte("pb")))

	postA := &model.Post{Id: "pa"}
	postB := &model.Post{Id: "pb", IsPinned: true}
	api.On("GetPost", "pa").Return(postA, nil)
	api.On("GetPost", "pb").Return(postB, nil)
	var updated []string
	api.On("UpdatePost", mock.Anything).Return(func(post *model.Post) *model.Post {
		if post.IsPinned {
			updated = append(updated, "pin "+post.Id)
		} else {
			updated = append(updated, "unpin "+post.Id)
		}
		return post
	}, nil)
	p.rememberPinnedEvents("!room:example.com", []string{"$b"})

	err := p.syncMatrixPins(MatrixEvent{RoomID: "!room:example.com", Content: map[string]any{"pinned": []any{"$a"}}}, "ch1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"pin pa", "unpin pb"}, updated)
	assert.Equal(t, []string{"$a"}, p.lastPinnedEvents("!room:example.com"))
}

func TestDifference(t *testing.T) {
	assert.Equal(t, []string{"a"}, difference([]string{"a", "b"}, []string{"b", "c"}))
	assert.Empty(t, difference(nil, []string{"x"}))
}
