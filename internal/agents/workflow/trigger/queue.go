// Package trigger is the dispatch + queue + dedup layer between
// trigger sources (cron, channel adapter, webhook, manual, error) and
// the engine. One Router instance per Engine.
package trigger

import (
	"context"
	"errors"
	"sync"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// ErrQueueFull is returned when overflow=reject and the queue is full.
var ErrQueueFull = errors.New("workflow queue full (reject policy)")

// WorkItem is one queued run request.
//
// Workflow is an optional override: when set, the worker runs THIS
// workflow definition instead of the one registered in
// Router.defs[id]. The UI's Run Now path uses this to execute a
// freshly-loaded DRAFT (workflow.draft.yaml) without first
// publishing it — Router.defs only ever holds the published copy
// so triggers (cron, channel, webhook) keep firing the live
// version while the user iterates on the draft.
type WorkItem struct {
	ID       string
	Event    workflow.Event
	Workflow *workflow.Workflow
	Done     chan RunResult
}

// RunResult is delivered back via WorkItem.Done.
type RunResult struct {
	State workflow.RunState
	Err   error
}

// Queue is a per-workflow FIFO with overflow policy.
type Queue struct {
	mu         sync.Mutex
	cond       *sync.Cond
	items      []WorkItem
	maxSize    int
	onOverflow string
	closed     bool
}

// NewQueue constructs a queue. maxSize=0 means unbounded.
func NewQueue(maxSize int, onOverflow string) *Queue {
	q := &Queue{maxSize: maxSize, onOverflow: onOverflow}
	q.cond = sync.NewCond(&q.mu)
	if q.onOverflow == "" {
		q.onOverflow = workflow.OverflowDropOldest
	}
	return q
}

// Enqueue adds an item.
func (q *Queue) Enqueue(it WorkItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return errors.New("queue closed")
	}
	if q.maxSize > 0 && len(q.items) >= q.maxSize {
		switch q.onOverflow {
		case workflow.OverflowReject:
			return ErrQueueFull
		case workflow.OverflowDropNew:
			return nil
		case workflow.OverflowDropOldest:
			q.items = q.items[1:]
		}
	}
	q.items = append(q.items, it)
	q.cond.Signal()
	return nil
}

// Dequeue blocks until an item is available or ctx cancels.
func (q *Queue) Dequeue(ctx context.Context) (WorkItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				q.cond.Broadcast()
			case <-done:
			}
		}()
		q.cond.Wait()
		close(done)
		if ctx.Err() != nil {
			return WorkItem{}, false
		}
	}
	if q.closed && len(q.items) == 0 {
		return WorkItem{}, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	return it, true
}

// Len returns current queue depth.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Close prevents future enqueues and unblocks Dequeue waiters.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}
