package main

import (
	"sync"
	"time"
)

// Batcher handles grouping and debouncing of tasks to improve performance
type Batcher struct {
	mu           sync.Mutex
	items        map[string][]any
	timers       map[string]*time.Timer
	batchTimeout time.Duration
	onFlush      func(key string, items []any)
}

// NewBatcher creates a new Batcher instance
func NewBatcher(timeout time.Duration, onFlush func(key string, items []any)) *Batcher {
	return &Batcher{
		items:        make(map[string][]any),
		timers:       make(map[string]*time.Timer),
		batchTimeout: timeout,
		onFlush:      onFlush,
	}
}

// Add adds an item to a batch identified by key
func (b *Batcher) Add(key string, item any) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items[key] = append(b.items[key], item)

	// If a timer already exists, let it continue
	if _, ok := b.timers[key]; ok {
		return
	}

	// Create a new timer for this batch
	b.timers[key] = time.AfterFunc(b.batchTimeout, func() {
		b.Flush(key)
	})
}

// Flush immediately processes a batch of items
func (b *Batcher) Flush(key string) {
	b.mu.Lock()
	items := b.items[key]
	delete(b.items, key)
	if timer, ok := b.timers[key]; ok {
		timer.Stop()
		delete(b.timers, key)
	}
	b.mu.Unlock()

	if len(items) > 0 {
		b.onFlush(key, items)
	}
}

// FlushAll processes all pending batches
func (b *Batcher) FlushAll() {
	b.mu.Lock()
	keys := make([]string, 0, len(b.items))
	for k := range b.items {
		keys = append(keys, k)
	}
	b.mu.Unlock()

	for _, k := range keys {
		b.Flush(k)
	}
}
