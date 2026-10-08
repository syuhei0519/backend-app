package main

import (
	release "core-platform/release-record"
	"crypto/sha256"
	"encoding/hex"
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
	if len(os.Args) != 2 {
		return release.ErrRefused
	}
	if os.Args[1] == "db-check" {
		reason := release.DatabaseFailure(read(".cache/trivy/db/metadata.json"), time.Now().UTC())
		if reason == "" {
			if os.Getenv("AT17_SAME_DIGEST_RESCAN") == "true" && release.NewDatabaseForRescan(read(".prior-scan/record.json"), read(".cache/trivy/db/metadata.json"), time.Now().UTC()) != nil {
				if os.WriteFile(".image-private/outcome", []byte("db-unavailable\n"), 0600) != nil {
					return release.ErrRefused
				}
				return release.ErrRefused
			}
			return nil
		}
		if os.WriteFile(".image-private/outcome", []byte(reason+"\n"), 0600) != nil {
			return release.ErrRefused
		}
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
	c := release.ScanContext{ProjectID: id("CI_PROJECT_ID"), PipelineID: id("CI_PIPELINE_ID"), JobID: id("CI_JOB_ID"), Commit: os.Getenv("CI_COMMIT_SHA"), ProjectURL: os.Getenv("CI_PROJECT_URL")}
	outcome := os.Args[1]
	var r release.Record
	var config string
	var e error
	registry := os.Getenv("OCI_REGISTRY_RETRIEVAL_ENABLED") == "true"
	historical := os.Getenv("OCI_ROLLBACK_RETRIEVAL_ENABLED") == "true" && os.Getenv("CI_PIPELINE_SOURCE") == "api"
	if registry && outcome == "input-unavailable" {
		if historical {
			r, e = release.FailedHistoricalRegistryInput(c, []byte(os.Getenv("OCI_REGISTRY_REQUEST")))
		} else {
			r, e = release.FailedRegistryInput(c, []byte(os.Getenv("OCI_REGISTRY_REQUEST")))
		}
		if e != nil {
			return release.ErrRefused
		}
	} else {
		f, e := os.Open(".oci/image.tar")
		if e != nil {
			return release.ErrRefused
		}
		h := sha256.New()
		size, e := io.Copy(h, io.LimitReader(f, 100*1024*1024+1))
		f.Close()
		if e != nil {
			return release.ErrRefused
		}
		if registry {
			origin, err := release.DecodeLegacyRegistryOrigin(read(".image-private/registry-origin.json"))
			if err != nil || (!historical && origin.SourceCommit != c.Commit) {
				return release.ErrRefused
			}
			r, config, e = release.BindLegacyRegistryIdentity(c, origin, read(".oci/layout-proof.json"), read(".image-private/layout.json"), read(".image-private/crane.json"), hex.EncodeToString(h.Sum(nil)), size)
		} else {
			r, config, e = release.BindOCIIdentity(c, read(".oci/build-input.txt"), read(".oci/layout-proof.json"), read(".image-private/layout.json"), read(".image-private/crane.json"), hex.EncodeToString(h.Sum(nil)), size)
		}
		if e != nil {
			return release.ErrRefused
		}
	}
	safe := read(".image-policy/public/scan-report.json")
	// A failure before a usable gated summary retains only a static reason.
	// Budget failure occurs after the scanner and also invalidates adoption.
	if outcome != "" && outcome != "policy-rejected" && outcome != "sbom-unavailable" && outcome != "sbom-invalid" || len(safe) == 0 {
		if outcome == "" {
			return release.ErrRefused
		}
		safe, _ = json.Marshal(struct {
			SchemaVersion int    `json:"schemaVersion"`
			Status        string `json:"status"`
			FailureReason string `json:"failureReason"`
		}{1, "failed", outcome})
	}
	var sbom []byte
	if outcome == "" {
		sbom = read(".image-policy/public/sbom.cdx.json")
	}
	r, e = release.CompleteScanRecord(r, outcome, config, read("security/scan-policy.json"), read(".cache/trivy/db/metadata.json"), safe, read(".image-private/raw.json"), sbom, time.Now().UTC())
	if e != nil {
		return release.ErrRefused
	}
	if os.Getenv("AT17_SAME_DIGEST_RESCAN") == "true" {
		if _, _, e := release.ValidateRescanPair(r, read(".prior-scan/record.json"), time.Now().UTC()); e != nil {
			return release.ErrRefused
		}
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return release.ErrRefused
	}
	// Never leave a record when its referenced report bytes could not be saved.
	if os.WriteFile(".image-policy/public/scan-report.json", safe, 0600) != nil || os.WriteFile(".image-private/record.json", append(b, '\n'), 0600) != nil {
		return release.ErrRefused
	}
	return os.Rename(".image-private/record.json", ".image-policy/public/record.json")
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "Scan record identity or evidence unavailable")
		os.Exit(1)
	}
}
