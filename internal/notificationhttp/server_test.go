// Notification API の入力・応答・稼働確認を、差し替えた保存処理で確認します。
package notificationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/syuhei0519/backend-app/internal/notification"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	calls  int
	err    error
	replay bool
}

func (f *fakeStore) Create(_ context.Context, in notification.Input) (notification.Notification, bool, error) {
	f.calls++
	return notification.Notification{ID: 1, Input: in, Status: notification.Result(in), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, !f.replay, f.err
}
func (f *fakeStore) Get(_ context.Context, id int64) (notification.Notification, error) {
	f.calls++
	return notification.Notification{ID: id}, f.err
}
func (f *fakeStore) Ready(context.Context) error { return f.err }

const valid = `{"request_id":"550e8400-e29b-41d4-a716-446655440001","order_id":1,"recipient":"demo-user","message":"Order accepted","simulate_result":"success"}`

func request(s *Server, method, path, body, media string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if media != "" {
		r.Header.Set("Content-Type", media)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestStrictJSON(t *testing.T) {
	bad := []string{"", "null", "[]", "1", "true", `"str"`, `{}`, valid + ` {}`, valid + ` x`, valid[:len(valid)-1], strings.Replace(valid, `"order_id":1`, `"order_id":1,"\u006frder_id":2`, 1), strings.Replace(valid, "Order accepted", string([]byte{255}), 1)}
	var fields map[string]any
	_ = json.Unmarshal([]byte(valid), &fields)
	for key := range fields {
		copyFields := func() map[string]any {
			m := map[string]any{}
			for k, v := range fields {
				m[k] = v
			}
			return m
		}
		m := copyFields()
		delete(m, key)
		raw, _ := json.Marshal(m)
		bad = append(bad, string(raw))
		for _, v := range []any{nil, true, []any{}, map[string]any{}} {
			m = copyFields()
			m[key] = v
			raw, _ = json.Marshal(m)
			bad = append(bad, string(raw))
		}
	}
	for _, v := range []string{`"1"`, `1.0`, `1e0`, `0`, `-1`, `9223372036854775808`} {
		bad = append(bad, strings.Replace(valid, `"order_id":1`, `"order_id":`+v, 1))
	}
	bad = append(bad, valid[:len(valid)-1]+`,"unknown":1}`)
	for _, body := range bad {
		f := &fakeStore{}
		w := request(New(notification.New(f), f), "POST", "/api/notifications", body, "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid status=%d calls=%d", w.Code, f.calls)
		}
	}
}
func TestPostAndLimits(t *testing.T) {
	for _, tt := range []struct {
		body, media string
		replay      bool
		status      int
	}{
		{valid, "application/json", false, 201}, {valid, "application/json; charset=UTF-8", true, 200}, {strings.Replace(valid, "success", "failure", 1), "application/json", false, 201},
		{valid + strings.Repeat(" ", MaxBody-len(valid)), "application/json", false, 201}, {valid + strings.Repeat(" ", MaxBody-len(valid)+1), "application/json", false, 413},
		{valid, "", false, 415}, {valid, "text/plain", false, 415}, {valid, "application/json; charset=latin1", false, 415}, {valid, "application/json; other=x", false, 415},
	} {
		f := &fakeStore{replay: tt.replay}
		w := request(New(notification.New(f), f), "POST", "/api/notifications", tt.body, tt.media)
		if w.Code != tt.status {
			t.Fatalf("status %d want %d", w.Code, tt.status)
		}
		if tt.status < 300 && w.Header().Get("Location") != "/api/notifications/1" {
			t.Fatal("Location")
		}
		if tt.status >= 400 && f.calls != 0 {
			t.Fatal("invalid called store")
		}
	}
}
func TestRoutesErrorsHealth(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"GET", "/api/notifications/1", 200, ""}, {"GET", "/api/notifications/9223372036854775807", 200, ""}, {"GET", "/api/notifications/01", 400, ""}, {"GET", "/api/notifications/0", 400, ""}, {"GET", "/api/notifications/+1", 400, ""}, {"GET", "/api/notifications/9223372036854775808", 400, ""}, {"GET", "/api/notifications/1e0", 400, ""}, {"GET", "/api/notifications/1.0", 400, ""},
		{"GET", "/api/notifications", 405, "POST"}, {"HEAD", "/api/notifications/1", 405, "GET"}, {"PUT", "/api/notifications/1", 405, "GET"}, {"GET", "/api/notifications/", 404, ""}, {"GET", "/api/notifications/1/x", 404, ""}, {"GET", "//api/notifications/1", 404, ""}, {"GET", "/unknown", 404, ""}, {"POST", "/readyz", 405, "GET"},
	} {
		f := &fakeStore{}
		w := request(New(notification.New(f), f), tt.method, tt.path, "", "")
		if w.Code != tt.status || w.Header().Get("Allow") != tt.allow {
			t.Fatalf("route %s %s got %d", tt.method, tt.path, w.Code)
		}
		if tt.status >= 400 && f.calls != 0 {
			t.Fatal("invalid called store")
		}
	}
	for _, tt := range []struct {
		err    error
		status int
	}{{notification.ErrConflict, 409}, {notification.ErrNotFound, 404}, {notification.ErrUnavailable, 503}, {notification.ErrInternal, 500}, {errors.New("private sql dsn"), 500}} {
		f := &fakeStore{err: tt.err}
		w := request(New(notification.New(f), f), "POST", "/api/notifications", valid, "application/json")
		if w.Code != tt.status || strings.Contains(w.Body.String(), "private") {
			t.Fatal("error mapping")
		}
	}
	f := &fakeStore{}
	s := New(notification.New(f), f)
	if request(s, "GET", "/readyz", "", "").Code != 200 {
		t.Fatal("ready")
	}
	s.BeginShutdown()
	if request(s, "GET", "/readyz", "", "").Code != 503 || request(s, "GET", "/livez", "", "").Code != 200 {
		t.Fatal("shutdown")
	}
}
