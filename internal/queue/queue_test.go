package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Actual implementation of Queue for testing
type localQueue struct {
	data []interface{}
}

// NewQueue creates a new queue
func NewQueue() *localQueue {
	return &localQueue{
		data: make([]interface{}, 0),
	}
}

// Enqueue adds an item to the queue
func (q *localQueue) Enqueue(item interface{}) {
	q.data = append(q.data, item)
}

// Dequeue removes and returns the next item
func (q *localQueue) Dequeue() (interface{}, bool) {
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
