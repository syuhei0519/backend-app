// scan終了時の段階markerと各成果物からschema v2 recordを確定する。ci/run-image-scan.shのEXIT trapが呼ぶ。
// 失敗箇所がSBOM/DB/scanner/inputかを分け、成功を推定せず安全な失敗証跡へ変換する。
package release

import (
	"bytes"
	"encoding/json"
	"path"
	"strings"
	"time"
)

type SafeFinding struct {
	Kind             string `json:"kind"`
	Rule             string `json:"rule"`
	Path             string `json:"path"`
	Package          string `json:"package,omitempty"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	FixedVersion     string `json:"fixedVersion,omitempty"`
	Severity         string `json:"severity"`
	Decision         string `json:"decision"`
	Exception        string `json:"exception,omitempty"`
}
type SafeScan struct {
	SchemaVersion int           `json:"schemaVersion"`
	Pass          bool          `json:"pass"`
	TrivyVersion  string        `json:"trivyVersion"`
	DBUpdatedAt   time.Time     `json:"dbUpdatedAt"`
	ScannedAt     time.Time     `json:"scannedAt"`
	Findings      []SafeFinding `json:"findings"`
	PolicySHA256  string        `json:"policySHA256"`
}

func strictData(b []byte, v any) error {
	if len(b) == 0 || len(b) > maxEvidenceBytes || uniqueKeys(b) != nil {
		return ErrRefused
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrRefused
	}
	return nil
}

// DatabaseFailureは固定DB metadataだけから分類し、raw診断を出さない。
func DatabaseFailure(b []byte, now time.Time) string {
	var db struct {
		Version                             int
		UpdatedAt, NextUpdate, DownloadedAt time.Time
	}
	if strictData(b, &db) != nil || db.Version != 2 || db.UpdatedAt.IsZero() {
		return "db-unavailable"
	}
	if db.UpdatedAt.After(now) || now.Sub(db.UpdatedAt) > 24*time.Hour {
		return "db-expired"
	}
	return ""
}

// CompleteScanRecord binds bytes produced in the scan job to its explicit run.
// The later protected writer must check actual job completion and original
// build/API identity before storing it. This function grants no upload authority.
// 元build/source/digest identityと実測したscan時刻・policy・DB metadata・成果物checksumを結ぶ。
// 公開可能な成功はすべての入力が整った場合のみ。raw reportはrecordへ丸ごと含めない。
func CompleteScanRecord(identity Record, outcome string, configDigest string, policyBytes, dbBytes, safeBytes, rawBytes, sbomBytes []byte, now time.Time) (Record, error) {
	r := identity
	r.SchemaVersion = 2
	r.TrivyVersion = "0.75.0"
	r.ScannedAt = now
	r.PolicySHA256 = Checksum(policyBytes)
	r.Exceptions = []AppliedException{}
	r.Database = nil
	r.SBOM = nil
	r.SBOMFailureReason = ""
	r.FailureReason = outcome
	r.Decision = "failed"
	r.ScanJobStatus = "failed"
	if outcome != "" && !knownFailure(outcome) {
		return Record{}, ErrRefused
	}
	var policy struct {
		SchemaVersion int    `json:"schemaVersion"`
		TrivyVersion  string `json:"trivyVersion"`
		DBMaxAgeHours int    `json:"dbMaxAgeHours"`
		Exceptions    []struct {
			ID, Kind, Rule, Package, Path, Owner, Reason string
			CreatedAt, ExpiresAt                         time.Time
		} `json:"exceptions"`
	}
	policyValid := strictData(policyBytes, &policy) == nil && policy.SchemaVersion == 1 && policy.TrivyVersion == "0.75.0" && policy.DBMaxAgeHours == 24 && policy.Exceptions != nil
	if !policyValid && outcome == "" {
		return Record{}, ErrRefused
	}
	var summary SafeScan
	hasSummary := strictData(safeBytes, &summary) == nil && summary.SchemaVersion == 1 && summary.TrivyVersion == "0.75.0" && summary.PolicySHA256 == r.PolicySHA256 && !summary.ScannedAt.IsZero() && !summary.ScannedAt.After(now) && now.Sub(summary.ScannedAt) <= 24*time.Hour && summary.Findings != nil
	if hasSummary {
		if outcome != "" && outcome != "policy-rejected" && outcome != "sbom-unavailable" && outcome != "sbom-invalid" {
			return Record{}, ErrRefused
		}
		r.ScannedAt = summary.ScannedAt
		if summary.Pass != (outcome != "policy-rejected") {
			return Record{}, ErrRefused
		}
		used := map[string]bool{}
		blocked := false
		for _, f := range summary.Findings {
			if !identifierPattern.MatchString(f.Rule) || !identifierPattern.MatchString(f.Path) || path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || f.Path == ".." || strings.HasPrefix(f.Path, "../") || strings.Contains(f.Path, ":") || (f.Package != "" && !identifierPattern.MatchString(f.Package)) || (f.Kind != "vulnerability" && f.Kind != "secret" && f.Kind != "misconfiguration") || (f.Decision != "warning" && f.Decision != "fail" && f.Decision != "excepted") || (f.Severity != "UNKNOWN" && f.Severity != "LOW" && f.Severity != "MEDIUM" && f.Severity != "HIGH" && f.Severity != "CRITICAL") {
				return Record{}, ErrRefused
			}
			if f.Decision == "fail" {
				blocked = true
			}
			if f.Exception != "" {
				if f.Decision != "excepted" {
					return Record{}, ErrRefused
				}
				if !policyValid {
					return Record{}, ErrRefused
				}
				found := false
				for _, p := range policy.Exceptions {
					if p.ID == f.Exception && p.Kind == f.Kind && p.Rule == f.Rule && p.Path == f.Path && p.Package == f.Package {
						found = true
						if !used[p.ID] {
							r.Exceptions = append(r.Exceptions, AppliedException{p.ID, p.Rule, p.Package, p.Path, p.CreatedAt, p.ExpiresAt})
							used[p.ID] = true
						}
					}
				}
				if !found {
					return Record{}, ErrRefused
				}
			} else if f.Decision == "excepted" {
				return Record{}, ErrRefused
			}
		}
		if summary.Pass == blocked {
			return Record{}, ErrRefused
		}
	} else {
		if outcome == "" {
			return Record{}, ErrRefused
		}
		var failure struct {
			SchemaVersion int    `json:"schemaVersion"`
			Status        string `json:"status"`
			FailureReason string `json:"failureReason"`
		}
		if strictData(safeBytes, &failure) != nil || failure.SchemaVersion != 1 || failure.Status != "failed" || failure.FailureReason != outcome {
			return Record{}, ErrRefused
		}
	}
	var db struct {
		Version      int
		UpdatedAt    time.Time
		NextUpdate   time.Time
		DownloadedAt time.Time
	}
	if len(dbBytes) > 0 && strictData(dbBytes, &db) == nil && db.Version == 2 && !db.UpdatedAt.IsZero() && !db.UpdatedAt.After(r.ScannedAt) {
		r.Database = &Database{2, db.UpdatedAt, Checksum(dbBytes)}
	}
	if hasSummary {
		if r.Database == nil || !summary.DBUpdatedAt.Equal(r.Database.UpdatedAt) {
			return Record{}, ErrRefused
		}
	}
	u, e := r.Run().URL("scan")
	if e != nil {
		return Record{}, ErrRefused
	}
	r.ScanReport = &Artifact{r.ImageDigest, r.ScanPipelineID, r.ScanJobID, u, Checksum(safeBytes), "safe-json", "1"}
	if outcome == "" {
		var rawIdentity struct {
			Metadata struct {
				ImageConfig struct {
					Config struct{ Labels map[string]string }
				}
			}
		}
		if uniqueKeys(rawBytes) != nil || json.Unmarshal(rawBytes, &rawIdentity) != nil || rawIdentity.Metadata.ImageConfig.Config.Labels["org.opencontainers.image.revision"] != r.SourceCommit || rawIdentity.Metadata.ImageConfig.Config.Labels["org.opencontainers.image.source"] != "https://gitlab.com/syuhei-platform-engineering-lab/"+r.Service+"-app" {
			return Record{}, ErrRefused
		}
		p, e := ValidateSBOM(sbomBytes, rawBytes, configDigest, now)
		if e != nil {
			return Record{}, ErrRefused
		}
		u, e = r.Run().URL("sbom")
		if e != nil {
			return Record{}, ErrRefused
		}
		r.SBOM = &Artifact{r.ImageDigest, r.ScanPipelineID, r.ScanJobID, u, p.SHA256, p.Format, p.SpecVersion}
		r.Decision = "passed"
		r.ScanJobStatus = "success"
	} else {
		if len(sbomBytes) != 0 {
			return Record{}, ErrRefused
		}
		r.SBOMFailureReason = outcome
	}
	if r.ValidateStored() != nil {
		return Record{}, ErrRefused
	}
	if outcome == "" && r.Adopt(now) != nil {
		return Record{}, ErrRefused
	}
	return r, nil
}
