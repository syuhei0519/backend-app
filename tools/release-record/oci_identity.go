package release

import (
	"strconv"
	"strings"
)

type ScanContext struct {
	ProjectID, PipelineID, JobID int64
	Commit, ProjectURL           string
}

type layoutIdentity struct {
	SchemaVersion int    `json:"schemaVersion"`
	SourceCommit  string `json:"sourceCommit"`
	SourceProject int64  `json:"sourceProjectId"`
	ArchiveSHA256 string `json:"archiveSha256"`
	ArchiveBytes  int64  `json:"archiveBytes"`
	ExpandedBytes int64  `json:"expandedBytes"`
	Digest        string `json:"digest"`
	LayerCount    int    `json:"layerCount"`
	Platform      string `json:"platform"`
	RegistryPush  bool   `json:"registryPush"`
}

// BindOCIIdentity requires both the original producer proof and a fresh consumer
// verification of the actual archive. Completed-job authority is checked later.
func BindOCIIdentity(c ScanContext, buildInput, producer, consumer, crane []byte, archiveSHA string, archiveBytes int64) (Record, string, error) {
	var r Record
	service := "frontend"
	if c.ProjectID == 86247033 {
		service = "backend"
	} else if c.ProjectID != 86247025 {
		return r, "", ErrRefused
	}
	if c.PipelineID <= 0 || c.JobID <= 0 || !shaPattern.MatchString(c.Commit) || c.ProjectURL != "https://gitlab.com/syuhei-platform-engineering-lab/"+service+"-app" || !checksumPattern.MatchString(archiveSHA) || archiveBytes <= 0 || archiveBytes > 100*1024*1024 {
		return r, "", ErrRefused
	}
	if len(buildInput) > 4096 {
		return r, "", ErrRefused
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(buildInput), "\n"), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || fields[parts[0]] != "" {
			return r, "", ErrRefused
		}
		fields[parts[0]] = parts[1]
	}
	expect := map[string]string{"schemaVersion": "1", "service": service, "sourceProjectId": strconv.FormatInt(c.ProjectID, 10), "sourceCommit": c.Commit, "platform": "linux/amd64", "archiveBytes": strconv.FormatInt(archiveBytes, 10), "registryPush": "false", "registryCache": "false", "layoutAccepted": "false"}
	if len(fields) != len(expect)+2 {
		return r, "", ErrRefused
	}
	for k, v := range expect {
		if fields[k] != v {
			return r, "", ErrRefused
		}
	}
	parseID := func(s string) int64 {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
			return 0
		}
		return n
	}
	buildPipeline, buildJob := parseID(fields["buildPipelineId"]), parseID(fields["buildJobId"])
	if buildPipeline != c.PipelineID || buildJob == 0 || buildJob == c.JobID {
		return r, "", ErrRefused
	}
	p, configDigest, err := checkedOCIIdentity(c.ProjectID, c.Commit, producer, consumer, crane, archiveSHA, archiveBytes)
	if err != nil {
		return r, "", ErrRefused
	}
	r = Record{Service: service, SourceProjectID: c.ProjectID, SourceCommit: c.Commit, BuildPipelineID: buildPipeline, BuildJobID: buildJob, ImageRepository: "registry.gitlab.com/syuhei-platform-engineering-lab/" + service + "-app", ImageTag: c.Commit, ImageDigest: p.Digest, ScanPipelineID: c.PipelineID, ScanJobID: c.JobID, InputKind: "build-oci", InputArchiveSHA256: archiveSHA, PolicyRevision: c.Commit}
	return r, configDigest, nil
}

func checkedOCIIdentity(project int64, commit string, producer, consumer, crane []byte, archiveSHA string, archiveBytes int64) (layoutIdentity, string, error) {
	var p, q layoutIdentity
	if !checksumPattern.MatchString(archiveSHA) || archiveBytes <= 0 || archiveBytes > 100*1024*1024 || strictData(producer, &p) != nil || strictData(consumer, &q) != nil || p != q || p.SchemaVersion != 1 || p.SourceCommit != commit || p.SourceProject != project || p.ArchiveSHA256 != archiveSHA || p.ArchiveBytes != archiveBytes || p.ExpandedBytes <= 0 || p.ExpandedBytes > 1024*1024*1024 || p.LayerCount <= 0 || p.Platform != "linux/amd64" || p.RegistryPush || !digestPattern.MatchString(p.Digest) {
		return layoutIdentity{}, "", ErrRefused
	}
	var reader struct {
		SchemaVersion       int    `json:"schemaVersion"`
		Reader              string `json:"reader"`
		Digest              string `json:"digest"`
		ConfigDigest        string `json:"configDigest"`
		FullLayerValidation bool   `json:"fullLayerValidation"`
		RegistryPush        bool   `json:"registryPush"`
	}
	if strictData(crane, &reader) != nil || reader.SchemaVersion != 1 || reader.Reader != "crane-v0.21.7-layout-reader" || reader.Digest != p.Digest || !digestPattern.MatchString(reader.ConfigDigest) || !reader.FullLayerValidation || reader.RegistryPush {
		return layoutIdentity{}, "", ErrRefused
	}
	return p, reader.ConfigDigest, nil
}
