// Command healthcheck is a tiny static HTTP probe for container healthchecks.
// The distroless final stage has no shell, curl, or wget, so we ship this
// binary alongside /app and invoke it from docker-compose's healthcheck.test.
//
// Usage:
//
//	healthcheck <url>
//
// Exits 0 if the URL returns a 2xx status within the timeout; exits 1 on
// any non-2xx status, connection error, or timeout.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "healthcheck: usage: healthcheck <url>")
		os.Exit(2)
	}
	url := os.Args[1]

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: build request:", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		os.Exit(1)
	}
}
