package release

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBackendOCIIdentityAndForeignServiceRefusal(t *testing.T) {
	c := ScanContext{86247033, 23, 25, strings.Repeat("a", 40), "https://gitlab.com/syuhei-platform-engineering-lab/backend-app"}
	hash, digest, config := strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64)
	input := []byte(fmt.Sprintf("schemaVersion=1\nservice=backend\nsourceProjectId=86247033\nsourceCommit=%s\nbuildPipelineId=23\nbuildJobId=24\nplatform=linux/amd64\narchiveBytes=123\nregistryPush=false\nregistryCache=false\nlayoutAccepted=false\n", c.Commit))
	p, _ := json.Marshal(layoutIdentity{1, c.Commit, c.ProjectID, hash, 123, 456, digest, 1, "linux/amd64", false})
	crane := []byte(fmt.Sprintf(`{"schemaVersion":1,"reader":"crane-v0.21.7-layout-reader","digest":"%s","configDigest":"%s","fullLayerValidation":true,"registryPush":false}`, digest, config))
	r, _, err := BindOCIIdentity(c, input, p, p, crane, hash, 123)
	if err != nil || r.Service != "backend" || r.SourceProjectID != 86247033 || r.ImageRepository != "registry.gitlab.com/syuhei-platform-engineering-lab/backend-app" {
		t.Fatal("backend source and producer binding refused")
	}
	for _, field := range [][2]string{{"service=backend", "service=frontend"}, {"sourceProjectId=86247033", "sourceProjectId=86247025"}} {
		bad := []byte(strings.Replace(string(input), field[0], field[1], 1))
		if _, _, err := BindOCIIdentity(c, bad, p, p, crane, hash, 123); err == nil {
			t.Fatal("foreign service or project accepted")
		}
	}
}

func TestBackendScanRecordRequiresBackendImageSource(t *testing.T) {
	r, config, policy, db, safe, raw, bom, now := scanRecordFixture()
	r.Service, r.SourceProjectID, r.ImageRepository = "backend", 86247033, "registry.gitlab.com/syuhei-platform-engineering-lab/backend-app"
	backendRaw := []byte(strings.ReplaceAll(string(raw), "/frontend-app", "/backend-app"))
	got, err := CompleteScanRecord(r, "", config, policy, db, safe, backendRaw, bom, now)
	if err != nil || got.Adopt(now) != nil || got.Service != "backend" {
		t.Fatal("backend scan and SBOM byte binding refused")
	}
	if _, err := CompleteScanRecord(r, "", config, policy, db, safe, raw, bom, now); err == nil {
		t.Fatal("frontend source label accepted for backend")
	}
}
