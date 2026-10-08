package release

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type sourceTransport func(*http.Request) (*http.Response, error)

func (f sourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCompletedJobAuthorityRejectsStatusAndIdentitySwaps(t *testing.T) {
	for _, bad := range []string{"", "scan-running", "scan-source", "scan-unprotected-ref", "build-source", "build-status", "build-unprotected-ref", "wrong-project", "allow-failure", "redirect", "403"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			calls := 0
			a, _ := NewSourceAPI("non-secret-read-token")
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Scheme != "https" || req.URL.Host != "gitlab.com" || req.Method != "GET" || req.Header.Get("PRIVATE-TOKEN") != "non-secret-read-token" {
					t.Fatal("auth target changed")
				}
				j := completedJob{Ref: "main"}
				j.Pipeline.ProjectID = 86247025
				if strings.HasSuffix(req.URL.Path, "/220") {
					j.ID = 220
					j.Name = "image-scan"
					j.Status = "success"
					j.Pipeline.ID = 110
					j.Commit.ID = r.PolicyRevision
					j.Pipeline.SHA = r.PolicyRevision
					if bad == "scan-running" {
						j.Status = "running"
					}
					if bad == "scan-unprotected-ref" {
						j.Ref = "codex/unprotected"
					}
					if bad == "scan-source" {
						j.Commit.ID = r.SourceCommit
					}
					if bad == "allow-failure" {
						j.AllowFailure = true
					}
				} else if strings.HasSuffix(req.URL.Path, "/11") {
					j.ID = 11
					j.Name = "container-build-oci"
					j.Status = "success"
					j.Pipeline.ID = 10
					j.Commit.ID = r.SourceCommit
					j.Pipeline.SHA = r.SourceCommit
					if bad == "build-source" {
						j.Pipeline.SHA = r.PolicyRevision
					}
					if bad == "build-status" {
						j.Status = "failed"
					}
					if bad == "build-unprotected-ref" {
						j.Ref = "codex/unprotected"
					}
				} else {
					t.Fatal("unexpected job lookup")
				}
				if bad == "wrong-project" {
					j.Pipeline.ProjectID = 86247033
				}
				status := 200
				if bad == "redirect" {
					status = 302
				}
				if bad == "403" {
					status = 403
				}
				b, _ := json.Marshal(j)
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://external.invalid/steal"}}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
			})
			e := a.ValidateCompletedJobs(r)
			if (e == nil) != (bad == "") {
				t.Fatal("job substitution or real successful identity decision wrong")
			}
			if bad == "" && calls != 2 {
				t.Fatal("original build and completed scan not checked")
			}
			if bad == "redirect" && calls != 1 {
				t.Fatal("authenticated redirect followed")
			}
		})
	}
}
