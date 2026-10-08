package release

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPhase0RegistryOriginRequiresActualBuildNotReusedTag(t *testing.T) {
	for _, bad := range []string{"", "reused-empty-metadata", "missing-metadata", "digest-swap", "missing-config", "build-failed", "build-source", "MR-pipeline", "pipeline-failed", "redirect"} {
		t.Run(bad, func(t *testing.T) {
			o := LegacyRegistryOrigin{"frontend", 86247025, strings.Repeat("a", 40), 10, 11, "sha256:" + strings.Repeat("b", 64)}
			a, _ := NewSourceAPI("synthetic-reader")
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" {
					t.Fatal("legacy provenance changed target or wrote data")
				}
				var data any
				status := 200
				switch req.URL.Path {
				case "/api/v4/projects/86247025/jobs/11":
					j := completedJob{ID: 11, Name: "publish-image", Status: "success", Ref: "main"}
					j.Commit.ID = o.SourceCommit
					j.Pipeline.ID = 10
					j.Pipeline.ProjectID = o.SourceProjectID
					j.Pipeline.SHA = o.SourceCommit
					if bad == "build-failed" {
						j.Status = "failed"
					}
					if bad == "build-source" {
						j.Commit.ID = strings.Repeat("c", 40)
					}
					data = j
				case "/api/v4/projects/86247025/pipelines/10":
					source := "push"
					state := "success"
					if bad == "MR-pipeline" {
						source = "merge_request_event"
					}
					if bad == "pipeline-failed" {
						state = "failed"
					}
					data = map[string]any{"id": 10, "project_id": 86247025, "sha": o.SourceCommit, "ref": "main", "status": state, "source": source}
				case "/api/v4/projects/86247025/jobs/11/artifacts/image-metadata.json":
					if bad == "reused-empty-metadata" {
						return response(200, ""), nil
					}
					if bad == "missing-metadata" {
						status = 404
					}
					m := map[string]string{"containerimage.digest": o.ImageDigest, "containerimage.config.digest": "sha256:" + strings.Repeat("c", 64)}
					if bad == "digest-swap" {
						m["containerimage.digest"] = "sha256:" + strings.Repeat("d", 64)
					}
					if bad == "missing-config" {
						delete(m, "containerimage.config.digest")
					}
					data = m
				default:
					t.Fatal("arbitrary artifact path read")
				}
				if bad == "redirect" {
					status = 302
				}
				b, _ := json.Marshal(data)
				return response(status, string(b)), nil
			})
			if e := a.ValidateLegacyRegistryBuild(o); (e == nil) != (bad == "") {
				t.Fatal("reused, missing or unproven original build accepted")
			}
		})
	}
}

func TestLegacyBindingNeverFabricatesPastScan(t *testing.T) {
	r := fixtureRecord()
	o := LegacyRegistryOrigin{r.Service, r.SourceProjectID, r.SourceCommit, r.BuildPipelineID, r.BuildJobID, r.ImageDigest}
	c := ScanContext{r.SourceProjectID, 500, 501, strings.Repeat("9", 40), "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
	archive := strings.Repeat("8", 64)
	p, _ := json.Marshal(layoutIdentity{1, o.SourceCommit, o.SourceProjectID, archive, 123, 456, o.ImageDigest, 1, "linux/amd64", false})
	reader, _ := json.Marshal(map[string]any{"schemaVersion": 1, "reader": "crane-v0.21.7-layout-reader", "digest": o.ImageDigest, "configDigest": "sha256:" + strings.Repeat("7", 64), "fullLayerValidation": true, "registryPush": false})
	got, _, e := BindLegacyRegistryIdentity(c, o, p, p, reader, archive, 123)
	if e != nil || got.BuildJobID != o.BuildJobID || got.SourceCommit != o.SourceCommit || got.PolicyRevision != c.Commit || got.ScanJobID != c.JobID || got.InputArchiveSHA256 != archive || got.InputKind != "registry-retrieval" {
		t.Fatal("legacy build/current scan binding missing")
	}
	if got.SBOM != nil || got.Database != nil || got.ScanReport != nil || got.Decision != "" || got.ValidateStored() == nil {
		t.Fatal("invented legacy security evidence")
	}
	o.ImageDigest = "sha256:" + strings.Repeat("0", 64)
	if _, _, e := BindLegacyRegistryIdentity(c, o, p, p, reader, archive, 123); e == nil {
		t.Fatal("different registry digest accepted")
	}
}

func TestHistoricalMissingInputNeverInventsBuildOrPastSafety(t *testing.T) {
	r := fixtureRecord()
	q := RegistryRequest{SchemaVersion: 1, OriginKind: "legacy", Service: r.Service, SourceCommit: r.SourceCommit, ImageDigest: r.ImageDigest, BuildPipelineID: r.BuildPipelineID, BuildJobID: r.BuildJobID}
	b, _ := json.Marshal(q)
	c := ScanContext{r.SourceProjectID, 500, 501, strings.Repeat("9", 40), "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
	if _, e := FailedRegistryInput(c, b); e == nil {
		t.Fatal("ordinary input accepted historical source")
	}
	got, e := FailedHistoricalRegistryInput(c, b)
	if e != nil || got.SourceCommit != q.SourceCommit || got.PolicyRevision != c.Commit || got.BuildPipelineID != 0 || got.BuildJobID != 0 || got.InputArchiveSHA256 != "" || got.Database != nil || got.SBOM != nil || got.Decision != "" {
		t.Fatal("historical unavailable input invented origin or safety")
	}
}
