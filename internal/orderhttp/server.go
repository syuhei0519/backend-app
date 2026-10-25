// Order の HTTP 入口です。要求の検証、業務処理の呼び出し、応答と稼働確認を担当します。
// Package orderhttp implements the strict Order wire contract.
package orderhttp

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

	"github.com/syuhei0519/backend-app/internal/accountclient"
	"github.com/syuhei0519/backend-app/internal/order"
	"github.com/syuhei0519/backend-app/internal/orderpayment"
	"github.com/syuhei0519/backend-app/internal/payment"
	"github.com/syuhei0519/backend-app/internal/paymentclient"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const MaxBody = 16 << 10

type readiness interface{ Ready(context.Context) error }
type Server struct {
	service  *order.Service
	payments *orderpayment.Service
	check    readiness
	stopping atomic.Bool
	tracer   trace.Tracer
}

func New(service *order.Service, check readiness) *Server {
	return NewWithPayments(service, check, nil)
}
func NewWithPayments(service *order.Service, check readiness, creator orderpayment.Creator) *Server {
	return &Server{service: service, check: check, payments: orderpayment.New(service, creator), tracer: noop.NewTracerProvider().Tracer("order-backend")}
}
func (s *Server) SetTracer(tracer trace.Tracer) { s.tracer = tracer }
func (s *Server) BeginShutdown()                { s.stopping.Store(true) }

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
				failure(w, order.ErrUnavailable)
				return
			}
		}
		write(w, 200, map[string]string{"status": "ok"})
	case path == "/api/orders":
		if !method(w, r, "POST") {
			return
		}
		ctx := accountclient.Extract(r.Context(), r.Header)
		ctx, span := s.tracer.Start(ctx, "POST /api/orders", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.request.method", "POST"), attribute.String("http.route", "/api/orders")))
		out := &traceResponse{ResponseWriter: w, status: 200}
		w = out
		w.Header().Set("X-Request-ID", accountclient.RequestID(ctx))
		defer func() {
			span.SetAttributes(attribute.Int("http.response.status_code", out.status))
			if out.status >= 500 {
				span.SetStatus(codes.Error, "request failed")
			}
			span.End()
		}()
		input, status, code := decode(w, r)
		if status != 0 {
			problem(w, status, code)
			return
		}
		item, err := s.service.Create(ctx, input)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Location", "/api/orders/"+strconv.FormatInt(item.ID, 10))
		write(w, 201, item)
	case strings.HasPrefix(path, "/api/orders/") && strings.HasSuffix(path, "/payments") && !strings.Contains(strings.TrimSuffix(strings.TrimPrefix(path, "/api/orders/"), "/payments"), "/"):
		if !method(w, r, "POST") {
			return
		}
		value := strings.TrimSuffix(strings.TrimPrefix(path, "/api/orders/"), "/payments")
		id, err := strconv.ParseInt(value, 10, 64)
		if !pathID.MatchString(value) || err != nil {
			failure(w, order.ErrInvalid)
			return
		}
		input, status, code := decodePayment(w, r)
		if status != 0 {
			problem(w, status, code)
			return
		}
		input.OrderID = id
		item, created, err := s.payments.Create(r.Context(), input)
		if err != nil {
			switch {
			case errors.Is(err, payment.ErrInvalid):
				problem(w, 400, "validation_error")
			case errors.Is(err, payment.ErrConflict):
				problem(w, 409, "idempotency_conflict")
			case errors.Is(err, orderpayment.ErrUnconfigured):
				problem(w, 503, "payment_unconfigured")
			case errors.Is(err, paymentclient.ErrTimeout):
				problem(w, 504, "payment_timeout")
			case errors.Is(err, paymentclient.ErrUnavailable):
				problem(w, 503, "payment_unavailable")
			case errors.Is(err, paymentclient.ErrBadResponse):
				problem(w, 502, "payment_bad_response")
			default:
				failure(w, err)
			}
			return
		}
		status = 200
		if created {
			status = 201
		}
		write(w, status, item)
	case strings.HasPrefix(path, "/api/orders/") && len(strings.TrimPrefix(path, "/api/orders/")) > 0 && !strings.Contains(strings.TrimPrefix(path, "/api/orders/"), "/"):
		if !method(w, r, "GET") {
			return
		}
		value := strings.TrimPrefix(path, "/api/orders/")
		id, err := strconv.ParseInt(value, 10, 64)
		if !pathID.MatchString(value) || err != nil {
			failure(w, order.ErrInvalid)
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
func decodePayment(w http.ResponseWriter, r *http.Request) (payment.Input, int, string) {
	var input payment.Input
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
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return input, 400, "validation_error"
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return input, 400, "validation_error"
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return input, 400, "validation_error"
		}
		switch key {
		case "request_id":
			err = json.Unmarshal(value, &input.RequestID)
		case "amount_minor":
			err = json.Unmarshal(value, &input.AmountMinor)
		case "currency":
			err = json.Unmarshal(value, &input.Currency)
		case "simulate_result":
			err = json.Unmarshal(value, &input.SimulateResult)
		default:
			return input, 400, "validation_error"
		}
		if err != nil {
			return input, 400, "validation_error"
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 4 {
		return input, 400, "validation_error"
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input, 400, "validation_error"
	}
	return input, 0, ""
}
func method(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}
	w.Header().Set("Allow", allowed)
	problem(w, 405, "method_not_allowed")
	return false
}
func decode(w http.ResponseWriter, r *http.Request) (order.Input, int, string) {
	var input order.Input
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
		case "account_id":
			err = json.Unmarshal(value, &input.AccountID)
		case "item_code":
			err = json.Unmarshal(value, &input.ItemCode)
		case "quantity":
			err = json.Unmarshal(value, &input.Quantity)
		default:
			return input, 400, "validation_error"
		}
		if err != nil {
			return input, 400, "validation_error"
		}
	}
	token, err = dec.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 3 {
		return input, 400, "validation_error"
	}
	if err = dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return input, 400, "validation_error"
	}
	return input, 0, ""
}
func failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, order.ErrAccountUnconfigured):
		problem(w, 503, "account_unconfigured")
	case errors.Is(err, order.ErrAccountNotFound):
		problem(w, 422, "account_not_found")
	case errors.Is(err, order.ErrAccountUnavailable):
		problem(w, 503, "account_unavailable")
	case errors.Is(err, order.ErrAccountTimeout):
		problem(w, 504, "account_timeout")
	case errors.Is(err, order.ErrAccountBadResponse):
		problem(w, 502, "account_bad_response")
	case errors.Is(err, order.ErrInvalid):
		problem(w, 400, "validation_error")
	case errors.Is(err, order.ErrNotFound):
		problem(w, 404, "not_found")
	case errors.Is(err, order.ErrUnavailable):
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

type traceResponse struct {
	http.ResponseWriter
	status int
}

func (w *traceResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
