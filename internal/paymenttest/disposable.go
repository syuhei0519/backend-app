// Package paymenttest provisions only an explicitly enabled, isolated Kubernetes
// fixture. No operator DSN, existing namespace, DB, pod or volume is accepted.
package paymenttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	if os.Getenv("PAYMENT_DISPOSABLE_TEST") != "1" {
		t.Skip("isolated Kubernetes integration not enabled; product AC unverified")
	}
	if os.Getenv("PAYMENT_TEST_KUBE_CONTEXT") == "" {
		t.Fatal("explicit approved isolated kube context required")
	}
}
func (d *Database) command(input string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--context", d.context, "--namespace", d.namespace}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Output() // Never include raw stderr, SQL or connection strings in test reports.
}
func Start(t *testing.T) *Database {
	t.Helper()
	Enabled(t)
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal("fixture randomness")
	}
	nonce := hex.EncodeToString(raw)
	d := &Database{t: t, context: os.Getenv("PAYMENT_TEST_KUBE_CONTEXT"), namespace: "payment-test-" + nonce, password: nonce}
	// create, never apply/reuse, and clean only our successfully created namespace.
	if _, err := d.command("", "create", "namespace", d.namespace); err != nil {
		t.Fatal("create isolated namespace")
	}
	t.Cleanup(func() {
		if _, err := d.command("", "delete", "namespace", d.namespace, "--wait=true", "--timeout=45s"); err != nil {
			t.Error("exact fixture namespace cleanup failed")
		}
	})
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": "db-auth"}, "type": "Opaque", "stringData": map[string]string{"POSTGRES_PASSWORD": nonce}}
	b, _ := json.Marshal(secret)
	if _, err := d.command(string(b), "create", "-f", "-"); err != nil {
		t.Fatal("create fixture secret")
	}
	pod := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"db"},"spec":{"restartPolicy":"Never","containers":[{"name":"postgres","image":%q,"envFrom":[{"secretRef":{"name":"db-auth"}}],"ports":[{"containerPort":5432}],"volumeMounts":[{"name":"data","mountPath":"/var/lib/postgresql"}]}],"volumes":[{"name":"data","emptyDir":{}}]}}`, Image)
	if _, err := d.command(pod, "create", "-f", "-"); err != nil {
		t.Fatal("create dedicated PostgreSQL pod")
	}
	if _, err := d.command("", "wait", "--for=jsonpath={.status.phase}=Running", "pod/db", "--timeout=45s"); err != nil {
		t.Fatal("fixture pod unavailable")
	}
	deadline := time.Now().Add(30 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := d.command("", "exec", "db", "--", "pg_isready", "-h", "127.0.0.1", "-U", "postgres"); err == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("fixture DB readiness")
	}
	sql := "CREATE ROLE payment_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '" + nonce + "';\nCREATE DATABASE payments OWNER payment_app;\n"
	if _, err := d.command(sql, "exec", "-i", "db", "--", "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1"); err != nil {
		t.Fatal("initialize owned fixture DB")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("fixture port")
	}
	_, d.port, err = net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	if err != nil {
		t.Fatal("fixture address")
	}
	forward := exec.Command("kubectl", "--context", d.context, "--namespace", d.namespace, "port-forward", "--address=127.0.0.1", "pod/db", d.port+":5432")
	if forward.Start() != nil {
		t.Fatal("fixture port-forward")
	}
	done := make(chan error, 1)
	go func() { done <- forward.Wait() }()
	t.Cleanup(func() { _ = forward.Process.Kill(); <-done })
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+d.port, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("fixture forwarding unavailable")
	return nil
}
func (d *Database) URL() string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword("payment_app", d.password), Host: net.JoinHostPort("127.0.0.1", d.port), Path: "/payments", RawQuery: "sslmode=disable"}).String()
}
func (d *Database) Stop() {
	d.t.Helper()
	if _, err := d.command("", "delete", "pod", "db", "--wait=true", "--timeout=30s"); err != nil {
		d.t.Fatal("stop exact fixture DB")
	}
}
