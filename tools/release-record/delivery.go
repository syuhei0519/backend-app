// 公開済みidentityを選択recordへ結合する。実公開jobとsafe proofを確認し提案前の検証へ渡す。
// ci/includes/oci-delivery.ymlのverify-oci-releaseに対応。manifest側での採用検証を省略するものではない。
package release

import (
	"encoding/json"
	"fmt"
)

type PublishedIdentity struct {
	SchemaVersion       int    `json:"schemaVersion"`
	ImageDigest         string `json:"imageDigest"`
	SourceCommit        string `json:"sourceCommit"`
	ScanPipelineID      int64  `json:"scanPipelineId"`
	ScanJobID           int64  `json:"scanJobId"`
	SameLayoutPublished *bool  `json:"sameLayoutPublished"`
	RegistryReused      *bool  `json:"registryReused"`
	RemoteDigestMatched bool   `json:"remoteDigestMatched"`
	RecordSHA256        string `json:"recordSha256"`
	ProposalAuthorized  *bool  `json:"proposalAuthorized"`
}

func (p PublishedIdentity) Check(r Record, recordSHA string) error {
	if p.SchemaVersion != 1 || p.ImageDigest != r.ImageDigest || p.SourceCommit != r.SourceCommit || p.ScanPipelineID != r.ScanPipelineID || p.ScanJobID != r.ScanJobID || !p.RemoteDigestMatched || !checksumPattern.MatchString(recordSHA) || p.RecordSHA256 != recordSHA || !explicitlyFalse(p.ProposalAuthorized) || p.SameLayoutPublished == nil || p.RegistryReused == nil {
		return ErrRefused
	}
	switch r.InputKind {
	case "build-oci":
		if !*p.SameLayoutPublished || *p.RegistryReused {
			return ErrRefused
		}
	case "registry-retrieval":
		if *p.SameLayoutPublished || !*p.RegistryReused {
			return ErrRefused
		}
	default:
		return ErrRefused
	}
	return nil
}

// delivery verifierは同pipeline内の一意な完了publisherの安全出力だけを読む。callerが渡すdotenv単体を権限根拠にしない。
func (a *SourceAPI) ReadPublishedIdentity(r Record, recordSHA string) error {
	if a == nil || r.ValidateStored() != nil || r.Decision != "passed" || !checksumPattern.MatchString(recordSHA) {
		return ErrRefused
	}
	var jobs []completedJob
	base := fmt.Sprintf("/projects/%d", r.SourceProjectID)
	if a.object(base+fmt.Sprintf("/pipelines/%d/jobs?per_page=100", r.ScanPipelineID), &jobs) != nil || len(jobs) >= 100 {
		return ErrRefused
	}
	count := 0
	for _, j := range jobs {
		if j.Name != "publish-oci" {
			continue
		}
		count++
		if j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != r.PolicyRevision || j.Pipeline.ID != r.ScanPipelineID || j.Pipeline.ProjectID != r.SourceProjectID || j.Pipeline.SHA != r.PolicyRevision {
			return ErrRefused
		}
		var raw json.RawMessage
		var p PublishedIdentity
		if a.object(base+fmt.Sprintf("/jobs/%d/artifacts/.oci-publish/public/published.json", j.ID), &raw) != nil || requiredObject(raw, []string{"schemaVersion", "imageDigest", "sourceCommit", "scanPipelineId", "scanJobId", "sameLayoutPublished", "registryReused", "remoteDigestMatched", "recordSha256", "proposalAuthorized"}) != nil || strictData(raw, &p) != nil || p.Check(r, recordSHA) != nil {
			return ErrRefused
		}
	}
	if count != 1 {
		return ErrRefused
	}
	return nil
}
