// 配備digestの固定runのrecord/scan/SBOMを認証付きで読む。package-read-releaseが呼ぶ。
// URLとchecksumを固定し、完全bytesの取得と対応を検証する。取得成功は配備承認や全保持設定の監査とは別。
package release

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type ReleaseReadInput struct {
	Service      string `json:"service"`
	Digest       string `json:"digest"`
	PipelineID   int64  `json:"pipelineId"`
	JobID        int64  `json:"jobId"`
	RecordURL    string `json:"recordUrl"`
	RecordSHA256 string `json:"recordSha256"`
}

type ReleaseReadProof struct {
	Input              ReleaseReadInput `json:"input"`
	Decision           string           `json:"decision"`
	FilesRead          int              `json:"filesRead"`
	ChecksumsMatched   bool             `json:"checksumsMatched"`
	AdoptionAuthorized bool             `json:"adoptionAuthorized"`
}

func DecodeReleaseReadInputs(b []byte) ([]ReleaseReadInput, error) {
	if len(b) == 0 || len(b) > 32768 || uniqueKeys(b) != nil {
		return nil, ErrRefused
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var inputs []ReleaseReadInput
	if d.Decode(&inputs) != nil || len(inputs) < 1 || len(inputs) > 4 {
		return nil, ErrRefused
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrRefused
	}
	seen := map[string]bool{}
	for _, input := range inputs {
		run := Run{input.Service, input.Digest, input.PipelineID, input.JobID}
		if run.CheckURL("record", input.RecordURL) != nil || !checksumPattern.MatchString(input.RecordSHA256) || seen[input.RecordURL] {
			return nil, ErrRefused
		}
		seen[input.RecordURL] = true
	}
	return inputs, nil
}

// Packageの読取権限とbytes/run同一性だけを確認する。Phase2配備gateとは別で、古い/失敗recordも証跡としては残る。
func (c *Client) ReadReleaseEvidence(input ReleaseReadInput) (ReleaseReadProof, error) {
	var proof ReleaseReadProof
	if c == nil || !checksumPattern.MatchString(input.RecordSHA256) {
		return proof, ErrRefused
	}
	run := Run{input.Service, input.Digest, input.PipelineID, input.JobID}
	b, e := c.Read(run, "record", input.RecordURL, input.RecordSHA256)
	if e != nil {
		return proof, e
	}
	record, e := DecodeRecord(b)
	if e != nil || record.Run() != run || record.ScanReport == nil {
		return proof, ErrRefused
	}
	if _, e = c.Read(run, "scan", record.ScanReport.URL, record.ScanReport.SHA256); e != nil {
		return proof, e
	}
	count := 2
	if record.SBOM != nil {
		if _, e = c.Read(run, "sbom", record.SBOM.URL, record.SBOM.SHA256); e != nil {
			return proof, e
		}
		count++
	}
	return ReleaseReadProof{input, record.Decision, count, true, false}, nil
}

// 予約済みの非release digestと、このmanifest job固有の新file名を使う。既存名へのHTTP400だけではread-only権限を証明できない。
func (c *Client) ConfirmSourceWriteForbidden(service string, manifestPipeline, manifestJob int64) error {
	if c == nil {
		return ErrRefused
	}
	digest, e := StorageFixtureDigest(service)
	if e != nil {
		return ErrRefused
	}
	u, e := (Run{service, digest, manifestPipeline, manifestJob}).URL("record")
	if e != nil {
		return ErrRefused
	}
	req, e := http.NewRequest(http.MethodPut, u, bytes.NewReader([]byte(`{"schemaVersion":0,"forbiddenWriteFixture":true}`)))
	if e != nil {
		return ErrRefused
	}
	req.Header.Set("JOB-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, e := c.http.Do(req)
	if e != nil {
		return ErrRefused
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode != http.StatusForbidden {
		return ErrRefused
	}
	return nil
}
