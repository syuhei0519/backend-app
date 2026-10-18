package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	release "github.com/syuhei0519/backend-app/tools/release-record"
)

type ReadProof struct {
	Service              string `json:"service"`
	ReadChecksum         bool   `json:"readChecksum"`
	FixtureNotDeployable bool   `json:"fixtureNotDeployable"`
	ForbiddenWriteHTTP   int    `json:"forbiddenWriteHttp"`
	SourcePipelineID     int64  `json:"sourcePipelineId"`
	SourceJobID          int64  `json:"sourceJobId"`
}

func main() {
	fail := func() { fmt.Fprintln(os.Stderr, "package reader contract rejected"); os.Exit(1) }
	if len(os.Args) != 1 || os.Getenv("AT12_PACKAGE_READ") != "true" || os.Getenv("CI_PROJECT_ID") != "86247034" || os.Getenv("CI_COMMIT_BRANCH") != "main" || os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" || (os.Getenv("CI_PIPELINE_SOURCE") != "api" && os.Getenv("CI_PIPELINE_SOURCE") != "web") {
		fail()
	}
	job, err := strconv.ParseInt(os.Getenv("CI_JOB_ID"), 10, 64)
	if err != nil || job <= 0 {
		fail()
	}
	pipeline, err := strconv.ParseInt(os.Getenv("CI_PIPELINE_ID"), 10, 64)
	if err != nil || pipeline <= 0 {
		fail()
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 32769))
	if err != nil || len(b) > 32768 {
		fail()
	}
	var inputs []release.StorageProof
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&inputs) != nil || len(inputs) != 2 {
		fail()
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		fail()
	}
	token := os.Getenv("CI_JOB_TOKEN")
	c, err := release.NewClient(token)
	if err != nil {
		fail()
	}
	seen := map[string]bool{}
	var proof []ReadProof
	for _, input := range inputs {
		digest, err := release.StorageFixtureDigest(input.Service)
		if err != nil || seen[input.Service] || input.Digest != digest || !input.Fixture || input.Phase2Adoptable || !input.UploadReadChecksum || !input.StoredBytesUnchanged || !input.SameBytesIdempotent || !input.DifferentBytesDenied || len(input.Artifacts) != 3 || input.JobID == job || (input.ServerDuplicateHTTP != 400 && input.ServerDuplicateHTTP != 403 && input.ServerDuplicateHTTP != 409) {
			fail()
		}
		seen[input.Service] = true
		run := release.Run{Service: input.Service, Digest: digest, PipelineID: input.PipelineID, JobID: input.JobID}
		kinds := map[string]bool{}
		for _, artifact := range input.Artifacts {
			if kinds[artifact.Kind] {
				fail()
			}
			kinds[artifact.Kind] = true
			data, err := c.Read(run, artifact.Kind, artifact.URL, artifact.SHA256)
			if err != nil {
				fail()
			}
			var fixture struct {
				SchemaVersion int    `json:"schemaVersion"`
				Fixture       bool   `json:"packageContractFixture"`
				Kind          string `json:"kind"`
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&fixture) != nil || fixture.SchemaVersion != 0 || !fixture.Fixture || fixture.Kind != artifact.Kind {
				fail()
			}
			if _, err := release.DecodeRecord(data); err == nil {
				fail()
			}
		}
		if !kinds["record"] || !kinds["sbom"] || !kinds["scan"] {
			fail()
		}
		// New, owned fixture file: an existing-file duplicate refusal would not
		// prove that the manifest token has read-only cross-project authority.
		probe := release.Run{Service: input.Service, Digest: digest, PipelineID: pipeline, JobID: job}
		u, err := probe.URL("record")
		if err != nil {
			fail()
		}
		req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader([]byte(`{"schemaVersion":0,"forbiddenWriteFixture":true}`)))
		if err != nil {
			fail()
		}
		req.Header.Set("JOB-TOKEN", token)
		req.Header.Set("Content-Type", "application/octet-stream")
		httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := httpClient.Do(req)
		if err != nil {
			fail()
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != 403 {
			fail()
		}
		proof = append(proof, ReadProof{input.Service, true, true, resp.StatusCode, input.PipelineID, input.JobID})
	}
	result := struct {
		Fixture        bool        `json:"fixture"`
		Adoptable      bool        `json:"phase2Adoptable"`
		ManifestCommit string      `json:"manifestCommit"`
		PipelineID     int64       `json:"pipelineId"`
		JobID          int64       `json:"jobId"`
		Sources        []ReadProof `json:"sources"`
	}{true, false, os.Getenv("CI_COMMIT_SHA"), pipeline, job, proof}
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil || os.MkdirAll(".security/public", 0700) != nil || os.WriteFile(".security/public/package-reader-contract.json", append(out, '\n'), 0600) != nil {
		fail()
	}
	fmt.Println("package reader contract validated; cross-project write refused")
}
