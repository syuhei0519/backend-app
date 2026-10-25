// 採取したトレースが親子関係・サービス名・秘密情報の除外条件を満たすことを確認します。
package distributedtrace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
)

type attribute struct {
	Key   string `json:"key"`
	Value struct {
		String string `json:"stringValue"`
		Int    string `json:"intValue"`
	} `json:"value"`
}
type span struct {
	TraceID    string      `json:"traceId"`
	ID         string      `json:"spanId"`
	Parent     string      `json:"parentSpanId"`
	Name       string      `json:"name"`
	Kind       int         `json:"kind"`
	Start      string      `json:"startTimeUnixNano"`
	End        string      `json:"endTimeUnixNano"`
	Attributes []attribute `json:"attributes"`
	Status     struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Events  []any `json:"events"`
	Links   []any `json:"links"`
	service string
}

func nano(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
func validate(t *testing.T, raw []byte, cases map[string]string) {
	t.Helper()
	allowed := map[string]bool{"http.request.method": true, "http.route": true, "http.response.status_code": true, "error.type": true, "db.system.name": true, "db.operation.name": true}
	resourceAllowed := map[string]bool{"service.name": true, "service.version": true, "service.instance.id": true, "deployment.environment.name": true, "k8s.namespace.name": true, "k8s.pod.uid": true}
	traces := map[string][]span{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var doc struct {
			Resources []struct {
				Resource struct {
					Attributes []attribute `json:"attributes"`
				} `json:"resource"`
				Scopes []struct {
					Spans []span `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if json.Unmarshal(line, &doc) != nil {
			t.Fatal("OTLP JSON parse")
		}
		for _, r := range doc.Resources {
			service := ""
			for _, a := range r.Resource.Attributes {
				if !resourceAllowed[a.Key] {
					t.Fatalf("unexpected resource key %s", a.Key)
				}
				if a.Key == "service.name" {
					service = a.Value.String
				}
			}
			for _, scope := range r.Scopes {
				for _, s := range scope.Spans {
					s.service = service
					for _, a := range s.Attributes {
						if !allowed[a.Key] {
							t.Fatalf("unexpected span key %s", a.Key)
						}
					}
					if len(s.Events) > 0 || len(s.Links) > 0 {
						t.Fatal("unexpected event/link")
					}
					traces[s.TraceID] = append(traces[s.TraceID], s)
				}
			}
		}
	}
	for _, label := range []string{"success", "missing", "error", "timeout", "recovered"} {
		spans := traces[cases[label]]
		if len(spans) != 4 {
			t.Fatalf("%s spans=%d want 4", label, len(spans))
		}
		var server, client, account, db span
		for _, s := range spans {
			switch {
			case s.service == "order-backend" && s.Kind == 2:
				server = s
			case s.service == "order-backend" && s.Kind == 3:
				client = s
			case s.service == "account-backend" && s.Kind == 2:
				account = s
			case s.service == "account-backend" && s.Kind == 3:
				db = s
			default:
				t.Fatal("unexpected service/kind")
			}
		}
		if server.Parent != "1111111111111111" || client.Parent != server.ID || account.Parent != client.ID || db.Parent != account.ID || db.ID == "" {
			t.Fatal("broken distributed parent chain")
		}
		if nano(client.Start) < nano(server.Start) || nano(account.Start) < nano(client.Start) || nano(db.Start) < nano(account.Start) || nano(db.End) > nano(account.End) || nano(client.End) > nano(server.End) {
			t.Fatal("invalid span nesting")
		}
		for _, s := range spans {
			if nano(s.End) <= nano(s.Start) {
				t.Fatal("nonpositive latency")
			}
		}
		if label == "error" || label == "timeout" {
			if server.Status.Code != 2 || client.Status.Code != 2 || account.Status.Code != 2 || db.Status.Code != 2 {
				t.Fatal("failure status missing")
			}
		}
		if label == "timeout" && (nano(client.End)-nano(client.Start) < 1900000000) {
			t.Fatal("timeout latency missing")
		}
		if label == "success" || label == "recovered" {
			for _, s := range spans {
				if s.Status.Code == 2 {
					t.Fatal("success span failed")
				}
			}
		}
		t.Logf("%s trace=%s", label, cases[label])
		for i, s := range []span{server, client, account, db} {
			t.Logf("%s%s %s span=%s parent=%s status=%d latency_ms=%.3f", fmt.Sprintf("%*s", i*2, ""), s.service, s.Name, s.ID, s.Parent, s.Status.Code, float64(nano(s.End)-nano(s.Start))/1e6)
		}
	}
}
