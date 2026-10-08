package release

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func scanRecordFixture() (Record, string, []byte, []byte, []byte, []byte, []byte, time.Time) {
	bom, raw, config, now := validSBOMFixture()
	r := fixtureRecord()
	r.PolicyRevision = r.SourceCommit
	policy := []byte(`{"schemaVersion":1,"trivyVersion":"0.75.0","dbMaxAgeHours":24,"exceptions":[]}`)
	db, _ := json.Marshal(struct {
		Version   int
		UpdatedAt time.Time
	}{2, now.Add(-time.Hour)})
	safe, _ := json.Marshal(SafeScan{1, true, "0.75.0", now.Add(-time.Hour), now.Add(-30 * time.Second), []SafeFinding{}, Checksum(policy)})
	var obj map[string]any
	json.Unmarshal(raw, &obj)
	obj["Metadata"].(map[string]any)["ImageConfig"] = map[string]any{"config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.revision": r.SourceCommit, "org.opencontainers.image.source": "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}}}
	raw, _ = json.Marshal(obj)
	return r, config, policy, db, safe, raw, bom, now
}

func TestCompleteActualByteBindingsAndFailedSecondRun(t *testing.T) {
	r, config, policy, db, safe, raw, bom, now := scanRecordFixture()
	first, e := CompleteScanRecord(r, "", config, policy, db, safe, raw, bom, now)
	if e != nil || first.Adopt(now) != nil || first.SBOM.SHA256 != Checksum(bom) || first.ScanReport.SHA256 != Checksum(safe) || first.Database.MetadataSHA256 != Checksum(db) || first.BuildJobID != r.BuildJobID {
		t.Fatal("complete scan bytes or original build not bound")
	}
	var summary SafeScan
	json.Unmarshal(safe, &summary)
	summary.Pass = false
	summary.Findings = []SafeFinding{{Kind: "vulnerability", Rule: "CVE-2026-12345", Path: "image/os", Package: "nginx", InstalledVersion: "1.0", FixedVersion: "2.0", Severity: "CRITICAL", Decision: "fail"}}
	negative, _ := json.Marshal(summary)
	r.ScanPipelineID++
	r.ScanJobID++
	second, e := CompleteScanRecord(r, "policy-rejected", config, policy, db, negative, nil, nil, now)
	if e != nil || second.Adopt(now) == nil || second.ImageDigest != first.ImageDigest || second.BuildJobID != first.BuildJobID || second.SBOM != nil || second.SBOMFailureReason != "policy-rejected" || second.ScanReport.URL == first.ScanReport.URL {
		t.Fatal("failed second run overwrote or adopted prior success")
	}
	encoded, _ := json.Marshal(first)
	if _, e := DecodeRecord(encoded); e != nil {
		t.Fatal("serialized completed run invalid")
	}
}

func TestCompleteScanRejectsSubstitutionsAndKeepsUnknowns(t *testing.T) {
	r, config, policy, db, safe, raw, bom, now := scanRecordFixture()
	for name, bad := range map[string][]byte{
		"policy-hash":      []byte(strings.Replace(string(safe), Checksum(policy), strings.Repeat("0", 64), 1)),
		"raw-secret-field": []byte(strings.Replace(string(safe), `"pass":true`, `"pass":true,"Match":"PRIVATE_SENTINEL"`, 1)),
		"future-db":        []byte(strings.Replace(string(safe), now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339), 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := CompleteScanRecord(r, "", config, policy, db, bad, raw, bom, now); e == nil {
				t.Fatal("unsafe or unbound summary accepted")
			}
		})
	}
	badRaw := []byte(strings.Replace(string(raw), r.SourceCommit, strings.Repeat("b", 40), 1))
	if _, e := CompleteScanRecord(r, "", config, policy, db, safe, badRaw, bom, now); e == nil {
		t.Fatal("image revision substituted")
	}
	if _, e := CompleteScanRecord(r, "", config, policy, db, safe, raw, nil, now); e == nil {
		t.Fatal("missing SBOM accepted")
	}
	r.BuildPipelineID = 0
	r.BuildJobID = 0
	r.InputArchiveSHA256 = ""
	failure := []byte(`{"schemaVersion":1,"status":"failed","failureReason":"input-unavailable"}`)
	failed, e := CompleteScanRecord(r, "input-unavailable", config, policy, nil, failure, nil, nil, now)
	if e != nil || failed.BuildPipelineID != 0 || failed.BuildJobID != 0 || failed.InputArchiveSHA256 != "" || failed.Database != nil || failed.Adopt(now) == nil {
		t.Fatal("unknown build/input fabricated or adopted")
	}
	if _, e := CompleteScanRecord(r, "PRIVATE_DIAGNOSTIC", config, policy, nil, failure, nil, nil, now); e == nil {
		t.Fatal("private failure reason accepted")
	}
}

func TestDatabaseFailureDoesNotRefreshOrInventTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 3, 7, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{{time.Hour, ""}, {24 * time.Hour, ""}, {24*time.Hour + time.Second, "db-expired"}, {-time.Second, "db-expired"}} {
		b, _ := json.Marshal(struct {
			Version   int
			UpdatedAt time.Time
		}{2, now.Add(-tc.age)})
		if got := DatabaseFailure(b, now); got != tc.want {
			t.Fatal("DB freshness classification failed")
		}
	}
	if DatabaseFailure(nil, now) != "db-unavailable" || DatabaseFailure([]byte(`{"Version":2,"UpdatedAt":"PRIVATE_INVALID"}`), now) != "db-unavailable" {
		t.Fatal("unavailable DB classified as usable")
	}
}
