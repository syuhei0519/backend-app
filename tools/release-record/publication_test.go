package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPublicationRequiresCurrentSuccessfulBytesAndActualOCI(t *testing.T) {
	identity, config, policy, db, scan, raw, bom, now := scanRecordFixture()
	identity.Service, identity.SourceProjectID, identity.ImageRepository = "backend", 86247033, "registry.gitlab.com/syuhei-platform-engineering-lab/backend-app"
	raw = []byte(strings.ReplaceAll(string(raw), "/frontend-app", "/backend-app"))
	r, e := CompleteScanRecord(identity, "", config, policy, db, scan, raw, bom, now)
	if e != nil {
		t.Fatal(e)
	}
	record, _ := json.Marshal(r)
	u, _ := r.Run().URL("record")
	p, _ := json.Marshal(layoutIdentity{1, r.SourceCommit, r.SourceProjectID, r.InputArchiveSHA256, 123, 456, r.ImageDigest, 1, "linux/amd64", false})
	reader := []byte(fmt.Sprintf(`{"schemaVersion":1,"reader":"crane-v0.21.7-layout-reader","digest":"%s","configDigest":"%s","fullLayerValidation":true,"registryPush":false}`, r.ImageDigest, config))
	if _, e = CheckPublicationInput(record, scan, bom, u, Checksum(record), p, p, reader, r.InputArchiveSHA256, 123, now); e != nil {
		t.Fatal("actual successful input refused")
	}
	for _, bad := range []string{"record-hash", "record-run", "SBOM", "archive", "layout", "expired", "failed", "historical-source"} {
		t.Run(bad, func(t *testing.T) {
			b, s, h, url, archive, proof, at := record, bom, Checksum(record), u, r.InputArchiveSHA256, p, now
			switch bad {
			case "record-hash":
				h = strings.Repeat("0", 64)
			case "record-run":
				url = strings.Replace(url, "-110-220", "-110-221", 1)
			case "SBOM":
				s = []byte(`{}`)
			case "archive":
				archive = strings.Repeat("0", 64)
			case "layout":
				proof = []byte(strings.Replace(string(p), r.ImageDigest, "sha256:"+strings.Repeat("0", 64), 1))
			case "expired":
				at = now.Add(25 * time.Hour)
			case "failed":
				failed, err := CompleteScanRecord(identity, "sbom-unavailable", config, policy, db, scan, nil, nil, now)
				if err != nil {
					t.Fatal(err)
				}
				b, _ = json.Marshal(failed)
				h = Checksum(b)
				s = nil
			case "historical-source":
				old := r
				old.PolicyRevision = strings.Repeat("9", 40)
				b, _ = json.Marshal(old)
				h = Checksum(b)
			}
			if _, err := CheckPublicationInput(b, scan, s, url, h, p, proof, reader, archive, 123, at); err == nil {
				t.Fatal("unbound or failed publication input accepted")
			}
		})
	}
}

func TestPublisherUsesLiveProtectedMainAndCompletedWriter(t *testing.T) {
	for _, bad := range []string{"", "unprotected", "stale-main", "MR-pipeline", "publisher-failed", "scan-running", "build-failed", "writer-failed", "writer-missing", "writer-duplicate", "source-scan-failed", "format-failed", "runtime-failed", "403", "redirect", "old-source"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			r.Service, r.SourceProjectID, r.ImageRepository = "backend", 86247033, "registry.gitlab.com/syuhei-platform-engineering-lab/backend-app"
			r.SBOM.URL, _ = r.Run().URL("sbom")
			r.ScanReport.URL, _ = r.Run().URL("scan")
			r.PolicyRevision = r.SourceCommit
			w := WriterContext{r.SourceProjectID, r.ScanPipelineID, 223, r.PolicyRevision, "publish-oci"}
			if bad == "old-source" {
				r.SourceCommit = strings.Repeat("9", 40)
				r.ImageTag = r.SourceCommit
			}
			a, _ := NewSourceAPI("synthetic-read-token")
			calls := 0
			job := func(id int64, name, status string, pipeline int64, sha string) completedJob {
				j := completedJob{ID: id, Name: name, Status: status, Ref: "main"}
				j.Commit.ID = sha
				j.Pipeline.ID = pipeline
				j.Pipeline.SHA = sha
				j.Pipeline.ProjectID = r.SourceProjectID
				return j
			}
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "GET" || req.URL.Host != "gitlab.com" || req.URL.Scheme != "https" {
					t.Fatal("publisher authority wrote or changed host")
				}
				var data any
				switch req.URL.Path {
				case "/api/v4/projects/86247033/jobs/220":
					j := job(220, "oci-image-policy-validation", "success", 110, r.PolicyRevision)
					if bad == "scan-running" {
						j.Status = "running"
					}
					data = j
				case "/api/v4/projects/86247033/jobs/11":
					j := job(11, "container-build-oci", "success", 10, r.SourceCommit)
					if bad == "build-failed" {
						j.Status = "failed"
					}
					data = j
				case "/api/v4/projects/86247033/jobs/223":
					j := job(223, "publish-oci", "running", 110, w.Commit)
					if bad == "publisher-failed" {
						j.Status = "failed"
					}
					data = j
				case "/api/v4/projects/86247033/pipelines/110":
					source := "api"
					if bad == "MR-pipeline" {
						source = "merge_request_event"
					}
					data = map[string]any{"id": 110, "project_id": 86247033, "sha": w.Commit, "ref": "main", "source": source}
				case "/api/v4/projects/86247033/repository/branches/main":
					sha := w.Commit
					if bad == "stale-main" {
						sha = strings.Repeat("8", 40)
					}
					data = map[string]any{"name": "main", "protected": bad != "unprotected", "commit": map[string]string{"id": sha}}
				case "/api/v4/projects/86247033/pipelines/110/jobs":
					jobs := []completedJob{job(224, "source-scan", "success", 110, w.Commit), job(225, "security-policy-test", "success", 110, w.Commit), job(226, "oci-format-compatibility", "success", 110, w.Commit), job(227, "oci-isolated-runtime", "success", 110, w.Commit)}
					writer := job(221, "release-record-store", "success", 110, w.Commit)
					if bad == "writer-failed" {
						writer.Status = "failed"
					}
					if bad == "source-scan-failed" {
						jobs[0].Status = "failed"
					}
					if bad == "format-failed" {
						jobs[2].Status = "failed"
					}
					if bad == "runtime-failed" {
						jobs[3].Status = "failed"
					}
					if bad != "writer-missing" {
						jobs = append(jobs, writer)
					}
					if bad == "writer-duplicate" {
						jobs = append(jobs, writer)
					}
					data = jobs
				default:
					t.Fatal("unexpected authority path")
				}
				status := 200
				if bad == "403" {
					status = 403
				}
				if bad == "redirect" {
					status = 302
				}
				b, _ := json.Marshal(data)
				return response(status, string(b)), nil
			})
			if err := a.ValidatePublisher(w, r); (err == nil) != (bad == "") {
				t.Fatal("publisher/main/job authority decision wrong")
			}
			if (bad == "403" || bad == "redirect") && calls != 1 {
				t.Fatal("refusal was followed")
			}
			if bad == "old-source" && calls != 0 {
				t.Fatal("rollback obtained publisher lookup")
			}
		})
	}
}
