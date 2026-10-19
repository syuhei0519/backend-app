// Package accountclient implements the bounded Order creation -> Account lookup.
package accountclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/syuhei0519/backend-app/internal/order"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const Timeout = 2 * time.Second
const MaxResponse = 16 << 10

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
var portNumber = regexp.MustCompile(`^[0-9]+$`)
var safeRequestID = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

type requestKey struct{}

// Extract accepts only W3C trace context and the Account API's safe request-ID
// format. It deliberately does not copy baggage, authorization or arbitrary headers.
func Extract(ctx context.Context, headers http.Header) context.Context {
	ctx = (propagation.TraceContext{}).Extract(ctx, propagation.HeaderCarrier(headers))
	id := strings.TrimSpace(headers.Get("X-Request-ID"))
	if !safeRequestID.MatchString(id) {
		id = uuid.NewString()
	}
	return context.WithValue(ctx, requestKey{}, id)
}
func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestKey{}).(string); return id }

// ValidateOrigin uses the same explicit operator-selected origin policy as Payment.
func ValidateOrigin(origin string) error {
	bad := errors.New("invalid ORDER_ACCOUNT_URL")
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") {
		return bad
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, pe := strconv.Atoi(port)
	if err != nil || host == "" || !portNumber.MatchString(port) || pe != nil || n < 1 || n > 65535 || len(host) > 253 {
		return bad
	}
	if net.ParseIP(host) == nil {
		for _, label := range strings.Split(host, ".") {
			if !hostLabel.MatchString(label) {
				return bad
			}
		}
	}
	return nil
}

type Client struct {
	origin string
	http   *http.Client
	tracer trace.Tracer
}

func New(origin string) (*Client, error) {
	return NewWithTracer(origin, noop.NewTracerProvider().Tracer("order-backend"))
}
func NewWithTracer(origin string, tracer trace.Tracer) (*Client, error) {
	if err := ValidateOrigin(origin); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// Disable reuse so net/http cannot implicitly replay a GET on a stale pooled
	// connection. Each check is one bounded request without application retries.
	transport.DisableKeepAlives = true
	return &Client{origin: origin, tracer: tracer, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) Check(ctx context.Context, id int64) (result error) {
	if id < 1 {
		return order.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	ctx, span := c.tracer.Start(ctx, "GET /api/accounts/{id}", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/api/accounts/{id}")))
	defer func() {
		if result != nil {
			kind := "account_unavailable"
			switch {
			case errors.Is(result, order.ErrAccountTimeout):
				kind = "timeout"
			case errors.Is(result, order.ErrAccountNotFound):
				kind = "account_not_found"
			case errors.Is(result, order.ErrAccountBadResponse):
				kind = "account_bad_response"
			}
			span.SetAttributes(attribute.String("error.type", kind))
			span.SetStatus(codes.Error, "account check failed")
		}
		span.End()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/api/accounts/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return order.ErrAccountUnavailable
	}
	(propagation.TraceContext{}).Inject(ctx, propagation.HeaderCarrier(req.Header))
	if id := RequestID(ctx); id != "" {
		req.Header.Set("X-Request-ID", id)
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return transportError(ctx, err)
	}
	defer res.Body.Close()
	span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
	// Classify status without trusting or echoing an error body. Closing also
	// prevents an unbounded error body from delaying the status response.
	switch {
	case res.StatusCode == 404:
		return order.ErrAccountNotFound
	case res.StatusCode >= 500 && res.StatusCode <= 599:
		return order.ErrAccountUnavailable
	case res.StatusCode != 200:
		return order.ErrAccountBadResponse
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxResponse+1))
	if err != nil {
		return transportError(ctx, err)
	}
	if ctx.Err() != nil {
		return transportError(ctx, ctx.Err())
	}
	media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(raw) > MaxResponse {
		return order.ErrAccountBadResponse
	}
	// Existence requires an unambiguous matching positive ID. Other Account
	// fields are intentionally unused; in particular no name/email is logged.
	d := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return order.ErrAccountBadResponse
	}
	seen := map[string]bool{}
	var found int64
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return order.ErrAccountBadResponse
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return order.ErrAccountBadResponse
		}
		if key == "id" && json.Unmarshal(value, &found) != nil {
			return order.ErrAccountBadResponse
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF || found != id {
		return order.ErrAccountBadResponse
	}
	return nil
}
func transportError(ctx context.Context, err error) error {
	var network net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return order.ErrAccountTimeout
	}
	return order.ErrAccountUnavailable
}
