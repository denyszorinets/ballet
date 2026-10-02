package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// SchedulerStore finds runnable tickets and the slots flows occupy.
type SchedulerStore interface {
	// RunnableTickets: ready tickets with resolved blockers, in start order.
	RunnableTickets(ctx context.Context) ([]tracker.Item, error)
	// BusyFlows counts flows occupying a slot, per project ID.
	BusyFlows(ctx context.Context) (map[string]int, error)
}

// Scheduler starts the pipelines of runnable tickets without human action,
// within global and per-project concurrency limits.
type Scheduler struct {
	Store SchedulerStore
	Start func(ctx context.Context, it tracker.Item) error // Flows.StartTicket
	// Paused reports whether a project's autonomous work is paused
	// (Control.Paused); optional.
	Paused func(ctx context.Context, projectID string) bool
	// Budget returns the budget a ticket's work ran out of
	// (Budgets.CheckTicket); optional. Such tickets wait.
	Budget func(ctx context.Context, it tracker.Item) (*Exceeded, error)
	Logger *slog.Logger
	// MaxActive and MaxActivePerProject limit the flows occupying a slot
	// (running, or waiting for checks); flows waiting for a human do not.
	MaxActive           int
	MaxActivePerProject int
	Interval            time.Duration // how often to look for runnable tickets

	mu   sync.Mutex
	kick chan struct{}
}

// Tick starts as many runnable tickets as the limits allow and returns how
// many it started.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	busy, err := s.Store.BusyFlows(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, n := range busy {
		total += n
	}
	if total >= s.MaxActive {
		return 0, nil
	}
	tickets, err := s.Store.RunnableTickets(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, it := range tickets {
		if total >= s.MaxActive {
			break
		}
		if busy[it.ProjectID] >= s.MaxActivePerProject || (s.Paused != nil && s.Paused(ctx, it.ProjectID)) {
			continue
		}
		if s.Budget != nil {
			if ex, err := s.Budget(ctx, it); err != nil || ex != nil {
				continue
			}
		}
		if err := s.Start(ctx, it); err != nil {
			// A human started or moved the ticket meanwhile: not an error.
			if !errors.Is(err, ErrConflict) && s.Logger != nil {
				s.Logger.WarnContext(ctx, "scheduler could not start ticket", "ticket", it.Key, "error", err)
			}
			continue
		}
		busy[it.ProjectID]++
		total++
		started++
		if s.Logger != nil {
			s.Logger.InfoContext(ctx, "scheduler started ticket", "ticket", it.Key)
		}
	}
	return started, nil
}

// Kick asks the scheduler to look for runnable tickets now.
func (s *Scheduler) Kick() {
	select {
	case s.kicks() <- struct{}{}:
	default:
	}
}

func (s *Scheduler) kicks() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kick == nil {
		s.kick = make(chan struct{}, 1)
	}
	return s.kick
}

// Run schedules until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	kick := s.kicks()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil && s.Logger != nil {
			s.Logger.WarnContext(ctx, "scheduling failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-kick:
		case <-time.After(s.Interval):
		}
	}
}
