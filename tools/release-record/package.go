// 固定projectのGeneric Package証跡client。record/scan/SBOMをdigestとscan run単位で保存・取得する。
// RunのURLは内部で構成し、MRの任意URLへ資格情報を送らない。writerはstore_bundle.go、consumerはconsumer.go。
// Package release defines fixed, authenticated, run-specific evidence storage.
// Runtime OCI scanning and its acceptance are separate PE017F/B work.
package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

var (
	ErrAbsent     = errors.New("fixed evidence artifact absent")
	ErrRefused    = errors.New("fixed evidence operation refused")
	ErrChanged    = errors.New("immutable evidence content differs")
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

const maxEvidenceBytes = 32 << 20

type Run struct {
	Service    string
	Digest     string
	PipelineID int64
	JobID      int64
}

func (r Run) File(kind string) (string, error) {
	if r.Service != "frontend" && r.Service != "backend" || !digestPattern.MatchString(r.Digest) || r.PipelineID <= 0 || r.JobID <= 0 {
		return "", ErrRefused
	}
	prefix, suffix := "", ""
	switch kind {
	case "record":
		prefix, suffix = "release-record", ".json"
	case "sbom":
		prefix, suffix = "sbom", ".cdx.json"
	case "scan":
		prefix, suffix = "scan-report", ".json"
	default:
		return "", ErrRefused
	}
	return fmt.Sprintf("%s-%d-%d%s", prefix, r.PipelineID, r.JobID, suffix), nil
}

// 1 digestに複数scan runを保存。pipeline/job IDをファイル名へ含め、再試行jobも別runとして保持する。
func (r Run) URL(kind string) (string, error) {
	file, err := r.File(kind)
	if err != nil {
		return "", err
	}
	project := int64(86247025)
	if r.Service == "backend" {
		project = 86247033
	}
	return fmt.Sprintf("https://gitlab.com/api/v4/projects/%d/packages/generic/core-platform-%s/sha256-%s/%s", project, r.Service, r.Digest[7:], file), nil
}

// MRのURLは内部で構成した宛先との一致確認にだけ使う。host/project/query/path/redirectや資格情報送信先をMR側から選ばせない。
func (r Run) CheckURL(kind, supplied string) error {
	expected, err := r.URL(kind)
	if err != nil || supplied != expected {
		return ErrRefused
	}
	return nil
}

type Client struct {
	token string
	http  *http.Client
}

// 30秒timeoutとredirect拒否で有界に取得する。tokenの実値はログやエラーへ含めない。
func NewClient(jobToken string) (*Client, error) {
	if jobToken == "" {
		return nil, ErrRefused
	}
	return &Client{token: jobToken, http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) request(method string, r Run, kind string, body []byte) ([]byte, error) {
	u, err := r.URL(kind)
	if err != nil || len(body) > maxEvidenceBytes {
		return nil, ErrRefused
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		return nil, ErrRefused
	}
	req.Header.Set("JOB-TOKEN", c.token)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrRefused
	}
	defer resp.Body.Close()
	if method == http.MethodGet && resp.StatusCode == http.StatusNotFound {
		return nil, ErrAbsent
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ErrRefused
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEvidenceBytes+1))
	if err != nil || len(data) > maxEvidenceBytes {
		return nil, ErrRefused
	}
	return data, nil
}

func Checksum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ConfirmDuplicateDeniedはこの固定run URLへ保存して読戻した同一bytesだけを再送する。HTTP400は受入で確認した同名upload拒否。成功や認証/通信異常ならrecord公開を拒否し、scan単体では採用を許可しない。
func (c *Client) ConfirmDuplicateDenied(r Run, kind string, data []byte) error {
	u, e := r.URL(kind)
	if e != nil || kind != "scan" || len(data) == 0 || len(data) > maxEvidenceBytes {
		return ErrRefused
	}
	stored, e := c.request(http.MethodGet, r, kind, nil)
	if e != nil || !bytes.Equal(stored, data) {
		return ErrRefused
	}
	req, e := http.NewRequest(http.MethodPut, u, bytes.NewReader(data))
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
	if res.StatusCode != http.StatusBadRequest {
		return ErrRefused
	}
	stored, e = c.request(http.MethodGet, r, kind, nil)
	if e != nil || !bytes.Equal(stored, data) {
		return ErrRefused
	}
	return nil
}

// 固定URLと外部checksumを確認してbytesを返す。URL一致だけで内容の不変性を保証しない。
func (c *Client) Read(r Run, kind, suppliedURL, expectedSHA string) ([]byte, error) {
	if err := r.CheckURL(kind, suppliedURL); err != nil {
		return nil, err
	}
	if len(expectedSHA) != 64 {
		return nil, ErrRefused
	}
	b, err := c.request(http.MethodGet, r, kind, nil)
	if err != nil {
		return nil, err
	}
	if Checksum(b) != expectedSHA {
		return nil, ErrChanged
	}
	return b, nil
}

// This must execute inside the fixed per-project single writer resource_group,
// with server generic duplicate-file publication disabled. The client check is
// not a replacement for those server/authority preconditions.
// 既存GETを照合し、同一bytesなら冪等成功・異なるbytesなら停止。
// GET→PUTの競合対策は単一writer直列化とサーバーduplicate拒否も必要。
func (c *Client) WriteImmutable(r Run, kind string, data []byte) error {
	if len(data) == 0 || len(data) > maxEvidenceBytes {
		return ErrRefused
	}
	existing, err := c.request(http.MethodGet, r, kind, nil)
	if err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return ErrChanged
	}
	if !errors.Is(err, ErrAbsent) {
		return err
	}
	if _, err = c.request(http.MethodPut, r, kind, data); err != nil {
		return err
	}
	stored, err := c.request(http.MethodGet, r, kind, nil)
	if err != nil || !bytes.Equal(stored, data) {
		return ErrChanged
	}
	return nil
}
