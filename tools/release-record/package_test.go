package release

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, data string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data))}
}
func run() Run { return Run{"frontend", "sha256:" + strings.Repeat("a", 64), 110, 220} }

func TestFixedDestinationBeforeAuthentication(t *testing.T) {
	r := run()
	u, _ := r.URL("record")
	c, _ := NewClient("synthetic-nonissued-job-token")
	calls := 0
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(403, "PRIVATE_RESPONSE_MUST_NOT_LEAK"), nil
	})
	for _, bad := range []string{
		strings.Replace(u, "gitlab.com", "other.example.invalid", 1),
		strings.Replace(u, "86247025", "86247033", 1),
		u + "?token=data", u + "#fragment", strings.Replace(u, "https://", "http://", 1),
		strings.Replace(u, "https://", "https://user:password@", 1),
		strings.Replace(u, "/generic/", "/generic/../", 1),
	} {
		if _, err := c.Read(r, "record", bad, strings.Repeat("b", 64)); !errors.Is(err, ErrRefused) {
			t.Fatal("invalid evidence URL accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid URL transmitted authentication")
	}
	_, err := c.Read(r, "record", u, strings.Repeat("b", 64))
	if !errors.Is(err, ErrRefused) || strings.Contains(err.Error(), "PRIVATE_RESPONSE") || calls != 1 {
		t.Fatal("403 or diagnostic redaction boundary failed")
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	c, _ := NewClient("synthetic-nonissued-job-token")
	calls := 0
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		r := response(302, "private server body")
		r.Header.Set("Location", "https://other.example.invalid/credential-trap")
		return r, nil
	})
	r := run()
	u, _ := r.URL("record")
	if _, err := c.Read(r, "record", u, strings.Repeat("b", 64)); !errors.Is(err, ErrRefused) || calls != 1 {
		t.Fatal("authenticated redirect followed")
	}
}

func TestImmutableRunsAndChecksum(t *testing.T) {
	c, _ := NewClient("synthetic-nonissued-job-token")
	stored := map[string]string{}
	puts := 0
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "gitlab.com" || req.Header.Get("JOB-TOKEN") != "synthetic-nonissued-job-token" {
			t.Fatal("fixed auth destination changed")
		}
		if req.Method == "PUT" {
			b, _ := io.ReadAll(req.Body)
			stored[req.URL.String()] = string(b)
			puts++
			return response(201, ""), nil
		}
		if b, ok := stored[req.URL.String()]; ok {
			return response(200, b), nil
		}
		return response(404, "absent"), nil
	})
	r := run()
	first := []byte(`{"schemaVersion":2,"fixture":"first"}`)
	if err := c.WriteImmutable(r, "record", first); err != nil {
		t.Fatal("initial fixed run write failed")
	}
	if err := c.WriteImmutable(r, "record", first); err != nil || puts != 1 {
		t.Fatal("same bytes did not remain idempotent")
	}
	if err := c.WriteImmutable(r, "record", []byte("different")); !errors.Is(err, ErrChanged) || puts != 1 {
		t.Fatal("same run overwritten")
	}
	second := r
	second.JobID++
	if err := c.WriteImmutable(second, "record", []byte(`{"schemaVersion":2,"fixture":"failed-second"}`)); err != nil || puts != 2 {
		t.Fatal("same digest distinct run rejected or overwritten")
	}
	u, _ := r.URL("record")
	if b, err := c.Read(r, "record", u, Checksum(first)); err != nil || string(b) != string(first) {
		t.Fatal("fixed run/checksum retrieval failed")
	}
	if _, err := c.Read(r, "record", u, Checksum([]byte("replacement"))); !errors.Is(err, ErrChanged) {
		t.Fatal("checksum substitution accepted")
	}
}

func TestInvalidRunAndMissingArtifact(t *testing.T) {
	for _, bad := range []Run{{"other", run().Digest, 1, 1}, {"frontend", "sha256:../escape", 1, 1}, {"backend", run().Digest, 0, 1}, {"backend", run().Digest, 1, -1}} {
		if _, err := bad.URL("record"); !errors.Is(err, ErrRefused) {
			t.Fatal("invalid fixed run accepted")
		}
	}
	c, _ := NewClient("synthetic-nonissued-job-token")
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(404, "absent"), nil })
	r := run()
	u, _ := r.URL("sbom")
	if _, err := c.Read(r, "sbom", u, strings.Repeat("b", 64)); !errors.Is(err, ErrAbsent) {
		t.Fatal("missing/expired artifact accepted")
	}
}
