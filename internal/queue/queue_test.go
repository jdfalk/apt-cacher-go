package queue

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Actual implementation of Queue for testing
type localQueue struct {
	data []any
}

// NewQueue creates a new queue
func NewQueue() *localQueue {
	return &localQueue{
		data: make([]any, 0),
	}
}

// Enqueue adds an item to the queue
func (q *localQueue) Enqueue(item any) {
	q.data = append(q.data, item)
}

// Dequeue removes and returns the next item
func (q *localQueue) Dequeue() (any, bool) {
	if len(q.data) == 0 {
		return nil, false
	}
	item := q.data[0]
	q.data = q.data[1:]
	return item, true
}

// Size returns the queue size
func (q *localQueue) Size() int {
	return len(q.data)
}

func TestQueueOperations(t *testing.T) {
	q := NewQueue()
	assert.Equal(t, 0, q.Size())

	// Enqueue items
	q.Enqueue("item1")
	q.Enqueue("item2")
	q.Enqueue("item3")

	assert.Equal(t, 3, q.Size())

	// Dequeue items
	item, ok := q.Dequeue()
	assert.True(t, ok)
	assert.Equal(t, "item1", item)
	assert.Equal(t, 2, q.Size())

	item, ok = q.Dequeue()
	assert.True(t, ok)
	assert.Equal(t, "item2", item)
	assert.Equal(t, 1, q.Size())

	item, ok = q.Dequeue()
	assert.True(t, ok)
	assert.Equal(t, "item3", item)
	assert.Equal(t, 0, q.Size())

	// Dequeue from empty queue
	_, ok = q.Dequeue()
	assert.False(t, ok)
}

// Add this test to verify queue shutdown behavior

func TestQueueShutdown(t *testing.T) {
	q := New(2)

	// Start the queue
	var processed int32
	q.Start(func(task any) error {
		// Simulate work
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&processed, 1)
		return nil
	})

	// Submit some tasks
	for i := 0; i < 5; i++ {
		err := q.Submit(i, 1)
		assert.NoError(t, err)
	}

	// Stop the queue
	q.Stop()

	// Verify that submitting after stop returns an error
	err := q.Submit("post-stop", 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")

	// Should not panic when stopping twice
	q.Stop()
}
