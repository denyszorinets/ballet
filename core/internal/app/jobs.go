package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// JobStatus is where a job stands.
type JobStatus string

// Job statuses.
const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobDead    JobStatus = "dead" // failed MaxAttempts times
)

// Job is a durable unit of work (ADR-0016).
type Job struct {
	ID          string
	Kind        string
	DedupeKey   string // optional: at most one pending or running job per key
	Payload     json.RawMessage
	Status      JobStatus
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LeaseUntil  time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

// NewJob builds a pending job of kind with payload (marshalled to JSON).
func NewJob(id, kind, dedupeKey string, payload any, runAt time.Time, maxAttempts int) Job {
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	return Job{ID: id, Kind: kind, DedupeKey: dedupeKey, Payload: mustJSON(payload), Status: JobPending, RunAt: runAt,
		MaxAttempts: maxAttempts, CreatedAt: runAt, UpdatedAt: runAt, Version: 1}
}

// JobStore persists jobs.
type JobStore interface {
	// EnqueueJobs inserts jobs; a job whose dedupe key is already live is
	// skipped.
	EnqueueJobs(ctx context.Context, jobs ...Job) error
	// DueJobs returns pending jobs due by now and running jobs whose lease
	// expired, oldest first.
	DueJobs(ctx context.Context, now time.Time, limit int) ([]Job, error)
	// UpdateJob stores j if it is at expectedVersion (ErrConflict otherwise).
	UpdateJob(ctx context.Context, j Job, expectedVersion int64) error
	Job(ctx context.Context, id string) (Job, error)
}

// JobHandler executes one job; an error retries it later.
type JobHandler func(ctx context.Context, j Job) error

// ErrPermanent wraps errors that must not be retried.
var ErrPermanent = errors.New("permanent failure")

// Orchestrator claims due jobs and runs their handlers (one instance runs
// per installation; ADR-0016).
type Orchestrator struct {
	Store       JobStore
	Now         func() time.Time
	Logger      *slog.Logger
	Interval    time.Duration // polling period; default 1 s
	Lease       time.Duration // how long a claim lasts; default 5 min
	Concurrency int           // jobs at a time; default 8
	// Backoff before retry n (1-based); default 2^n seconds, at most 10 min.
	Backoff func(attempt int) time.Duration
	// OnFinished observes job results (metrics); optional.
	OnFinished func(kind string, result string)

	mu       sync.Mutex
	handlers map[string]JobHandler
	kick     chan struct{}
}

// Handle registers the handler of a job kind.
func (o *Orchestrator) Handle(kind string, h JobHandler) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.handlers == nil {
		o.handlers = map[string]JobHandler{}
	}
	o.handlers[kind] = h
}

// Kick asks the orchestrator to look for due jobs now.
func (o *Orchestrator) Kick() {
	o.mu.Lock()
	if o.kick == nil {
		o.kick = make(chan struct{}, 1)
	}
	k := o.kick
	o.mu.Unlock()
	select {
	case k <- struct{}{}:
	default:
	}
}

// Enqueue stores jobs and kicks the orchestrator.
func (o *Orchestrator) Enqueue(ctx context.Context, jobs ...Job) error {
	if err := o.Store.EnqueueJobs(ctx, jobs...); err != nil {
		return err
	}
	o.Kick()
	return nil
}

// Run executes due jobs until ctx ends, then waits for running handlers.
func (o *Orchestrator) Run(ctx context.Context) {
	o.mu.Lock()
	if o.kick == nil {
		o.kick = make(chan struct{}, 1)
	}
	kick := o.kick
	o.mu.Unlock()
	interval := o.Interval
	if interval <= 0 {
		interval = time.Second
	}
	conc := o.Concurrency
	if conc <= 0 {
		conc = 8
	}
	slots := make(chan struct{}, conc)
	var wg sync.WaitGroup
	defer wg.Wait()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		free := conc - len(slots)
		if free > 0 {
			due, err := o.Store.DueJobs(ctx, o.Now(), free)
			if err != nil && ctx.Err() == nil {
				o.logger().ErrorContext(ctx, "list due jobs failed", "error", err)
			}
			for _, j := range due {
				claimed, ok := o.claim(ctx, j)
				if !ok {
					continue
				}
				slots <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() { <-slots; wg.Done() }()
					o.execute(ctx, claimed)
				}()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-t.C:
		}
	}
}

func (o *Orchestrator) claim(ctx context.Context, j Job) (Job, bool) {
	lease := o.Lease
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	now := o.Now()
	c := j
	c.Status, c.Attempts, c.LeaseUntil, c.UpdatedAt, c.Version = JobRunning, j.Attempts+1, now.Add(lease), now, j.Version+1
	if err := o.Store.UpdateJob(ctx, c, j.Version); err != nil {
		if !errors.Is(err, ErrConflict) {
			o.logger().ErrorContext(ctx, "claim job failed", "job", j.ID, "error", err)
		}
		return Job{}, false
	}
	return c, true
}

func (o *Orchestrator) execute(ctx context.Context, j Job) {
	o.mu.Lock()
	h := o.handlers[j.Kind]
	o.mu.Unlock()
	var err error
	if h == nil {
		err = fmt.Errorf("%w: no handler for job kind %q", ErrPermanent, j.Kind)
	} else {
		err = safely(ctx, h, j)
	}
	if ctx.Err() != nil {
		return // shutting down: the lease expires and the job runs again
	}
	now := o.Now()
	next := j
	next.UpdatedAt, next.Version, next.LeaseUntil = now, j.Version+1, time.Time{}
	result := "done"
	switch {
	case err == nil:
		next.Status, next.LastError = JobDone, ""
	case errors.Is(err, ErrPermanent) || j.Attempts >= j.MaxAttempts:
		next.Status, next.LastError, result = JobDead, err.Error(), "dead"
		o.logger().ErrorContext(ctx, "job failed for good", "job", j.ID, "kind", j.Kind, "attempts", j.Attempts, "error", err)
	default:
		next.Status, next.LastError, next.RunAt, result = JobPending, err.Error(), now.Add(o.backoff(j.Attempts)), "retry"
		o.logger().WarnContext(ctx, "job failed; will retry", "job", j.ID, "kind", j.Kind, "attempt", j.Attempts, "error", err)
	}
	if err := o.Store.UpdateJob(context.WithoutCancel(ctx), next, j.Version); err != nil {
		o.logger().ErrorContext(ctx, "record job result failed", "job", j.ID, "error", err)
	}
	if o.OnFinished != nil {
		o.OnFinished(j.Kind, result)
	}
}

func safely(ctx context.Context, h JobHandler, j Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("job handler panicked: %v", r)
		}
	}()
	return h(ctx, j)
}

func (o *Orchestrator) backoff(attempt int) time.Duration {
	if o.Backoff != nil {
		return o.Backoff(attempt)
	}
	d := time.Duration(1<<min(attempt, 10)) * time.Second
	return min(d, 10*time.Minute)
}

func (o *Orchestrator) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}
