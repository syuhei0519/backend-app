// Order の DB 結合試験で使う、使い捨て環境の準備と片付けをまとめます。
// Package ordertest is test support for fresh, local-only PostgreSQL containers.
// It never accepts an operator DSN or reuses an existing container/volume.
package ordertest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

const Image = "postgres@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f"

const defaultReadyTimeout = 30 * time.Second

type fixtureOptions struct {
	readyTimeout time.Duration
	initDBArgs   string
}

func socket() string {
	if runtime.GOOS == "windows" {
		return "npipe:////./pipe/dockerDesktopLinuxEngine"
	}
	return "unix:///var/run/docker.sock"
}

type Database struct {
	t                    *testing.T
	name, password, port string
	DB, Role             string
}

func Enabled(t *testing.T) {
	t.Helper()
	if os.Getenv("ORDER_DISPOSABLE_TEST") != "1" {
		t.Skip("ORDER_DISPOSABLE_TEST=1 required; real DB unverified")
	}
}
func command(input string, args ...string) ([]byte, error) {
	return commandWithin(60*time.Second, input, args...)
}

func commandWithin(timeout time.Duration, input string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", socket()}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	// Do not inherit remote Docker context/host/TLS settings or record stderr.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp"}
	return cmd.Output()
}
func Start(t *testing.T, db, role string) *Database {
	t.Helper()
	return start(t, db, role, fixtureOptions{readyTimeout: defaultReadyTimeout})
}

func start(t *testing.T, db, role string, options fixtureOptions) *Database {
	t.Helper()
	Enabled(t)
	if options.readyTimeout <= 0 || options.readyTimeout > defaultReadyTimeout {
		t.Fatal("invalid disposable DB ready timeout")
	}
	allowed := map[string]string{
		"accounts": "account_app", "orders": "order_app",
		"payments": "payment_app", "notifications": "notification_app",
	}
	if allowed[db] != role {
		t.Fatal("invalid disposable DB identity")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal("random fixture failed")
	}
	nonce := hex.EncodeToString(raw)
	d := &Database{t: t, name: "order-b1-test-" + nonce, password: nonce, DB: db, Role: role}
	// Reserve a loopback port number before creation. An explicit published port
	// remains stable across stop/start; Docker-assigned random ports can change.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("allocate disposable DB port")
	}
	_, allocated, err := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	if err != nil {
		t.Fatal("parse disposable DB port")
	}
	// A digest already present locally is required. No pull, credentials or remote service.
	if _, err := command("", "image", "inspect", Image); err != nil {
		t.Fatal("pinned PostgreSQL image not available locally")
	}
	args := []string{"create", "--pull=never", "--name", d.name, "--label", "order-b1-disposable=true"}
	if runID := os.Getenv("FOUR_SERVICE_RUN_ID"); runID != "" {
		args = append(args, "--label", "backend-four-service-run="+runID)
	}
	args = append(args, "--publish", "127.0.0.1:"+allocated+":5432", "--env", "POSTGRES_PASSWORD="+nonce)
	if options.initDBArgs != "" {
		args = append(args, "--env", "POSTGRES_INITDB_ARGS="+options.initDBArgs)
	}
	args = append(args, Image)
	if _, err := command("", args...); err != nil {
		t.Fatal("create fresh disposable container failed")
	}
	t.Cleanup(func() {
		if _, err := command("", "rm", "--force", "--volumes", d.name); err != nil {
			t.Error("disposable container cleanup failed")
		}
	})
	if _, err := command("", "start", d.name); err != nil {
		t.Fatal("start disposable DB failed")
	}
	d.wait(options.readyTimeout)
	if _, err := command("CREATE ROLE "+role+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '"+nonce+"';\nCREATE DATABASE "+db+" OWNER "+role+";\n", "exec", "-i", d.name, "psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1"); err != nil {
		t.Fatal("initialize disposable DB failed")
	}
	port, err := command("", "port", d.name, "5432/tcp")
	if err != nil {
		t.Fatal("resolve disposable port failed")
	}
	host, p, e := net.SplitHostPort(strings.TrimSpace(string(port)))
	if e != nil || host != "127.0.0.1" {
		t.Fatal("disposable port is not loopback")
	}
	d.port = p
	return d
}
func (d *Database) wait(timeout time.Duration) {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := command("", "exec", d.name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatal("disposable DB readiness timed out")
}
func (d *Database) URL() string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(d.Role, d.password), Host: net.JoinHostPort("127.0.0.1", d.port), Path: "/" + d.DB, RawQuery: "sslmode=disable"}).String()
}
func (d *Database) AdminURL(db string) string {
	if db != d.DB && db != "postgres" {
		d.t.Fatal("invalid test admin DB")
	}
	u, _ := url.Parse(d.URL())
	u.User = url.UserPassword("postgres", d.password)
	u.Path = "/" + db
	return u.String()
}
func (d *Database) Stop() {
	d.t.Helper()
	if _, err := command("", "stop", "--time", "5", d.name); err != nil {
		d.t.Fatal("stop disposable DB failed")
	}
}
func (d *Database) Restart() {
	d.t.Helper()
	if _, err := command("", "start", d.name); err != nil {
		d.t.Fatal("restart disposable DB failed")
	}
	d.wait(defaultReadyTimeout)
}
