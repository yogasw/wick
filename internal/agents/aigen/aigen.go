// Package aigen runs one-shot "generate this for me" LLM calls — a system
// prompt from a brief, a connector definition from a pasted snippet — as
// queued jobs instead of synchronous request handlers.
//
// Every job borrows a slot from the agent pool before it forks a CLI, so a
// generate button can never push the machine past MaxConcurrent the way a
// direct provider call inside an HTTP handler could. The caller gets a job
// id back immediately and polls it through queued (with its position) →
// working → done / failed / canceled.
//
// A job runs as the user who submitted it and only that user can read or
// cancel it. The call itself is a bare structured completion: no MCP
// config, no tools, no workspace — it can reach nothing its owner could
// not, because it can reach nothing at all.
//
// Results live in memory with a TTL. They are drafts the UI shows for a
// person to accept or discard within seconds; surviving a restart would
// buy nothing (the browser polling for them is gone too), so no table.
package aigen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

// Status is where a job is in its lifecycle.
type Status string

const (
	StatusQueued   Status = "queued"
	StatusWorking  Status = "working"
	StatusDone     Status = "done"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// Finished reports whether the job will not change any more.
func (s Status) Finished() bool {
	return s == StatusDone || s == StatusFailed || s == StatusCanceled
}

var (
	// ErrNotFound covers both "no such job" and "not your job" so a
	// caller cannot probe for other people's job ids.
	ErrNotFound = errors.New("job not found")
	// ErrUnknownKind is returned by Submit for an unregistered kind.
	ErrUnknownKind = errors.New("unknown generate kind")
	// ErrBusy is returned when the user already has MaxPerUser jobs
	// waiting or running.
	ErrBusy = errors.New("you already have generate jobs waiting — let them finish or cancel one")
	// ErrNoProvider means no structured-output provider can serve the job.
	ErrNoProvider = errors.New("no AI provider with structured output is configured")
)

// Input is what the caller sends for one job. Text is the brief or paste;
// Fields carries kind-specific context (the current value to improve, a
// name already typed, …). Provider names an instance; "" = the default.
type Input struct {
	Text     string            `json:"text"`
	Provider string            `json:"provider,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// Kind is one thing the service knows how to generate. Consumers register
// their kinds at boot; the service itself knows no prompts.
type Kind struct {
	Name string
	// MaxInput caps len(Input.Text) in bytes. 0 = 8 KB.
	MaxInput int
	// Validate rejects an input before it is queued (optional), so a
	// mistake fails at the button rather than after a wait in line.
	Validate func(in Input) error
	// Build turns the input into the one structured call to make.
	Build func(in Input) (wfprovider.StructuredRequest, error)
	// Finish shapes the provider's answer into the job result. A returned
	// error fails the job with that message.
	Finish func(res wfprovider.StructuredResult) (any, error)
}

// Gate lends pool slots. *pool.Pool satisfies it via TryLease.
type Gate interface {
	TryLease(key, pType, pName string) (release func(), ok bool)
}

// GateFunc adapts a function to Gate.
type GateFunc func(key, pType, pName string) (func(), bool)

// TryLease implements Gate.
func (f GateFunc) TryLease(key, pType, pName string) (func(), bool) { return f(key, pType, pName) }

// Resolved is the provider a job will run on and the pool identity its
// slot is charged to.
type Resolved struct {
	Provider wfprovider.Provider
	Type     string
	Name     string
}

// Resolver picks the provider for userID; name "" = the default.
type Resolver func(ctx context.Context, userID, name string) (Resolved, error)

// Config knobs. Zero values get sane defaults in New.
type Config struct {
	Gate    Gate
	Resolve Resolver
	// Workers is how many jobs may hold a slot at once. Default 1: the
	// generate path is a convenience and should never crowd sessions.
	Workers int
	// RunTimeout bounds one provider call. Default 2m.
	RunTimeout time.Duration
	// QueueTimeout bounds the wait for a slot. Default 5m.
	QueueTimeout time.Duration
	// ResultTTL is how long a finished job stays readable. Default 10m.
	ResultTTL time.Duration
	// MaxPerUser caps one user's unfinished jobs. Default 3.
	MaxPerUser int
	// RetryEvery is how often a waiting job retries the gate. Default 1s.
	RetryEvery time.Duration
}

// Job is the read model returned to callers.
type Job struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Status     Status     `json:"status"`
	Position   int        `json:"position,omitempty"`
	Provider   string     `json:"provider,omitempty"`
	Result     any        `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type job struct {
	Job
	owner   string
	input   Input
	kind    Kind
	prov    Resolved
	claimed bool               // a worker is waiting on the gate for it
	cancel  context.CancelFunc // set while working
}

// Service queues and runs generate jobs.
type Service struct {
	cfg Config

	mu    sync.Mutex
	kinds map[string]Kind
	jobs  map[string]*job
	queue []*job // unfinished, not yet working, FIFO
	wake  chan struct{}
	stop  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
	now   func() time.Time
}

// New builds a service. Workers start lazily on the first Submit.
func New(cfg Config) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.RunTimeout <= 0 {
		cfg.RunTimeout = 2 * time.Minute
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 5 * time.Minute
	}
	if cfg.ResultTTL <= 0 {
		cfg.ResultTTL = 10 * time.Minute
	}
	if cfg.MaxPerUser <= 0 {
		cfg.MaxPerUser = 3
	}
	if cfg.RetryEvery <= 0 {
		cfg.RetryEvery = time.Second
	}
	return &Service{
		cfg:   cfg,
		kinds: map[string]Kind{},
		jobs:  map[string]*job{},
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		now:   time.Now,
	}
}

// Register adds (or replaces) a kind.
func (s *Service) Register(k Kind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kinds[k.Name] = k
}

// HasKind reports whether name is registered.
func (s *Service) HasKind(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.kinds[name]
	return ok
}

// Close stops the workers. Waiting jobs fail; a running call is canceled.
func (s *Service) Close() {
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		return
	default:
	}
	close(s.stop)
	for _, j := range s.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Submit validates and queues one job owned by userID.
func (s *Service) Submit(ctx context.Context, userID, kindName string, in Input) (Job, error) {
	if userID == "" {
		return Job{}, errors.New("generate needs a signed-in user")
	}
	s.mu.Lock()
	k, ok := s.kinds[kindName]
	s.mu.Unlock()
	if !ok {
		return Job{}, fmt.Errorf("%w %q", ErrUnknownKind, kindName)
	}
	maxIn := k.MaxInput
	if maxIn <= 0 {
		maxIn = 8 * 1024
	}
	if len(in.Text) > maxIn {
		return Job{}, fmt.Errorf("input is larger than %d KB — trim it down", maxIn/1024)
	}
	if k.Validate != nil {
		if err := k.Validate(in); err != nil {
			return Job{}, err
		}
	}
	if s.cfg.Resolve == nil {
		return Job{}, ErrNoProvider
	}
	prov, err := s.cfg.Resolve(ctx, userID, in.Provider)
	if err != nil {
		return Job{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	pending := 0
	for _, j := range s.jobs {
		if j.owner == userID && !j.Status.Finished() {
			pending++
		}
	}
	if pending >= s.cfg.MaxPerUser {
		return Job{}, ErrBusy
	}
	j := &job{
		Job: Job{
			ID:        newID(),
			Kind:      kindName,
			Status:    StatusQueued,
			Provider:  prov.Name,
			CreatedAt: s.now(),
		},
		owner: userID,
		input: in,
		kind:  k,
		prov:  prov,
	}
	s.jobs[j.ID] = j
	s.queue = append(s.queue, j)
	s.once.Do(s.startLocked)
	s.signal()
	return s.snapshotLocked(j), nil
}

// Get returns userID's job id.
func (s *Service) Get(userID, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	j, ok := s.jobs[id]
	if !ok || j.owner != userID {
		return Job{}, ErrNotFound
	}
	return s.snapshotLocked(j), nil
}

// Cancel stops userID's job id. Canceling a finished job is a no-op that
// returns its final state.
func (s *Service) Cancel(userID, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.owner != userID {
		return Job{}, ErrNotFound
	}
	if !j.Status.Finished() {
		if j.cancel != nil {
			j.cancel()
		}
		s.removeQueuedLocked(j)
		s.finishLocked(j, StatusCanceled, nil, "canceled")
	}
	return s.snapshotLocked(j), nil
}

func (s *Service) startLocked() {
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

// signal nudges idle workers; never blocks.
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		j := s.claim()
		if j == nil {
			select {
			case <-s.stop:
				return
			case <-s.wake:
			}
			continue
		}
		s.run(j)
	}
}

// claim takes the oldest unclaimed queued job.
func (s *Service) claim() *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.queue {
		if !j.claimed {
			j.claimed = true
			return j
		}
	}
	return nil
}

// run waits for a slot, then makes the call. The job stays in the queue
// (keeping its position visible) until the slot is granted.
func (s *Service) run(j *job) {
	release, ok := s.acquire(j)
	if !ok {
		return
	}
	defer release()

	s.mu.Lock()
	if j.Status.Finished() { // canceled between grant and start
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.RunTimeout)
	defer cancel()
	j.cancel = cancel
	s.removeQueuedLocked(j)
	now := s.now()
	j.Status = StatusWorking
	j.StartedAt = &now
	in, k, prov := j.input, j.kind, j.prov
	s.mu.Unlock()
	// The next waiter may now be first in line; let it re-check the gate.
	s.signal()

	result, err := call(ctx, k, prov.Provider, in)
	s.mu.Lock()
	defer s.mu.Unlock()
	j.cancel = nil
	if j.Status.Finished() { // canceled while working: drop the answer
		return
	}
	switch {
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		s.finishLocked(j, StatusFailed, nil, fmt.Sprintf("timed out after %s — try again or shorten the input", s.cfg.RunTimeout))
	case err != nil:
		s.finishLocked(j, StatusFailed, nil, err.Error())
	default:
		s.finishLocked(j, StatusDone, result, "")
	}
}

// acquire loops on the gate until it lends a slot, the job is canceled,
// the wait times out, or the service stops.
func (s *Service) acquire(j *job) (func(), bool) {
	deadline := s.now().Add(s.cfg.QueueTimeout)
	t := time.NewTicker(s.cfg.RetryEvery)
	defer t.Stop()
	for {
		s.mu.Lock()
		if j.Status.Finished() {
			s.mu.Unlock()
			return nil, false
		}
		// Only the head of the line asks for a slot, so a later job held
		// by another worker can never overtake it.
		head := len(s.queue) > 0 && s.queue[0] == j
		s.mu.Unlock()
		if head {
			if s.cfg.Gate == nil {
				return func() {}, true
			}
			if rel, ok := s.cfg.Gate.TryLease("aigen:"+j.ID, j.prov.Type, j.prov.Name); ok {
				return rel, true
			}
		}
		if s.now().After(deadline) {
			s.mu.Lock()
			if !j.Status.Finished() {
				s.removeQueuedLocked(j)
				s.finishLocked(j, StatusFailed, nil, "no free agent slot — every slot stayed busy, try again later")
			}
			s.mu.Unlock()
			return nil, false
		}
		select {
		case <-s.stop:
			s.mu.Lock()
			if !j.Status.Finished() {
				s.removeQueuedLocked(j)
				s.finishLocked(j, StatusFailed, nil, "wick is shutting down")
			}
			s.mu.Unlock()
			return nil, false
		case <-t.C:
		}
	}
}

func call(ctx context.Context, k Kind, prov wfprovider.Provider, in Input) (any, error) {
	if prov == nil {
		return nil, ErrNoProvider
	}
	req, err := k.Build(in)
	if err != nil {
		return nil, err
	}
	res, err := prov.StructuredCall(ctx, req)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if k.Finish == nil {
		if !res.OK {
			return nil, errors.New(res.Error)
		}
		return res.Parsed, nil
	}
	return k.Finish(res)
}

func (s *Service) finishLocked(j *job, st Status, result any, msg string) {
	now := s.now()
	j.Status = st
	j.Result = result
	j.Error = msg
	j.FinishedAt = &now
}

func (s *Service) removeQueuedLocked(j *job) {
	for i, q := range s.queue {
		if q == j {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}

func (s *Service) snapshotLocked(j *job) Job {
	out := j.Job
	out.Position = 0
	if j.Status == StatusQueued {
		for i, q := range s.queue {
			if q == j {
				out.Position = i + 1
				break
			}
		}
	}
	return out
}

// pruneLocked drops finished jobs past their TTL.
func (s *Service) pruneLocked() {
	cutoff := s.now().Add(-s.cfg.ResultTTL)
	for id, j := range s.jobs {
		if j.Status.Finished() && j.FinishedAt != nil && j.FinishedAt.Before(cutoff) {
			delete(s.jobs, id)
		}
	}
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "gen_" + hex.EncodeToString(b[:])
}
