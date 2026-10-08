package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	release "core-platform/release-record"
)

func main() {
	fail := func() { fmt.Fprintln(os.Stderr, "package storage contract rejected"); os.Exit(1) }
	if len(os.Args) != 1 || os.Getenv("AT12_PACKAGE_CONTRACT") != "true" || os.Getenv("CI_COMMIT_BRANCH") != "main" || os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" || (os.Getenv("CI_PIPELINE_SOURCE") != "api" && os.Getenv("CI_PIPELINE_SOURCE") != "web") {
		fail()
	}
	service := "frontend"
	if os.Getenv("CI_PROJECT_ID") == "86247033" {
		service = "backend"
	} else if os.Getenv("CI_PROJECT_ID") != "86247025" {
		fail()
	}
	pipeline, err := strconv.ParseInt(os.Getenv("CI_PIPELINE_ID"), 10, 64)
	if err != nil || pipeline <= 0 {
		fail()
	}
	job, err := strconv.ParseInt(os.Getenv("CI_JOB_ID"), 10, 64)
	if err != nil || job <= 0 {
		fail()
	}
	c, err := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if err != nil {
		fail()
	}
	digest, err := release.StorageFixtureDigest(service)
	if err != nil {
		fail()
	}
	proof, err := c.StorageContract(release.Run{Service: service, Digest: digest, PipelineID: pipeline, JobID: job}, os.Getenv("CI_COMMIT_SHA"))
	if err != nil {
		fail()
	}
	b, err := json.MarshalIndent(proof, "", "  ")
	if err != nil || os.MkdirAll(".security/public", 0700) != nil || os.WriteFile(".security/public/package-contract.json", append(b, '\n'), 0600) != nil {
		fail()
	}
	fmt.Println("package storage contract validated; fixture is not deployable")
}
