package main

import (
	"sync"
	"testing"
	"time"
)

// flushRecorder collects flushes. Batches have their own timers, so the
// callback runs on several goroutines.
type flushRecorder struct {
	mu      sync.Mutex
	flushed []string
	byKey   map[string][]any
}

func (r *flushRecorder) record(key string, items []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string][]any{}
	}
	r.flushed = append(r.flushed, key)
	r.byKey[key] = items
}

func (r *flushRecorder) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.flushed...)
}

func (r *flushRecorder) items() map[string][]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]any, len(r.byKey))
	for k, v := range r.byKey {
		out[k] = v
	}
	return out
}

func TestBatcher_Basic(t *testing.T) {
	rec := &flushRecorder{}

	batcher := NewBatcher(50*time.Millisecond, rec.record)

	// Add items to the same batch
	batcher.Add("batch1", "item1")
	batcher.Add("batch1", "item2")
	batcher.Add("batch1", "item3")

	// Wait for the batch to flush
	time.Sleep(100 * time.Millisecond)

	if len(rec.keys()) != 1 {
		t.Errorf("Expected 1 batch, got %d", len(rec.keys()))
	}
	if rec.keys()[0] != "batch1" {
		t.Errorf("Expected key 'batch1', got '%s'", rec.keys()[0])
	}
	if len(rec.items()["batch1"]) != 3 {
		t.Errorf("Expected 3 items, got %d", len(rec.items()["batch1"]))
	}
}

func TestBatcher_MultipleBatches(t *testing.T) {
	rec := &flushRecorder{}

	batcher := NewBatcher(50*time.Millisecond, rec.record)

	// Add items to different batches
	batcher.Add("batch1", "item1")
	batcher.Add("batch2", "item2")
	batcher.Add("batch1", "item3")

	// Wait for the batches to flush
	time.Sleep(100 * time.Millisecond)

	if len(rec.keys()) != 2 {
		t.Errorf("Expected 2 batches, got %d", len(rec.keys()))
	}

	if len(rec.items()["batch1"]) != 2 {
		t.Errorf("Expected 2 items in batch1, got %d", len(rec.items()["batch1"]))
	}
	if len(rec.items()["batch2"]) != 1 {
		t.Errorf("Expected 1 item in batch2, got %d", len(rec.items()["batch2"]))
	}
}

func TestBatcher_ManualFlush(t *testing.T) {
	rec := &flushRecorder{}

	batcher := NewBatcher(1*time.Second, rec.record)

	batcher.Add("batch1", "item1")
	batcher.Add("batch1", "item2")

	// Manually flush before timeout
	batcher.Flush("batch1")

	if len(rec.keys()) != 1 {
		t.Errorf("Expected 1 batch after manual flush, got %d", len(rec.keys()))
	}
	if len(rec.items()["batch1"]) != 2 {
		t.Errorf("Expected 2 items, got %d", len(rec.items()["batch1"]))
	}

	// Wait to ensure timeout doesn't trigger again
	time.Sleep(100 * time.Millisecond)
	if len(rec.keys()) != 1 {
		t.Errorf("Expected still only 1 batch after waiting, got %d", len(rec.keys()))
	}
}

func TestBatcher_FlushAll(t *testing.T) {
	rec := &flushRecorder{}

	batcher := NewBatcher(1*time.Second, rec.record)

	batcher.Add("batch1", "item1")
	batcher.Add("batch2", "item2")
	batcher.Add("batch3", "item3")

	// Flush all batches at once
	batcher.FlushAll()

	if len(rec.keys()) != 3 {
		t.Errorf("Expected 3 batches after FlushAll, got %d", len(rec.keys()))
	}
}
