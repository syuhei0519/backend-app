package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInspectionProofRefusesSwappedRunAndUnsafeRuntime(t *testing.T) {
	r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
	r, e := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
	if e != nil {
		t.Fatal(e)
	}
	r.InputKind = "registry-retrieval"
	for _, kind := range []string{"format", "runtime"} {
		j := completedJob{ID: 900, Name: "registry-" + kind + "-inspection", Status: "success", Ref: "main"}
		j.Commit.ID = r.PolicyRevision
		j.Pipeline.ID = r.ScanPipelineID
		j.Pipeline.ProjectID = r.SourceProjectID
		j.Pipeline.SHA = r.PolicyRevision
		proof := map[string]any{"schemaVersion": 1, "sourceProjectId": r.SourceProjectID, "sourceCommit": r.SourceCommit, "buildPipelineId": r.BuildPipelineID, "buildJobId": r.BuildJobID, "archiveSha256": r.InputArchiveSHA256, "digest": r.ImageDigest, "registryPush": false, "phase2Adoptable": false}
		if kind == "runtime" {
			for k, v := range map[string]any{"runtimePipelineId": r.ScanPipelineID, "runtimeJobId": j.ID, "runtimeUid": 10001, "frontendReadyHttp": 200, "frontendLiveHttp": 200, "staticIndexHttp": 200, "network": "none-loopback-only", "sourceDatabaseConnected": false} {
				proof[k] = v
			}
		} else {
			for k, v := range map[string]any{"validationPipelineId": r.ScanPipelineID, "validationJobId": j.ID, "trivyVersion": "0.75.0", "craneReader": "v0.21.7", "sameDirectoryRead": true, "fullCraneLayerValidation": true, "securityGateApplied": false, "dbUpdatedAt": now.Add(-time.Hour), "reportSchemaVersion": 2, "archiveBytes": 1024, "expandedBytes": 2048} {
				proof[k] = v
			}
		}
		budget := map[string]any{"schemaVersion": 1, "kind": kind, "samples": 2, "measurementAvailable": true, "withinBudget": true, "maxWorkspaceKiB": 1024, "maxBuildkitStoreKiB": 4, "minFilesystemFreePercent": 80}
		encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
		if checkInspection(r, j, kind, encode(proof), encode(budget), now) != nil {
			t.Fatal("Valid synthetic native inspection refused")
		}
		bad := map[string]any{"sourceCommit": "other", "buildPipelineId": int64(42), "buildJobId": int64(42), "digest": "other", "archiveSha256": "other", "registryPush": true, "phase2Adoptable": true}
		if kind == "runtime" {
			for k, v := range map[string]any{"runtimeJobId": 901, "runtimePipelineId": 42, "runtimeUid": 0, "frontendReadyHttp": 500, "staticIndexHttp": 500, "network": "host", "sourceDatabaseConnected": true} {
				bad[k] = v
			}
		} else {
			for k, v := range map[string]any{"validationJobId": 901, "validationPipelineId": 42, "trivyVersion": "other", "fullCraneLayerValidation": false, "securityGateApplied": true, "dbUpdatedAt": now.Add(-25 * time.Hour), "expandedBytes": 2 << 30} {
				bad[k] = v
			}
		}
		for k, v := range bad {
			t.Run(kind+"/"+k, func(t *testing.T) {
				old := proof[k]
				proof[k] = v
				defer func() { proof[k] = old }()
				if checkInspection(r, j, kind, encode(proof), encode(budget), now) == nil {
					t.Fatal("Swapped or unsafe proof accepted")
				}
			})
		}
		for _, k := range []string{"registryPush", "phase2Adoptable"} {
			old := proof[k]
			delete(proof, k)
			if checkInspection(r, j, kind, encode(proof), encode(budget), now) == nil {
				t.Fatal("Absent negative assertion accepted")
			}
			proof[k] = old
		}
		for k, v := range map[string]any{"samples": 1, "measurementAvailable": false, "withinBudget": false, "maxWorkspaceKiB": 2097153, "maxBuildkitStoreKiB": 10485761, "minFilesystemFreePercent": 19} {
			old := budget[k]
			budget[k] = v
			if checkInspection(r, j, kind, encode(proof), encode(budget), now) == nil {
				t.Fatal("Over budget or missing measurement accepted", k)
			}
			budget[k] = old
		}
		j.Status = "failed"
		if checkInspection(r, j, kind, encode(proof), encode(budget), now) == nil {
			t.Fatal("Failed native job accepted")
		}
	}
}

