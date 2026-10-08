package main

import (
	release "core-platform/release-record"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func read(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32*1024*1024+1))
	if err != nil || len(b) > 32*1024*1024 {
		return nil
	}
	return b
}
func check() error {
	if len(os.Args) != 1 || os.Getenv("OCI_DELIVERY_VALIDATION_ENABLED") != "true" || os.Getenv("CI_ENVIRONMENT_NAME") != "release-evidence" {
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
	api, err := release.NewSourceAPI(os.Getenv("RELEASE_EVIDENCE_READ_API_TOKEN"))
	if err != nil {
		return release.ErrRefused
	}
	client, err := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if err != nil {
		return release.ErrRefused
	}
	recordBytes := read(".image-policy/public/record.json")
	r, err := release.DecodeRecord(recordBytes)
	if err != nil {
		return release.ErrRefused
	}
	w := release.WriterContext{ProjectID: id("CI_PROJECT_ID"), PipelineID: id("CI_PIPELINE_ID"), JobID: id("CI_JOB_ID"), Commit: os.Getenv("CI_COMMIT_SHA"), JobName: os.Getenv("CI_JOB_NAME")}
	if api.ValidatePublisher(w, r) != nil {
		return release.ErrRefused
	}
	u, _ := r.Run().URL("record")
	hash := release.Checksum(recordBytes)
	stored, err := client.Read(r.Run(), "record", u, hash)
	if err != nil {
		return release.ErrRefused
	}
	scan, err := client.Read(r.Run(), "scan", r.ScanReport.URL, r.ScanReport.SHA256)
	if err != nil || r.SBOM == nil {
		return release.ErrRefused
	}
	bom, err := client.Read(r.Run(), "sbom", r.SBOM.URL, r.SBOM.SHA256)
	if err != nil {
		return release.ErrRefused
	}
	f, err := os.Open(".oci/image.tar")
	if err != nil {
		return release.ErrRefused
	}
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, 100*1024*1024+1))
	f.Close()
	if err != nil {
		return release.ErrRefused
	}
	archiveSHA := hex.EncodeToString(h.Sum(nil))
	_, err = release.CheckPublicationInput(stored, scan, bom, u, hash, read(".oci/layout-proof.json"), read(".oci-publish/private/layout.json"), read(".oci-publish/private/crane.json"), archiveSHA, size, time.Now().UTC())
	if err != nil || api.ValidatePublisher(w, r) != nil {
		return release.ErrRefused
	}
	// Only strictly validated public identity fields leave the checker.
	text := fmt.Sprintf("IMAGE_TAG=%s\nIMAGE_DIGEST=%s\nSOURCE_PROJECT_ID=%d\nSOURCE_COMMIT=%s\nBUILD_PIPELINE_ID=%d\nBUILD_JOB_ID=%d\nSCAN_PIPELINE_ID=%d\nSCAN_JOB_ID=%d\nRELEASE_RECORD_URL=%s\nRELEASE_RECORD_SHA256=%s\nSBOM_URL=%s\nSBOM_SHA256=%s\n", r.ImageTag, r.ImageDigest, r.SourceProjectID, r.SourceCommit, r.BuildPipelineID, r.BuildJobID, r.ScanPipelineID, r.ScanJobID, u, hash, r.SBOM.URL, r.SBOM.SHA256)
	if os.WriteFile(".oci-publish/private/verified.env", []byte(text), 0600) != nil {
		return release.ErrRefused
	}
	return nil
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "OCI publication input or authority refused")
		os.Exit(1)
	}
}
