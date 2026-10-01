package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/denyszorinets/ballet/kit/auth"
)

// Close status codes used by Ballet (application range 4000-4999).
const (
	StatusHeartbeatTimeout websocket.StatusCode = 4000
	StatusUnauthenticated  websocket.StatusCode = 4001
)

// ErrClosed is returned by calls on a closed connection.
var ErrClosed = errors.New("rpc: connection closed")

// Handler handles a request or notification from the peer. For requests,
// the returned value becomes the result; an *Error is sent as is, other
// errors as CodeInternal. Return values of notifications are ignored.
//
// Requests are handled concurrently. Notifications are handled one at a
// time, in arrival order (stream events depend on it); a slow notification
// handler applies backpressure to the connection.
type Handler func(ctx context.Context, req *Request) (any, error)

// Request is an incoming request or notification.
type Request struct {
	Method string
	Conn   *Conn
	params []byte
}

// Decode decodes the params into v.
func (r *Request) Decode(v any) error {
	if err := r.Conn.codec.unmarshal(r.params, v); err != nil {
		return Errorf(CodeInvalidParams, "invalid params for %s: %v", r.Method, err)
	}
	return nil
}

// Options configure a connection.
type Options struct {
	// HeartbeatInterval between $/heartbeat notifications (default 15s).
	// Both peers must use the same interval: each closes the connection
	// when the other has been silent for HeartbeatMisses intervals.
	HeartbeatInterval time.Duration
	// HeartbeatMisses: the connection is closed when nothing was received
	// for HeartbeatMisses intervals (default 3).
	HeartbeatMisses int
	// Handler handles the peer's requests and notifications. Without one,
	// requests are answered with CodeMethodNotFound.
	Handler Handler
	// MaxMessageBytes limits inbound frames (default 4 MiB).
	MaxMessageBytes int64
	Logger          *slog.Logger
}

func (o *Options) setDefaults() {
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = 15 * time.Second
	}
	if o.HeartbeatMisses <= 0 {
		o.HeartbeatMisses = 3
	}
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = 4 << 20
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Conn is an open, authenticated connection.
type Conn struct {
	ws    *websocket.Conn
	codec codec
	opts  Options

	ctx    context.Context // lifetime of the connection
	cancel context.CancelFunc

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan message

	lastSeen atomic.Int64 // Unix nanoseconds of the last inbound frame

	authMu   sync.RWMutex
	identity auth.Identity
	expiry   time.Time // zero: no expiry

	// intercept handles reserved methods (auth.refresh on servers) before
	// the user handler. It reports whether it handled the message.
	intercept func(m message) bool

	notifications chan message // processed in order by notifyLoop

	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
}

// notificationQueue is how many notifications may wait for the handler
// before reading from the connection pauses.
const notificationQueue = 1024

func newConn(ws *websocket.Conn, c codec, opts Options) *Conn {
	opts.setDefaults()
	ws.SetReadLimit(opts.MaxMessageBytes)
	ctx, cancel := context.WithCancel(context.Background())
	conn := &Conn{
		ws: ws, codec: c, opts: opts, ctx: ctx, cancel: cancel,
		pending: map[int64]chan message{}, done: make(chan struct{}),
		notifications: make(chan message, notificationQueue),
	}
	conn.lastSeen.Store(time.Now().UnixNano())
	return conn
}

// start launches the read and heartbeat loops.
func (c *Conn) start() {
	go c.readLoop()
	go c.heartbeatLoop()
	go c.notifyLoop()
}

// Subprotocol returns the negotiated subprotocol.
func (c *Conn) Subprotocol() string { return c.ws.Subprotocol() }

// Identity returns the authenticated identity of the peer (servers) or of
// this client as reported by the server.
func (c *Conn) Identity() auth.Identity {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return c.identity
}

func (c *Conn) setIdentity(id auth.Identity, expiry time.Time) {
	c.authMu.Lock()
	c.identity, c.expiry = id, expiry
	c.authMu.Unlock()
}

func (c *Conn) expired(now time.Time) bool {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return !c.expiry.IsZero() && now.After(c.expiry)
}

// Done is closed when the connection is closed.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection closed (nil while open).
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.closeErr
	default:
		return nil
	}
}

// Close closes the connection with a normal closure.
func (c *Conn) Close() error {
	c.closeWith(websocket.StatusNormalClosure, "closed", ErrClosed)
	return nil
}

func (c *Conn) closeWith(status websocket.StatusCode, reason string, err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		c.cancel()
		_ = c.ws.Close(status, reason)
		close(c.done)
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
	})
}

