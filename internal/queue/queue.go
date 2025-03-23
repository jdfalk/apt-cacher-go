package queue

import (
	"context"
	"sync"
	"time"
)

// Task represents a download task
type Task struct {
	URL      string
	Priority int
	Result   chan<- TaskResult
	Created  time.Time
}

// TaskResult represents the result of a download task
type TaskResult struct {
	Data  []byte
	Error error
}

// DownloadFunc is a function that downloads data from a URL
type DownloadFunc func(url string) ([]byte, error)

// Queue manages parallel downloads
type Queue struct {
	tasks        chan Task
	wg           sync.WaitGroup
	downloadFunc DownloadFunc
	concurrency  int
}

// New creates a new download queue with the specified concurrency
func New(concurrency int, downloadFunc DownloadFunc) *Queue {
	if concurrency < 1 {
		concurrency = 1
	}

	q := &Queue{
		tasks:        make(chan Task, 100), // Buffer up to 100 tasks
		downloadFunc: downloadFunc,
		concurrency:  concurrency,
	}

	return q
}

// Start begins processing downloads
func (q *Queue) Start(ctx context.Context) {
	// Start worker goroutines
	for i := 0; i < q.concurrency; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}
}

// Stop waits for all downloads to finish
func (q *Queue) Stop() {
	close(q.tasks)
	q.wg.Wait()
}

// Submit adds a download task to the queue
func (q *Queue) Submit(url string, priority int) <-chan TaskResult {
	result := make(chan TaskResult, 1)

	task := Task{
		URL:      url,
		Priority: priority,
		Result:   result,
		Created:  time.Now(),
	}

	q.tasks <- task

	return result
}

// worker processes tasks from the queue
func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return // Context cancelled

		case task, ok := <-q.tasks:
			if !ok {
				return // Channel closed
			}

			// Download the file
			data, err := q.downloadFunc(task.URL)

			// Send result
			task.Result <- TaskResult{
				Data:  data,
				Error: err,
			}

			// Close the result channel to prevent leaks
			close(task.Result)
		}
	}
}
