package main

import (
	release "github.com/syuhei0519/backend-app/tools/release-record"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

func check() error {
	if len(os.Args) != 1 || os.Getenv("AT17_RELEASE_READ") != "true" || os.Getenv("CI_PROJECT_ID") != "86247034" || os.Getenv("CI_JOB_NAME") != "package-release-reader" || os.Getenv("CI_COMMIT_BRANCH") != "main" || os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" || os.Getenv("CI_PIPELINE_SOURCE") != "api" {
		return release.ErrRefused
	}
	ids := make([]int64, 2)
	for i, key := range []string{"CI_PIPELINE_ID", "CI_JOB_ID"} {
		s := os.Getenv(key)
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
			return release.ErrRefused
		}
		ids[i] = n
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 32769))
	if e != nil || len(b) > 32768 {
		return release.ErrRefused
	}
	inputs, e := release.DecodeReleaseReadInputs(b)
	if e != nil {
		return release.ErrRefused
	}
	c, e := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	seen := map[string]bool{}
	services := map[string]bool{}
	proofs := []release.ReleaseReadProof{}
	for _, input := range inputs {
		if seen[input.RecordURL] {
			return release.ErrRefused
		}
		seen[input.RecordURL] = true
		proof, e := c.ReadReleaseEvidence(input)
		if e != nil {
			return release.ErrRefused
		}
		proofs = append(proofs, proof)
		services[input.Service] = true
	}
	for service := range services {
		if c.ConfirmSourceWriteForbidden(service, ids[0], ids[1]) != nil {
			return release.ErrRefused
		}
	}
	out, e := json.MarshalIndent(struct {
		ManifestCommit     string                     `json:"manifestCommit"`
		PipelineID         int64                      `json:"pipelineId"`
		JobID              int64                      `json:"jobId"`
		Runs               []release.ReleaseReadProof `json:"runs"`
		ForbiddenWriteHTTP int                        `json:"forbiddenWriteHttp"`
		AdoptionAuthorized bool                       `json:"adoptionAuthorized"`
	}{os.Getenv("CI_COMMIT_SHA"), ids[0], ids[1], proofs, 403, false}, "", "  ")
	if e != nil || os.MkdirAll(".security/public", 0700) != nil || os.WriteFile(".security/public/package-release-reader.json", append(out, '\n'), 0600) != nil {
		return release.ErrRefused
	}
	return nil
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "release evidence reader refused")
		os.Exit(1)
	}
}
