// Runs the exact validated backend binary without a source database or OTLP.
package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func smoke() bool {
	if os.Getuid() != 10001 {
		return false
	}
	server := exec.Command("/server")
	server.Env = []string{"DATABASE_URL=postgres://smoke@127.0.0.1:1/smoke?sslmode=disable", "LOG_LEVEL=error"}
	server.Stdout, server.Stderr = io.Discard, io.Discard
	if server.Start() != nil {
		return false
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	client := &http.Client{Timeout: 2 * time.Second}
	status := func(path string) int {
		resp, err := client.Get("http://127.0.0.1:8080" + path)
		if err != nil {
			return 0
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode
	}
	for attempt := 0; attempt < 10; attempt++ {
		if status("/livez") == 200 {
			// Readiness must refuse the deliberately absent DB; no CRUD here.
			return status("/readyz") == 503 && status("/livez") == 200
		}
		time.Sleep(time.Second)
	}
	return false
}

func main() {
	if !smoke() {
		os.Exit(1)
	}
}
