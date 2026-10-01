// Core is the Ballet system of record: tenancy, tracker, scheduler, orchestrator, planner and APIs.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denyszorinets/ballet/kit/health"
	"github.com/denyszorinets/ballet/kit/server"
)

const serviceName = "core"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("/healthz", health.Handler(serviceName))

	return server.ListenAndServe(ctx, *addr, mux, 10*time.Second)
}
