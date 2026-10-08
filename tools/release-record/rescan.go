// 同じdigestの異なるscan runの対応を検証する。元buildを保持し、scan jobは新しいIDで識別する。
// 失敗した新run自体の拒否と、後から古い成功一式を再選択させないmanifest側ゲートは別の責任。
package release

import "time"

type RescanProof struct {
	PriorRun             Run    `json:"priorRun"`
	CurrentRun           Run    `json:"currentRun"`
	PriorRecordURL       string `json:"priorRecordUrl"`
	PriorRecordSHA256    string `json:"priorRecordSha256"`
	SameImageAndBuild    bool   `json:"sameImageAndBuild"`
	DifferentDatabase    bool   `json:"differentDatabase"`
	CurrentFailed        bool   `json:"currentFailed"`
	PriorSuccessFallback bool   `json:"priorSuccessFallback"`
}

// ValidateRescanPairは同じ元OCIの第2runと以前の成功scanを結合する。失敗/DB未更新の証跡は残せるが、DifferentDatabaseを独立確認するまでAT04を満たさない。
func ValidateRescanPair(current Record, priorBytes []byte, now time.Time) (Record, RescanProof, error) {
	var proof RescanProof
	prior, e := DecodeRecord(priorBytes)
	if e != nil || prior.Decision != "passed" || prior.ScannedAt.After(now) || current.ScannedAt.After(now) || current.ValidateStored() != nil || prior.Service != current.Service || prior.SourceProjectID != current.SourceProjectID || prior.SourceCommit != current.SourceCommit || prior.PolicyRevision != current.PolicyRevision || prior.PolicySHA256 != current.PolicySHA256 || prior.ImageDigest != current.ImageDigest || prior.ImageRepository != current.ImageRepository || prior.ImageTag != current.ImageTag || prior.BuildPipelineID != current.BuildPipelineID || prior.BuildJobID != current.BuildJobID || prior.InputKind != current.InputKind || prior.InputArchiveSHA256 != current.InputArchiveSHA256 || prior.ScanPipelineID != current.ScanPipelineID || prior.ScanJobID == current.ScanJobID || !current.ScannedAt.After(prior.ScannedAt) {
		return Record{}, proof, ErrRefused
	}
	u, _ := prior.Run().URL("record")
	proof = RescanProof{prior.Run(), current.Run(), u, Checksum(priorBytes), true, current.Database != nil && !prior.Database.UpdatedAt.Equal(current.Database.UpdatedAt), current.Decision != "passed", false}
	return prior, proof, nil
}

// manual受入rescanでも実DB更新を観測する必要がある。負例fixtureでも欠落/同じmetadataの日時を新しく偽装しない。
func NewDatabaseForRescan(priorBytes, dbBytes []byte, now time.Time) error {
	prior, e := DecodeRecord(priorBytes)
	if e != nil || prior.Decision != "passed" || prior.ScannedAt.After(now) || DatabaseFailure(dbBytes, now) != "" {
		return ErrRefused
	}
	var db struct {
		Version                             int
		UpdatedAt, NextUpdate, DownloadedAt time.Time
	}
	if strictData(dbBytes, &db) != nil || prior.Database.UpdatedAt.Equal(db.UpdatedAt) {
		return ErrRefused
	}
	return nil
}
