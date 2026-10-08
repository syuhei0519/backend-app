// 検査済みregistry由来の解決。新規採用と歴史的sourceのrollbackを区別する。
// 古いbuildを使っても現在policy/DBのscan成功を免除しない。inspection_consumer.goで互換性/runtimeを確認する。
package release

import "time"

// ResolveInspectedRegistryOriginは実行中の保護inspectionと成功native scanの実bytesを結合する。読取/検査だけを許し、任意selectorやPackage uploadをruntime jobの権限へ変換しない。
func ResolveInspectedRegistryOrigin(a *SourceAPI, c *Client, w WriterContext, native []byte, now time.Time) (LegacyRegistryOrigin, Record, error) {
	return resolveInspectedRegistryOrigin(a, c, w, native, now, false)
}

func ResolveHistoricalInspectedRegistryOrigin(a *SourceAPI, c *Client, w WriterContext, native []byte, now time.Time) (LegacyRegistryOrigin, Record, error) {
	return resolveInspectedRegistryOrigin(a, c, w, native, now, true)
}

func resolveInspectedRegistryOrigin(a *SourceAPI, c *Client, w WriterContext, native []byte, now time.Time, historical bool) (LegacyRegistryOrigin, Record, error) {
	r, e := DecodeRecord(native)
	if e != nil || a == nil || (w.JobName != "registry-format-inspection" && w.JobName != "registry-runtime-inspection") || w.JobID <= 0 || w.ProjectID != r.SourceProjectID || w.PipelineID != r.ScanPipelineID || w.Commit != r.PolicyRevision || (!historical && r.SourceCommit != w.Commit) || r.InputKind != "registry-retrieval" || r.Adopt(now) != nil {
		return LegacyRegistryOrigin{}, Record{}, ErrRefused
	}
	if historical && a.validateHistoricalPipeline(w) != nil {
		return LegacyRegistryOrigin{}, Record{}, ErrRefused
	}
	if e := c.ValidateInspectionPrincipal(w); e != nil {
		return LegacyRegistryOrigin{}, Record{}, e
	}
	validate := func() error {
		if a.validateProtectedMainActor(w, []string{"source-scan", "security-policy-test", "image-scan", "release-record-store"}) != nil || a.ValidateCompletedJobs(r) != nil {
			return ErrRefused
		}
		j, e := a.job(w.ProjectID, r.ScanJobID)
		if e != nil || j.Name != "image-scan" || j.Ref != "main" {
			return ErrRefused
		}
		b, e := a.ReadWriterExportedRecord(r, Checksum(native))
		if e != nil || Checksum(b) != Checksum(native) {
			return ErrRefused
		}
		return nil
	}
	if validate() != nil {
		return LegacyRegistryOrigin{}, Record{}, ErrRefused
	}
	o := LegacyRegistryOrigin{r.Service, r.SourceProjectID, r.SourceCommit, r.BuildPipelineID, r.BuildJobID, r.ImageDigest}
	if o.validate() != nil || validate() != nil {
		return LegacyRegistryOrigin{}, Record{}, ErrRefused
	}
	return o, r, nil
}
