package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnavailableRegistryInputHasNoInventedBuildOrAdoption(t *testing.T) {
	sample, _, policy, _, _, _, _, now := scanRecordFixture()
	q := RegistryRequest{SchemaVersion: 1, OriginKind: "legacy", Service: sample.Service, SourceCommit: sample.SourceCommit, ImageDigest: sample.ImageDigest, BuildPipelineID: 123, BuildJobID: 456}
	b, _ := json.Marshal(q)
	c := ScanContext{ProjectID: sample.SourceProjectID, PipelineID: sample.ScanPipelineID, JobID: sample.ScanJobID, Commit: sample.SourceCommit, ProjectURL: "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
	r, e := FailedRegistryInput(c, b)
	if e != nil {
		t.Fatal(e)
	}
	safe := []byte(`{"schemaVersion":1,"status":"failed","failureReason":"input-unavailable"}`)
	r, e = CompleteScanRecord(r, "input-unavailable", "", policy, nil, safe, nil, nil, now)
	if e != nil || r.ValidateStored() != nil || r.Adopt(now) == nil || r.BuildJobID != 0 || r.BuildPipelineID != 0 || r.InputArchiveSHA256 != "" || r.Database != nil || r.SBOM != nil {
		t.Fatal("failed retrieval invented provenance or became adoptable")
	}
	for _, mutate := range []func(*ScanContext){func(v *ScanContext) { v.Commit = strings.Repeat("f", 40) }, func(v *ScanContext) { v.ProjectID = 86247033 }, func(v *ScanContext) { v.JobID = 0 }} {
		changed := c
		mutate(&changed)
		if _, e := FailedRegistryInput(changed, b); e == nil {
			t.Fatal("foreign current scan accepted")
		}
	}
}
