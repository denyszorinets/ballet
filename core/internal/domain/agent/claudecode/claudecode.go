// Package claudecode runs Claude Code headless (claude -p) as an agent
// session.
package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/denyszorinets/ballet/core/internal/domain/agent"
)

// Name of the adapter.
const Name = "claude-code"

// Adapter builds Claude Code sessions.
type Adapter struct {
	Command    string // the claude executable (default "claude")
	GatewayURL string // ANTHROPIC_BASE_URL: Ballet's LLM gateway, as seen from the session
	// APIKeyEnv names the environment variable holding the run token the
	// gateway accepts as API key (delivered as a secret).
	APIKeyEnv string
}

// Name returns "claude-code".
func (Adapter) Name() string { return Name }

// Files the session gets, relative to the workspace. HOME is
// <workspace>/.home, so skills, instructions and MCP servers are Claude
// Code's user-level configuration — outside the repository, so agents
// cannot commit them.
const (
	promptFile = ".ballet/prompt.md"
	homeDir    = ".home"
)

// Build returns the files, environment and command of a session.
func (a Adapter) Build(s agent.Session) (agent.Built, error) {
	if strings.TrimSpace(s.Prompt) == "" {
		return agent.Built{}, errors.New("claude-code: the session has no prompt")
	}
	if a.GatewayURL == "" || a.APIKeyEnv == "" {
		return agent.Built{}, errors.New("claude-code: gateway URL and API key variable are required")
	}
	files := map[string]string{promptFile: s.Prompt}
	if s.Instructions != "" {
		files[homeDir+"/.claude/CLAUDE.md"] = s.Instructions
	}
	for _, sk := range s.Skills {
		dir := homeDir + "/.claude/skills/" + sk.Name
		files[dir+"/SKILL.md"] = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", sk.Name, oneLine(sk.Description), sk.Body)
		for p, content := range sk.Files {
			clean := path.Clean(p)
			if path.IsAbs(clean) || strings.HasPrefix(clean, "..") {
				return agent.Built{}, fmt.Errorf("claude-code: skill %s file %q escapes the skill", sk.Name, p)
			}
			files[dir+"/"+clean] = content
		}
	}
	if len(s.MCP) > 0 {
		servers := map[string]any{}
		for _, m := range s.MCP {
			// Claude Code expands ${VAR}: the token stays in the environment.
			servers[m.Name] = map[string]any{"type": "http", "url": m.URL,
				"headers": map[string]string{"Authorization": "Bearer ${" + m.TokenEnv + "}"}}
		}
		cfg, err := json.MarshalIndent(map[string]any{"mcpServers": servers}, "", "  ")
		if err != nil {
			return agent.Built{}, err
		}
		files[homeDir+"/.claude.json"] = string(cfg)
	}

	command := a.Command
	if command == "" {
		command = "claude"
	}
	args := []string{quote(command), "-p", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions"}
	if s.Model != "" {
		args = append(args, "--model", quote(s.Model))
	}
	if s.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(s.MaxTurns))
	}
	// The prompt goes through stdin: arguments are limited in size.
	script := `export ANTHROPIC_API_KEY="${` + a.APIKeyEnv + `}"` + "\n" +
		"exec " + strings.Join(args, " ") + ` < "$BALLET_WORKSPACE/` + promptFile + `"`
	return agent.Built{
		Command: []string{"sh", "-c", script},
		Files:   files,
		Env: map[string]string{
			"ANTHROPIC_BASE_URL": a.GatewayURL,
			// Sessions run in their own disposable container (ADR-0009), so
			// Claude Code may skip permission prompts even as root there.
			"IS_SANDBOX":          "1",
			"DISABLE_AUTOUPDATER": "1",
			"DISABLE_TELEMETRY":   "1",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		},
	}, nil
}

// Result reads the final "result" event of claude's stream-json output.
func (Adapter) Result(stdout string) (agent.Result, bool) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"result"`) {
			continue
		}
		var ev struct {
			Type    string  `json:"type"`
			Subtype string  `json:"subtype"`
			IsError bool    `json:"is_error"`
			Result  string  `json:"result"`
			Turns   int     `json:"num_turns"`
			Cost    float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "result" {
			continue
		}
		return agent.Result{Success: ev.Subtype == "success" && !ev.IsError, Summary: ev.Result, Turns: ev.Turns,
			CostUSD: ev.Cost}, true
	}
	return agent.Result{}, false
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
