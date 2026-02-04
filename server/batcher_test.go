package main

import (
	"testing"
	"time"
)

func TestBatcher_Basic(t *testing.T) {
	flushedKeys := []string{}
	flushedItems := make(map[string][]any)

	batcher := NewBatcher(50*time.Millisecond, func(key string, items []any) {
		flushedKeys = append(flushedKeys, key)
		flushedItems[key] = items
	})

	// Add items to the same batch
	batcher.Add("batch1", "item1")
	batcher.Add("batch1", "item2")
	batcher.Add("batch1", "item3")

	// Wait for the batch to flush
	time.Sleep(100 * time.Millisecond)

	if len(flushedKeys) != 1 {
		t.Errorf("Expected 1 batch, got %d", len(flushedKeys))
	}
	if flushedKeys[0] != "batch1" {
		t.Errorf("Expected key 'batch1', got '%s'", flushedKeys[0])
	}
	if len(flushedItems["batch1"]) != 3 {
		t.Errorf("Expected 3 items, got %d", len(flushedItems["batch1"]))
	}
}

func TestBatcher_MultipleBatches(t *testing.T) {
	flushedKeys := []string{}
	flushedItems := make(map[string][]any)

	batcher := NewBatcher(50*time.Millisecond, func(key string, items []any) {
		flushedKeys = append(flushedKeys, key)
		flushedItems[key] = items
	})

	// Add items to different batches
	batcher.Add("batch1", "item1")
	batcher.Add("batch2", "item2")
	batcher.Add("batch1", "item3")

	// Wait for the batches to flush
	time.Sleep(100 * time.Millisecond)

	if len(flushedKeys) != 2 {
		t.Errorf("Expected 2 batches, got %d", len(flushedKeys))
	}

	if len(flushedItems["batch1"]) != 2 {
		t.Errorf("Expected 2 items in batch1, got %d", len(flushedItems["batch1"]))
	}
	if len(flushedItems["batch2"]) != 1 {
		t.Errorf("Expected 1 item in batch2, got %d", len(flushedItems["batch2"]))
	}
}

func TestBatcher_ManualFlush(t *testing.T) {
	flushedKeys := []string{}
	flushedItems := make(map[string][]any)

	batcher := NewBatcher(1*time.Second, func(key string, items []any) {
		flushedKeys = append(flushedKeys, key)
		flushedItems[key] = items
	})

	batcher.Add("batch1", "item1")
	batcher.Add("batch1", "item2")

	// Manually flush before timeout
	batcher.Flush("batch1")

	if len(flushedKeys) != 1 {
		t.Errorf("Expected 1 batch after manual flush, got %d", len(flushedKeys))
	}
	if len(flushedItems["batch1"]) != 2 {
		t.Errorf("Expected 2 items, got %d", len(flushedItems["batch1"]))
	}

	// Wait to ensure timeout doesn't trigger again
	time.Sleep(100 * time.Millisecond)
	if len(flushedKeys) != 1 {
		t.Errorf("Expected still only 1 batch after waiting, got %d", len(flushedKeys))
	}
}

func TestBatcher_FlushAll(t *testing.T) {
	flushedKeys := []string{}
	flushedItems := make(map[string][]any)

	batcher := NewBatcher(1*time.Second, func(key string, items []any) {
		flushedKeys = append(flushedKeys, key)
		flushedItems[key] = items
	})

	batcher.Add("batch1", "item1")
	batcher.Add("batch2", "item2")
	batcher.Add("batch3", "item3")

	// Flush all batches at once
	batcher.FlushAll()

	if len(flushedKeys) != 3 {
		t.Errorf("Expected 3 batches after FlushAll, got %d", len(flushedKeys))
	}
}
