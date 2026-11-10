package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a unit of work to be processed by the queue
type Task struct {
	// Key keeps related tasks in order: tasks with the same key run one after
	// the other, in the order they were queued (for example one Matrix room).
	// Tasks without a key are spread over the workers.
	Key       string
	ID        string
	Type      string
	Payload   any
	Execute   func(ctx context.Context) error
	CreatedAt time.Time
	Retries   int
}

// EventQueue manages a pool of workers to process tasks asynchronously. Each
// worker has its own lane; a task goes to the lane its Key hashes to, so tasks
// with one key are strictly ordered while different keys run in parallel.
type EventQueue struct {
	lanes       []chan Task
	workerCount int
	stopChan    chan struct{}
	wg          sync.WaitGroup
	logger      Logger
	metrics     *Metrics
	next        atomic.Uint64

	// MaxAttempts is how many times a failing task is tried (default 1: no
	// retry). Between attempts the worker waits RetryBackoff[attempt-1], the
	// last value repeating.
	MaxAttempts  int
	RetryBackoff []time.Duration

	// Optional: Callback for failed tasks after all retries
	OnFinalFailure func(task Task, err error)
}

// NewEventQueue creates a new EventQueue with the specified number of workers
func NewEventQueue(workerCount int, bufferSize int, logger Logger, metrics *Metrics) *EventQueue {
	if workerCount <= 0 {
		workerCount = 1
	}
	if bufferSize < 0 {
		bufferSize = 100
	}
	// The buffer is shared out between the lanes, rounded up.
	perLane := (bufferSize + workerCount - 1) / workerCount
	lanes := make([]chan Task, workerCount)
	for i := range lanes {
		lanes[i] = make(chan Task, perLane)
	}

	return &EventQueue{
		lanes:       lanes,
		workerCount: workerCount,
		stopChan:    make(chan struct{}),
		logger:      logger,
		metrics:     metrics,
		MaxAttempts: 1,
	}
}

// Depth is how many tasks are waiting for a worker.
func (q *EventQueue) Depth() int {
	n := 0
	for _, l := range q.lanes {
		n += len(l)
	}
	return n
}

// Capacity is how many tasks can wait.
func (q *EventQueue) Capacity() int {
	n := 0
	for _, l := range q.lanes {
		n += cap(l)
	}
	return n
}

func (q *EventQueue) lane(key string) chan Task {
	if key == "" {
		return q.lanes[int(q.next.Add(1)%uint64(len(q.lanes)))]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return q.lanes[int(h.Sum32()%uint32(len(q.lanes)))]
}

// Start spawns the worker goroutines
func (q *EventQueue) Start() {
	for i := 0; i < q.workerCount; i++ {
		q.wg.Add(1)
		go q.worker(q.lanes[i])
	}
	q.logger.LogInfo("Event queue started", "workers", q.workerCount)
}

// Stop gracefully shuts down the worker pool
func (q *EventQueue) Stop() {
	close(q.stopChan)
	// Workers finish the task they are running; queued tasks stay journaled (see inbox.go)
	q.wg.Wait()
	q.logger.LogInfo("Event queue stopped")
}

// Enqueue adds a task to the queue
func (q *EventQueue) Enqueue(task Task) error {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}

	select {
	case q.lane(task.Key) <- task:
		return nil
	default:
		// Queue is full
		return fmt.Errorf("event queue is full")
	}
}

func (q *EventQueue) worker(lane chan Task) {
	defer q.wg.Done()

	for {
		select {
		case <-q.stopChan:
			return
		case task, ok := <-lane:
			if !ok {
				return
			}

			q.processTask(task)
		}
	}
}

func (q *EventQueue) processTask(task Task) {
	attempts := q.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		task.Retries = attempt - 1
		start := time.Now()

		// Create a context with timeout for the task
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err = task.Execute(ctx)
		cancel()

		duration := time.Since(start)
		if q.metrics != nil {
			q.metrics.RecordAPILatency(duration)
		}

		if err == nil {
			q.logger.LogDebug("Task completed successfully",
				"task_id", task.ID,
				"type", task.Type,
				"duration", duration)
			return
		}

		q.logger.LogError("Task failed",
			"task_id", task.ID,
			"type", task.Type,
			"error", err,
			"duration", duration,
			"attempt", attempt,
			"max_attempts", attempts)
		if q.metrics != nil {
			q.metrics.RecordMatrixAPIError()
		}
		if attempt < attempts {
			select {
			case <-q.stopChan:
				// Shutting down: leave the task to whoever journals it.
				return
			case <-time.After(q.backoff(attempt)):
			}
		}
	}

	if q.OnFinalFailure != nil {
		q.OnFinalFailure(task, err)
	}
}

func (q *EventQueue) backoff(attempt int) time.Duration {
	if len(q.RetryBackoff) == 0 {
		return time.Second
	}
	i := attempt - 1
	if i >= len(q.RetryBackoff) {
		i = len(q.RetryBackoff) - 1
	}
	return q.RetryBackoff[i]
}
