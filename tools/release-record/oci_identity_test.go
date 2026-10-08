package release

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOCIIdentityUsesActualArchiveAndOriginalBuild(t *testing.T) {
	c := ScanContext{86247025, 23, 25, strings.Repeat("a", 40), "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
	hash := strings.Repeat("b", 64)
	digest := "sha256:" + strings.Repeat("c", 64)
	config := "sha256:" + strings.Repeat("d", 64)
	input := []byte(fmt.Sprintf("schemaVersion=1\nservice=frontend\nsourceProjectId=86247025\nsourceCommit=%s\nbuildPipelineId=23\nbuildJobId=24\nplatform=linux/amd64\narchiveBytes=123\nregistryPush=false\nregistryCache=false\nlayoutAccepted=false\n", c.Commit))
	p, _ := json.Marshal(layoutIdentity{1, c.Commit, c.ProjectID, hash, 123, 456, digest, 1, "linux/amd64", false})
	crane := []byte(fmt.Sprintf(`{"schemaVersion":1,"reader":"crane-v0.21.7-layout-reader","digest":"%s","configDigest":"%s","fullLayerValidation":true,"registryPush":false}`, digest, config))
	r, got, e := BindOCIIdentity(c, input, p, p, crane, hash, 123)
	if e != nil || got != config || r.BuildJobID != 24 || r.ScanJobID != 25 || r.ImageDigest != digest || r.InputArchiveSHA256 != hash {
		t.Fatal("actual archive/original identity unbound")
	}
	for name, bad := range map[string][]byte{
		"duplicate-build": append(append([]byte{}, input...), []byte("buildJobId=26\n")...),
		"scan-as-build":   []byte(strings.Replace(string(input), "buildJobId=24", "buildJobId=25", 1)),
		"other-pipeline":  []byte(strings.Replace(string(input), "buildPipelineId=23", "buildPipelineId=22", 1)),
		"unknown-build":   []byte(strings.Replace(string(input), "buildJobId=24", "buildJobId=0", 1)),
		"source-swap":     []byte(strings.Replace(string(input), c.Commit, strings.Repeat("e", 40), 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, e := BindOCIIdentity(c, bad, p, p, crane, hash, 123); e == nil {
				t.Fatal("substitution accepted")
			}
		})
	}
	if _, _, e := BindOCIIdentity(c, input, p, p, crane, strings.Repeat("f", 64), 123); e == nil {
		t.Fatal("actual archive hash ignored")
	}
	if _, _, e := BindOCIIdentity(c, input, p, []byte(strings.Replace(string(p), digest, "sha256:"+hash, 1)), crane, hash, 123); e == nil {
		t.Fatal("fresh consumer swap accepted")
	}
	if _, _, e := BindOCIIdentity(c, input, p, p, []byte(strings.Replace(string(crane), "true", "false", 1)), hash, 123); e == nil {
		t.Fatal("partial crane proof accepted")
	}
}
