package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockQueueLogger struct {
	t *testing.T
}

func (l *mockQueueLogger) LogDebug(msg string, keyvals ...any) { l.t.Logf("[DEBUG] "+msg, keyvals...) }
func (l *mockQueueLogger) LogInfo(msg string, keyvals ...any)  { l.t.Logf("[INFO] "+msg, keyvals...) }
func (l *mockQueueLogger) LogWarn(msg string, keyvals ...any)  { l.t.Logf("[WARN] "+msg, keyvals...) }
func (l *mockQueueLogger) LogError(msg string, keyvals ...any) { l.t.Logf("[ERROR] "+msg, keyvals...) }

func TestEventQueueBasic(t *testing.T) {
	logger := &mockQueueLogger{t: t}
	q := NewEventQueue(2, 10, logger, nil)
	q.Start()
	defer q.Stop()

	var wg sync.WaitGroup
	processedCount := 0
	var mu sync.Mutex

	taskCount := 5
	wg.Add(taskCount)

	for i := 0; i < taskCount; i++ {
		err := q.Enqueue(Task{
			ID:   "test-task",
			Type: "test",
			Execute: func(ctx context.Context) error {
				mu.Lock()
				processedCount++
				mu.Unlock()
				wg.Done()
				return nil
			},
		})
		assert.NoError(t, err)
	}

	// Wait for processing
	wg.Wait()
	assert.Equal(t, taskCount, processedCount)
}

func TestEventQueueFull(t *testing.T) {
	logger := &mockQueueLogger{t: t}
	// Small buffer
	q := NewEventQueue(1, 1, logger, nil)
	// Don't start workers yet so buffer fills up
	
	err := q.Enqueue(Task{ID: "1", Execute: func(ctx context.Context) error { return nil }})
	assert.NoError(t, err)
	
	err = q.Enqueue(Task{ID: "2", Execute: func(ctx context.Context) error { return nil }})
	// Buffer is 1, so 1 in chan, 1 can be added if we're lucky but usually 2nd fails if 1 worker not reading
	// Wait, chan size is 1. We can put 1. Next one blocks. Select default handles it.
	
	err = q.Enqueue(Task{ID: "3", Execute: func(ctx context.Context) error { return nil }})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "full")
}

func TestEventQueueFailure(t *testing.T) {
	logger := &mockQueueLogger{t: t}
	q := NewEventQueue(1, 10, logger, nil)
	
	var finalTask Task
	var finalErr error
	var wg sync.WaitGroup
	wg.Add(1)
	
	q.OnFinalFailure = func(task Task, err error) {
		finalTask = task
		finalErr = err
		wg.Done()
	}
	
	q.Start()
	defer q.Stop()

	expectedErr := errors.New("boom")
	q.Enqueue(Task{
		ID: "fail-task",
		Execute: func(ctx context.Context) error {
			return expectedErr
		},
	})

	wg.Wait()
	assert.Equal(t, "fail-task", finalTask.ID)
	assert.Equal(t, expectedErr, finalErr)
}
