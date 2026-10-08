// 公開直前の同一OCI/成功record結合。ci/publish-oci.shのpublication-checkが呼ぶ。
// ここはregistryへ書き込まない。公開側が別途live権限、SHAタグ不存在、公開後digest/labelを確認する。
package release

import "time"

// CheckPublicationInputは保存済み成功runと公開対象OCIを検証する。registry書込はしない。callerはlive公開権限、既存SHA tagを上書きしないこと、未存在時だけ同layoutをpushすること、公開後digest/labelを別途確認してから提案する。
func CheckPublicationInput(recordBytes, scanBytes, sbomBytes []byte, recordURL, recordSHA string, producer, consumer, crane []byte, archiveSHA string, archiveBytes int64, now time.Time) (Record, error) {
	r, err := ValidateBundle(recordBytes, scanBytes, sbomBytes, now)
	if err != nil || r.Adopt(now) != nil || !checksumPattern.MatchString(recordSHA) || Checksum(recordBytes) != recordSHA || r.Run().CheckURL("record", recordURL) != nil || r.InputArchiveSHA256 != archiveSHA || r.SourceCommit != r.PolicyRevision {
		return Record{}, ErrRefused
	}
	p, _, err := checkedOCIIdentity(r.SourceProjectID, r.SourceCommit, producer, consumer, crane, archiveSHA, archiveBytes)
	if err != nil || p.Digest != r.ImageDigest {
		return Record{}, ErrRefused
	}
	return r, nil
}
