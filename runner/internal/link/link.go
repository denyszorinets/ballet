// Package link connects a Runner to Core (kit/runnerproto): it dials with
// the runner token, reconnects with backoff, executes the runs Core sends
// through a Backend, streams their output and reports their end.
package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// Backend executes one run's session. out receives the session's output;
// the exit code is the session's (meaningless when err is not nil).
type Backend interface {
	Run(ctx context.Context, runID string, spec runnerproto.Spec, out func(stream, text string)) (int, error)
}

// Runner keeps a Runner connected to Core and executes its runs.
type Runner struct {
	URL            string // ws(s)://<core>/runner/rpc
	Token          func(ctx context.Context) (string, error)
	Name           string
	Labels         map[string]string
	Capacity       int
	Backend        Backend
	DefaultTimeout time.Duration // per run, when the spec sets none (default 2h)
	Logger         *slog.Logger
	// FlushInterval batches output before sending it (default 200 ms).
	FlushInterval time.Duration

	mu      sync.Mutex
	conn    *rpc.Conn
	runs    map[string]context.CancelFunc
	pending []runnerproto.Finished // results not yet delivered
	wg      sync.WaitGroup
}

// Run keeps the Runner connected until ctx ends, then cancels its runs and
// waits for them.
func (r *Runner) Run(ctx context.Context) error {
	if r.Name == "" || r.Capacity < 1 || r.Backend == nil {
		return errors.New("link: a runner needs a name, a capacity and a backend")
	}
	r.mu.Lock()
	r.runs = map[string]context.CancelFunc{}
	r.mu.Unlock()
	defer r.wg.Wait()
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		started := time.Now()
		err := r.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if time.Since(started) > time.Minute {
			backoff = 500 * time.Millisecond
		}
		r.logger().WarnContext(ctx, "disconnected from Core; reconnecting", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff + time.Duration(rand.Int64N(int64(backoff/2)))):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	r.mu.Lock()
	for _, cancel := range r.runs {
		cancel()
	}
	r.mu.Unlock()
	return nil
}

// session runs one connection until it ends.
func (r *Runner) session(ctx context.Context) error {
	conn, _, err := rpc.Dial(ctx, r.URL, rpc.DialOptions{
		Token:   r.Token,
		Options: rpc.Options{Handler: r.handle(ctx), Logger: r.Logger},
	})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	r.mu.Lock()
	// Runs whose result is not delivered yet are still ours: Core fails
	// runs a reconnecting Runner does not claim.
	active := make([]string, 0, len(r.runs)+len(r.pending))
	for id := range r.runs {
		active = append(active, id)
	}
	for _, f := range r.pending {
		active = append(active, f.Run)
	}
	sort.Strings(active)
	r.mu.Unlock()
	if err := conn.Call(ctx, runnerproto.MethodHello, runnerproto.Hello{
		Runner: r.Name, Labels: r.Labels, Capacity: r.Capacity, Active: active,
	}, nil); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	r.mu.Lock()
	r.conn = conn
	pending := r.pending
	r.pending = nil
	r.mu.Unlock()
	r.logger().InfoContext(ctx, "connected to Core", "runner", r.Name, "active", len(active))
	for _, f := range pending {
		r.finish(ctx, f)
	}
	select {
	case <-ctx.Done():
	case <-conn.Done():
	}
	r.mu.Lock()
	if r.conn == conn {
		r.conn = nil
	}
	r.mu.Unlock()
	return conn.Err()
}

// handle answers Core's requests on a connection.
func (r *Runner) handle(ctx context.Context) rpc.Handler {
	return func(_ context.Context, req *rpc.Request) (any, error) {
		switch req.Method {
		case runnerproto.MethodStart:
			var p runnerproto.Start
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			return struct{}{}, r.start(ctx, p)
		case runnerproto.MethodCancel:
			var p runnerproto.Cancel
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			r.mu.Lock()
			cancel, ok := r.runs[p.Run]
			r.mu.Unlock()
			if !ok {
				return nil, rpc.Errorf(rpc.CodeNotFound, "run %s is not running here", p.Run)
			}
			cancel()
			return struct{}{}, nil
		}
		return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
	}
}

