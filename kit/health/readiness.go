package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// checkTimeout bounds each readiness check so a hung dependency cannot hang
// the probe.
const checkTimeout = 2 * time.Second

// Check is a named readiness check. Func returns an error when the
// dependency it checks is not usable.
type Check struct {
	Name string
	Func func(ctx context.Context) error
}

type readinessBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Readiness returns a handler that runs all checks and answers 200 when
// every check passes, 503 otherwise. Failure details are logged, not
// returned, so the endpoint does not leak internals.
func Readiness(checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readinessBody{Status: "ready", Checks: make(map[string]string, len(checks))}
		code := http.StatusOK
		for _, c := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
			err := c.Func(ctx)
			cancel()
			if err != nil {
				slog.WarnContext(r.Context(), "readiness check failed", "check", c.Name, "error", err)
				body.Checks[c.Name] = "failing"
				body.Status = "not ready"
				code = http.StatusServiceUnavailable
				continue
			}
			body.Checks[c.Name] = "ok"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	})
}
