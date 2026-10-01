// Command rpc-testserver runs a kit/rpc server with fake authentication and
// an in-memory event stream. Client implementations (the web UI's
// TypeScript client) use it as an integration test peer. Not for
// production.
//
// Tokens: "valid:<subject>" never expires; "short:<subject>" expires after
// -short-ttl. Methods: echo, test.emit {count}, test.close, test.append
// {count}, stream.subscribe {stream, from_seq}, stream.unsubscribe.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/rpc"
)

type streamEvent struct {
	Subscription string `json:"subscription"`
	Seq          int64  `json:"seq"`
	Type         string `json:"type"`
	EntityKey    string `json:"entity_key"`
}

type sub struct {
	id   string
	conn *rpc.Conn
}

type eventLog struct {
	mu        sync.Mutex
	seq       int64
	maxReplay int64
	subs      map[string]sub
}

func (l *eventLog) append(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for range n {
		l.seq++
		for _, s := range l.subs {
			_ = s.conn.Notify(context.Background(), "stream.event", streamEvent{
				Subscription: s.id, Seq: l.seq, Type: "item.created", EntityKey: fmt.Sprintf("T-%d", l.seq),
			})
		}
	}
}

// subscribe replays before answering, like Core: events may reach the
// client before the response, which is why clients choose the ID.
func (l *eventLog) subscribe(c *rpc.Conn, id string, fromSeq *int64) (map[string]any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		return nil, rpc.Errorf(rpc.CodeInvalidParams, "subscription ID required")
	}
	if fromSeq != nil && l.seq-*fromSeq > l.maxReplay {
		return nil, rpc.Errorf(-32010, "resync required")
	}
	key := fmt.Sprintf("%p/%s", c, id)
	if fromSeq != nil {
		for s := *fromSeq + 1; s <= l.seq; s++ {
			_ = c.Notify(context.Background(), "stream.event", streamEvent{
				Subscription: id, Seq: s, Type: "item.created", EntityKey: fmt.Sprintf("T-%d", s),
			})
		}
	}
	l.subs[key] = sub{id: id, conn: c}
	go func() {
		<-c.Done()
		l.mu.Lock()
		delete(l.subs, key)
		l.mu.Unlock()
	}()
	return map[string]any{"subscription": id, "seq": l.seq}, nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	heartbeat := flag.Duration("heartbeat", 200*time.Millisecond, "heartbeat interval")
	shortTTL := flag.Duration("short-ttl", 2*time.Second, "lifetime of short:<subject> tokens")
	maxReplay := flag.Int64("max-replay", 50, "events replayable on resume")
	flag.Parse()

	log := &eventLog{maxReplay: *maxReplay, subs: map[string]sub{}}
	authenticate := func(_ context.Context, token string) (auth.Identity, time.Time, error) {
		if s, ok := strings.CutPrefix(token, "short:"); ok {
			return auth.Identity{Kind: auth.KindHuman, Subject: s}, time.Now().Add(*shortTTL), nil
		}
		if s, ok := strings.CutPrefix(token, "valid:"); ok {
			return auth.Identity{Kind: auth.KindHuman, Subject: s}, time.Time{}, nil
		}
		return auth.Identity{}, time.Time{}, errors.New("bad token")
	}
	handler := func(ctx context.Context, req *rpc.Request) (any, error) {
		switch req.Method {
		case "echo":
			var v any
			if err := req.Decode(&v); err != nil {
				return nil, err
			}
			return v, nil
		case "whoami":
			id, _ := auth.FromContext(ctx)
			return id.Subject, nil
		case "test.emit":
			var p struct {
				Count int `json:"count"`
			}
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			go func() {
				for i := range p.Count {
					_ = req.Conn.Notify(context.Background(), "test.event", map[string]int{"i": i})
				}
			}()
			return struct{}{}, nil
		case "test.close":
			go func() { time.Sleep(20 * time.Millisecond); _ = req.Conn.Close() }()
			return struct{}{}, nil
		case "test.append":
			var p struct {
				Count int `json:"count"`
			}
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			log.append(p.Count)
			return struct{}{}, nil
		case "stream.subscribe":
			var p struct {
				Subscription string `json:"subscription"`
				Stream       string `json:"stream"`
				FromSeq      *int64 `json:"from_seq"`
			}
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			return log.subscribe(req.Conn, p.Subscription, p.FromSeq)
		case "stream.unsubscribe":
			return struct{}{}, nil
		}
		return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
	}

	srv := rpc.NewServer(rpc.ServerOptions{
		Options:      rpc.Options{Handler: handler, HeartbeatInterval: *heartbeat, Logger: slog.New(slog.DiscardHandler)},
		Authenticate: authenticate,
	})
	mux := http.NewServeMux()
	mux.Handle("/rpc", srv)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("listening %s\n", ln.Addr())
	if err := http.Serve(ln, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
