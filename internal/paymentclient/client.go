// Package paymentclient implements the bounded Order -> Payment HTTP contract.
package paymentclient

import (
	"bytes"
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
	"unicode/utf8"

	"gitlab.com/platform-engineering-lab/backend-app/internal/payment"
)

const Timeout = 2 * time.Second
const MaxResponse = 16 << 10

var (
	ErrTimeout     = errors.New("payment timeout")
	ErrUnavailable = errors.New("payment unavailable")
	ErrBadResponse = errors.New("invalid payment response")
)

var label = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
var portNumber = regexp.MustCompile(`^[0-9]+$`)

// ValidateOrigin accepts an explicit http host:port origin only. Errors never echo it.
func ValidateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") {
		return errors.New("invalid ORDER_PAYMENT_URL")
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || !portNumber.MatchString(port) || portErr != nil || n < 1 || n > 65535 || len(host) > 253 {
		return errors.New("invalid ORDER_PAYMENT_URL")
	}
	if net.ParseIP(host) == nil {
		for _, part := range strings.Split(host, ".") {
			if !label.MatchString(part) {
				return errors.New("invalid ORDER_PAYMENT_URL")
			}
		}
	}
	return nil
}

type Client struct {
	endpoint string
	http     *http.Client
}

func New(origin string) (*Client, error) {
	if err := ValidateOrigin(origin); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{endpoint: origin + "/api/payments", http: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Create(ctx context.Context, in payment.Input) (payment.Payment, bool, error) {
	if err := payment.Validate(in); err != nil {
		return payment.Payment{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	raw, _ := json.Marshal(in)
	// A non-rewindable body, no idempotency headers and no retry middleware: one POST.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		return payment.Payment{}, false, ErrUnavailable
	}
	req.ContentLength = int64(len(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return payment.Payment{}, false, transportError(ctx, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxResponse+1))
	if err != nil {
		return payment.Payment{}, false, transportError(ctx, err)
	}
	if ctx.Err() != nil {
		return payment.Payment{}, false, transportError(ctx, ctx.Err())
	}
	media, params, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) || len(body) > MaxResponse {
		return payment.Payment{}, false, ErrBadResponse
	}
	fields, err := object(body)
	if err != nil {
		return payment.Payment{}, false, ErrBadResponse
	}
	if res.StatusCode == 409 || res.StatusCode == 503 {
		e, err := object(fields["error"])
		var code, message string
		if err != nil || json.Unmarshal(e["code"], &code) != nil || json.Unmarshal(e["message"], &message) != nil || message != http.StatusText(res.StatusCode) {
			return payment.Payment{}, false, ErrBadResponse
		}
		if res.StatusCode == 409 && code == "idempotency_conflict" {
			return payment.Payment{}, false, payment.ErrConflict
		}
		if res.StatusCode == 503 && code == "unavailable" {
			return payment.Payment{}, false, ErrUnavailable
		}
		return payment.Payment{}, false, ErrBadResponse
	}
	if res.StatusCode != 200 && res.StatusCode != 201 {
		return payment.Payment{}, false, ErrBadResponse
	}
	var item payment.Payment
	// Decode exact wire keys separately: encoding/json's case-insensitive struct
	// matching must not let unknown extension names override required fields.
	for key, target := range map[string]any{
		"request_id": &item.RequestID, "order_id": &item.OrderID,
		"amount_minor": &item.AmountMinor, "currency": &item.Currency,
		"simulate_result": &item.SimulateResult, "id": &item.ID,
		"status": &item.Status, "created_at": &item.CreatedAt,
	} {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, target) != nil {
			return payment.Payment{}, false, ErrBadResponse
		}
	}
	if item.ID < 1 || item.CreatedAt.IsZero() || item.Input != in || item.Status != payment.Result(in) {
		return payment.Payment{}, false, ErrBadResponse
	}
	return item, res.StatusCode == 201, nil
}

func transportError(ctx context.Context, err error) error {
	var network net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return ErrTimeout
	}
	return ErrUnavailable
}

// object rejects duplicate keys at every depth and trailing JSON, including in
// unknown extension fields, while allowing bounded compatible response additions.
func object(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, ErrBadResponse
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := value(d, 0); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrBadResponse
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrBadResponse
	}
	return fields, nil
}

func value(d *json.Decoder, depth int) error {
	if depth > 100 {
		return ErrBadResponse
	}
	t, err := d.Token()
	if err != nil {
		return ErrBadResponse
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok || seen[key] {
				return ErrBadResponse
			}
			seen[key] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return ErrBadResponse
		}
	case json.Delim('['):
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return ErrBadResponse
		}
	}
	return nil
}
