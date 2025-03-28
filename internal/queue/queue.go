package queue

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a task in the queue
type Task struct {
	Data     interface{}
	Priority int
}

// Queue implements a priority-based task queue
type Queue struct {
	taskCh     chan *Task
	stopCh     chan struct{}
	wg         sync.WaitGroup
	workers    int
	isStopping int32     // Using atomic for thread safety
	stopOnce   sync.Once // Ensure Stop is only called once
}

// New creates a new queue with the specified number of workers
func New(workers int) *Queue {
	if workers <= 0 {
		workers = 1
	}

	return &Queue{
		taskCh:     make(chan *Task, 100), // Buffer 100 tasks
		stopCh:     make(chan struct{}),
		workers:    workers,
		isStopping: 0, // Initialize to not stopping
	}
}

// Start starts processing tasks
func (q *Queue) Start(handler func(interface{}) error) {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for {
				select {
				case task, ok := <-q.taskCh:
					if !ok {
						return // Channel closed
					}
					// Execute task
					err := handler(task.Data)
					if err != nil {
						// Log error but continue processing
					}
				case <-q.stopCh:
					return
				}
			}
		}()
	}
}

// Stop stops the queue
func (q *Queue) Stop() {
	// Use sync.Once to ensure we only close channels once
	q.stopOnce.Do(func() {
		// Set flag first to prevent new submissions
		atomic.StoreInt32(&q.isStopping, 1)

		// Signal workers to stop
		close(q.stopCh)

		// Wait for workers to exit
		q.wg.Wait()

		// Only now close the task channel
		close(q.taskCh)
	})
}

// Submit adds a task to the queue
func (q *Queue) Submit(task interface{}, priority int) error {
	// Check if queue is stopping before attempting to submit
	if atomic.LoadInt32(&q.isStopping) == 1 {
		return fmt.Errorf("queue is stopping or stopped")
	}

	// Try to submit with timeout to avoid blocking forever
	select {
	case q.taskCh <- &Task{task, priority}:
		return nil
	case <-time.After(50 * time.Millisecond):
		// Check again if queue is stopping before retrying
		if atomic.LoadInt32(&q.isStopping) == 1 {
			return fmt.Errorf("queue is stopping")
		}
		// Try once more (non-blocking)
		select {
		case q.taskCh <- &Task{task, priority}:
			return nil
		default:
			return fmt.Errorf("queue is full")
		}
	}
}
