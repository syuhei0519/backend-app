// Whitelisted PE016B format compatibility evidence, never a release decision.
package main

import (
	"bufio"
	oci "core-platform/oci-input"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func read(p string, out any) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 32*1024*1024+1))
	if e != nil || len(b) > 32*1024*1024 {
		return fmt.Errorf("size")
	}
	return json.Unmarshal(b, out)
}
func positive(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n <= 0 {
		return 0, fmt.Errorf("id")
	}
	return n, nil
}
func proof(now time.Time) (map[string]any, error) {
	if os.Getenv("CI_PROJECT_ID") != "86247033" {
		return nil, fmt.Errorf("project")
	}
	pipeline, e := positive(os.Getenv("CI_PIPELINE_ID"))
	if e != nil {
		return nil, e
	}
	job, e := positive(os.Getenv("CI_JOB_ID"))
	if e != nil {
		return nil, e
	}
	var input oci.Proof
	inspection := os.Getenv("OCI_REGISTRY_INSPECTION_ENABLED") == "true" && os.Getenv("OCI_REGISTRY_RETRIEVAL_ENABLED") == "true" && os.Getenv("CI_JOB_NAME") == "registry-format-inspection"
	expectedSource := os.Getenv("CI_COMMIT_SHA")
	if inspection {
		expectedSource = os.Getenv("REGISTRY_SOURCE_COMMIT")
	}
	if read(".oci/rechecked-proof.json", &input) != nil || input.SchemaVersion != 1 || input.SourceProject != 86247033 || input.SourceCommit != expectedSource || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(input.SourceCommit) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(input.ArchiveSHA256) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(input.Digest) || input.ArchiveBytes <= 0 || input.ArchiveBytes > oci.MaxArchive || input.ExpandedBytes <= 0 || input.ExpandedBytes > oci.MaxExpanded || input.LayerCount <= 0 || input.Platform != "linux/amd64" || input.RegistryPush {
		return nil, fmt.Errorf("input")
	}
	var crane struct {
		Schema int    `json:"schemaVersion"`
		Reader string `json:"reader"`
		Digest string `json:"digest"`
		Full   bool   `json:"fullLayerValidation"`
		Push   bool   `json:"registryPush"`
	}
	if read(".oci-private/crane.json", &crane) != nil || crane.Schema != 1 || crane.Reader != "crane-v0.21.7-layout-reader" || !crane.Full || crane.Push || crane.Digest != input.Digest {
		return nil, fmt.Errorf("crane")
	}
	var version struct{ Version string }
	if read(".oci-private/version.json", &version) != nil || version.Version != "0.75.0" {
		return nil, fmt.Errorf("version")
	}
	var db struct {
		Version   int
		UpdatedAt time.Time
	}
	if read(".cache/trivy/db/metadata.json", &db) != nil || db.Version != 2 || db.UpdatedAt.IsZero() || db.UpdatedAt.After(now) || now.Sub(db.UpdatedAt) > 24*time.Hour {
		return nil, fmt.Errorf("db")
	}
	var report struct {
		Schema  int    `json:"SchemaVersion"`
		Type    string `json:"ArtifactType"`
		Results []struct {
			Vulns      []json.RawMessage `json:"Vulnerabilities"`
			Secrets    []json.RawMessage `json:"Secrets"`
			Misconfigs []json.RawMessage `json:"Misconfigurations"`
		} `json:"Results"`
	}
	if read(".oci-private/report.json", &report) != nil || report.Schema != 2 || report.Type != "container_image" || len(report.Results) == 0 {
		return nil, fmt.Errorf("report")
	}
	f, e := os.Open(".oci/build-input.txt")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	fields := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		kv := strings.SplitN(scanner.Text(), "=", 2)
		if len(kv) != 2 || fields[kv[0]] != "" {
			return nil, fmt.Errorf("build")
		}
		fields[kv[0]] = kv[1]
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("build")
	}
	buildPipeline, e := positive(fields["buildPipelineId"])
	if e != nil || (!inspection && buildPipeline != pipeline) || fields["sourceCommit"] != input.SourceCommit || fields["sourceProjectId"] != "86247033" || fields["registryPush"] != "false" || fields["registryCache"] != "false" {
		return nil, fmt.Errorf("build")
	}
	buildJob, e := positive(fields["buildJobId"])
	if e != nil || buildJob == job {
		return nil, fmt.Errorf("build")
	}
	v, s, m := 0, 0, 0
	for _, r := range report.Results {
		v += len(r.Vulns)
		s += len(r.Secrets)
		m += len(r.Misconfigs)
	}
	return map[string]any{"schemaVersion": 1, "scope": "PE016B format compatibility only", "sourceProjectId": 86247033, "sourceCommit": input.SourceCommit, "buildPipelineId": buildPipeline, "buildJobId": buildJob, "validationPipelineId": pipeline, "validationJobId": job, "archiveSha256": input.ArchiveSHA256, "digest": input.Digest, "archiveBytes": input.ArchiveBytes, "expandedBytes": input.ExpandedBytes, "trivyVersion": version.Version, "dbUpdatedAt": db.UpdatedAt, "reportSchemaVersion": report.Schema, "vulnerabilityCount": v, "secretCount": s, "misconfigurationCount": m, "craneReader": "v0.21.7", "sameDirectoryRead": true, "fullCraneLayerValidation": true, "registryPush": false, "securityGateApplied": false, "phase2Adoptable": false, "utc": now}, nil
}
func main() {
	p, e := proof(time.Now().UTC())
	if e != nil {
		fmt.Fprintln(os.Stderr, "OCI compatibility evidence refused")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(p) != nil {
		os.Exit(1)
	}
}
