// record schema/freshness・failed run拒否・run固有参照の回帰試験。
// 第二失敗recordを拒否する試験だけでは第一成功一式の再選択を拒否した証明にはならず、application-manifest/ci/trusted_attempts_test.pyで補う。
package release

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)

func fixtureRecord() Record {
	r := Record{SchemaVersion: 2, Service: "frontend", SourceProjectID: 86247025, SourceCommit: strings.Repeat("a", 40), BuildPipelineID: 10, BuildJobID: 11, ImageRepository: "registry.gitlab.com/syuhei-platform-engineering-lab/frontend-app", ImageTag: strings.Repeat("a", 40), ImageDigest: run().Digest, ScanPipelineID: 110, ScanJobID: 220, ScanJobStatus: "success", InputKind: "build-oci", InputArchiveSHA256: strings.Repeat("b", 64), TrivyVersion: "0.75.0", Database: &Database{2, at.Add(-time.Hour), strings.Repeat("c", 64)}, ScannedAt: at, PolicyRevision: strings.Repeat("d", 40), PolicySHA256: strings.Repeat("e", 64), Exceptions: []AppliedException{}, Decision: "passed"}
	sbomURL, _ := r.Run().URL("sbom")
	reportURL, _ := r.Run().URL("scan")
	r.SBOM = &Artifact{r.ImageDigest, 110, 220, sbomURL, strings.Repeat("f", 64), "CycloneDX", "1.6"}
	r.ScanReport = &Artifact{r.ImageDigest, 110, 220, reportURL, strings.Repeat("1", 64), "safe-json", "1"}
	return r
}

func TestStrictRecordAndRunBinding(t *testing.T) {
	r := fixtureRecord()
	b, _ := json.Marshal(r)
	got, err := DecodeRecord(b)
	if err != nil || got.Adopt(at.Add(time.Minute)) != nil {
		t.Fatal("valid v2 run rejected")
	}
	for _, change := range []func(*Record){
		func(r *Record) { r.SchemaVersion = 1 }, func(r *Record) { r.SourceProjectID = 86247033 },
		func(r *Record) { r.ImageTag = strings.Repeat("b", 40) }, func(r *Record) { r.ImageRepository = "registry.example.invalid/frontend" },
		func(r *Record) { r.BuildJobID = r.ScanJobID }, func(r *Record) { r.BuildJobID = 0 },
		func(r *Record) { r.SBOM.ScanJobID++ }, func(r *Record) { r.SBOM.ImageDigest = "sha256:" + strings.Repeat("b", 64) },
		func(r *Record) { r.ScanReport.ScanPipelineID++ }, func(r *Record) { r.SBOM.URL = r.ScanReport.URL },
		func(r *Record) { r.SBOM = nil }, func(r *Record) { r.Database = nil }, func(r *Record) { r.ScanJobStatus = "running" },
		func(r *Record) { r.SBOM.Format = "safe-json" }, func(r *Record) { r.ScanReport.SchemaVersion = "1.6" },
		func(r *Record) { r.SBOMFailureReason = "PRIVATE_DIAGNOSTIC_NOT_ALLOWED" },
	} {
		bad := fixtureRecord()
		change(&bad)
		b, _ := json.Marshal(bad)
		if _, err := DecodeRecord(b); err == nil {
			t.Fatal("schema/project/build/SBOM/job substitution accepted")
		}
	}
	b, _ = json.Marshal(fixtureRecord())
	for _, bad := range []string{
		strings.Replace(string(b), `"schemaVersion":2`, `"schemaVersion":2,"SchemaVersion":2`, 1),
		strings.Replace(string(b), `"schemaVersion":2`, `"schemaVersion":2,"schemaVersion":2`, 1),
		strings.Replace(string(b), `"schemaVersion":2`, `"schemaVersion":2,"recordSha256":"self"`, 1),
		strings.Replace(string(b), `"failureReason":"",`, "", 1),
		strings.Replace(string(b), `"failureReason":""`, `"failureReason":null`, 1),
		strings.Replace(string(b), `"schemaVersion":"1.6"`, `"schemaVersion":"1.6","unexpected":"hidden"`, 1),
		string(b) + ` {"second":true}`,
	} {
		if _, err := DecodeRecord([]byte(bad)); err == nil {
			t.Fatal("duplicate/unknown/missing/trailing JSON accepted")
		}
	}
}

