package teamlink

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
)

// genStore is a taskstore.Store that forgets old tasks. a2a-go's
// in-memory store has no delete, so tasks are kept in two generations:
// new tasks go to cur, and rotate drops prev and demotes cur. A task
// therefore lives between one and two rotation periods — long enough for
// any turn to finish and be read, short enough that a daemon running for
// weeks does not hold every task it ever ran.
type genStore struct {
	mu        sync.RWMutex
	cur, prev *taskstore.InMemory
	rotated   time.Time
}

var _ taskstore.Store = (*genStore)(nil)

func newGenStore(now time.Time) *genStore {
	return &genStore{cur: newMemStore(), prev: newMemStore(), rotated: now}
}

// newMemStore matches the store a2asrv.NewHandler would build itself.
func newMemStore() *taskstore.InMemory {
	return taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{Authenticator: a2asrv.NewTaskStoreAuthenticator()})
}

// rotate drops the older generation when every is up.
func (s *genStore) rotate(now time.Time, every time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.rotated) < every {
		return
	}
	s.prev, s.cur, s.rotated = s.cur, newMemStore(), now
}

// holder returns the generation holding id, cur when neither does.
func (s *genStore) holder(ctx context.Context, id a2a.TaskID) *taskstore.InMemory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.prev.Get(ctx, id); err == nil {
		return s.prev
	}
	return s.cur
}

func (s *genStore) Create(ctx context.Context, task *a2a.Task) (taskstore.TaskVersion, error) {
	s.mu.RLock()
	cur := s.cur
	s.mu.RUnlock()
	return cur.Create(ctx, task)
}

func (s *genStore) Update(ctx context.Context, req *taskstore.UpdateRequest) (taskstore.TaskVersion, error) {
	if req == nil || req.Task == nil {
		return s.current().Update(ctx, req)
	}
	return s.holder(ctx, req.Task.ID).Update(ctx, req)
}

func (s *genStore) Get(ctx context.Context, id a2a.TaskID) (*taskstore.StoredTask, error) {
	t, err := s.current().Get(ctx, id)
	if err == nil || !errors.Is(err, a2a.ErrTaskNotFound) {
		return t, err
	}
	s.mu.RLock()
	prev := s.prev
	s.mu.RUnlock()
	return prev.Get(ctx, id)
}

// List covers the current generation only; nothing in wick lists tasks.
func (s *genStore) List(ctx context.Context, req *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return s.current().List(ctx, req)
}

func (s *genStore) current() *taskstore.InMemory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}
