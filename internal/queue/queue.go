package queue

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a task in the queue
type Task struct {
	Data     any
	Priority int
}

// Queue implements a priority-based task queue
type Queue struct {
	taskCh     chan *Task
	stopCh     chan struct{}
	wg         sync.WaitGroup
	workers    int
	isStopping int32     // Use atomic for thread safety
	stopOnce   sync.Once // Ensure Stop is called exactly once
}

// New creates a new queue with the specified number of workers
func New(workers int) *Queue {
	if workers <= 0 {
		workers = 1
	}

	return &Queue{
		taskCh:  make(chan *Task, 100), // Buffer 100 tasks
		stopCh:  make(chan struct{}),
		workers: workers,
	}
}

// Start starts processing tasks
func (q *Queue) Start(handler func(any) error) {
	for i := range q.workers {
		q.wg.Add(1)
		go func(i int) {
			defer q.wg.Done()
			for {
				select {
				case task, ok := <-q.taskCh:
					if !ok {
						return // Channel closed
					}
					// Execute task
					if err := handler(task.Data); err != nil {
						// Log error but continue processing
					}
				case <-q.stopCh:
					return
				}
			}
		}(i)
	}
}

// Stop stops the queue
func (q *Queue) Stop() {
	q.stopOnce.Do(func() {
		// Set stopping flag before closing channels
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
func (q *Queue) Submit(task any, priority int) error {
	// Check if queue is stopping or stopped
	if atomic.LoadInt32(&q.isStopping) == 1 {
		return errors.New("queue is stopping or stopped")
	}

	// Try to submit with timeout to avoid blocking forever
	select {
	case q.taskCh <- &Task{task, priority}:
		return nil
	case <-time.After(50 * time.Millisecond):
		// Check again if the queue is stopping
		if atomic.LoadInt32(&q.isStopping) == 1 {
			return errors.New("queue is stopping")
		}

		// Try once more
		select {
		case q.taskCh <- &Task{task, priority}:
			return nil
		default:
			return errors.New("queue is full")
		}
	}
}
