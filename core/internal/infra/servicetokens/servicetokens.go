// Package servicetokens issues the long-lived service tokens of Ballet's own
// services (gateway, knowledge, agent) into token files those services
// read. Tokens are re-issued periodically, well before they expire.
package servicetokens

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

// Service describes one service's identity.
type Service struct {
	Name         string
	Audience     []string
	Capabilities []string
}

// Services is the fixed table of Ballet services and what they may do.
var Services = []Service{
	{Name: "gateway", Audience: []string{"core"}, Capabilities: []string{runtoken.CapCredentialsRead, runtoken.CapUsageWrite}},
	{Name: "knowledge", Audience: []string{"core", "gateway"}, Capabilities: []string{runtoken.CapLLMEmbed}},
	{Name: "agent", Audience: []string{"core"}, Capabilities: []string{runtoken.CapAgentConnect}},
}

// Issuer writes service tokens to Dir/<name>.token.
type Issuer struct {
	Tokens *runtoken.TokenIssuer
	Dir    string
	TTL    time.Duration
	Logger *slog.Logger
}

// IssueAll writes a fresh token for every service.
func (i *Issuer) IssueAll() error {
	if err := os.MkdirAll(i.Dir, 0o700); err != nil {
		return fmt.Errorf("create service token directory: %w", err)
	}
	for _, s := range Services {
		raw, err := i.Tokens.Issue(runtoken.Claims{
			Kind: runtoken.KindService, Subject: "service:" + s.Name, Audience: s.Audience, Capabilities: s.Capabilities,
		}, i.TTL)
		if err != nil {
			return fmt.Errorf("issue %s token: %w", s.Name, err)
		}
		if err := writeAtomic(filepath.Join(i.Dir, s.Name+".token"), raw+"\n"); err != nil {
			return fmt.Errorf("write %s token: %w", s.Name, err)
		}
	}
	return nil
}

// Run re-issues all tokens every TTL/4 until ctx is cancelled.
func (i *Issuer) Run(ctx context.Context) {
	t := time.NewTicker(i.TTL / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := i.IssueAll(); err != nil && i.Logger != nil {
				i.Logger.ErrorContext(ctx, "re-issuing service tokens failed", "error", err)
			}
		}
	}
}

func writeAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after rename
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
