// manifestのPodSpec注釈から選択されたrunを検証する共通Go consumer。
// record/scan/SBOM、保護writerのnative record、実APIのjob/mainを照合する。
// この関数単独の成功は配備許可ではなく、manifestのtrusted consumerがregistry・最新attempt・期限も確認する。
package release

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// ConsumerSelectionはrender済みPodSpec注釈のデータ。source/build/scanのIDと外部checksumを個別に結び付ける。
type ConsumerSelection struct {
	Service         string `json:"service"`
	SourceProjectID int64  `json:"sourceProjectId"`
	SourceCommit    string `json:"sourceCommit"`
	ImageDigest     string `json:"imageDigest"`
	BuildPipelineID int64  `json:"buildPipelineId"`
	BuildJobID      int64  `json:"buildJobId"`
	ScanPipelineID  int64  `json:"scanPipelineId"`
	ScanJobID       int64  `json:"scanJobId"`
	RecordURL       string `json:"recordUrl"`
	RecordSHA256    string `json:"recordSha256"`
	SBOMURL         string `json:"sbomUrl"`
	SBOMSHA256      string `json:"sbomSha256"`
}

func DecodeConsumerSelection(b []byte) (ConsumerSelection, error) {
	var s ConsumerSelection
	keys := []string{"service", "sourceProjectId", "sourceCommit", "imageDigest", "buildPipelineId", "buildJobId", "scanPipelineId", "scanJobId", "recordUrl", "recordSha256", "sbomUrl", "sbomSha256"}
	if len(b) > 8192 || requiredObject(b, keys) != nil || strictData(b, &s) != nil || !shaPattern.MatchString(s.SourceCommit) || !checksumPattern.MatchString(s.RecordSHA256) || !checksumPattern.MatchString(s.SBOMSHA256) || s.BuildPipelineID <= 0 || s.BuildJobID <= 0 || s.BuildJobID == s.ScanJobID {
		return ConsumerSelection{}, ErrRefused
	}
	project := int64(86247025)
	if s.Service == "backend" {
		project = 86247033
	} else if s.Service != "frontend" {
		return ConsumerSelection{}, ErrRefused
	}
	run := Run{s.Service, s.ImageDigest, s.ScanPipelineID, s.ScanJobID}
	if s.SourceProjectID != project || run.CheckURL("record", s.RecordURL) != nil || run.CheckURL("sbom", s.SBOMURL) != nil {
		return ConsumerSelection{}, ErrRefused
	}
	return s, nil
}

func (a *SourceAPI) ReadProtectedMain(project int64) (string, error) {
	if a == nil || (project != 86247025 && project != 86247033) {
		return "", ErrRefused
	}
	var branch struct {
		Name      string
		Protected bool
		Commit    struct{ ID string }
	}
	if a.object(fmt.Sprintf("/projects/%d/repository/branches/main", project), &branch) != nil || branch.Name != "main" || !branch.Protected || !shaPattern.MatchString(branch.Commit.ID) {
		return "", ErrRefused
	}
	return branch.Commit.ID, nil
}

// ReadConsumerScan proves the immutable current scan, not registry identity,
// runtime compatibility, manifest authority or a rollback approval. Those are
// independent checks of the fixed trusted manifest consumer.
// 選択runを外部checksumで取得しnative writer由来と照合、処理前後の保護main SHAが同じか確認する。
// rollbackでも現在policyの再scanが必要であり、古いsourceを許すことと古いscanを許すことを分ける。
func ReadConsumerScan(a *SourceAPI, client *Client, s ConsumerSelection, rollback bool, now time.Time) (Record, error) {
	if a == nil || client == nil {
		return Record{}, ErrRefused
	}
	main, e := a.ReadProtectedMain(s.SourceProjectID)
	if e != nil {
		return Record{}, ErrRefused
	}
	run := Run{s.Service, s.ImageDigest, s.ScanPipelineID, s.ScanJobID}
	record, e := client.Read(run, "record", s.RecordURL, s.RecordSHA256)
	if e != nil {
		return Record{}, ErrRefused
	}
	r, e := DecodeRecord(record)
	if e != nil || r.Run() != run || r.SBOM == nil {
		return Record{}, ErrRefused
	}
	scan, e := client.Read(run, "scan", r.ScanReport.URL, r.ScanReport.SHA256)
	if e != nil {
		return Record{}, ErrRefused
	}
	bom, e := client.Read(run, "sbom", s.SBOMURL, s.SBOMSHA256)
	if e != nil {
		return Record{}, ErrRefused
	}
	native, e := a.ReadWriterExportedRecord(r, s.RecordSHA256)
	if e != nil {
		return Record{}, ErrRefused
	}
	r, e = CheckConsumerBundle(s, record, scan, bom, native, main, rollback, now)
	if e != nil {
		return Record{}, ErrRefused
	}
	fresh, e := a.ReadProtectedMain(s.SourceProjectID)
	if e != nil || fresh != main {
		return Record{}, ErrRefused
	}
	return r, nil
}

// 保護writerは権限と不変Packageを検証後、同pipelineのnative needsから受けたscan bytesをそのまま安全artifactへ出す。Reporterに公開するのはそのJSONだけでOCIはprivateのまま。
func (a *SourceAPI) ReadWriterExportedRecord(r Record, expectedSHA string) ([]byte, error) {
	return a.readWriterExportedRecord(r, expectedSHA, true)
}

