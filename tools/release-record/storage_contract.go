package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// StorageFixtureDigest reserves a non-image fixture version. Its payload is
// deliberately NOT a release-record v2 and cannot be adopted by DecodeRecord.
func StorageFixtureDigest(service string) (string, error) {
	if service != "frontend" && service != "backend" {
		return "", ErrRefused
	}
	return "sha256:" + Checksum([]byte("Core Platform PE017C package storage fixture: "+service)), nil
}

type StorageArtifact struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type StorageProof struct {
	Fixture              bool              `json:"fixture"`
	Phase2Adoptable      bool              `json:"phase2Adoptable"`
	Service              string            `json:"service"`
	SourceCommit         string            `json:"sourceCommit"`
	PipelineID           int64             `json:"pipelineId"`
	JobID                int64             `json:"jobId"`
	Digest               string            `json:"digest"`
	UploadReadChecksum   bool              `json:"uploadReadChecksum"`
	SameBytesIdempotent  bool              `json:"sameBytesIdempotent"`
	DifferentBytesDenied bool              `json:"differentBytesDenied"`
	ServerDuplicateHTTP  int               `json:"serverDuplicateHttp"`
	StoredBytesUnchanged bool              `json:"storedBytesUnchanged"`
	Artifacts            []StorageArtifact `json:"artifacts"`
}

// Actual, bounded Package capability test with the job's own CI_JOB_TOKEN.
// This is not image scanning and does not assert release-record acceptance.
func (c *Client) StorageContract(r Run, sourceCommit string) (StorageProof, error) {
	var proof StorageProof
	expected, err := StorageFixtureDigest(r.Service)
	if err != nil || r.Digest != expected || !shaPattern.MatchString(sourceCommit) {
		return proof, ErrRefused
	}
	payload := []byte(`{"schemaVersion":0,"packageContractFixture":true,"phase2Adoptable":false}`)
	if _, err := DecodeRecord(payload); err == nil {
		return proof, ErrRefused
	}
	proof = StorageProof{Fixture: true, Service: r.Service, SourceCommit: sourceCommit, PipelineID: r.PipelineID, JobID: r.JobID, Digest: r.Digest}
	for _, kind := range []string{"record", "sbom", "scan"} {
		data, _ := json.Marshal(struct {
			SchemaVersion int    `json:"schemaVersion"`
			Fixture       bool   `json:"packageContractFixture"`
			Kind          string `json:"kind"`
		}{0, true, kind})
		if c.WriteImmutable(r, kind, data) != nil || c.WriteImmutable(r, kind, data) != nil {
			return StorageProof{}, ErrRefused
		}
		u, _ := r.URL(kind)
		if _, err := c.Read(r, kind, u, Checksum(data)); err != nil {
			return StorageProof{}, ErrRefused
		}
		if err := c.WriteImmutable(r, kind, payload); !errors.Is(err, ErrChanged) {
			return StorageProof{}, ErrRefused
		}
		proof.Artifacts = append(proof.Artifacts, StorageArtifact{kind, u, Checksum(data)})
	}
	// Deliberately bypass the client preflight on our reserved fixture only, to
	// prove the server duplicate prohibition itself. Never target a real release.
	u, _ := r.URL("record")
	req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return StorageProof{}, ErrRefused
	}
	req.Header.Set("JOB-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return StorageProof{}, ErrRefused
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 400 && resp.StatusCode != 403 && resp.StatusCode != 409 {
		return StorageProof{}, ErrRefused
	}
	if _, err := c.Read(r, "record", proof.Artifacts[0].URL, proof.Artifacts[0].SHA256); err != nil {
		return StorageProof{}, ErrRefused
	}
	proof.UploadReadChecksum, proof.SameBytesIdempotent, proof.DifferentBytesDenied, proof.StoredBytesUnchanged = true, true, true, true
	proof.ServerDuplicateHTTP = resp.StatusCode
	return proof, nil
}
