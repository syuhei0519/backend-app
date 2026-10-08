package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDeliveryReadsActualPublisherAndRefusesSwappedProof(t *testing.T) {
	for _, bad := range []string{"", "failed", "duplicate", "missing", "wrong-source", "wrong-run", "wrong-hash", "missing-negative", "missing-mode", "reused-as-build", "alias", "redirect", "403", "reuse-positive", "reuse-published-mode"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			r.PolicyRevision = r.SourceCommit
			if strings.HasPrefix(bad, "reuse-") {
				r.InputKind = "registry-retrieval"
			}
			hash := strings.Repeat("b", 64)
			a, _ := NewSourceAPI("fixture-readonly")
			base := fmt.Sprintf("/api/v4/projects/%d", r.SourceProjectID)
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" || req.Header.Get("PRIVATE-TOKEN") != "fixture-readonly" {
					t.Fatal("publisher proof read changed authority destination")
				}
				var data any
				switch req.URL.Path {
				case base + fmt.Sprintf("/pipelines/%d/jobs", r.ScanPipelineID):
					j := completedJob{ID: 900, Name: "publish-oci", Status: "success", Ref: "main"}
					j.Commit.ID = r.PolicyRevision
					j.Pipeline.ID = r.ScanPipelineID
					j.Pipeline.ProjectID = r.SourceProjectID
					j.Pipeline.SHA = r.PolicyRevision
					if bad == "failed" {
						j.Status = "failed"
					}
					if bad == "wrong-source" {
						j.Commit.ID = strings.Repeat("0", 40)
					}
					jobs := []completedJob{j}
					if bad == "duplicate" {
						jobs = append(jobs, j)
					}
					if bad == "missing" {
						jobs = nil
					}
					data = jobs
				case base + "/jobs/900/artifacts/.oci-publish/public/published.json":
					p := map[string]any{"schemaVersion": 1, "imageDigest": r.ImageDigest, "sourceCommit": r.SourceCommit, "scanPipelineId": r.ScanPipelineID, "scanJobId": r.ScanJobID, "sameLayoutPublished": true, "registryReused": false, "remoteDigestMatched": true, "recordSha256": hash, "proposalAuthorized": false}
					if strings.HasPrefix(bad, "reuse-") {
						p["sameLayoutPublished"] = false
						p["registryReused"] = true
					}
					if bad == "reuse-published-mode" {
						p["sameLayoutPublished"] = true
						p["registryReused"] = false
					}
					if bad == "wrong-run" {
						p["scanJobId"] = r.ScanJobID + 1
					}
					if bad == "wrong-hash" {
						p["recordSha256"] = strings.Repeat("0", 64)
					}
					if bad == "missing-negative" {
						delete(p, "proposalAuthorized")
					}
					if bad == "missing-mode" {
						delete(p, "registryReused")
					}
					if bad == "reused-as-build" {
						p["registryReused"] = true
					}
					if bad == "alias" {
						delete(p, "recordSha256")
						p["RecordSHA256"] = hash
					}
					data = p
				default:
					t.Fatal("unexpected publisher proof endpoint")
				}
				status := 200
				if bad == "redirect" {
					status = 302
				}
				if bad == "403" {
					status = 403
				}
				b, _ := json.Marshal(data)
				return response(status, string(b)), nil
			})
			if (a.ReadPublishedIdentity(r, hash) == nil) != (bad == "" || bad == "reuse-positive") {
				t.Fatal("publisher proof acceptance wrong")
			}
		})
	}
}
