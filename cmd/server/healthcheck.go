package main

import (
	"fmt"
	"net/http"
	"time"
)

// runHealthcheck probes the local /healthz; returns a process exit code.
// Used as the container HEALTHCHECK (distroless has no curl).
func runHealthcheck(port string) int {
	if port == "" {
		port = "8080"
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Println("unhealthy:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Println("unhealthy: status", resp.StatusCode)
		return 1
	}
	return 0
}
