package main

import (
	"context"
	oci "core-platform/oci-input"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 1 || os.Getenv("CI_PROJECT_ID") != "86247033" || os.Getenv("CI_PROJECT_URL") != "https://gitlab.com/syuhei-platform-engineering-lab/backend-app" || os.Getenv("CI_JOB_NAME") != "container-build-oci" || os.Getenv("CI_COMMIT_BRANCH") != "main" || os.Getenv("CI_COMMIT_REF_PROTECTED") != "true" || os.Getenv("PHASE2_DELIVERY_ENABLED") != "true" || os.Getenv("OCI_DELIVERY_VALIDATION_ENABLED") != "true" || os.Getenv("CI_REGISTRY_IMAGE") != "registry.gitlab.com/syuhei-platform-engineering-lab/backend-app" || os.Getenv("CI_REGISTRY_PASSWORD") != os.Getenv("CI_JOB_TOKEN") {
		fmt.Fprintln(os.Stderr, "Fixed protected tag preflight refused")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exists, err := oci.RegistrySHAExists(ctx, "backend", os.Getenv("CI_COMMIT_SHA"), os.Getenv("CI_REGISTRY_USER"), os.Getenv("CI_JOB_TOKEN"), http.DefaultTransport)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Registry tag state unavailable; build stopped")
		os.Exit(1)
	}
	if exists {
		fmt.Fprintln(os.Stderr, "Existing SHA requires protected registry reinspection; no build started")
		os.Exit(10)
	}
}