// 古い成功native writerは由来の証明にはなるが、現在の安全性の証明にはならない。現在scan consumerではpolicyが現mainであることも必要。
func (a *SourceAPI) ReadOriginWriterRecord(r Record, expectedSHA string) ([]byte, error) {
	if r.Decision != "passed" {
		return nil, ErrRefused
	}
	return a.readWriterExportedRecord(r, expectedSHA, false)
}

func (a *SourceAPI) readWriterExportedRecord(r Record, expectedSHA string, requireCurrentPolicy bool) ([]byte, error) {
	if a == nil || !checksumPattern.MatchString(expectedSHA) || a.ValidateCompletedJobs(r) != nil {
		return nil, ErrRefused
	}
	if requireCurrentPolicy {
		main, e := a.ReadProtectedMain(r.SourceProjectID)
		if e != nil || main != r.PolicyRevision {
			return nil, ErrRefused
		}
	}
	var jobs []completedJob
	if a.object(fmt.Sprintf("/projects/%d/pipelines/%d/jobs?per_page=100", r.SourceProjectID, r.ScanPipelineID), &jobs) != nil || len(jobs) >= 100 {
		return nil, ErrRefused
	}
	var matched []byte
	count := 0
	for _, j := range jobs {
		if (j.Name != "release-record-store" && j.Name != "release-record-store-rescan") || j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != r.PolicyRevision || j.Pipeline.ID != r.ScanPipelineID || j.Pipeline.ProjectID != r.SourceProjectID || j.Pipeline.SHA != r.PolicyRevision {
			continue
		}
		var proof StoredBundle
		base := fmt.Sprintf("/projects/%d/jobs/%d/artifacts/.release-store/public/", r.SourceProjectID, j.ID)
		if a.object(base+"stored.json", &proof) != nil {
			return nil, ErrRefused
		}
		if proof.Run != r.Run() {
			continue
		}
		url, _ := r.Run().URL("record")
		if proof.SchemaVersion != 1 || proof.RecordURL != url || proof.RecordSHA256 != expectedSHA || proof.Decision != r.Decision || !proof.CompletedJobAuthority || !proof.ProtectedMainWriter || !proof.RecordWrittenLast || !proof.ServerDuplicateDenied {
			return nil, ErrRefused
		}
		req, e := http.NewRequest(http.MethodGet, "https://gitlab.com/api/v4"+base+"record.json", nil)
		if e != nil {
			return nil, ErrRefused
		}
		req.Header.Set("PRIVATE-TOKEN", a.token)
		res, e := a.http.Do(req)
		if e != nil {
			return nil, ErrRefused
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
		res.Body.Close()
		if e != nil || res.StatusCode != 200 || len(b) > 2*1024*1024 || Checksum(b) != expectedSHA {
			return nil, ErrRefused
		}
		parsed, e := DecodeRecord(b)
		if e != nil || parsed.Run() != r.Run() {
			return nil, ErrRefused
		}
		matched = b
		count++
	}
	if count != 1 {
		return nil, ErrRefused
	}
	return matched, nil
}

// 選択tupleをrecord・safe scan・SBOM・native recordへ結合する純粋検証。別runの後続失敗一覧はこの入力にはない。
func CheckConsumerBundle(s ConsumerSelection, record, scan, sbom, nativeRecord []byte, currentSourceMain string, rollback bool, now time.Time) (Record, error) {
	r, e := ValidateBundle(record, scan, sbom, now)
	if e != nil || r.Adopt(now) != nil || !shaPattern.MatchString(currentSourceMain) || r.PolicyRevision != currentSourceMain || (!rollback && r.SourceCommit != currentSourceMain) || !checksumPattern.MatchString(s.RecordSHA256) || Checksum(record) != s.RecordSHA256 || Checksum(nativeRecord) != s.RecordSHA256 || r.Run().CheckURL("record", s.RecordURL) != nil || r.SBOM == nil {
		return Record{}, ErrRefused
	}
	if s.Service != r.Service || s.SourceProjectID != r.SourceProjectID || s.SourceCommit != r.SourceCommit || s.ImageDigest != r.ImageDigest || s.BuildPipelineID != r.BuildPipelineID || s.BuildJobID != r.BuildJobID || s.ScanPipelineID != r.ScanPipelineID || s.ScanJobID != r.ScanJobID || s.SBOMURL != r.SBOM.URL || s.SBOMSHA256 != r.SBOM.SHA256 {
		return Record{}, ErrRefused
	}
	return r, nil
}

// 固定の安全JSON artifactだけを読む。trace/OCI binary/archive/任意pathへ到達させない。project contributorが新Package名へ書けても、偽造recordだけでは通さない。
func (a *SourceAPI) ReadNativeRecord(r Record, expectedSHA string) ([]byte, error) {
	if a == nil || !checksumPattern.MatchString(expectedSHA) || a.ValidateCompletedJobs(r) != nil {
		return nil, ErrRefused
	}
	path := fmt.Sprintf("https://gitlab.com/api/v4/projects/%d/jobs/%d/artifacts/.image-policy/public/record.json", r.SourceProjectID, r.ScanJobID)
	req, e := http.NewRequest(http.MethodGet, path, nil)
	if e != nil {
		return nil, ErrRefused
	}
	req.Header.Set("PRIVATE-TOKEN", a.token)
	res, e := a.http.Do(req)
	if e != nil {
		return nil, ErrRefused
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if e != nil || len(b) > 2*1024*1024 || Checksum(b) != expectedSHA {
		return nil, ErrRefused
	}
	native, e := DecodeRecord(b)
	if e != nil || native.Run() != r.Run() {
		return nil, ErrRefused
	}
	return b, nil
}
