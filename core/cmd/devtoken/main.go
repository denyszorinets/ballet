// Command devtoken mints a run token with Core's signing keys, for manual
// testing of services that accept run tokens (gateway, knowledge MCP).
// Development only: it needs read access to Core's key file.
//
//	go run ./core/cmd/devtoken -keys data/token-keys.json \
//	  -organization acme -project WEB -ticket WEB-1 -aud gateway -caps llm.invoke
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func main() {
	keys := flag.String("keys", "data/token-keys.json", "Core's token key file ([tokens] key_file)")
	kind := flag.String("kind", "run", "token kind: run, agent, planner or service")
	sub := flag.String("sub", "run:dev", "subject")
	aud := flag.String("aud", "gateway", "comma-separated audience")
	organization := flag.String("organization", "", "organization key")
	project := flag.String("project", "", "project key")
	ticket := flag.String("ticket", "", "ticket key (run tokens)")
	caps := flag.String("caps", "", "comma-separated capabilities")
	ttl := flag.Duration("ttl", time.Hour, "lifetime")
	flag.Parse()

	ring, err := runtoken.LoadKeyRing(*keys, time.Now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devtoken:", err)
		os.Exit(1)
	}
	var capabilities []string
	if *caps != "" {
		capabilities = strings.Split(*caps, ",")
	}
	tok, err := runtoken.NewIssuer(ring, time.Now).Issue(runtoken.Claims{
		Kind: runtoken.Kind(*kind), Subject: *sub, Audience: strings.Split(*aud, ","),
		Organization: *organization, Project: *project, Ticket: *ticket, Capabilities: capabilities,
	}, *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devtoken:", err)
		os.Exit(1)
	}
	fmt.Println(tok)
}
