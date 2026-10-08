package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryInspectionRejectsUntrustedActorsAndScanByteSwaps(t *testing.T) {
	for _, bad := range []string{"", "publisher", "historical", "historical-explicit", "historical-push", "unprotected", "stale-main", "actor-ref", "scan-running", "native-byte-swap", "redirect", "missing-native", "failed-prerequisite", "scan-job-swap", "token-job", "token-project", "token-pipeline", "token-sha", "token-ref", "token-name", "token-status", "token-forbidden", "artifact-forbidden", "failed-writer", "writer-byte-swap", "writer-package-only"} {
		t.Run(bad, func(t *testing.T) {
			r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
			r, e := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
			if e != nil {
				t.Fatal(e)
			}
			r.PolicyRevision = r.SourceCommit
			if bad == "historical-explicit" || bad == "historical-push" {
				r.PolicyRevision = strings.Repeat("9", 40)
			}
			r.InputKind = "registry-retrieval"
			native, _ := json.Marshal(r)
			w := WriterContext{r.SourceProjectID, r.ScanPipelineID, 900, r.PolicyRevision, "registry-runtime-inspection"}
			if bad == "publisher" {
				w.JobName = "publish-oci"
			}
			if bad == "historical" {
				w.Commit = strings.Repeat("9", 40)
			}
			a, _ := NewSourceAPI("synthetic-inspection-reader")
			c, _ := NewClient("synthetic-native-job")
			nativeReads := 0
			job := func(id int64, name, status string, pid int64, sha string) completedJob {
				j := completedJob{ID: id, Name: name, Status: status, Ref: "main"}
				j.Commit.ID = sha
				j.Pipeline.ID = pid
				j.Pipeline.ProjectID = r.SourceProjectID
				j.Pipeline.SHA = sha
				return j
			}
			base := fmt.Sprintf("/api/v4/projects/%d", r.SourceProjectID)
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" {
					t.Fatal("Inspection wrote or changed host")
				}
				var data any
				switch req.URL.Path {
				case "/api/v4/job":
					if req.Header.Get("JOB-TOKEN") != "synthetic-native-job" || req.Header.Get("PRIVATE-TOKEN") != "" {
						t.Fatal("Wrong principal token")
					}
					if bad == "token-forbidden" {
						return response(403, ""), nil
					}
					j := job(900, w.JobName, "running", w.PipelineID, w.Commit)
					switch bad {
					case "token-job":
						j.ID++
					case "token-project":
						j.Pipeline.ProjectID++
					case "token-pipeline":
						j.Pipeline.ID++
					case "token-sha":
						j.Commit.ID = strings.Repeat("0", 40)
					case "token-ref":
						j.Ref = "other"
					case "token-name":
						j.Name = "other"
					case "token-status":
						j.Status = "success"
					}
					data = j
				case base + "/jobs/900":
					j := job(900, w.JobName, "running", w.PipelineID, w.Commit)
					if bad == "actor-ref" {
						j.Ref = "codex/unprotected"
					}
					data = j
				case base + fmt.Sprintf("/jobs/%d", r.ScanJobID):
					j := job(r.ScanJobID, "image-scan", "success", r.ScanPipelineID, r.PolicyRevision)
					if bad == "scan-running" {
						j.Status = "running"
					}
					if bad == "scan-job-swap" {
						j.Name = "oci-image-policy-validation"
					}
					data = j
				case base + fmt.Sprintf("/jobs/%d", r.BuildJobID):
					data = job(r.BuildJobID, "publish-image", "success", r.BuildPipelineID, r.SourceCommit)
				case base + fmt.Sprintf("/pipelines/%d", w.PipelineID):
					kind := "api"
					if bad == "historical-push" {
						kind = "push"
					}
					data = map[string]any{"id": w.PipelineID, "project_id": w.ProjectID, "sha": w.Commit, "ref": "main", "source": kind}
				case base + "/repository/branches/main":
					sha := w.Commit
					if bad == "stale-main" {
						sha = strings.Repeat("8", 40)
					}
					data = map[string]any{"name": "main", "protected": bad != "unprotected", "commit": map[string]string{"id": sha}}
				case base + fmt.Sprintf("/pipelines/%d/jobs", w.PipelineID):
					scanStatus := "success"
					if bad == "failed-prerequisite" {
						scanStatus = "failed"
					}
					writerStatus := "success"
					if bad == "failed-writer" {
						writerStatus = "failed"
					}
					data = []completedJob{job(901, "source-scan", "success", w.PipelineID, w.Commit), job(902, "security-policy-test", "success", w.PipelineID, w.Commit), job(r.ScanJobID, "image-scan", scanStatus, w.PipelineID, w.Commit), job(903, "release-record-store", writerStatus, w.PipelineID, w.Commit)}
				case base + "/jobs/903/artifacts/.release-store/public/stored.json":
					if req.Header.Get("PRIVATE-TOKEN") != "synthetic-inspection-reader" || req.Header.Get("JOB-TOKEN") != "" {
						t.Fatal("Wrong safe writer credential")
					}
					url, _ := r.Run().URL("record")
					sha := Checksum(native)
					if bad == "writer-package-only" {
						sha = strings.Repeat("0", 64)
					}
					data = StoredBundle{1, r.Run(), url, sha, r.Decision, true, true, true, true}
				case base + "/jobs/903/artifacts/.release-store/public/record.json":
					nativeReads++
					if req.Header.Get("PRIVATE-TOKEN") != "synthetic-inspection-reader" || req.Header.Get("JOB-TOKEN") != "" {
						t.Fatal("Safe writer token differs")
					}
					if bad == "artifact-forbidden" {
						return response(403, ""), nil
					}
					if bad == "redirect" {
						return response(302, ""), nil
					}
					if bad == "missing-native" {
						return response(404, ""), nil
					}
					body := string(native)
					if bad == "native-byte-swap" || bad == "writer-byte-swap" {
						body += "\n"
					}
					return response(200, body), nil
				default:
					t.Fatalf("Unexpected inspection path %s", req.URL.Path)
				}
				body, _ := json.Marshal(data)
				return response(200, string(body)), nil
			})
			c.http.Transport = a.http.Transport
			o, got, e := ResolveInspectedRegistryOrigin(a, c, w, native, now)
			if bad == "historical-explicit" || bad == "historical-push" {
				o, got, e = ResolveHistoricalInspectedRegistryOrigin(a, c, w, native, now)
			}
			valid := bad == "" || bad == "historical-explicit"
			if (e == nil) != valid {
				t.Fatal("Inspection authority decision wrong", e)
			}
			if valid && (nativeReads != 2 || o.ImageDigest != r.ImageDigest || o.BuildJobID != r.BuildJobID || got.Run() != r.Run()) {
				t.Fatal("Original build/current scan not retained")
			}
			if (bad == "publisher" || bad == "historical" || bad == "unprotected" || bad == "stale-main" || bad == "actor-ref" || bad == "failed-prerequisite") && nativeReads != 0 {
				t.Fatal("Untrusted inspection reached artifact")
			}
		})
	}
}
