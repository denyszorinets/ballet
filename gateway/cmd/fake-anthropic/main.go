// Command fake-anthropic serves a fake Anthropic Messages API (JSON and
// streaming, with usage) for local end-to-end tests of the gateway.
// Development only.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/denyszorinets/ballet/gateway/internal/fakeprovider"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9900", "listen address")
	key := flag.String("key", "sk-fake", "accepted x-api-key")
	flag.Parse()
	fmt.Printf("fake Anthropic API on http://%s (key %q)\n", *addr, *key)
	if err := http.ListenAndServe(*addr, &fakeprovider.Provider{Key: *key}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
