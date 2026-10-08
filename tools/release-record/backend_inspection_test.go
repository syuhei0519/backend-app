package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackendInspectionRequiresDatabaseReadinessRefusal(t *testing.T) {
	r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
	r, err := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
	if err != nil {
		t.Fatal(err)
	}
	// This is a controlled contract fixture, not an actual backend scan claim.
	b, _ := json.Marshal(r)
	b = []byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(b), "frontend", "backend"), "86247025", "86247033"), "build-oci", "registry-retrieval"))
	if json.Unmarshal(b, &r) != nil || r.Adopt(now) != nil {
		t.Fatal("Backend fixture identity refused")
	}
	j := completedJob{ID: 900, Name: "registry-runtime-inspection", Status: "success", Ref: "main"}
	j.Commit.ID = r.PolicyRevision
	j.Pipeline.ID, j.Pipeline.ProjectID, j.Pipeline.SHA = r.ScanPipelineID, r.SourceProjectID, r.PolicyRevision
	p := map[string]any{"schemaVersion": 1, "sourceProjectId": r.SourceProjectID, "sourceCommit": r.SourceCommit, "buildPipelineId": r.BuildPipelineID, "buildJobId": r.BuildJobID, "archiveSha256": r.InputArchiveSHA256, "digest": r.ImageDigest, "registryPush": false, "phase2Adoptable": false, "runtimePipelineId": r.ScanPipelineID, "runtimeJobId": j.ID, "runtimeUid": 10001, "backendReadyHttp": 503, "backendLiveHttp": 200, "databaseReadinessRefused": true, "network": "none-loopback-only", "sourceDatabaseConnected": false}
	budget := map[string]any{"schemaVersion": 1, "kind": "runtime", "samples": 2, "measurementAvailable": true, "withinBudget": true, "maxWorkspaceKiB": 1024, "maxBuildkitStoreKiB": 4, "minFilesystemFreePercent": 80}
	encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
	if checkInspection(r, j, "runtime", encode(p), encode(budget), now) != nil {
		t.Fatal("Valid backend isolation refused")
	}
	for key, bad := range map[string]any{"backendReadyHttp": 200, "backendLiveHttp": 503, "databaseReadinessRefused": false, "frontendReadyHttp": 200, "frontendLiveHttp": 200, "staticIndexHttp": 200, "sourceDatabaseConnected": true, "runtimeUid": 0, "network": "host", "runtimeJobId": 901, "sourceProjectId": int64(86247025)} {
		t.Run(key, func(t *testing.T) {
			old, present := p[key]
			p[key] = bad
			defer func() {
				if present {
					p[key] = old
				} else {
					delete(p, key)
				}
			}()
			if checkInspection(r, j, "runtime", encode(p), encode(budget), now) == nil {
				t.Fatal("Unsafe or foreign backend proof accepted")
			}
		})
	}
	for _, key := range []string{"backendReadyHttp", "backendLiveHttp", "databaseReadinessRefused", "sourceDatabaseConnected"} {
		old := p[key]
		delete(p, key)
		if checkInspection(r, j, "runtime", encode(p), encode(budget), now) == nil {
			t.Fatal("Required backend assertion absent", key)
		}
		p[key] = old
	}
}
