package ordertest

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const helperMode = "PLAT208_FIXTURE_HELPER"

func helperEnvironment(mode, runID string) []string {
	blocked := map[string]bool{
		helperMode:              true,
		"FOUR_SERVICE_RUN_ID":   true,
		"ORDER_DISPOSABLE_TEST": true,
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			environment = append(environment, value)
		}
	}
	return append(environment,
		helperMode+"="+mode,
		"FOUR_SERVICE_RUN_ID="+runID,
		"ORDER_DISPOSABLE_TEST=1",
	)
}

func runID(suffix string) string {
	return fmt.Sprintf("plat208-%d-%d-%s", os.Getpid(), time.Now().UnixNano(), suffix)
}

func containerIDs(run string) ([]string, error) {
	output, err := commandWithin(2*time.Second, "", "ps", "-aq", "--filter", "label=backend-four-service-run="+run)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(output)), nil
}

func cleanupRun(run string) error {
	deadline := time.Now().Add(8 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ids, err := containerIDs(run)
		if err != nil {
			lastErr = fmt.Errorf("list run-labeled containers: %w", err)
		} else if len(ids) == 0 {
			return nil
		} else if _, err := commandWithin(2*time.Second, "", append([]string{"rm", "--force", "--volumes"}, ids...)...); err != nil {
			lastErr = fmt.Errorf("remove %d run-labeled containers: %w", len(ids), err)
		} else {
			lastErr = nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	ids, err := containerIDs(run)
	if err != nil {
		return fmt.Errorf("verify run-labeled containers: %w", err)
	}
	if len(ids) != 0 {
		return fmt.Errorf("run-labeled containers remain after cleanup deadline: %d (%v)", len(ids), lastErr)
	}
	return nil
}

func requireNoContainers(t *testing.T, run string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ids, err := containerIDs(run)
		if err == nil && len(ids) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	ids, err := containerIDs(run)
	if err != nil {
		t.Fatal("inspect disposable cleanup")
	}
	t.Fatalf("run-labeled containers remain after bounded cleanup: %d", len(ids))
}

func TestDisposableFixtureFailureHelper(t *testing.T) {
	if os.Getenv(helperMode) != "startup-failure" {
		return
	}
	start(t, "orders", "order_app", fixtureOptions{
		readyTimeout: 3 * time.Second,
		initDBArgs:   "--plat208-invalid-initdb-option",
	})
	t.Fatal("invalid PostgreSQL startup unexpectedly became ready")
}

func TestDisposableFixtureCancellationHelper(t *testing.T) {
	if os.Getenv(helperMode) != "cancellation" {
		return
	}
	Start(t, "orders", "order_app")
	fmt.Println("PLAT208_FIXTURE_READY")
	if err := os.Stdout.Sync(); err != nil {
		t.Fatal("flush fixture readiness")
	}
	select {}
}

func TestDisposableStartupFailureCleansUp(t *testing.T) {
	Enabled(t)
	run := runID("startup")
	t.Cleanup(func() {
		if err := cleanupRun(run); err != nil {
			t.Errorf("startup-failure fallback cleanup failed: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDisposableFixtureFailureHelper$")
	child.Env = helperEnvironment("startup-failure", run)
	started := time.Now()
	output, err := child.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("startup failure exceeded finite helper deadline")
	}
	if err == nil {
		t.Fatal("invalid PostgreSQL startup did not fail")
	}
	if !bytes.Contains(output, []byte("disposable DB readiness timed out")) {
		t.Fatal("fixture did not observe the real PostgreSQL readiness failure")
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("startup failure was not bounded: %s", elapsed)
	}
	requireNoContainers(t, run, 5*time.Second)
}

func TestDisposableCancellationCleanup(t *testing.T) {
	Enabled(t)
	run := runID("cancel")
	t.Cleanup(func() {
		if err := cleanupRun(run); err != nil {
			t.Errorf("cancellation fallback cleanup failed: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDisposableFixtureCancellationHelper$")
	child.Env = helperEnvironment("cancellation", run)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal("open cancellation helper stdout")
	}
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		t.Fatal("start cancellation helper")
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "PLAT208_FIXTURE_READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("cancellation helper exited before fixture readiness")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cancellation helper readiness timed out")
	}
	ids, err := containerIDs(run)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ready cancellation fixture containers = %d", len(ids))
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal("cancel fixture helper")
	}
	_ = child.Wait()
	cleanupStarted := time.Now()
	if err := cleanupRun(run); err != nil {
		t.Fatalf("clean cancelled fixture: %v", err)
	}
	if elapsed := time.Since(cleanupStarted); elapsed > 10*time.Second {
		t.Fatalf("cancelled fixture cleanup was not bounded: %s", elapsed)
	}
	requireNoContainers(t, run, time.Second)
}