func TestFreshDBAndExceptionAtAdoption(t *testing.T) {
	r := fixtureRecord()
	if r.Adopt(at.Add(24*time.Hour)) == nil {
		t.Fatal("old DB adopted")
	}
	r.Database.UpdatedAt = at.Add(time.Minute)
	if r.ValidateStored() == nil {
		t.Fatal("future DB at scan accepted")
	}
	r = fixtureRecord()
	r.Exceptions = []AppliedException{{ID: "fixture", Rule: "CVE-2026-12345", Package: "demo", Path: "lib/demo", CreatedAt: at.Add(-time.Hour), ExpiresAt: at.Add(time.Hour)}}
	if r.Adopt(at.Add(time.Minute)) != nil || r.Adopt(at.Add(2*time.Hour)) == nil {
		t.Fatal("exception freshness at current adoption failed")
	}
	r.Exceptions[0].ExpiresAt = at.Add(20 * 24 * time.Hour)
	if r.ValidateStored() == nil {
		t.Fatal("over14day exception accepted")
	}
	r = fixtureRecord()
	r.Exceptions = []AppliedException{{ID: "fixture", Rule: "rule", Path: "*", CreatedAt: at.Add(-time.Hour), ExpiresAt: at.Add(time.Hour)}}
	if r.ValidateStored() == nil {
		t.Fatal("wildcard exception accepted")
	}
}

func TestFailedSecondRunNeverAdoptedAsOldSuccess(t *testing.T) {
	first := fixtureRecord()
	second := fixtureRecord()
	second.ScanJobID++
	second.ScanJobStatus = "failed"
	second.Decision = "indeterminate"
	second.FailureReason = "scanner-unavailable"
	second.Database = nil
	second.SBOM = nil
	second.SBOMFailureReason = "sbom-unavailable"
	second.ScanReport.ScanJobID = second.ScanJobID
	second.ScanReport.URL, _ = second.Run().URL("scan")
	if second.ValidateStored() != nil || second.Adopt(at) == nil {
		t.Fatal("failed record was not retained distinctly or was deployable")
	}
	u1, _ := first.Run().URL("record")
	u2, _ := second.Run().URL("record")
	if u1 == u2 || first.ImageDigest != second.ImageDigest {
		t.Fatal("1 digest:N scan runs collapsed")
	}
	first.InputKind = "registry-retrieval"
	if first.ValidateStored() != nil || first.BuildJobID == first.ScanJobID {
		t.Fatal("registry rescan lost original build identity")
	}
}

func TestUnknownBuildOrExpiredInputRetainedOnlyAsFailure(t *testing.T) {
	for _, reason := range []string{"build-unproven", "input-unavailable"} {
		r := fixtureRecord()
		r.Decision, r.ScanJobStatus, r.FailureReason = "indeterminate", "failed", reason
		r.SBOM, r.SBOMFailureReason = nil, "sbom-unavailable"
		if reason == "build-unproven" {
			r.BuildPipelineID, r.BuildJobID = 0, 0
		} else {
			r.InputArchiveSHA256 = ""
			r.BuildPipelineID, r.BuildJobID = 0, 0 // missing input can prevent both proofs
		}
		b, _ := json.Marshal(r)
		got, err := DecodeRecord(b)
		if err != nil || got.Adopt(at) == nil {
			t.Fatal("unknown proof was lost or deployment authorized")
		}
		r.FailureReason = "scanner-unavailable"
		if r.ValidateStored() == nil {
			t.Fatal("unknown identity/input lacked its explicit failure reason")
		}
	}
}
