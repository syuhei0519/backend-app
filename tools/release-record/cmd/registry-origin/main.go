package main

import (
	release "core-platform/release-record"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

// Only a protected running scan can resolve data selectors into proven origin.
// This command neither downloads an image nor authorizes deployment.
func check() error {
	if len(os.Args) != 1 || os.Getenv("OCI_REGISTRY_RETRIEVAL_ENABLED") != "true" || os.Getenv("CI_ENVIRONMENT_NAME") != "release-evidence" {
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
	request := []byte(os.Getenv("OCI_REGISTRY_REQUEST"))
	api, e := release.NewSourceAPI(os.Getenv("RELEASE_EVIDENCE_READ_API_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	client, e := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	w := release.WriterContext{ProjectID: id("CI_PROJECT_ID"), PipelineID: id("CI_PIPELINE_ID"), JobID: id("CI_JOB_ID"), Commit: os.Getenv("CI_COMMIT_SHA"), JobName: os.Getenv("CI_JOB_NAME")}
	var origin release.LegacyRegistryOrigin
	var record release.Record
	historical := os.Getenv("OCI_ROLLBACK_RETRIEVAL_ENABLED") == "true" && os.Getenv("CI_PIPELINE_SOURCE") == "api"
	if w.JobName == "image-scan" {
		// Historical rollback requests need their separate adoption gate.
		origin, e = release.ResolveRegistryOrigin(api, client, w, request, historical)
	} else {
		f, err := os.Open(".release-store/public/record.json")
		if err != nil {
			return release.ErrRefused
		}
		native, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
		f.Close()
		if err != nil || len(native) > 2*1024*1024 {
			return release.ErrRefused
		}
		if historical {
			origin, record, e = release.ResolveHistoricalInspectedRegistryOrigin(api, client, w, native, time.Now().UTC())
		} else {
			origin, record, e = release.ResolveInspectedRegistryOrigin(api, client, w, native, time.Now().UTC())
		}
	}
	if e != nil {
		return e
	}
	b, e := json.Marshal(origin)
	if e != nil {
		return release.ErrRefused
	}
	f, e := os.OpenFile(".image-private/registry-origin.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return release.ErrRefused
	}
	_, e = f.Write(append(b, '\n'))
	ce := f.Close()
	if e != nil || ce != nil {
		return release.ErrRefused
	}
	// Public strict identity only; shell does not evaluate the original JSON input.
	text := fmt.Sprintf("REGISTRY_SERVICE=%s\nREGISTRY_SOURCE_COMMIT=%s\nREGISTRY_IMAGE_DIGEST=%s\nREGISTRY_BUILD_PIPELINE_ID=%d\nREGISTRY_BUILD_JOB_ID=%d\n", origin.Service, origin.SourceCommit, origin.ImageDigest, origin.BuildPipelineID, origin.BuildJobID)
	if w.JobName != "image-scan" {
		text += fmt.Sprintf("REGISTRY_INPUT_ARCHIVE_SHA256=%s\nREGISTRY_SCAN_PIPELINE_ID=%d\nREGISTRY_SCAN_JOB_ID=%d\n", record.InputArchiveSHA256, record.ScanPipelineID, record.ScanJobID)
	}
	f, e = os.OpenFile(".image-private/registry-origin.env", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return release.ErrRefused
	}
	_, e = io.WriteString(f, text)
	ce = f.Close()
	if e != nil || ce != nil {
		return release.ErrRefused
	}
	return nil
}
func main() {
	if e := check(); e != nil {
		stage := "native-writer-authority"
		if errors.Is(e, release.ErrInspectionPrincipalRequest) {
			stage = "native-principal-request"
		}
		if errors.Is(e, release.ErrInspectionPrincipalIdentity) {
			stage = "native-principal-identity"
		}
		folder := ""
		switch os.Getenv("CI_JOB_NAME") {
		case "registry-format-inspection":
			folder = ".oci-compatibility/public/"
		case "registry-runtime-inspection":
			folder = ".oci-runtime/public/"
		}
		if folder != "" {
			b, _ := json.Marshal(map[string]any{"schemaVersion": 1, "status": "failed", "stage": stage})
			_ = os.WriteFile(folder+"failure.json", append(b, '\n'), 0600)
		}
		fmt.Fprintln(os.Stderr, "Registry origin or scan authority refused")
		os.Exit(1)
	}
}
