// 再取得archiveと既存digest・元build・現在scanを結合する。
// 以前のbuild tar checksumを再取得tarへ流用せず、今回の実bytesで検証する。registry_identity_test.goを参照。
package release

// BindRegistryIdentityは新規取得archiveを、外部checksumで識別した不変recordの元buildへ結合する。昔のscanは現在採用を許可しない。callerは固定runのGET、元build実権限、registry digest/labelを確認し、今回bytesを現policy/DBで再scanする。この境界自体は通信/registry書込をしない。
func BindRegistryIdentity(c ScanContext, priorBytes []byte, priorURL, priorSHA string, producer, consumer, crane []byte, archiveSHA string, archiveBytes int64) (Record, string, error) {
	prior, err := DecodeRecord(priorBytes)
	if err != nil || !checksumPattern.MatchString(priorSHA) || Checksum(priorBytes) != priorSHA || prior.Run().CheckURL("record", priorURL) != nil || prior.Decision != "passed" || prior.BuildPipelineID <= 0 || prior.BuildJobID <= 0 || c.ProjectID != prior.SourceProjectID || c.PipelineID <= 0 || c.JobID <= 0 || c.JobID == prior.BuildJobID || c.JobID == prior.ScanJobID || !shaPattern.MatchString(c.Commit) || c.ProjectURL != "https://gitlab.com/syuhei-platform-engineering-lab/"+prior.Service+"-app" {
		return Record{}, "", ErrRefused
	}
	p, config, err := checkedOCIIdentity(c.ProjectID, prior.SourceCommit, producer, consumer, crane, archiveSHA, archiveBytes)
	if err != nil || p.Digest != prior.ImageDigest {
		return Record{}, "", ErrRefused
	}
	// registry exportでtar bytesが異なることがある。今回checksumで実入力を識別し、manifest digestと元buildは維持する。昔のscan判定/DB/SBOM/期限/policyを新runへ流用しない。
	r := Record{Service: prior.Service, SourceProjectID: prior.SourceProjectID, SourceCommit: prior.SourceCommit, BuildPipelineID: prior.BuildPipelineID, BuildJobID: prior.BuildJobID, ImageRepository: prior.ImageRepository, ImageTag: prior.ImageTag, ImageDigest: prior.ImageDigest, ScanPipelineID: c.PipelineID, ScanJobID: c.JobID, InputKind: "registry-retrieval", InputArchiveSHA256: archiveSHA, PolicyRevision: c.Commit}
	return r, config, nil
}
