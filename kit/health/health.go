// Package health provides the liveness endpoint shared by all Ballet services.
package health

import (
	"encoding/json"
	"net/http"
)

type status struct {
	Service string `json:"service"`
	Status  string `json:"status"`
}

// Handler returns an HTTP handler that reports the service as alive.
func Handler(service string) http.Handler {
	body, err := json.Marshal(status{Service: service, Status: "ok"})
	if err != nil {
		panic(err) // marshalling two strings cannot fail
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}
