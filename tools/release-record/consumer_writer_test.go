package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReadonlyConsumerRequiresSuccessfulNativeWriterExport(t *testing.T) {
	for _, bad := range []string{"", "writer-failed", "writer-ref", "stale-policy", "package-only", "record-bytes", "unproven", "another-run", "duplicate-writer", "redirect"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			b, _ := json.Marshal(r)
			sum := Checksum(b)
			u, _ := r.Run().URL("record")
			a, _ := NewSourceAPI("fixture-readonly-token")
			job := func(id int64, name string, pipeline int64, sha string) completedJob {
				j := completedJob{ID: id, Name: name, Status: "success", Ref: "main"}
				j.Commit.ID = sha
				j.Pipeline.ID = pipeline
				j.Pipeline.ProjectID = r.SourceProjectID
				j.Pipeline.SHA = sha
				return j
			}
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Scheme != "https" || req.URL.Host != "gitlab.com" || req.Header.Get("PRIVATE-TOKEN") != "fixture-readonly-token" {
					t.Fatal("non-readonly export request")
				}
				status := 200
				var value any
				var body []byte
				root := fmt.Sprintf("/api/v4/projects/%d", r.SourceProjectID)
				switch req.URL.Path {
				case root + fmt.Sprintf("/jobs/%d", r.ScanJobID):
					value = job(r.ScanJobID, "image-scan", r.ScanPipelineID, r.PolicyRevision)
				case root + fmt.Sprintf("/jobs/%d", r.BuildJobID):
					value = job(r.BuildJobID, "container-build-oci", r.BuildPipelineID, r.SourceCommit)
				case root + "/repository/branches/main":
					sha := r.PolicyRevision
					if bad == "stale-policy" {
						sha = strings.Repeat("e", 40)
					}
					value = map[string]any{"name": "main", "protected": true, "commit": map[string]any{"id": sha}}
				case root + fmt.Sprintf("/pipelines/%d/jobs", r.ScanPipelineID):
					j := job(300, "release-record-store", r.ScanPipelineID, r.PolicyRevision)
					if bad == "writer-failed" {
						j.Status = "failed"
					}
					if bad == "writer-ref" {
						j.Ref = "codex/fixture"
					}
					jobs := []completedJob{j}
					if bad == "package-only" {
						jobs = []completedJob{}
					}
					if bad == "duplicate-writer" {
						jobs = append(jobs, job(301, "release-record-store-rescan", r.ScanPipelineID, r.PolicyRevision))
					}
					value = jobs
				case root + "/jobs/300/artifacts/.release-store/public/stored.json", root + "/jobs/301/artifacts/.release-store/public/stored.json":
					proof := StoredBundle{1, r.Run(), u, sum, r.Decision, true, true, true, true}
					if bad == "unproven" {
						proof.ProtectedMainWriter = false
					}
					if bad == "another-run" {
						proof.Run.JobID++
					}
					value = proof
				case root + "/jobs/300/artifacts/.release-store/public/record.json", root + "/jobs/301/artifacts/.release-store/public/record.json":
					body = b
					if bad == "record-bytes" {
						body = append(append([]byte{}, b...), '\n')
					}
					if bad == "redirect" {
						status = 302
					}
				default:
					t.Fatal("unexpected export path")
				}
				if body == nil {
					body, _ = json.Marshal(value)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			got, e := a.ReadWriterExportedRecord(r, sum)
			if (e == nil) != (bad == "") {
				t.Fatal("writer export authority mismatch", bad)
			}
			if e == nil && string(got) != string(b) {
				t.Fatal("export bytes mismatch")
			}
			origin, originError := a.ReadOriginWriterRecord(r, sum)
			if (originError == nil) != (bad == "" || bad == "stale-policy") {
				t.Fatal("historical origin must retain native writer/byte authority", bad)
			}
			if originError == nil && string(origin) != string(b) {
				t.Fatal("historical origin changed native bytes")
			}
			failed := r
			failed.Decision = "failed"
			if _, e := a.ReadOriginWriterRecord(failed, sum); e == nil {
				t.Fatal("failed scan used as successful origin")
			}
		})
	}
}
