package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryRecordOriginRequiresActualWriterBytes(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(fmt.Sprint(forged), func(t *testing.T) {
			original := fixtureRecord()
			native, _ := json.Marshal(original)
			supplied := original
			if forged {
				supplied.PolicySHA256 = strings.Repeat("0", 64)
			}
			packaged, _ := json.Marshal(supplied)
			url, _ := original.Run().URL("record")
			q := RegistryRequest{SchemaVersion: 1, OriginKind: "record", Service: original.Service, SourceCommit: original.SourceCommit, ImageDigest: original.ImageDigest, ScanPipelineID: original.ScanPipelineID, ScanJobID: original.ScanJobID, RecordURL: url, RecordSHA256: Checksum(packaged)}
			request, _ := json.Marshal(q)
			w := WriterContext{original.SourceProjectID, 500, 501, original.PolicyRevision, "image-scan"}
			a, _ := NewSourceAPI("synthetic-origin-reader")
			c, _ := NewClient("synthetic-package-reader")
			c.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.String() != url {
					t.Fatal("unbounded Package request")
				}
				return response(200, string(packaged)), nil
			})
			job := func(id int64, name, status string, pid int64, sha string) completedJob {
				j := completedJob{ID: id, Name: name, Status: status, Ref: "main"}
				j.Commit.ID = sha
				j.Pipeline.ID = pid
				j.Pipeline.ProjectID = w.ProjectID
				j.Pipeline.SHA = sha
				return j
			}
			base := fmt.Sprintf("/api/v4/projects/%d", w.ProjectID)
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" {
					t.Fatal("origin request wrote or changed host")
				}
				var value any
				switch req.URL.Path {
				case base + "/jobs/501":
					value = job(501, "image-scan", "running", 500, w.Commit)
				case base + "/pipelines/500":
					value = map[string]any{"id": 500, "project_id": w.ProjectID, "sha": w.Commit, "ref": "main", "source": "api"}
				case base + "/repository/branches/main":
					value = map[string]any{"name": "main", "protected": true, "commit": map[string]string{"id": w.Commit}}
				case base + "/pipelines/500/jobs":
					value = []completedJob{job(502, "source-scan", "success", 500, w.Commit), job(503, "security-policy-test", "success", 500, w.Commit)}
				case base + fmt.Sprintf("/jobs/%d", original.ScanJobID):
					value = job(original.ScanJobID, "image-scan", "success", original.ScanPipelineID, original.PolicyRevision)
				case base + fmt.Sprintf("/jobs/%d", original.BuildJobID):
					value = job(original.BuildJobID, "container-build-oci", "success", original.BuildPipelineID, original.SourceCommit)
				case base + fmt.Sprintf("/pipelines/%d/jobs", original.ScanPipelineID):
					value = []completedJob{job(300, "release-record-store", "success", original.ScanPipelineID, original.PolicyRevision)}
				case base + "/jobs/300/artifacts/.release-store/public/stored.json":
					value = StoredBundle{1, original.Run(), url, Checksum(native), "passed", true, true, true, true}
				case base + "/jobs/300/artifacts/.release-store/public/record.json":
					return response(200, string(native)), nil
				default:
					t.Fatalf("unexpected origin path %s", req.URL.Path)
				}
				body, _ := json.Marshal(value)
				return response(200, string(body)), nil
			})
			origin, e := ResolveRegistryOrigin(a, c, w, request, true)
			if (e == nil) == forged {
				t.Fatal("forged Package-only policy reached proven registry origin", e)
			}
			if e == nil && (origin.BuildJobID != original.BuildJobID || origin.ImageDigest != original.ImageDigest || origin.SourceCommit != original.SourceCommit) {
				t.Fatal("original identity replaced by new actor")
			}
		})
	}
}
