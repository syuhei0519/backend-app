package main

import (
	release "core-platform/release-record"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func read(p string) []byte {
	f, e := os.Open(p)
	if e != nil {
		return nil
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 32*1024*1024+1))
	if e != nil || len(b) > 32*1024*1024 {
		return nil
	}
	return b
}
func check() error {
	if len(os.Args) != 1 || os.Getenv("CI_COMMIT_BRANCH") != "main" || os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" || os.Getenv("CI_ENVIRONMENT_NAME") != "release-evidence" || os.Getenv("RELEASE_RECORD_STORE_ENABLED") != "true" {
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
	a, e := release.NewSourceAPI(os.Getenv("RELEASE_EVIDENCE_READ_API_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	c, e := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	w := release.WriterContext{ProjectID: id("CI_PROJECT_ID"), PipelineID: id("CI_PIPELINE_ID"), JobID: id("CI_JOB_ID"), Commit: os.Getenv("CI_COMMIT_SHA"), JobName: os.Getenv("CI_JOB_NAME")}
	var proof release.StoredBundle
	var pair *release.RescanProof
	recordBytes := read(".image-policy/public/record.json")
	if w.JobName == "release-record-store-rescan" {
		var p release.RescanProof
		proof, p, e = c.StoreRescanBundle(a, w, recordBytes, read(".image-policy/public/scan-report.json"), read(".image-policy/public/sbom.cdx.json"), read(".prior-scan/record.json"), time.Now().UTC())
		pair = &p
	} else {
		proof, e = c.StoreCompletedBundle(a, w, recordBytes, read(".image-policy/public/scan-report.json"), read(".image-policy/public/sbom.cdx.json"), time.Now().UTC())
	}
	if e != nil {
		return release.ErrRefused
	}
	b, e := json.MarshalIndent(proof, "", "  ")
	if e != nil || os.MkdirAll(".release-store/public", 0700) != nil || os.WriteFile(".release-store/public/stored.json", append(b, '\n'), 0600) != nil || os.WriteFile(".release-store/public/record.json", recordBytes, 0600) != nil {
		return release.ErrRefused
	}
	if pair != nil {
		b, e = json.MarshalIndent(pair, "", "  ")
		if e != nil || os.WriteFile(".release-store/public/rescan.json", append(b, '\n'), 0600) != nil {
			return release.ErrRefused
		}
	}
	return exposeSafeNativeJSON()
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "Protected release evidence store refused")
		os.Exit(1)
	}
}
