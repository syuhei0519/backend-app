// 固定registry候補の再検査要求を厳密に解析し、元buildの由来を解決する。
// registry-originが使用し、未証明buildを今回scanのIDで補完しない。registry_request_test.goを参照。
package release

// RegistryRequestはデータのみ。未使用fieldを明示的に空とし、legacy build selectorと別record/runの混在を防ぐ。
type RegistryRequest struct {
	SchemaVersion   int    `json:"schemaVersion"`
	OriginKind      string `json:"originKind"`
	Service         string `json:"service"`
	SourceCommit    string `json:"sourceCommit"`
	ImageDigest     string `json:"imageDigest"`
	BuildPipelineID int64  `json:"buildPipelineId"`
	BuildJobID      int64  `json:"buildJobId"`
	ScanPipelineID  int64  `json:"scanPipelineId"`
	ScanJobID       int64  `json:"scanJobId"`
	RecordURL       string `json:"recordUrl"`
	RecordSHA256    string `json:"recordSha256"`
}

func DecodeRegistryRequest(b []byte) (RegistryRequest, error) {
	var q RegistryRequest
	if len(b) > 4096 || requiredObject(b, []string{"schemaVersion", "originKind", "service", "sourceCommit", "imageDigest", "buildPipelineId", "buildJobId", "scanPipelineId", "scanJobId", "recordUrl", "recordSha256"}) != nil || strictData(b, &q) != nil || q.SchemaVersion != 1 || (q.Service != "frontend" && q.Service != "backend") || !shaPattern.MatchString(q.SourceCommit) || !digestPattern.MatchString(q.ImageDigest) {
		return RegistryRequest{}, ErrRefused
	}
	switch q.OriginKind {
	case "legacy":
		if q.BuildPipelineID <= 0 || q.BuildJobID <= 0 || q.ScanPipelineID != 0 || q.ScanJobID != 0 || q.RecordURL != "" || q.RecordSHA256 != "" {
			return RegistryRequest{}, ErrRefused
		}
	case "record":
		if q.BuildPipelineID != 0 || q.BuildJobID != 0 || q.ScanPipelineID <= 0 || q.ScanJobID <= 0 || !checksumPattern.MatchString(q.RecordSHA256) || (Run{q.Service, q.ImageDigest, q.ScanPipelineID, q.ScanJobID}).CheckURL("record", q.RecordURL) != nil {
			return RegistryRequest{}, ErrRefused
		}
	default:
		return RegistryRequest{}, ErrRefused
	}
	return q, nil
}

// ResolveRegistryOriginは由来だけを証明する。imageをdownloadせず、昔のscan判定/DB/SBOMを今回入力へコピーしない。
func ResolveRegistryOrigin(a *SourceAPI, client *Client, w WriterContext, request []byte, allowHistorical bool) (LegacyRegistryOrigin, error) {
	q, e := DecodeRegistryRequest(request)
	if e != nil || a == nil || a.ValidateRetrievalScan(w) != nil || (!allowHistorical && q.SourceCommit != w.Commit) {
		return LegacyRegistryOrigin{}, ErrRefused
	}
	if allowHistorical && a.validateHistoricalPipeline(w) != nil {
		return LegacyRegistryOrigin{}, ErrRefused
	}
	project := int64(86247025)
	if q.Service == "backend" {
		project = 86247033
	}
	if w.ProjectID != project {
		return LegacyRegistryOrigin{}, ErrRefused
	}
	o := LegacyRegistryOrigin{q.Service, project, q.SourceCommit, q.BuildPipelineID, q.BuildJobID, q.ImageDigest}
	if q.OriginKind == "legacy" {
		if a.ValidateLegacyRegistryBuild(o) != nil {
			return LegacyRegistryOrigin{}, ErrRefused
		}
	} else {
		if client == nil {
			return LegacyRegistryOrigin{}, ErrRefused
		}
		run := Run{q.Service, q.ImageDigest, q.ScanPipelineID, q.ScanJobID}
		b, e := client.Read(run, "record", q.RecordURL, q.RecordSHA256)
		if e != nil {
			return LegacyRegistryOrigin{}, ErrRefused
		}
		r, e := DecodeRecord(b)
		if e != nil || r.Run() != run || r.SourceCommit != q.SourceCommit || r.Decision != "passed" || a.ValidateCompletedJobs(r) != nil {
			return LegacyRegistryOrigin{}, ErrRefused
		}
		native, e := a.ReadOriginWriterRecord(r, q.RecordSHA256)
		if e != nil || Checksum(native) != Checksum(b) {
			return LegacyRegistryOrigin{}, ErrRefused
		}
		o.BuildPipelineID, o.BuildJobID = r.BuildPipelineID, r.BuildJobID
	}
	if o.validate() != nil || a.ValidateRetrievalScan(w) != nil {
		return LegacyRegistryOrigin{}, ErrRefused
	}
	return o, nil
}