func TestInspectionAPIRefusesFailedPipelineAndChangedPolicy(t *testing.T) {
	for _, bad := range []string{"", "build-push", "build-api", "build-other-pipeline", "build-wrong-job", "pipeline-failed", "pipeline-running", "push", "duplicate-job", "policy-swap", "main-advanced", "redirect", "delivery-proof-denied", "delivery-positive", "delivery-unprotected", "delivery-actor-failed", "delivery-writer-missing", "delivery-publisher-failed", "reuse-positive", "reuse-historical", "reuse-unprotected", "reuse-actor-failed", "reuse-writer-missing", "reuse-format-failed"} {
		t.Run(bad, func(t *testing.T) {
			r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
			r, e := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
			if e != nil {
				t.Fatal(e)
			}
			r.InputKind = "registry-retrieval"
			delivery := strings.HasPrefix(bad, "delivery-")
			reuse := strings.HasPrefix(bad, "reuse-")
			if bad == "reuse-historical" {
				r.SourceCommit = strings.Repeat("1", 40)
				r.ImageTag = r.SourceCommit
			}
			build := strings.HasPrefix(bad, "build-") || delivery
			if build {
				r.InputKind = "build-oci"
				r.BuildPipelineID = r.ScanPipelineID
				if bad == "build-other-pipeline" {
					r.BuildPipelineID++
				}
			}
			job := func(id int64, name string, pid int64, sha string) completedJob {
				j := completedJob{ID: id, Name: name, Status: "success", Ref: "main"}
				j.Commit.ID = sha
				j.Pipeline.ID = pid
				j.Pipeline.SHA = sha
				j.Pipeline.ProjectID = r.SourceProjectID
				return j
			}
			jobs := []completedJob{job(900, "registry-format-inspection", r.ScanPipelineID, r.PolicyRevision), job(901, "registry-runtime-inspection", r.ScanPipelineID, r.PolicyRevision), job(902, "source-scan", r.ScanPipelineID, r.PolicyRevision), job(903, "security-policy-test", r.ScanPipelineID, r.PolicyRevision)}
			if build && bad != "build-wrong-job" {
				jobs[0].Name = "oci-format-compatibility"
				jobs[1].Name = "oci-isolated-runtime"
			}
			if delivery || reuse {
				if bad != "delivery-writer-missing" && bad != "reuse-writer-missing" {
					jobs = append(jobs, job(905, "release-record-store", r.ScanPipelineID, r.PolicyRevision))
				}
				publisher := job(906, "publish-oci", r.ScanPipelineID, r.PolicyRevision)
				if bad == "delivery-publisher-failed" {
					publisher.Status = "failed"
				}
				if delivery {
					jobs = append(jobs, publisher)
				}
				if bad == "reuse-format-failed" {
					jobs[0].Status = "failed"
				}
			}
			if bad == "duplicate-job" {
				jobs = append(jobs, jobs[0])
			}
			if bad == "policy-swap" {
				jobs[0].Commit.ID = strings.Repeat("0", 40)
			}
			a, _ := NewSourceAPI("synthetic-reader")
			base := fmt.Sprintf("/api/v4/projects/%d", r.SourceProjectID)
			mainReads := 0
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" || req.Header.Get("PRIVATE-TOKEN") != "synthetic-reader" {
					t.Fatal("Changed read-only credential destination")
				}
				var data any
				switch req.URL.Path {
				case base + "/jobs/904":
					actor := job(904, "verify-oci-release", r.ScanPipelineID, r.PolicyRevision)
					actor.Status = "running"
					if reuse {
						actor.Name = "publish-oci"
					}
					if bad == "delivery-actor-failed" || bad == "reuse-actor-failed" {
						actor.Status = "failed"
					}
					data = actor
				case base + fmt.Sprintf("/jobs/%d", r.ScanJobID):
					name := "image-scan"
					if build {
						name = "oci-image-policy-validation"
					}
					data = job(r.ScanJobID, name, r.ScanPipelineID, r.PolicyRevision)
				case base + fmt.Sprintf("/jobs/%d", r.BuildJobID):
					name := "publish-image"
					if build {
						name = "container-build-oci"
					}
					data = job(r.BuildJobID, name, r.BuildPipelineID, r.SourceCommit)
				case base + "/repository/branches/main":
					mainReads++
					sha := r.PolicyRevision
					if bad == "main-advanced" && mainReads > 1 {
						sha = strings.Repeat("0", 40)
					}
					data = map[string]any{"name": "main", "protected": bad != "delivery-unprotected" && bad != "reuse-unprotected", "commit": map[string]string{"id": sha}}
				case base + fmt.Sprintf("/pipelines/%d", r.ScanPipelineID):
					kind, status := "api", "success"
					if bad == "pipeline-failed" {
						status = "failed"
					}
					if bad == "pipeline-running" || delivery || reuse {
						status = "running"
					}
					if bad == "push" {
						kind = "push"
					}
					if build && bad != "build-api" {
						kind = "push"
					}
					data = map[string]any{"id": r.ScanPipelineID, "project_id": r.SourceProjectID, "sha": r.PolicyRevision, "ref": "main", "source": kind, "status": status}
				case base + fmt.Sprintf("/pipelines/%d/jobs", r.ScanPipelineID):
					data = jobs
				default:
					if !strings.HasPrefix(req.URL.Path, base+"/jobs/900/artifacts/") && !strings.HasPrefix(req.URL.Path, base+"/jobs/901/artifacts/") {
						t.Fatal("Arbitrary artifact path", req.URL.Path)
					}
					if bad == "delivery-proof-denied" {
						return response(403, "synthetic secret response must not escape"), nil
					}
					if bad == "redirect" {
						return response(302, ""), nil
					}
					kind := "format"
					id := int64(900)
					if strings.Contains(req.URL.Path, "/901/") {
						kind = "runtime"
						id = 901
					}
					if strings.HasSuffix(req.URL.Path, "disk.json") {
						data = map[string]any{"schemaVersion": 1, "kind": kind, "samples": 2, "measurementAvailable": true, "withinBudget": true, "maxWorkspaceKiB": 1024, "maxBuildkitStoreKiB": 4, "minFilesystemFreePercent": 80}
					} else {
						p := map[string]any{"schemaVersion": 1, "sourceProjectId": r.SourceProjectID, "sourceCommit": r.SourceCommit, "buildPipelineId": r.BuildPipelineID, "buildJobId": r.BuildJobID, "archiveSha256": r.InputArchiveSHA256, "digest": r.ImageDigest, "registryPush": false, "phase2Adoptable": false}
						if kind == "runtime" {
							for k, v := range map[string]any{"runtimePipelineId": r.ScanPipelineID, "runtimeJobId": id, "runtimeUid": 10001, "frontendReadyHttp": 200, "frontendLiveHttp": 200, "staticIndexHttp": 200, "network": "none-loopback-only", "sourceDatabaseConnected": false} {
								p[k] = v
							}
						} else {
							for k, v := range map[string]any{"validationPipelineId": r.ScanPipelineID, "validationJobId": id, "trivyVersion": "0.75.0", "craneReader": "v0.21.7", "sameDirectoryRead": true, "fullCraneLayerValidation": true, "securityGateApplied": false, "dbUpdatedAt": now.Add(-time.Hour), "reportSchemaVersion": 2, "archiveBytes": 1024, "expandedBytes": 2048} {
								p[k] = v
							}
						}
						data = p
					}
				}
				b, _ := json.Marshal(data)
				return response(200, string(b)), nil
			})
			accepted := a.ReadConsumerInspection(r, now) == nil
			if delivery {
				err := a.ReadDeliveryInspection(WriterContext{r.SourceProjectID, r.ScanPipelineID, 904, r.PolicyRevision, "verify-oci-release"}, r, now)
				accepted = err == nil
				if bad == "delivery-proof-denied" && (InspectionFailureStage(err) != "inspection-format-proof-read" || !errors.Is(err, ErrRefused) || strings.Contains(err.Error(), "secret")) {
					t.Fatal("Unsafe or inaccurate inspection diagnostic")
				}
			}
			if reuse {
				accepted = a.ValidateRegistryReuse(WriterContext{r.SourceProjectID, r.ScanPipelineID, 904, r.PolicyRevision, "publish-oci"}, r, now) == nil
			}
			if accepted != (bad == "" || bad == "build-push" || bad == "build-api" || bad == "delivery-positive" || bad == "reuse-positive") {
				t.Fatal("Native inspection API authority wrong")
			}
		})
	}
}
