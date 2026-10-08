// writerのjob由来・checksum・duplicate拒否・record最後書込をfake APIで検証する。
// 途中失敗を成功として採用しないことを確認し、実Packageへのuploadは行わない。
package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWriterAuthorityBeforeAnyImmutableUpload(t *testing.T) {
	for _, bad := range []string{"", "failed-evidence", "unprotected", "stale-main", "writer-failed", "source-scan-failed", "duplicate-allowed", "duplicate-exception", "duplicate-auth", "MR-pipeline", "scan-running", "SBOM-swap", "scan-swap"} {
		t.Run(bad, func(t *testing.T) {
			identity, config, policy, db, scan, raw, bom, now := scanRecordFixture()
			r, e := CompleteScanRecord(identity, "", config, policy, db, scan, raw, bom, now)
			if bad == "failed-evidence" {
				r, e = CompleteScanRecord(identity, "sbom-invalid", config, policy, db, scan, nil, nil, now)
				bom = nil
			}
			if e != nil {
				t.Fatal("fixture completion failed")
			}
			record, _ := json.Marshal(r)
			w := WriterContext{r.SourceProjectID, r.ScanPipelineID, 221, r.PolicyRevision, "release-record-store"}
			a, _ := NewSourceAPI("synthetic-read-token")
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if (req.Method != "GET" && req.URL.Path != "/api/graphql") || req.URL.Host != "gitlab.com" || req.Header.Get("PRIVATE-TOKEN") != "synthetic-read-token" {
					t.Fatal("authority request target changed")
				}
				var data any
				job := func(id int64, name, status string, pipeline int64, sha string) completedJob {
					j := completedJob{ID: id, Name: name, Status: status, Ref: "main"}
					j.Commit.ID = sha
					j.Pipeline.ID = pipeline
					j.Pipeline.ProjectID = r.SourceProjectID
					j.Pipeline.SHA = sha
					return j
				}
				switch req.URL.Path {
				case "/api/v4/projects/86247025/jobs/221":
					j := job(w.JobID, "release-record-store", "running", w.PipelineID, w.Commit)
					if bad == "writer-failed" {
						j.Status = "failed"
					}
					data = j
				case "/api/v4/projects/86247025/jobs/220":
					j := job(r.ScanJobID, "oci-image-policy-validation", r.ScanJobStatus, r.ScanPipelineID, r.PolicyRevision)
					if bad == "scan-running" {
						j.Status = "running"
					}
					data = j
				case "/api/v4/projects/86247025/jobs/11":
					data = job(r.BuildJobID, "container-build-oci", "success", r.BuildPipelineID, r.SourceCommit)
				case "/api/v4/projects/86247025/pipelines/110":
					source := "api"
					if bad == "MR-pipeline" {
						source = "merge_request_event"
					}
					data = map[string]any{"id": 110, "project_id": 86247025, "sha": w.Commit, "ref": "main", "source": source}
				case "/api/v4/projects/86247025/repository/branches/main":
					sha := w.Commit
					if bad == "stale-main" {
						sha = strings.Repeat("e", 40)
					}
					data = map[string]any{"name": "main", "protected": bad != "unprotected", "commit": map[string]string{"id": sha}}
				case "/api/v4/projects/86247025/pipelines/110/jobs":
					j := job(225, "source-scan", "success", w.PipelineID, w.Commit)
					if bad == "source-scan-failed" {
						j.Status = "failed"
					}
					data = []completedJob{j, job(224, "security-policy-test", "success", w.PipelineID, w.Commit)}
				default:
					t.Fatal("unexpected authority lookup")
				}
				b, _ := json.Marshal(data)
				return response(200, string(b)), nil
			})
			c, _ := NewClient("synthetic-job-token")
			stored := map[string]string{}
			writes := []string{}
			c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("JOB-TOKEN") != "synthetic-job-token" {
					t.Fatal("store credential changed")
				}
				key := req.URL.String()
				if req.Method == "PUT" {
					if _, ok := stored[key]; ok && bad == "duplicate-auth" {
						return response(403, "PRIVATE_DIAGNOSTIC"), nil
					}
					if _, ok := stored[key]; ok && bad != "duplicate-allowed" && bad != "duplicate-exception" {
						return response(400, "private server duplicate diagnostics"), nil
					}
					b, _ := io.ReadAll(req.Body)
					stored[key] = string(b)
					writes = append(writes, key)
					return response(201, "{}"), nil
				}
				b, ok := stored[key]
				if !ok {
					return response(404, ""), nil
				}
				return response(200, b), nil
			})
			if bad == "SBOM-swap" {
				bom = append(bom, ' ')
			}
			if bad == "scan-swap" {
				scan = append(scan, ' ')
			}
			proof, e := c.StoreCompletedBundle(a, w, record, scan, bom, now)
			if bad == "duplicate-allowed" || bad == "duplicate-exception" {
				if e == nil || len(writes) != 2 || !strings.Contains(writes[0], "scan-report-") || writes[0] != writes[1] {
					t.Fatal("server accepting duplicate allowed record publication")
				}
				return
			}
			if bad == "duplicate-auth" {
				if e == nil || len(writes) != 1 || !strings.Contains(writes[0], "scan-report-") {
					t.Fatal("auth refusal treated as server duplicate denial")
				}
				return
			}
			if bad != "" && bad != "failed-evidence" {
				if e == nil || len(writes) != 0 {
					t.Fatal("invalid authority/content transmitted immutable writes")
				}
				return
			}
			expectedWrites := 3
			if bad == "failed-evidence" {
				expectedWrites = 2
				if proof.Decision != "failed" || r.Adopt(now) == nil {
					t.Fatal("stored failure became adoptable")
				}
			}
			if e != nil || len(writes) != expectedWrites || !strings.HasSuffix(writes[expectedWrites-1], fmt.Sprintf("release-record-%d-%d.json", r.ScanPipelineID, r.ScanJobID)) || proof.RecordSHA256 != Checksum(record) || !proof.RecordWrittenLast {
				t.Fatal("record not committed last")
			}
			if _, e := c.StoreCompletedBundle(a, w, record, scan, bom, now); e != nil || len(writes) != expectedWrites {
				t.Fatal("identical retry rewrote bytes")
			}
		})
	}
}
