package release

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyResolveChecksLiveActorAndNeverGrantsHistoricalAdoption(t *testing.T) {
	for _, bad := range []string{"", "historical-not-enabled", "unprotected", "stale-main", "origin-digest", "missing-metadata", "actor-ref", "historical-from-push"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			q := RegistryRequest{SchemaVersion: 1, OriginKind: "legacy", Service: r.Service, SourceCommit: r.SourceCommit, ImageDigest: r.ImageDigest, BuildPipelineID: 10, BuildJobID: 11}
			b, _ := json.Marshal(q)
			w := WriterContext{86247025, 500, 501, strings.Repeat("9", 40), "image-scan"}
			a, _ := NewSourceAPI("synthetic-source-reader")
			metadataReads := 0
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "gitlab.com" || req.Method != "GET" {
					t.Fatal("origin resolve wrote or changed target")
				}
				job := func(id int64, name, status string, pipeline int64, sha string) completedJob {
					j := completedJob{ID: id, Name: name, Status: status, Ref: "main"}
					j.Commit.ID = sha
					j.Pipeline.ID = pipeline
					j.Pipeline.SHA = sha
					j.Pipeline.ProjectID = 86247025
					return j
				}
				var data any
				status := 200
				switch req.URL.Path {
				case "/api/v4/projects/86247025/jobs/501":
					j := job(501, "image-scan", "running", 500, w.Commit)
					if bad == "actor-ref" {
						j.Ref = "codex/unprotected"
					}
					data = j
				case "/api/v4/projects/86247025/pipelines/500":
					kind := "api"
					if bad == "historical-from-push" {
						kind = "push"
					}
					data = map[string]any{"id": 500, "project_id": 86247025, "sha": w.Commit, "ref": "main", "source": kind}
				case "/api/v4/projects/86247025/repository/branches/main":
					sha := w.Commit
					if bad == "stale-main" {
						sha = strings.Repeat("8", 40)
					}
					data = map[string]any{"name": "main", "protected": bad != "unprotected", "commit": map[string]string{"id": sha}}
				case "/api/v4/projects/86247025/pipelines/500/jobs":
					data = []completedJob{job(502, "source-scan", "success", 500, w.Commit), job(503, "security-policy-test", "success", 500, w.Commit)}
				case "/api/v4/projects/86247025/jobs/11":
					data = job(11, "publish-image", "success", 10, r.SourceCommit)
				case "/api/v4/projects/86247025/pipelines/10":
					data = map[string]any{"id": 10, "project_id": 86247025, "sha": r.SourceCommit, "ref": "main", "source": "push", "status": "success"}
				case "/api/v4/projects/86247025/jobs/11/artifacts/image-metadata.json":
					metadataReads++
					digest := r.ImageDigest
					if bad == "origin-digest" {
						digest = "sha256:" + strings.Repeat("0", 64)
					}
					if bad == "missing-metadata" {
						status = 404
					}
					data = map[string]any{"containerimage.digest": digest, "containerimage.config.digest": "sha256:" + strings.Repeat("8", 64)}
				default:
					t.Fatal("unbounded selector path")
				}
				body, _ := json.Marshal(data)
				return response(status, string(body)), nil
			})
			origin, e := ResolveRegistryOrigin(a, nil, w, b, bad != "historical-not-enabled")
			if (e == nil) != (bad == "") {
				t.Fatal("actor/origin authority decision wrong")
			}
			if bad == "" && (origin.SourceCommit != r.SourceCommit || origin.BuildJobID != 11 || metadataReads != 1) {
				t.Fatal("historical build was replaced by current scan")
			}
			if (bad == "historical-not-enabled" || bad == "unprotected" || bad == "stale-main" || bad == "actor-ref") && metadataReads != 0 {
				t.Fatal("untrusted actor reached legacy artifact")
			}
		})
	}
}

func TestRegistryRequestSeparatesOriginsAndRejectsAmbiguousSelectors(t *testing.T) {
	r := fixtureRecord()
	url, _ := r.Run().URL("record")
	legacy := RegistryRequest{SchemaVersion: 1, OriginKind: "legacy", Service: r.Service, SourceCommit: r.SourceCommit, ImageDigest: r.ImageDigest, BuildPipelineID: r.BuildPipelineID, BuildJobID: r.BuildJobID}
	record := RegistryRequest{SchemaVersion: 1, OriginKind: "record", Service: r.Service, SourceCommit: r.SourceCommit, ImageDigest: r.ImageDigest, ScanPipelineID: r.ScanPipelineID, ScanJobID: r.ScanJobID, RecordURL: url, RecordSHA256: strings.Repeat("8", 64)}
	for _, q := range []RegistryRequest{legacy, record} {
		b, _ := json.Marshal(q)
		if _, e := DecodeRegistryRequest(b); e != nil {
			t.Fatal("canonical request refused")
		}
	}
	for _, change := range []func(*RegistryRequest){
		func(q *RegistryRequest) { q.SchemaVersion = 2 }, func(q *RegistryRequest) { q.Service = "other" }, func(q *RegistryRequest) { q.SourceCommit = "main" }, func(q *RegistryRequest) { q.BuildJobID = 11 }, func(q *RegistryRequest) { q.RecordURL = "https://external.invalid/record.json" }, func(q *RegistryRequest) { q.ScanJobID++ }, func(q *RegistryRequest) { q.RecordSHA256 = "" },
	} {
		bad := record
		change(&bad)
		b, _ := json.Marshal(bad)
		if _, e := DecodeRegistryRequest(b); e == nil {
			t.Fatal("ambiguous run/legacy/URL accepted")
		}
	}
	b, _ := json.Marshal(legacy)
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":1,"SchemaVersion":2`, 1)),
		[]byte(strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":2`, 1)),
		[]byte(strings.Replace(string(b), `"recordUrl":""`, `"recordUrl":null`, 1)),
		append(b, []byte(` {}`)...), []byte(strings.Repeat(" ", 4097)),
	} {
		if _, e := DecodeRegistryRequest(bad); e == nil {
			t.Fatal("ambiguous, null, trailing or oversized request accepted")
		}
	}
}
