package main

import (
	release "core-platform/release-record"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func check() error {
	if len(os.Args) != 1 || os.Getenv("PHASE2_DELIVERY_ENABLED") != "true" || os.Getenv("CI_ENVIRONMENT_NAME") != "release-evidence" || os.Getenv("CI_JOB_NAME") != "publish-oci" || os.Getenv("OCI_REGISTRY_RETRIEVAL_ENABLED") != "true" || os.Getenv("OCI_ROLLBACK_RETRIEVAL_ENABLED") == "true" {
		return release.ErrRefused
	}
	id := func(k string) int64 {
		s := os.Getenv(k)
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
			return 0
		}
		return n
	}
	f, e := os.Open(".release-store/public/record.json")
	if e != nil {
		return release.ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(f, 2<<20+1))
	f.Close()
	if e != nil || len(b) > 2<<20 {
		return release.ErrRefused
	}
	r, e := release.DecodeRecord(b)
	if e != nil || r.SBOM == nil {
		return release.ErrRefused
	}
	a, e := release.NewSourceAPI(os.Getenv("RELEASE_EVIDENCE_READ_API_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	c, e := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	u, e := r.Run().URL("record")
	if e != nil {
		return release.ErrRefused
	}
	hash := release.Checksum(b)
	s := release.ConsumerSelection{Service: r.Service, SourceProjectID: r.SourceProjectID, SourceCommit: r.SourceCommit, ImageDigest: r.ImageDigest, BuildPipelineID: r.BuildPipelineID, BuildJobID: r.BuildJobID, ScanPipelineID: r.ScanPipelineID, ScanJobID: r.ScanJobID, RecordURL: u, RecordSHA256: hash, SBOMURL: r.SBOM.URL, SBOMSHA256: r.SBOM.SHA256}
	w := release.WriterContext{ProjectID: id("CI_PROJECT_ID"), PipelineID: id("CI_PIPELINE_ID"), JobID: id("CI_JOB_ID"), Commit: os.Getenv("CI_COMMIT_SHA"), JobName: os.Getenv("CI_JOB_NAME")}
	now := time.Now().UTC()
	if _, e = release.ReadConsumerScan(a, c, s, false, now); e != nil || a.ValidateRegistryReuse(w, r, now) != nil {
		return release.ErrRefused
	}
	text := fmt.Sprintf("IMAGE_TAG=%s\nIMAGE_DIGEST=%s\nSOURCE_PROJECT_ID=%d\nSOURCE_COMMIT=%s\nBUILD_PIPELINE_ID=%d\nBUILD_JOB_ID=%d\nSCAN_PIPELINE_ID=%d\nSCAN_JOB_ID=%d\nRELEASE_RECORD_URL=%s\nRELEASE_RECORD_SHA256=%s\nSBOM_URL=%s\nSBOM_SHA256=%s\n", r.ImageTag, r.ImageDigest, r.SourceProjectID, r.SourceCommit, r.BuildPipelineID, r.BuildJobID, r.ScanPipelineID, r.ScanJobID, u, hash, r.SBOM.URL, r.SBOM.SHA256)
	if os.WriteFile(".oci-publish/private/verified.env", []byte(text), 0600) != nil {
		return release.ErrRefused
	}
	return nil
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "Current scan and native inspection authority for registry reuse refused")
		os.Exit(1)
	}
}
