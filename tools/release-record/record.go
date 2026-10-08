// release-record schema v2の厳密解析と採用判定。scan job/writer/consumerが共有する。
// source・build・scan・digest・policy・成果物checksumを結合。保存可能な失敗証跡と配備採用可能な成功を区別する。
package release

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"time"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9@._/+:-]{1,240}$`)

type Artifact struct {
	ImageDigest    string `json:"imageDigest"`
	ScanPipelineID int64  `json:"scanPipelineId"`
	ScanJobID      int64  `json:"scanJobId"`
	URL            string `json:"url"`
	SHA256         string `json:"sha256"`
	Format         string `json:"format"`
	SchemaVersion  string `json:"schemaVersion"`
}

type Database struct {
	Version        int       `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
	MetadataSHA256 string    `json:"metadataSha256"`
}

type AppliedException struct {
	ID        string    `json:"id"`
	Rule      string    `json:"rule"`
	Package   string    `json:"package"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type Record struct {
	SchemaVersion      int                `json:"schemaVersion"`
	Service            string             `json:"service"`
	SourceProjectID    int64              `json:"sourceProjectId"`
	SourceCommit       string             `json:"sourceCommit"`
	BuildPipelineID    int64              `json:"buildPipelineId"`
	BuildJobID         int64              `json:"buildJobId"`
	ImageRepository    string             `json:"imageRepository"`
	ImageTag           string             `json:"imageTag"`
	ImageDigest        string             `json:"imageDigest"`
	ScanPipelineID     int64              `json:"scanPipelineId"`
	ScanJobID          int64              `json:"scanJobId"`
	ScanJobStatus      string             `json:"scanJobStatus"`
	InputKind          string             `json:"inputKind"`
	InputArchiveSHA256 string             `json:"inputArchiveSha256"`
	TrivyVersion       string             `json:"trivyVersion"`
	Database           *Database          `json:"database"`
	ScannedAt          time.Time          `json:"scannedAt"`
	PolicyRevision     string             `json:"policyRevision"`
	PolicySHA256       string             `json:"policySha256"`
	Exceptions         []AppliedException `json:"exceptions"`
	Decision           string             `json:"decision"`
	FailureReason      string             `json:"failureReason"`
	SBOM               *Artifact          `json:"sbom"`
	SBOMFailureReason  string             `json:"sbomFailureReason"`
	ScanReport         *Artifact          `json:"scanReport"`
}

func (r Record) Run() Run { return Run{r.Service, r.ImageDigest, r.ScanPipelineID, r.ScanJobID} }

// ValidateStored accepts failed records as evidence, never as deployment authority.
// Freshness of an adopted successful run is checked separately at the current time.
// 失敗recordも履歴として許容する。失敗時の不明build/inputは限定条件でのみ認め、成功の由来へ流用しない。
func (r Record) ValidateStored() error {
	project := int64(86247025)
	if r.Service == "backend" {
		project = 86247033
	}
	if _, err := r.Run().URL("record"); err != nil {
		return ErrRefused
	}
	if r.SchemaVersion != 2 || r.SourceProjectID != project || !shaPattern.MatchString(r.SourceCommit) || r.ImageTag != r.SourceCommit || r.ImageRepository != "registry.gitlab.com/syuhei-platform-engineering-lab/"+r.Service+"-app" || (r.InputKind != "build-oci" && r.InputKind != "registry-retrieval") || r.TrivyVersion != "0.75.0" || r.ScannedAt.IsZero() || !shaPattern.MatchString(r.PolicyRevision) || !checksumPattern.MatchString(r.PolicySHA256) || r.Exceptions == nil {
		return ErrRefused
	}
	// 0は失敗証跡内で元build不明を明示する値。再scan jobのIDで置き換えたり、入力checksumを作り上げたりしない。
	unknownBuild := r.BuildPipelineID == 0 && r.BuildJobID == 0 && r.Decision != "passed" && (r.FailureReason == "build-unproven" || r.FailureReason == "input-unavailable")
	if !unknownBuild && (r.BuildPipelineID <= 0 || r.BuildJobID <= 0 || r.BuildJobID == r.ScanJobID) {
		return ErrRefused
	}
	unknownInput := r.InputArchiveSHA256 == "" && r.Decision != "passed" && r.FailureReason == "input-unavailable"
	if !unknownInput && !checksumPattern.MatchString(r.InputArchiveSHA256) {
		return ErrRefused
	}
	if r.Decision != "passed" && r.Decision != "failed" && r.Decision != "indeterminate" {
		return ErrRefused
	}
	if r.Decision == "passed" {
		if r.ScanJobStatus != "success" || r.FailureReason != "" || r.Database == nil || r.SBOM == nil || r.SBOMFailureReason != "" {
			return ErrRefused
		}
	} else {
		if r.ScanJobStatus != "failed" || !knownFailure(r.FailureReason) {
			return ErrRefused
		}
		if r.SBOM == nil && !knownFailure(r.SBOMFailureReason) {
			return ErrRefused
		}
	}
	if r.Database != nil && (r.Database.Version != 2 || r.Database.UpdatedAt.IsZero() || r.Database.UpdatedAt.After(r.ScannedAt) || !checksumPattern.MatchString(r.Database.MetadataSHA256)) {
		return ErrRefused
	}
	ids := map[string]bool{}
	for _, e := range r.Exceptions {
		if !identifierPattern.MatchString(e.ID) || ids[e.ID] || !identifierPattern.MatchString(e.Rule) || (e.Package != "" && !identifierPattern.MatchString(e.Package)) || !identifierPattern.MatchString(e.Path) || path.IsAbs(e.Path) || path.Clean(e.Path) != e.Path || e.Path == ".." || len(e.Path) >= 3 && e.Path[:3] == "../" || bytes.ContainsRune([]byte(e.Path), ':') || e.CreatedAt.After(r.ScannedAt) || !e.ExpiresAt.After(r.ScannedAt) || e.ExpiresAt.Sub(e.CreatedAt) > 14*24*time.Hour || !e.ExpiresAt.After(e.CreatedAt) {
			return ErrRefused
		}
		ids[e.ID] = true
	}
	if r.ScanReport == nil || r.checkArtifact(*r.ScanReport, "scan") != nil {
		return ErrRefused
	}
	if r.ScanReport.Format != "safe-json" || r.ScanReport.SchemaVersion != "1" {
		return ErrRefused
	}
	if r.SBOM != nil {
		if r.SBOMFailureReason != "" || r.checkArtifact(*r.SBOM, "sbom") != nil || r.SBOM.Format != "CycloneDX" || (r.SBOM.SchemaVersion != "1.5" && r.SBOM.SchemaVersion != "1.6" && r.SBOM.SchemaVersion != "1.7") {
			return ErrRefused
		}
	}
	return nil
}

func knownFailure(s string) bool {
	switch s {
	case "scanner-unavailable", "db-unavailable", "db-expired", "policy-rejected", "sbom-unavailable", "sbom-invalid", "input-unavailable", "build-unproven":
		return true
	}
	return false
}

func (r Record) checkArtifact(a Artifact, kind string) error {
	if a.ImageDigest != r.ImageDigest || a.ScanPipelineID != r.ScanPipelineID || a.ScanJobID != r.ScanJobID || !checksumPattern.MatchString(a.SHA256) {
		return ErrRefused
	}
	return r.Run().CheckURL(kind, a.URL)
}

// 現在時刻で成功・scan/DBの24時間以内・例外期限を検査。単体では最新attemptの選択を保証しない。
// 配備ゲートはapplication-manifest/ci/trusted_attempts.pyでも後続attemptと時間失効を確認する。
func (r Record) Adopt(now time.Time) error {
	if r.ValidateStored() != nil || r.Decision != "passed" || r.ScannedAt.After(now) || now.Sub(r.ScannedAt) > 24*time.Hour || r.Database.UpdatedAt.After(now) || now.Sub(r.Database.UpdatedAt) > 24*time.Hour {
		return ErrRefused
	}
	for _, e := range r.Exceptions {
		if !e.ExpiresAt.After(now) {
			return ErrRefused
		}
	}
	return nil
}

// Duplicate keys and unknown fields are rejected, including a self-checksum.
// Decoder errors never leave this package: they can contain untrusted values.
// 重複キー・未知フィールド・末尾の追加JSON・過大入力を拒否。自己checksumはrecord内でなくmanifest参照側に保持する。
func DecodeRecord(b []byte) (Record, error) {
	var r Record
	if len(b) > 1<<20 || uniqueKeys(b) != nil {
		return r, ErrRefused
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		return r, ErrRefused
	}
	keys := []string{"schemaVersion", "service", "sourceProjectId", "sourceCommit", "buildPipelineId", "buildJobId", "imageRepository", "imageTag", "imageDigest", "scanPipelineId", "scanJobId", "scanJobStatus", "inputKind", "inputArchiveSha256", "trivyVersion", "database", "scannedAt", "policyRevision", "policySha256", "exceptions", "decision", "failureReason", "sbom", "sbomFailureReason", "scanReport"}
	if len(fields) != len(keys) {
		return r, ErrRefused
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || key != "database" && key != "sbom" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return r, ErrRefused
		}
	}
	for _, key := range []string{"sbom", "scanReport"} {
		if string(bytes.TrimSpace(fields[key])) != "null" && requiredObject(fields[key], []string{"imageDigest", "scanPipelineId", "scanJobId", "url", "sha256", "format", "schemaVersion"}) != nil {
			return r, ErrRefused
		}
	}
	if string(bytes.TrimSpace(fields["database"])) != "null" && requiredObject(fields["database"], []string{"version", "updatedAt", "metadataSha256"}) != nil {
		return r, ErrRefused
	}
	var exceptions []json.RawMessage
	if json.Unmarshal(fields["exceptions"], &exceptions) != nil {
		return r, ErrRefused
	}
	for _, e := range exceptions {
		if requiredObject(e, []string{"id", "rule", "package", "path", "createdAt", "expiresAt"}) != nil {
			return r, ErrRefused
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return Record{}, ErrRefused
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF || r.ValidateStored() != nil {
		return Record{}, ErrRefused
	}
	return r, nil
}

func requiredObject(b []byte, keys []string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != len(keys) {
		return ErrRefused
	}
	for _, key := range keys {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return ErrRefused
		}
	}
	return nil
}

func uniqueKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var read func(int) error
	read = func(depth int) error {
		if depth > 64 {
			return ErrRefused
		}
		t, err := d.Token()
		if err != nil {
			return ErrRefused
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					s, ok := k.(string)
					if e != nil || !ok || keys[s] {
						return ErrRefused
					}
					keys[s] = true
					if read(depth+1) != nil {
						return ErrRefused
					}
				}
			case '[':
				for d.More() {
					if read(depth+1) != nil {
						return ErrRefused
					}
				}
			default:
				return ErrRefused
			}
			if _, err = d.Token(); err != nil {
				return ErrRefused
			}
		}
		return nil
	}
	if read(0) != nil {
		return ErrRefused
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrRefused
	}
	return nil
}
