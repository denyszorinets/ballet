// Package runnerapi serves the agent protocol (kit/runnerproto): agents
// connect over WebSocket with their token, introduce themselves, receive
// runs and report on them (ADR-0025).
package runnerapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/agent"
	"github.com/denyszorinets/ballet/core/internal/domain/run"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
	"github.com/denyszorinets/ballet/kit/rpc"
	"github.com/denyszorinets/ballet/kit/runnerproto"
)

// Deps are the collaborators of the Runner API.
type Deps struct {
	Verifier   *runtoken.Verifier // Core's own tokens
	Dispatcher *app.Dispatcher
	Options    rpc.Options
}

// Register mounts the Runner API on mux at runnerproto.Path.
func Register(mux *http.ServeMux, d Deps) {
	h := &handlers{deps: d}
	opts := d.Options
	opts.Handler = h.handle
	mux.Handle("GET "+runnerproto.Path, rpc.NewServer(rpc.ServerOptions{
		Options:      opts,
		Authenticate: authenticator(d.Verifier),
	}))
}

// authenticator accepts tokens for Core with runner.connect.
func authenticator(v *runtoken.Verifier) rpc.Authenticator {
	return func(ctx context.Context, token string) (auth.Identity, time.Time, error) {
		c, err := v.Verify(ctx, token, "core")
		if err != nil {
			return auth.Identity{}, time.Time{}, err
		}
		if !c.Can(runtoken.CapRunnerConnect) {
			return auth.Identity{}, time.Time{}, errors.New("token lacks runner.connect")
		}
		return auth.Identity{Kind: auth.KindService, Subject: c.Subject}, c.Expiry, nil
	}
}

type handlers struct {
	deps  Deps
	names sync.Map // *rpc.Conn → runner name
}

// conn reaches a Runner over its connection.
type conn struct{ c *rpc.Conn }

func (rc conn) Start(ctx context.Context, r run.Run, secretEnv map[string]string) error {
	return rc.c.Call(ctx, runnerproto.MethodStart, runnerproto.Start{Run: r.ID, Spec: runnerproto.Spec{
		Image: r.Spec.Image, Command: r.Spec.Command, Env: r.Spec.Env, SecretEnv: secretEnv, Workdir: r.Spec.Workdir,
		TimeoutSeconds: r.Spec.TimeoutSeconds, Files: r.Spec.Files, Session: session(r.Spec.Session),
	}}, nil)
}

// session is a run's session as the protocol carries it.
func session(s *agent.Session) *runnerproto.Session {
	if s == nil {
		return nil
	}
	out := &runnerproto.Session{Runtime: s.Runtime, Prompt: s.Prompt, Instructions: s.Instructions, Model: s.Model,
		MaxTurns: s.MaxTurns, LLMURL: s.LLMURL, TokenEnv: s.TokenEnv, Dir: s.Dir}
	for _, sk := range s.Skills {
		out.Skills = append(out.Skills, runnerproto.Skill{Name: sk.Name, Description: sk.Description, Body: sk.Body, Files: sk.Files})
	}
	for _, m := range s.MCP {
		out.MCP = append(out.MCP, runnerproto.MCPServer{Name: m.Name, URL: m.URL})
	}
	return out
}

func (rc conn) Cancel(ctx context.Context, runID string) error {
	return rc.c.Call(ctx, runnerproto.MethodCancel, runnerproto.Cancel{Run: runID}, nil)
}

func (h *handlers) handle(ctx context.Context, req *rpc.Request) (any, error) {
	d := h.deps.Dispatcher
	if req.Method == runnerproto.MethodHello {
		var p runnerproto.Hello
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		if _, done := h.names.Load(req.Conn); done {
			return nil, rpc.Errorf(rpc.CodeInvalidRequest, "already introduced")
		}
		rc := conn{c: req.Conn}
		if err := d.Connect(ctx, app.RunnerInfo{Name: p.Runner, Labels: p.Labels, Capacity: p.Capacity, Active: p.Active}, rc); err != nil {
			return nil, rpcError(err)
		}
		h.names.Store(req.Conn, p.Runner)
		go func() {
			<-req.Conn.Done()
			h.names.Delete(req.Conn)
			d.Disconnect(p.Runner, rc)
		}()
		return struct{}{}, nil
	}
	v, ok := h.names.Load(req.Conn)
	if !ok {
		return nil, rpc.Errorf(rpc.CodeInvalidRequest, "send %s first", runnerproto.MethodHello)
	}
	name := v.(string)
	switch req.Method {
	case runnerproto.MethodStatus:
		var p runnerproto.Status
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		if p.Status != "running" {
			return nil, rpc.Errorf(rpc.CodeInvalidParams, "unknown status %q", p.Status)
		}
		return struct{}{}, rpcError(d.Running(ctx, name, p.Run))
	case runnerproto.MethodLog:
		var p runnerproto.Log
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		return struct{}{}, rpcError(d.Log(ctx, name, p.Run, p.Stream, p.Text))
	case runnerproto.MethodFinished:
		var p runnerproto.Finished
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		var res *app.SessionResult
		if p.Result != nil {
			res = &app.SessionResult{Success: p.Result.Success, Summary: p.Result.Summary, Turns: p.Result.Turns,
				CostUSD: p.Result.CostUSD}
		}
		return struct{}{}, rpcError(d.Finished(ctx, name, p.Run, p.ExitCode, p.Error, p.Cancelled, res))
	}
	return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
}

// rpcError maps app errors to RPC errors; nil stays nil.
func rpcError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrRunnerConflict), errors.Is(err, app.ErrConflict):
		return rpc.Errorf(rpc.CodeConflict, "%v", err)
	case errors.Is(err, app.ErrInvalid):
		return rpc.Errorf(rpc.CodeInvalidParams, "%v", err)
	case errors.Is(err, app.ErrForbidden):
		return rpc.Errorf(rpc.CodeForbidden, "%v", err)
	case errors.Is(err, app.ErrNotFound):
		return rpc.Errorf(rpc.CodeNotFound, "%v", err)
	}
	return rpc.Errorf(rpc.CodeInternal, "internal error")
}
