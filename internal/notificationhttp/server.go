// Package notificationhttp implements the strict Notification wire contract independently of Account.
package notificationhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/syuhei0519/backend-app/internal/notification"
)

const MaxBody = 16 << 10

type readiness interface{ Ready(context.Context) error }
type Server struct {
	service  *notification.Service
	check    readiness
	stopping atomic.Bool
}

func New(service *notification.Service, check readiness) *Server {
	return &Server{service: service, check: check}
}
func (s *Server) BeginShutdown() { s.stopping.Store(true) }

var pathID = regexp.MustCompile(`^[1-9][0-9]*$`)

// Dispatch directly to avoid implicit HEAD acceptance and path-cleaning redirects.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/livez" || path == "/readyz":
		if !method(w, r, "GET") {
			return
		}
		if path == "/readyz" {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if s.stopping.Load() || s.check.Ready(ctx) != nil {
				failure(w, notification.ErrUnavailable)
				return
			}
		}
		write(w, 200, map[string]string{"status": "ok"})
	case path == "/api/notifications":
		if !method(w, r, "POST") {
			return
		}
		input, status, code := decode(w, r)
		if status != 0 {
			problem(w, status, code)
			return
		}
		item, created, err := s.service.Create(r.Context(), input)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Location", "/api/notifications/"+strconv.FormatInt(item.ID, 10))
		status = http.StatusOK
		if created {
			status = http.StatusCreated
		}
		write(w, status, item)
	case strings.HasPrefix(path, "/api/notifications/") && len(strings.TrimPrefix(path, "/api/notifications/")) > 0 && !strings.Contains(strings.TrimPrefix(path, "/api/notifications/"), "/"):
		if !method(w, r, "GET") {
			return
		}
		value := strings.TrimPrefix(path, "/api/notifications/")
		id, err := strconv.ParseInt(value, 10, 64)
		if !pathID.MatchString(value) || err != nil {
			failure(w, notification.ErrInvalid)
			return
		}
		item, err := s.service.Get(r.Context(), id)
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, item)
	default:
		problem(w, 404, "not_found")
	}
}
func method(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}
	w.Header().Set("Allow", allowed)
	problem(w, 405, "method_not_allowed")
	return false
}
func decode(w http.ResponseWriter, r *http.Request) (notification.Input, int, string) {
	var input notification.Input
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) {
		return input, 415, "unsupported_media_type"
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return input, 413, "payload_too_large"
	}
	if err != nil || !utf8.Valid(raw) {
		return input, 400, "validation_error"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return input, 400, "validation_error"
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err = dec.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return input, 400, "validation_error"
		}
		seen[key] = true
		var value json.RawMessage
		if dec.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return input, 400, "validation_error"
		}
		switch key {
		case "request_id":
			err = json.Unmarshal(value, &input.RequestID)
		case "order_id":
			err = json.Unmarshal(value, &input.OrderID)
		case "recipient":
			err = json.Unmarshal(value, &input.Recipient)
		case "message":
			err = json.Unmarshal(value, &input.Message)
		case "simulate_result":
			err = json.Unmarshal(value, &input.SimulateResult)
		default:
			return input, 400, "validation_error"
		}
		if err != nil {
			return input, 400, "validation_error"
		}
	}
	token, err = dec.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 5 {
		return input, 400, "validation_error"
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input, 400, "validation_error"
	}
	return input, 0, ""
}
func failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notification.ErrInvalid):
		problem(w, 400, "validation_error")
	case errors.Is(err, notification.ErrConflict):
		problem(w, 409, "idempotency_conflict")
	case errors.Is(err, notification.ErrNotFound):
		problem(w, 404, "not_found")
	case errors.Is(err, notification.ErrUnavailable):
		problem(w, 503, "unavailable")
	default:
		problem(w, 500, "internal_error")
	}
}
func problem(w http.ResponseWriter, status int, code string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
