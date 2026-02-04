package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Task represents a unit of work to be processed by the queue
type Task struct {
	ID        string
	Type      string
	Payload   any
	Execute   func(ctx context.Context) error
	CreatedAt time.Time
	Retries   int
}

// EventQueue manages a pool of workers to process tasks asynchronously
type EventQueue struct {
	taskChan    chan Task
	workerCount int
	stopChan    chan struct{}
	wg          sync.WaitGroup
	logger      Logger
	metrics     *Metrics
	
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
	
	return &EventQueue{
		taskChan:    make(chan Task, bufferSize),
		workerCount: workerCount,
		stopChan:    make(chan struct{}),
		logger:      logger,
		metrics:     metrics,
	}
}

// Start spawns the worker goroutines
func (q *EventQueue) Start() {
	for i := 0; i < q.workerCount; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
	q.logger.LogInfo("Event queue started", "workers", q.workerCount)
}

// Stop gracefully shuts down the worker pool
func (q *EventQueue) Stop() {
	close(q.stopChan)
	// Don't close taskChan yet to let workers finish currently processing tasks
	q.wg.Wait()
	q.logger.LogInfo("Event queue stopped")
}

// Enqueue adds a task to the queue
func (q *EventQueue) Enqueue(task Task) error {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	
	select {
	case q.taskChan <- task:
		return nil
	default:
		// Queue is full
		return fmt.Errorf("event queue is full")
	}
}

func (q *EventQueue) worker(id int) {
	defer q.wg.Done()
	
	for {
		select {
		case <-q.stopChan:
			return
		case task, ok := <-q.taskChan:
			if !ok {
				return
			}
			
			q.processTask(task)
		}
	}
}

func (q *EventQueue) processTask(task Task) {
	start := time.Now()
	
	// Create a context with timeout for the task
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	
	err := task.Execute(ctx)
	
	duration := time.Since(start)
	if q.metrics != nil {
		q.metrics.RecordAPILatency(duration)
	}
	
	if err != nil {
		q.logger.LogError("Task failed", 
			"task_id", task.ID, 
			"type", task.Type, 
			"error", err,
			"duration", duration,
			"retry_count", task.Retries)
			
		if q.metrics != nil {
			q.metrics.RecordMatrixAPIError()
		}
		
		// If the task itself doesn't handle retries, we could implement a simple one here
		// but usually our bridge logic already has internal retry logic for HTTP calls.
		// However, for total queueing failures, we might want to log it specifically.
		if q.OnFinalFailure != nil {
			q.OnFinalFailure(task, err)
		}
	} else {
		q.logger.LogDebug("Task completed successfully", 
			"task_id", task.ID, 
			"type", task.Type, 
			"duration", duration)
	}
}