func (r *Runner) start(ctx context.Context, p runnerproto.Start) error {
	timeout := r.DefaultTimeout
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	if p.Spec.TimeoutSeconds > 0 {
		timeout = time.Duration(p.Spec.TimeoutSeconds) * time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.runs[p.Run]; dup {
		return rpc.Errorf(rpc.CodeConflict, "run %s is already running", p.Run)
	}
	if len(r.runs) >= r.Capacity {
		return rpc.Errorf(rpc.CodeConflict, "runner is at capacity")
	}
	runCtx, cancel := context.WithCancel(ctx)
	runCtx, cancelTimeout := context.WithTimeout(runCtx, timeout)
	r.runs[p.Run] = cancel
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer cancelTimeout()
		r.execute(runCtx, ctx, p)
		r.mu.Lock()
		delete(r.runs, p.Run)
		r.mu.Unlock()
		cancel()
	}()
	return nil
}

// execute runs a session and reports it; reports outlive the run's
// context (runCtx) but not the Runner's (ctx).
func (r *Runner) execute(runCtx, ctx context.Context, p runnerproto.Start) {
	r.call(ctx, runnerproto.MethodStatus, runnerproto.Status{Run: p.Run, Status: "running"})
	out := newBuffer(func(stream, text string) {
		// A request, not a notification: Core handles requests and
		// notifications separately, and output must reach Core before
		// run.finished does.
		if err := r.call(ctx, runnerproto.MethodLog, runnerproto.Log{Run: p.Run, Stream: stream, Text: text}); err != nil {
			r.logger().DebugContext(ctx, "run output not delivered", "run", p.Run, "error", err)
		}
	})
	stop := out.flushEvery(r.flushInterval())
	code, err := r.Backend.Run(runCtx, p.Run, p.Spec, out.write)
	stop()
	f := runnerproto.Finished{Run: p.Run, ExitCode: code}
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		f.Error = "the run timed out"
	case runCtx.Err() != nil && ctx.Err() == nil:
		f.Cancelled = true
	case err != nil:
		f.Error = err.Error()
	}
	r.finish(ctx, f)
}

// finish delivers a result, or keeps it for the next connection.
func (r *Runner) finish(ctx context.Context, f runnerproto.Finished) {
	if err := r.call(ctx, runnerproto.MethodFinished, f); err != nil {
		var rerr *rpc.Error
		if errors.As(err, &rerr) {
			r.logger().WarnContext(ctx, "Core rejected the run result", "run", f.Run, "error", err)
			return
		}
		r.mu.Lock()
		r.pending = append(r.pending, f)
		r.mu.Unlock()
	}
}

func (r *Runner) current() *rpc.Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conn
}

func (r *Runner) call(ctx context.Context, method string, params any) error {
	c := r.current()
	if c == nil {
		return errors.New("not connected")
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.Call(callCtx, method, params, nil)
}

func (r *Runner) flushInterval() time.Duration {
	if r.FlushInterval > 0 {
		return r.FlushInterval
	}
	return 200 * time.Millisecond
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// buffer batches output per stream, flushing periodically, when a stream
// changes, or past maxChunk bytes.
type buffer struct {
	mu     sync.Mutex
	send   func(stream, text string)
	stream string
	text   strings.Builder
}

const maxChunk = 16 << 10

func newBuffer(send func(stream, text string)) *buffer { return &buffer{send: send} }

func (b *buffer) write(stream, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stream != stream {
		b.flushLocked()
		b.stream = stream
	}
	b.text.WriteString(text)
	if b.text.Len() >= maxChunk {
		b.flushLocked()
	}
}

func (b *buffer) flushLocked() {
	if b.text.Len() > 0 {
		b.send(b.stream, b.text.String())
		b.text.Reset()
	}
}

// flushEvery flushes periodically until the returned stop function is
// called, which flushes what is left.
func (b *buffer) flushEvery(d time.Duration) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				b.mu.Lock()
				b.flushLocked()
				b.mu.Unlock()
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
		b.mu.Lock()
		b.flushLocked()
		b.mu.Unlock()
	}
}
