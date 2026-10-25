// 使い捨ての DB と Notification API をつなぎ、実際の保存・取得と失敗時の応答を確認します。
// One lifecycle in the separately approved dedicated Kubernetes test environment.
package notificationintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/notification"
	notificationtest "github.com/syuhei0519/backend-app/internal/notificationdockertest"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestOwnedLifecycle(t *testing.T) {
	notificationtest.Enabled(t)
	db := notificationtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal("fixture pool")
	}
	defer pool.Close()
	name := "notification-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	if exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/notification-server").Run() != nil {
		t.Fatal("build API process")
	}
	env := func(address string) []string {
		return []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + db.URL(), "NOTIFICATION_HTTP_ADDRESS=" + address, "LOG_LEVEL=error"}
	}
	migrate := func() {
		t.Helper()
		c := exec.CommandContext(ctx, binary, "migrate")
		c.Env = env("127.0.0.1:0")
		if c.Run() != nil {
			t.Fatal("migration process")
		}
	}
	history := func() string {
		t.Helper()
		var s string
		if pool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM public.notification_schema_migrations h`).Scan(&s) != nil {
			t.Fatal("owned history")
		}
		return s
	}
	migrate()
	before := history()
	migrate()
	if history() != before {
		t.Fatal("no-op migration changed history")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	type response struct {
		status   int
		raw      []byte
		location string
		err      error
	}
	call := func(method, url, body string) response {
		r, e := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if e != nil {
			return response{err: e}
		}
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		res, e := client.Do(r)
		if e != nil {
			return response{err: e}
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		return response{res.StatusCode, raw, res.Header.Get("Location"), e}
	}
	check := func(r response, status int) {
		t.Helper()
		if r.err != nil || r.status != status {
			t.Fatalf("HTTP expected %d got %d (transport failure=%t)", status, r.status, r.err != nil)
		}
	}
	type process struct {
		cmd     *exec.Cmd
		done    chan error
		base    string
		stopped bool
	}
	start := func() *process {
		t.Helper()
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal("allocate API port")
		}
		addr := l.Addr().String()
		l.Close()
		p := &process{cmd: exec.CommandContext(ctx, binary), done: make(chan error, 1), base: "http://" + addr}
		p.cmd.Env = env(addr)
		if p.cmd.Start() != nil {
			t.Fatal("start API")
		}
		go func() { p.done <- p.cmd.Wait() }()
		t.Cleanup(func() {
			if !p.stopped {
				_ = p.cmd.Process.Kill()
				<-p.done
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if call("GET", p.base+"/readyz", "").status == 200 {
				return p
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("API readiness")
		return nil
	}
	stop := func(p *process) {
		t.Helper()
		if runtime.GOOS == "windows" {
			_ = p.cmd.Process.Kill()
			<-p.done
			p.stopped = true
			return
		}
		if p.cmd.Process.Signal(syscall.SIGTERM) != nil {
			t.Fatal("signal API")
		}
		select {
		case e := <-p.done:
			p.stopped = true
			if e != nil {
				t.Fatal("API shutdown")
			}
		case <-time.After(25 * time.Second):
			t.Fatal("API shutdown timeout")
		}
	}
	p := start()
	type accepted struct {
		body string
		r    response
		item notification.Notification
	}
	var saved []accepted
	for index, result := range []string{"success", "failure"} {
		body := fmt.Sprintf(`{"request_id":"550e8400-e29b-41d4-a716-44665544000%d","order_id":9223372036854775807,"recipient":"demo-user","message":"Order accepted","simulate_result":%q}`, index+1, result)
		r := call("POST", p.base+"/api/notifications", body)
		check(r, 201)
		var item notification.Notification
		if json.Unmarshal(r.raw, &item) != nil || item.ID < 1 || item.OrderID != 9223372036854775807 || item.CreatedAt.IsZero() || item.Status != notification.Result(item.Input) {
			t.Fatal("terminal representation")
		}
		if r.location != fmt.Sprintf("/api/notifications/%d", item.ID) {
			t.Fatal("Location")
		}
		got := call("GET", p.base+r.location, "")
		check(got, 200)
		if !bytes.Equal(got.raw, r.raw) {
			t.Fatal("GET changed state")
		}
		replay := call("POST", p.base+"/api/notifications", body)
		check(replay, 200)
		if replay.location != r.location || !bytes.Equal(replay.raw, r.raw) {
			t.Fatal("replay changed state")
		}
		for _, pair := range [][2]string{{`"order_id":9223372036854775807`, `"order_id":1`}, {`"demo-user"`, `"another"`}, {`"Order accepted"`, `"changed"`}, {`"simulate_result":"` + result + `"`, `"simulate_result":"` + map[string]string{"success": "failure", "failure": "success"}[result] + `"`}} {
			check(call("POST", p.base+"/api/notifications", strings.Replace(body, pair[0], pair[1], 1)), 409)
		}
		// Race first acceptance, then replay all contenders: one 201 and one receipt.
		concurrent := strings.Replace(body, "550e8400-e29b-41d4-a716-44665544000", "650e8400-e29b-41d4-a716-44665544000", 1)
		results := make(chan response, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); results <- call("POST", p.base+"/api/notifications", concurrent) }()
		}
		wg.Wait()
		close(results)
		newCount := 0
		var concurrentRaw []byte
		for got := range results {
			if got.err != nil || (got.status != 200 && got.status != 201) {
				t.Fatal("concurrent request failed")
			}
			if got.status == 201 {
				newCount++
			}
			if concurrentRaw == nil {
				concurrentRaw = got.raw
			}
			if !bytes.Equal(got.raw, concurrentRaw) {
				t.Fatal("concurrent identity differs")
			}
		}
		if newCount != 1 {
			t.Fatal("concurrent create count")
		}
		saved = append(saved, accepted{body, r, item})
	}
	assertCounts := func() {
		t.Helper()
		var rows, receipts, mismatch int
		if pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.notifications),(SELECT count(*) FROM public.notification_sink_receipts),(SELECT count(*) FROM public.notifications n LEFT JOIN public.notification_sink_receipts r ON n.id=r.notification_id WHERE r.notification_id IS NULL OR r.result<>n.status OR n.status<>CASE n.simulate_result WHEN 'success' THEN 'sent' ELSE 'failed' END)`).Scan(&rows, &receipts, &mismatch) != nil || rows != 4 || receipts != 4 || mismatch != 0 {
			t.Fatal("one consistent durable fake receipt per accepted request")
		}
	}
	assertCounts()
	for _, body := range []string{`{}`, strings.Replace(saved[0].body, `"order_id":9223372036854775807`, `"order_id":1.0`, 1), strings.Replace(saved[0].body, `"message":"Order accepted"`, `"message":null`, 1)} {
		check(call("POST", p.base+"/api/notifications", body), 400)
	}
	check(call("GET", p.base+"/api/notifications/9223372036854775807", ""), 404)
	oldPID := p.cmd.Process.Pid
	stop(p)
	migrate()
	p = start()
	if p.cmd.Process.Pid == oldPID {
		t.Fatal("API PID unchanged")
	}
	for _, a := range saved {
		got := call("GET", p.base+a.r.location, "")
		check(got, 200)
		replay := call("POST", p.base+"/api/notifications", a.body)
		check(replay, 200)
		if !bytes.Equal(got.raw, a.r.raw) || !bytes.Equal(replay.raw, a.r.raw) || replay.location != a.r.location {
			t.Fatal("restart changed identity or state")
		}
	}
	assertCounts()
	if history() != before {
		t.Fatal("restart migration changed history")
	}
	// The same fixture closes the DB, proving transport failure is not fake failure.
	db.Stop()
	check(call("GET", p.base+"/readyz", ""), 503)
	check(call("GET", p.base+saved[0].r.location, ""), 503)
	check(call("POST", p.base+"/api/notifications", saved[0].body), 503)
	check(call("GET", p.base+"/livez", ""), 200)
	stop(p)
	t.Log("success/failure, exact and concurrent replay, conflicts, one receipt, API restart with changed PID, migration no-op and offline 503 measured")
}
