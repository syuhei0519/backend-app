package release

import (
	"encoding/json"
	"testing"
	"time"
)

// Synthetic timestamps test the model; real two-DB acceptance is separate CI.
func TestRescanModelRetainsBuildAndRejectsRunSubstitutions(t *testing.T) {
	identity, config, policy, db, scan, raw, bom, now := scanRecordFixture()
	prior, e := CompleteScanRecord(identity, "", config, policy, db, scan, raw, bom, now)
	if e != nil {
		t.Fatal("prior fixture invalid")
	}
	priorBytes, _ := json.Marshal(prior)
	var summary SafeScan
	json.Unmarshal(scan, &summary)
	summary.DBUpdatedAt = now.Add(-30 * time.Minute)
	summary.ScannedAt = now.Add(-10 * time.Second)
	nextDB, _ := json.Marshal(struct {
		Version   int
		UpdatedAt time.Time
	}{2, summary.DBUpdatedAt})
	nextScan, _ := json.Marshal(summary)
	identity.ScanJobID++
	failed, e := CompleteScanRecord(identity, "sbom-unavailable", config, policy, nextDB, nextScan, nil, nil, now)
	if e != nil {
		t.Fatal("second fixture invalid")
	}
	_, p, e := ValidateRescanPair(failed, priorBytes, now)
	if e != nil || !p.SameImageAndBuild || !p.DifferentDatabase || !p.CurrentFailed || p.PriorSuccessFallback || failed.Adopt(now) == nil || p.PriorRecordSHA256 != Checksum(priorBytes) {
		t.Fatal("failed new run adopted prior success or lost actual original build")
	}
	for _, mutate := range []func(*Record){
		func(r *Record) { r.ImageDigest = run().Digest[:10] + "b" + run().Digest[11:] },
		func(r *Record) { r.ScanJobID = prior.ScanJobID }, func(r *Record) { r.BuildJobID++ },
		func(r *Record) { r.BuildPipelineID++ }, func(r *Record) { r.InputArchiveSHA256 = Checksum([]byte("different archive")) },
		func(r *Record) { r.ScannedAt = prior.ScannedAt }, func(r *Record) { r.ScanPipelineID++ },
	} {
		bad := failed
		mutate(&bad)
		if _, _, e := ValidateRescanPair(bad, priorBytes, now); e == nil {
			t.Fatal("pair substitution accepted")
		}
	}
	if NewDatabaseForRescan(priorBytes, db, now) == nil || NewDatabaseForRescan(priorBytes, nil, now) == nil || NewDatabaseForRescan(priorBytes, nextDB, now) != nil {
		t.Fatal("new DB availability fabricated")
	}
	same := failed
	same.Database = prior.Database
	_, p, e = ValidateRescanPair(same, priorBytes, now)
	if e != nil || p.DifferentDatabase {
		t.Fatal("unchanged DB evidence claimed AT04 change")
	}
}
