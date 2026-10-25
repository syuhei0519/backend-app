// Notification の DB 結合試験で使う、使い捨て環境の準備と片付けをまとめます。
// Package notificationtest owns one opt-in disposable Kubernetes DB fixture.
// It never accepts a DSN or reuses a namespace, pod or volume. Do not run in an
// implementation workspace: credentials/resources require separately approved testing.
package notificationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const Image = "postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f"

type Database struct {
	t                                  *testing.T
	context, namespace, password, port string
}

func Enabled(t *testing.T) {
	t.Helper()
	if os.Getenv("NOTIFICATION_DISPOSABLE_TEST") != "1" {
		t.Skip("independent approved Kubernetes integration only; DB behavior unverified")
	}
	if os.Getenv("NOTIFICATION_TEST_CONTEXT") == "" || os.Getenv("NOTIFICATION_TEST_APPROVED") != "dedicated-kubernetes" {
		t.Fatal("explicit approved dedicated Kubernetes context required")
	}
}
func (d *Database) command(input string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--context", d.context}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	// Discard raw stderr; it can contain private resource data.
	return cmd.Output()
}
func Start(t *testing.T) *Database {
	t.Helper()
	Enabled(t)
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal("fixture randomness")
	}
	nonce := hex.EncodeToString(raw)
	d := &Database{t: t, context: os.Getenv("NOTIFICATION_TEST_CONTEXT"), namespace: "notification-test-" + nonce, password: nonce}
	if _, err := d.command("", "create", "namespace", d.namespace); err != nil {
		t.Fatal("create unique test namespace failed; inspect uncertain outcome before retry")
	}
	t.Cleanup(func() {
		if _, err := d.command("", "delete", "namespace", d.namespace, "--wait=true", "--timeout=45s"); err != nil {
			t.Error("exact fixture namespace cleanup failed; independent operator must inspect")
		}
	})
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "db", "namespace": d.namespace}, "spec": map[string]any{"restartPolicy": "Never", "containers": []any{map[string]any{"name": "db", "image": Image, "env": []any{map[string]string{"name": "POSTGRES_PASSWORD", "value": nonce}}, "volumeMounts": []any{map[string]string{"name": "data", "mountPath": "/var/lib/postgresql/data"}}}}, "volumes": []any{map[string]any{"name": "data", "emptyDir": map[string]any{}}}}}
	data, _ := json.Marshal(pod)
	if _, err := d.command(string(data), "create", "-f", "-"); err != nil {
		t.Fatal("create fixture DB pod failed")
	}
	if _, err := d.command("", "-n", d.namespace, "wait", "--for=condition=Ready", "pod/db", "--timeout=45s"); err != nil {
		t.Fatal("fixture pod readiness")
	}
	deadline := time.Now().Add(30 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := d.command("", "-n", d.namespace, "exec", "db", "--", "pg_isready", "-h", "127.0.0.1", "-U", "postgres"); err == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("fixture DB readiness")
	}
	sql := "CREATE ROLE notification_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '" + nonce + "';\nCREATE DATABASE notifications OWNER notification_app;\n"
	if _, err := d.command(sql, "-n", d.namespace, "exec", "-i", "db", "--", "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1"); err != nil {
		t.Fatal("initialize owned fixture DB")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("fixture port")
	}
	_, d.port, _ = net.SplitHostPort(listener.Addr().String())
	listener.Close()
	forward := exec.Command("kubectl", "--context", d.context, "-n", d.namespace, "port-forward", "--address=127.0.0.1", "pod/db", d.port+":5432")
	if forward.Start() != nil {
		t.Fatal("fixture port-forward")
	}
	done := make(chan error, 1)
	go func() { done <- forward.Wait() }()
	t.Cleanup(func() { _ = forward.Process.Kill(); <-done })
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", d.port), 100*time.Millisecond)
		if e == nil {
			c.Close()
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("fixture port-forward readiness")
	return nil
}
func (d *Database) URL() string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword("notification_app", d.password), Host: net.JoinHostPort("127.0.0.1", d.port), Path: "/notifications", RawQuery: "sslmode=disable"}).String()
}
func (d *Database) Stop() {
	d.t.Helper()
	if _, err := d.command("", "-n", d.namespace, "delete", "pod", "db", "--wait=true", "--timeout=30s"); err != nil {
		d.t.Fatal("stop exact fixture DB pod failed")
	}
}
