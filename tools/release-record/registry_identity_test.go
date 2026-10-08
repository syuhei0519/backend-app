package release

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRegistryRetrievalPreservesBuildWithoutAdoptingOldScan(t *testing.T) {
	prior := fixtureRecord()
	c := ScanContext{prior.SourceProjectID, 500, 501, strings.Repeat("9", 40), "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
	priorBytes, _ := json.Marshal(prior)
	url, _ := prior.Run().URL("record")
	archive := strings.Repeat("8", 64)
	p, _ := json.Marshal(layoutIdentity{1, prior.SourceCommit, prior.SourceProjectID, archive, 123, 456, prior.ImageDigest, 1, "linux/amd64", false})
	crane := []byte(fmt.Sprintf(`{"schemaVersion":1,"reader":"crane-v0.21.7-layout-reader","digest":"%s","configDigest":"sha256:%s","fullLayerValidation":true,"registryPush":false}`, prior.ImageDigest, strings.Repeat("7", 64)))
	bind := func(ctx ScanContext, origin []byte, u, h string, proof, reader []byte, checksum string, size int64) (Record, error) {
		r, _, err := BindRegistryIdentity(ctx, origin, u, h, p, proof, reader, checksum, size)
		return r, err
	}
	r, err := bind(c, priorBytes, url, Checksum(priorBytes), p, crane, archive, 123)
	if err != nil || r.SourceCommit != prior.SourceCommit || r.BuildPipelineID != prior.BuildPipelineID || r.BuildJobID != prior.BuildJobID || r.ScanPipelineID != c.PipelineID || r.ScanJobID != c.JobID || r.PolicyRevision != c.Commit || r.InputArchiveSHA256 != archive || r.InputKind != "registry-retrieval" || r.ImageDigest != prior.ImageDigest {
		t.Fatal("retrieved archive, original build, and current policy/run not separated")
	}
	if r.Database != nil || r.SBOM != nil || r.ScanReport != nil || r.Decision != "" || r.TrivyVersion != "" || !r.ScannedAt.IsZero() || r.Adopt(at) == nil {
		t.Fatal("historical success became current adoption authority")
	}
	// A historical record may be expired as evidence of origin. It must not
	// authorize this current input: CompleteScanRecord and live API gates follow.
	if prior.Adopt(at.Add(48*time.Hour)) == nil {
		t.Fatal("fixture should be expired for adoption")
	}
	for name, change := range map[string]func(*ScanContext){
		"wrong-project":     func(c *ScanContext) { c.ProjectID = 86247033 },
		"wrong-url":         func(c *ScanContext) { c.ProjectURL += "/other" },
		"build-job-as-scan": func(c *ScanContext) { c.JobID = prior.BuildJobID },
		"old-scan-as-new":   func(c *ScanContext) { c.JobID = prior.ScanJobID },
		"missing-pipeline":  func(c *ScanContext) { c.PipelineID = 0 },
		"invalid-policy":    func(c *ScanContext) { c.Commit = "main" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := c
			change(&bad)
			if _, e := bind(bad, priorBytes, url, Checksum(priorBytes), p, crane, archive, 123); e == nil {
				t.Fatal("identity swap accepted")
			}
		})
	}
	for name, input := range map[string]struct {
		url, hash     string
		proof, reader []byte
		archive       string
		size          int64
	}{
		"external-record-url":  {"https://external.invalid/record.json", Checksum(priorBytes), p, crane, archive, 123},
		"other-run-url":        {strings.Replace(url, "-110-220", "-110-221", 1), Checksum(priorBytes), p, crane, archive, 123},
		"record-hash":          {url, strings.Repeat("0", 64), p, crane, archive, 123},
		"image-digest":         {url, Checksum(priorBytes), []byte(strings.Replace(string(p), prior.ImageDigest, "sha256:"+archive, 1)), crane, archive, 123},
		"source-label":         {url, Checksum(priorBytes), []byte(strings.Replace(string(p), prior.SourceCommit, c.Commit, 1)), crane, archive, 123},
		"old-archive-checksum": {url, Checksum(priorBytes), p, crane, prior.InputArchiveSHA256, 123},
		"partial-reader":       {url, Checksum(priorBytes), p, []byte(strings.Replace(string(crane), "true", "false", 1)), archive, 123},
		"oversize":             {url, Checksum(priorBytes), p, crane, archive, 100*1024*1024 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := bind(c, priorBytes, input.url, input.hash, input.proof, input.reader, input.archive, input.size); e == nil {
				t.Fatal("input/run/reader substitution accepted")
			}
		})
	}
	failed := prior
	failed.Decision, failed.ScanJobStatus, failed.FailureReason = "failed", "failed", "policy-rejected"
	b, _ := json.Marshal(failed)
	if _, e := bind(c, b, url, Checksum(b), p, crane, archive, 123); e == nil {
		t.Fatal("failed origin accepted")
	}
}
