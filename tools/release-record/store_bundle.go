// scan jobから受け渡した安全なrecord/scan/SBOMをGeneric Packageへ保存するwriter境界。
// 完成job・保護main・checksumを検証し、recordを最後に公開する。失敗記録の保存は配備許可ではない。
package release

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type StoredBundle struct {
	SchemaVersion         int    `json:"schemaVersion"`
	Run                   Run    `json:"run"`
	RecordURL             string `json:"recordUrl"`
	RecordSHA256          string `json:"recordSha256"`
	Decision              string `json:"decision"`
	CompletedJobAuthority bool   `json:"completedJobAuthority"`
	ProtectedMainWriter   bool   `json:"protectedMainWriter"`
	RecordWrittenLast     bool   `json:"recordWrittenLast"`
	ServerDuplicateDenied bool   `json:"serverDuplicateDenied"`
}

// ValidateBundle checks bytes again at the writer boundary. The native scan
// already checked private raw inventory; it is never exported to the writer.
// writer側でもbytesを再検証。SBOMのschema/時刻・scanとの対応が不一致なら保存を拒否する。
func ValidateBundle(recordBytes, scanBytes, sbomBytes []byte, now time.Time) (Record, error) {
	r, e := DecodeRecord(recordBytes)
	if e != nil || r.ScannedAt.After(now) || len(scanBytes) == 0 || len(scanBytes) > maxEvidenceBytes || Checksum(scanBytes) != r.ScanReport.SHA256 {
		return Record{}, ErrRefused
	}
	var s SafeScan
	if strictData(scanBytes, &s) == nil && s.SchemaVersion == 1 && s.Findings != nil {
		if s.PolicySHA256 != r.PolicySHA256 || s.TrivyVersion != r.TrivyVersion || !s.ScannedAt.Equal(r.ScannedAt) || r.Database == nil || !s.DBUpdatedAt.Equal(r.Database.UpdatedAt) || s.Pass != (r.Decision == "passed" || r.FailureReason == "sbom-unavailable" || r.FailureReason == "sbom-invalid") {
			return Record{}, ErrRefused
		}
	} else {
		var f struct {
			SchemaVersion int    `json:"schemaVersion"`
			Status        string `json:"status"`
			FailureReason string `json:"failureReason"`
		}
		if r.Decision == "passed" || strictData(scanBytes, &f) != nil || f.SchemaVersion != 1 || f.Status != "failed" || f.FailureReason != r.FailureReason {
			return Record{}, ErrRefused
		}
	}
	if r.SBOM == nil {
		if len(sbomBytes) != 0 {
			return Record{}, ErrRefused
		}
	} else {
		if len(sbomBytes) == 0 || len(sbomBytes) > maxEvidenceBytes || Checksum(sbomBytes) != r.SBOM.SHA256 || uniqueKeys(sbomBytes) != nil || r.SBOM.SchemaVersion != "1.7" {
			return Record{}, ErrRefused
		}
		sch, e := schemaSBOM()
		if e != nil {
			return Record{}, ErrRefused
		}
		value, e := jsonschema.UnmarshalJSON(bytes.NewReader(sbomBytes))
		if e != nil || sch.Validate(value) != nil {
			return Record{}, ErrRefused
		}
		var bom struct {
			Metadata struct {
				Timestamp time.Time `json:"timestamp"`
			} `json:"metadata"`
		}
		if json.Unmarshal(sbomBytes, &bom) != nil || bom.Metadata.Timestamp.IsZero() || bom.Metadata.Timestamp.After(now) || now.Sub(bom.Metadata.Timestamp) > 24*time.Hour {
			return Record{}, ErrRefused
		}
	}
	if r.Decision == "passed" && r.Adopt(now) != nil {
		return Record{}, ErrRefused
	}
	return r, nil
}

// 最初のsafe report uploadより前にjob/main権限とbytesを照合する。同一report bytesでサーバーduplicate拒否を確かめてからSBOM/recordを公開し、recordは最後に書く。reportだけでは採用できない。
func (c *Client) StoreCompletedBundle(a *SourceAPI, w WriterContext, recordBytes, scanBytes, sbomBytes []byte, now time.Time) (StoredBundle, error) {
	if w.JobName != "release-record-store" {
		return StoredBundle{}, ErrRefused
	}
	return c.storeCompletedBundle(a, w, recordBytes, scanBytes, sbomBytes, now)
}

func (c *Client) StoreRescanBundle(a *SourceAPI, w WriterContext, recordBytes, scanBytes, sbomBytes, priorBytes []byte, now time.Time) (StoredBundle, RescanProof, error) {
	var proof RescanProof
	if a == nil || c == nil || w.JobName != "release-record-store-rescan" {
		return StoredBundle{}, proof, ErrRefused
	}
	r, e := ValidateBundle(recordBytes, scanBytes, sbomBytes, now)
	if e != nil {
		return StoredBundle{}, proof, ErrRefused
	}
	prior, proof, e := ValidateRescanPair(r, priorBytes, now)
	if e != nil || a.ValidateCompletedJobs(prior) != nil {
		return StoredBundle{}, RescanProof{}, ErrRefused
	}
	if _, e = c.Read(prior.Run(), "record", proof.PriorRecordURL, proof.PriorRecordSHA256); e != nil {
		return StoredBundle{}, RescanProof{}, ErrRefused
	}
	stored, e := c.storeCompletedBundle(a, w, recordBytes, scanBytes, sbomBytes, now)
	return stored, proof, e
}

// scan→duplicate拒否確認→SBOM→recordの順。recordが揃わない途中状態から採用しない。
// 複数ファイルの書込はDB transactionのような原子的操作ではなく、途中失敗の既存reportは残り得る。
func (c *Client) storeCompletedBundle(a *SourceAPI, w WriterContext, recordBytes, scanBytes, sbomBytes []byte, now time.Time) (StoredBundle, error) {
	var proof StoredBundle
	if a == nil || c == nil {
		return proof, ErrRefused
	}
	r, e := ValidateBundle(recordBytes, scanBytes, sbomBytes, now)
	if e != nil || a.ValidateWriter(w, r) != nil || a.ValidateCompletedJobs(r) != nil {
		return proof, ErrRefused
	}
	if c.WriteImmutable(r.Run(), "scan", scanBytes) != nil {
		return proof, ErrRefused
	}
	if c.ConfirmDuplicateDenied(r.Run(), "scan", scanBytes) != nil {
		return proof, ErrRefused
	}
	if r.SBOM != nil && c.WriteImmutable(r.Run(), "sbom", sbomBytes) != nil {
		return proof, ErrRefused
	}
	if c.WriteImmutable(r.Run(), "record", recordBytes) != nil {
		return proof, ErrRefused
	}
	u, _ := r.Run().URL("record")
	proof = StoredBundle{1, r.Run(), u, Checksum(recordBytes), r.Decision, true, true, true, true}
	return proof, nil
}