// Call sends a request and decodes the result into result (may be nil).
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	raw, err := c.codec.marshal(params)
	if err != nil {
		return fmt.Errorf("rpc: encode params: %w", err)
	}
	id := c.nextID.Add(1)
	ch := make(chan message, 1)
	c.pendingMu.Lock()
	select {
	case <-c.done:
		c.pendingMu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.send(ctx, message{ID: &id, Method: method, Params: raw}); err != nil {
		c.dropPending(id)
		return err
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		if err := c.codec.unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("rpc: decode result of %s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.dropPending(id)
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

// Notify sends a notification (no response).
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	raw, err := c.codec.marshal(params)
	if err != nil {
		return fmt.Errorf("rpc: encode params: %w", err)
	}
	return c.send(ctx, message{Method: method, Params: raw})
}

func (c *Conn) dropPending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

const writeTimeout = 10 * time.Second

func (c *Conn) send(ctx context.Context, m message) error {
	data, err := c.codec.encode(m)
	if err != nil {
		return fmt.Errorf("rpc: encode message: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if err := c.ws.Write(ctx, c.codec.frameType(), data); err != nil {
		return fmt.Errorf("rpc: write: %w", err)
	}
	return nil
}

func (c *Conn) readLoop() {
	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			c.closeWith(websocket.StatusNormalClosure, "", fmt.Errorf("rpc: read: %w", err))
			return
		}
		c.lastSeen.Store(time.Now().UnixNano())
		m, err := c.codec.decode(data)
		if err != nil {
			c.opts.Logger.Debug("rpc: undecodable message", "error", err)
			continue
		}
		c.dispatch(m)
	}
}

func (c *Conn) dispatch(m message) {
	switch {
	case m.isResponse():
		c.pendingMu.Lock()
		ch, ok := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.pendingMu.Unlock()
		if ok {
			ch <- m
		}
	case m.Method == MethodHeartbeat:
		// Liveness is tracked for every inbound frame.
	case c.intercept != nil && c.intercept(m):
	case m.isRequest():
		go c.handle(m)
	case m.isNotification():
		select {
		case c.notifications <- m:
		case <-c.done:
		}
	}
}

func (c *Conn) notifyLoop() {
	for {
		select {
		case <-c.done:
			return
		case m := <-c.notifications:
			c.handle(m)
		}
	}
}

func (c *Conn) handle(m message) {
	ctx := auth.WithIdentity(c.ctx, c.Identity())
	var result any
	var err error
	if c.opts.Handler == nil {
		err = Errorf(CodeMethodNotFound, "method %s not found", m.Method)
	} else {
		result, err = c.opts.Handler(ctx, &Request{Method: m.Method, Conn: c, params: m.Params})
	}
	if m.isNotification() {
		if err != nil {
			c.opts.Logger.Debug("rpc: notification handler failed", "method", m.Method, "error", err)
		}
		return
	}
	c.respond(m.ID, result, err)
}

func (c *Conn) respond(id *int64, result any, err error) {
	resp := message{ID: id}
	if err != nil {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			c.opts.Logger.Error("rpc: handler failed", "error", err)
			rpcErr = Errorf(CodeInternal, "internal error")
		}
		resp.Error = rpcErr
	} else {
		raw, mErr := c.codec.marshal(result)
		if mErr != nil {
			resp.Error = Errorf(CodeInternal, "encode result: %v", mErr)
		} else {
			resp.Result = raw
		}
	}
	if err := c.send(c.ctx, resp); err != nil && !errors.Is(err, ErrClosed) {
		c.opts.Logger.Debug("rpc: send response failed", "error", err)
	}
}

func (c *Conn) heartbeatLoop() {
	t := time.NewTicker(c.opts.HeartbeatInterval)
	defer t.Stop()
	limit := time.Duration(c.opts.HeartbeatMisses) * c.opts.HeartbeatInterval
	for {
		select {
		case <-c.done:
			return
		case now := <-t.C:
			if now.Sub(time.Unix(0, c.lastSeen.Load())) > limit {
				c.closeWith(StatusHeartbeatTimeout, "heartbeat timeout", errors.New("rpc: heartbeat timeout"))
				return
			}
			if c.expired(now) {
				c.closeWith(StatusUnauthenticated, "token expired", errors.New("rpc: token expired"))
				return
			}
			_ = c.Notify(c.ctx, MethodHeartbeat, Heartbeat{Time: now.UnixMilli()})
		}
	}
}
