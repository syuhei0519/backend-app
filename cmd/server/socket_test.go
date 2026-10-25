package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/account"
	"github.com/syuhei0519/backend-app/internal/httpapi"
)

type socketService struct{ creates atomic.Int32 }

func (*socketService) List(context.Context) ([]account.Account, error) {
	return []account.Account{}, nil
}
func (*socketService) Get(context.Context, int64) (account.Account, error) {
	return account.Account{ID: 1, Name: "synthetic", Email: "fixture@example.test"}, nil
}
func (s *socketService) Create(_ context.Context, input account.Input) (account.Account, error) {
	s.creates.Add(1)
	return account.Account{ID: 1, Name: input.Name, Email: input.Email}, nil
}
func (*socketService) Update(_ context.Context, id int64, input account.Input) (account.Account, error) {
	return account.Account{ID: id, Name: input.Name, Email: input.Email}, nil
}
func (*socketService) Delete(context.Context, int64) error { return nil }
func (*socketService) Ready(context.Context) error         { return nil }

type lockedBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func TestHTTPServerBodyDeadlineAndLimitsOnSocket(t *testing.T) {
	const (
		maxBody     = 1 << 20
		readTimeout = 500 * time.Millisecond
	)

	var logs lockedBuffer
	svc := &socketService{}
	handler := httpapi.New(svc, svc, slog.New(slog.NewJSONHandler(&logs, nil)))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPServer(listener.Addr().String(), handler, readTimeout)
	if server.ReadTimeout != readTimeout {
		t.Fatalf("read timeout = %s", server.ReadTimeout)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		if err := <-stopped; err != nil && err != http.ErrServerClosed {
			t.Errorf("serve: %v", err)
		}
	})

	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 3 * time.Second}
	request := func(method, path string, body []byte) int {
		t.Helper()
		req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer synthetic-header-secret")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode
	}

	valid := []byte(`{"name":"synthetic-body-secret","email":"fixture@example.test"}`)
	atLimit := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), maxBody-len(valid))...)
	if status := request(http.MethodPost, "/api/accounts", atLimit); status != http.StatusCreated {
		t.Fatalf("body at limit: status %d", status)
	}
	overLimit := append(bytes.Clone(atLimit), ' ')
	if status := request(http.MethodPost, "/api/accounts", overLimit); status != http.StatusBadRequest {
		t.Fatalf("body over limit: status %d", status)
	}
	if got := svc.creates.Load(); got != 1 {
		t.Fatalf("create calls after size boundary = %d", got)
	}

	for _, test := range []struct {
		method string
		path   string
		body   []byte
		want   int
	}{
		{http.MethodGet, "/api/accounts", nil, http.StatusOK},
		{http.MethodGet, "/api/accounts/1", nil, http.StatusOK},
		{http.MethodPut, "/api/accounts/1", valid, http.StatusOK},
		{http.MethodDelete, "/api/accounts/1", nil, http.StatusNoContent},
	} {
		if status := request(test.method, test.path, test.body); status != test.want {
			t.Fatalf("%s %s: status %d", test.method, test.path, status)
		}
	}

	started := time.Now()
	response := incompleteRequest(t, listener.Addr().String(), `{"name":"synthetic-timeout-secret`, false)
	elapsed := time.Since(started)
	if response.StatusCode != http.StatusBadRequest {
		response.Body.Close()
		t.Fatalf("slow body: status %d", response.StatusCode)
	}
	response.Body.Close()
	if elapsed < readTimeout/2 || elapsed > 2*time.Second {
		t.Fatalf("slow body deadline elapsed = %s", elapsed)
	}

	completed := strings.Count(logs.String(), `"msg":"request completed"`)
	disconnectedAt := time.Now()
	incompleteRequest(t, listener.Addr().String(), `{"name":"synthetic-disconnect-secret`, true)
	deadline := time.Now().Add(time.Second)
	for strings.Count(logs.String(), `"msg":"request completed"`) == completed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Count(logs.String(), `"msg":"request completed"`) == completed {
		t.Fatal("disconnected request did not complete")
	}
	if elapsed := time.Since(disconnectedAt); elapsed >= readTimeout/2 {
		t.Fatalf("disconnected request waited for read deadline: %s", elapsed)
	}
	if got := svc.creates.Load(); got != 1 {
		t.Fatalf("create calls after incomplete bodies = %d", got)
	}

	if status := request(http.MethodGet, "/readyz", nil); status != http.StatusOK {
		t.Fatalf("ready before shutdown: status %d", status)
	}
	handler.BeginShutdown()
	if status := request(http.MethodGet, "/readyz", nil); status != http.StatusServiceUnavailable {
		t.Fatalf("ready during shutdown: status %d", status)
	}
	if status := request(http.MethodGet, "/livez", nil); status != http.StatusOK {
		t.Fatalf("live during shutdown: status %d", status)
	}

	for _, secret := range []string{
		"synthetic-body-secret",
		"synthetic-timeout-secret",
		"synthetic-disconnect-secret",
		"synthetic-header-secret",
		"fixture@example.test",
	} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("request body or secret leaked to logs: %s", secret)
		}
	}
}

func incompleteRequest(t *testing.T, address, body string, disconnect bool) *http.Response {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf("POST /api/accounts HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: 256\r\nConnection: close\r\n\r\n%s", address, body)
	if _, err := io.WriteString(connection, request); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	if disconnect {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{Reader: response.Body, Closer: connection}
	return response
}
