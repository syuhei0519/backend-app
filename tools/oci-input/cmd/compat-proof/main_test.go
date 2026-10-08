package main

import (
	oci "core-platform/oci-input"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 10, 3, 3, 0, 0, 0, time.UTC)

func write(t *testing.T, p string, x any) {
	t.Helper()
	b, e := json.Marshal(x)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func setup(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("CI_PROJECT_ID", "86247033")
	t.Setenv("CI_PIPELINE_ID", "123")
	t.Setenv("CI_JOB_ID", "456")
	t.Setenv("CI_COMMIT_SHA", strings.Repeat("a", 40))
	p := oci.Proof{SchemaVersion: 1, SourceCommit: strings.Repeat("a", 40), SourceProject: 86247033, ArchiveSHA256: strings.Repeat("b", 64), ArchiveBytes: 1024, ExpandedBytes: 4096, Digest: "sha256:" + strings.Repeat("c", 64), LayerCount: 1, Platform: "linux/amd64"}
	write(t, ".oci/rechecked-proof.json", p)
	write(t, ".oci-private/crane.json", map[string]any{"schemaVersion": 1, "reader": "crane-v0.21.7-layout-reader", "digest": p.Digest, "fullLayerValidation": true, "registryPush": false})
	write(t, ".oci-private/version.json", map[string]any{"Version": "0.75.0"})
	write(t, ".cache/trivy/db/metadata.json", map[string]any{"Version": 2, "UpdatedAt": at.Add(-time.Hour)})
	write(t, ".oci-private/report.json", map[string]any{"SchemaVersion": 2, "ArtifactType": "container_image", "ArtifactName": "untrusted-input-name", "Results": []any{map[string]any{"Secrets": []any{map[string]any{"Match": "raw-content-must-never-appear", "Title": "untrusted-title"}}, "Vulnerabilities": []any{map[string]any{"PkgName": "untrusted-package"}}}}})
	if e := os.WriteFile(".oci/build-input.txt", []byte("sourceCommit="+p.SourceCommit+"\nsourceProjectId=86247033\nbuildPipelineId=123\nbuildJobId=111\nregistryPush=false\nregistryCache=false\n"), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCompatibilityProofNeverLeaksFindingsOrClaimsRelease(t *testing.T) {
	setup(t)
	p, e := proof(at)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p)
	for _, secret := range []string{"raw-content-must-never-appear", "untrusted-title", "untrusted-package", "untrusted-input-name"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("raw scanner data escaped")
		}
	}
	if p["phase2Adoptable"] != false || p["securityGateApplied"] != false || p["registryPush"] != false || p["buildJobId"] != int64(111) || p["validationJobId"] != int64(456) || p["secretCount"] != 1 {
		t.Fatal("compatibility scope or run binding lost")
	}
}

func TestRegistryInspectionBindsOriginalBuildAndCurrentJob(t *testing.T) {
	setup(t)
	t.Setenv("OCI_REGISTRY_INSPECTION_ENABLED", "true")
	t.Setenv("OCI_REGISTRY_RETRIEVAL_ENABLED", "true")
	t.Setenv("CI_JOB_NAME", "registry-format-inspection")
	original := strings.Repeat("d", 40)
	t.Setenv("REGISTRY_SOURCE_COMMIT", original)
	var input oci.Proof
	if read(".oci/rechecked-proof.json", &input) != nil {
		t.Fatal("Fixture missing")
	}
	input.SourceCommit = original
	write(t, ".oci/rechecked-proof.json", input)
	if e := os.WriteFile(".oci/build-input.txt", []byte("sourceCommit="+original+"\nsourceProjectId=86247033\nbuildPipelineId=122\nbuildJobId=111\nregistryPush=false\nregistryCache=false\n"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := proof(at)
	if e != nil || p["buildPipelineId"] != int64(122) || p["validationPipelineId"] != int64(123) || p["sourceCommit"] != original {
		t.Fatal("Original build and current scan not distinct", e)
	}
	t.Setenv("REGISTRY_SOURCE_COMMIT", strings.Repeat("e", 40))
	if _, e := proof(at); e == nil {
		t.Fatal("Swapped original source accepted")
	}
}
func TestForeignRunAndExpiredDBCannotProduceCompatibilityProof(t *testing.T) {
	for _, bad := range []string{"project", "pipeline", "job", "version", "future-db", "stale-db", "report", "source"} {
		t.Run(bad, func(t *testing.T) {
			setup(t)
			switch bad {
			case "project":
				t.Setenv("CI_PROJECT_ID", "86247025")
			case "pipeline":
				t.Setenv("CI_PIPELINE_ID", "999")
			case "job":
				t.Setenv("CI_JOB_ID", "111")
			case "version":
				write(t, ".oci-private/version.json", map[string]any{"Version": "unknown"})
			case "future-db":
				write(t, ".cache/trivy/db/metadata.json", map[string]any{"Version": 2, "UpdatedAt": at.Add(time.Second)})
			case "stale-db":
				write(t, ".cache/trivy/db/metadata.json", map[string]any{"Version": 2, "UpdatedAt": at.Add(-25 * time.Hour)})
			case "report":
				write(t, ".oci-private/report.json", map[string]any{"SchemaVersion": 3, "ArtifactType": "unknown"})
			case "source":
				t.Setenv("CI_COMMIT_SHA", strings.Repeat("f", 40))
			}
			if _, e := proof(at); e == nil {
				t.Fatal("bad compatibility proof accepted")
			}
		})
	}
}
