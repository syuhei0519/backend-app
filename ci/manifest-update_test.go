package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhase2ProposalRequiresActualSuccessfulVerifier(t *testing.T) {
	sha := strings.Repeat("a", 40)
	values := map[string]string{"VERIFY_RELEASE_JOB_ID": "23", "SCAN_JOB_ID": "21", "BUILD_JOB_ID": "11", "CI_PROJECT_ID": "86247033", "CI_PIPELINE_ID": "20", "CI_COMMIT_SHA": sha}
	for _, bad := range []string{"", "failed", "wrong-job", "wrong-source", "wrong-pipeline", "wrong-project", "unprotected-ref", "allow-failure"} {
		t.Run(bad, func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/projects/86247033/jobs/23" {
					t.Fatal("verifier checked unknown endpoint")
				}
				job := map[string]any{"id": 23, "name": "verify-oci-release", "status": "success", "allow_failure": false, "ref": "main", "commit": map[string]string{"id": sha}, "pipeline": map[string]any{"id": 20, "project_id": 86247033, "sha": sha}}
				switch bad {
				case "failed":
					job["status"] = "failed"
				case "wrong-job":
					job["name"] = "propose-manifest-update"
				case "wrong-source":
					job["commit"] = map[string]string{"id": strings.Repeat("b", 40)}
				case "wrong-pipeline":
					job["pipeline"] = map[string]any{"id": 19, "project_id": 86247033, "sha": sha}
				case "wrong-project":
					job["pipeline"] = map[string]any{"id": 20, "project_id": 86247025, "sha": sha}
				case "unprotected-ref":
					job["ref"] = "codex/fixture"
				case "allow-failure":
					job["allow_failure"] = true
				}
				_ = json.NewEncoder(w).Encode(job)
			}))
			defer fixture.Close()
			c := client{fixture.URL, "fixture-token", proposalHTTPClient()}
			if (checkDeliveryJob(&c, func(k string) string { return values[k] }) == nil) != (bad == "") {
				t.Fatal("verifier authority wrong")
			}
		})
	}
}

func TestProposalAPIRefusesRedirectAndSuppressesResponseBody(t *testing.T) {
	received := false
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true; w.WriteHeader(200) }))
	defer outside.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", outside.URL)
			w.WriteHeader(307)
			return
		}
		w.WriteHeader(403)
		_, _ = w.Write([]byte("private-fixture-response-body"))
	}))
	defer origin.Close()
	c := client{origin.URL, "fixture-proposal-token", proposalHTTPClient()}
	for _, path := range []string{"/redirect", "/refused"} {
		if err := c.call("GET", path, nil, nil); err == nil || strings.Contains(err.Error(), "private-fixture-response-body") || strings.Contains(err.Error(), "fixture-proposal-token") {
			t.Fatalf("unsafe refusal: %v", err)
		}
	}
	if received {
		t.Fatal("proposal credential followed redirect")
	}
}

func TestUpdateChangesOnlyDeclaredKeys(t *testing.T) {
	input := "image:\n  repository: registry.example/app\n  tag: \"old\"\n  digest: \"sha256:old\"\nrelease:\n  sourceProjectId: \"1\"\n  sourceCommit: \"old\"\n  pipelineId: \"1\"\n  pipelineUrl: \"https://old\"\nuntouched:\n  value: true\n"
	values := map[string]map[string]string{
		"image":   {"repository": "registry.example/new-app", "tag": "new", "digest": "sha256:new"},
		"release": {"sourceProjectId": "2", "sourceCommit": "new", "pipelineId": "3", "pipelineUrl": "https://new"},
	}
	got, err := update(input, values)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"repository: \"registry.example/new-app\"", "tag: \"new\"", "digest: \"sha256:new\"", "sourceProjectId: \"2\"", "sourceCommit: \"new\"", "pipelineId: \"3\"", "pipelineUrl: \"https://new\"", "untouched:\n  value: true"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("missing %q in:\n%s", expected, got)
		}
	}
}

func TestUpdateRejectsMissingContractKey(t *testing.T) {
	_, err := update("image:\n  tag: old\n", map[string]map[string]string{"image": {"tag": "new", "digest": "sha256:new"}})
	if err == nil {
		t.Fatal("expected missing digest to fail")
	}
}

func TestPhase2ProposalBindsOneCurrentRunBeforeWrites(t *testing.T) {
	sha, hash := strings.Repeat("a", 40), strings.Repeat("b", 64)
	base := "https://gitlab.com/api/v4/projects/86247033/packages/generic/core-platform-backend/sha256-" + hash + "/"
	valid := map[string]string{"SERVICE": "backend", "CI_PROJECT_ID": "86247033", "SOURCE_PROJECT_ID": "86247033", "CI_COMMIT_SHA": sha, "SOURCE_COMMIT": sha, "IMAGE_TAG": sha, "IMAGE_DIGEST": "sha256:" + hash, "CI_COMMIT_BRANCH": "main", "CI_COMMIT_REF_PROTECTED": "true", "CI_PIPELINE_ID": "20", "BUILD_PIPELINE_ID": "10", "BUILD_JOB_ID": "11", "SCAN_PIPELINE_ID": "20", "SCAN_JOB_ID": "21", "RELEASE_RECORD_URL": base + "release-record-20-21.json", "RELEASE_RECORD_SHA256": hash, "SBOM_URL": base + "sbom-20-21.cdx.json", "SBOM_SHA256": hash}
	for _, changed := range []struct{ key, value string }{{}, {"CI_COMMIT_REF_PROTECTED", "false"}, {"CI_COMMIT_BRANCH", "codex/fixture"}, {"SOURCE_COMMIT", strings.Repeat("c", 40)}, {"SOURCE_PROJECT_ID", "86247025"}, {"IMAGE_TAG", "latest"}, {"IMAGE_DIGEST", "sha256:bad"}, {"SCAN_PIPELINE_ID", "19"}, {"BUILD_PIPELINE_ID", "010"}, {"BUILD_JOB_ID", "21"}, {"SCAN_JOB_ID", "0"}, {"RELEASE_RECORD_URL", "https://outside.invalid/record.json"}, {"RELEASE_RECORD_URL", base + "release-record-20-22.json"}, {"SBOM_URL", base + "sbom-20-22.cdx.json"}, {"SBOM_SHA256", "bad"}} {
		t.Run(changed.key+changed.value, func(t *testing.T) {
			values := map[string]string{}
			for k, v := range valid {
				values[k] = v
			}
			if changed.key != "" {
				values[changed.key] = changed.value
			}
			got, err := phase2Release(func(k string) string { return values[k] })
			if changed.key == "" {
				if err != nil || got["schemaVersion"] != "2" || got["buildPipelineId"] != "10" || got["scanPipelineId"] != "20" {
					t.Fatalf("valid original build/current run refused: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe proposal identity accepted")
			}
		})
	}
}

func TestPhase2ReleaseAddsRunFieldsAndPreservesOtherValues(t *testing.T) {
	input := "image:\n  digest: old\nrelease:\n  sourceCommit: old\nuntouched:\n  value: true\n"
	vals := map[string]string{"sourceCommit": "current", "schemaVersion": "2", "scanJobId": "21"}
	expanded, err := addPhase2ReleaseKeys(input, vals)
	if err != nil {
		t.Fatal(err)
	}
	result, err := update(expanded, map[string]map[string]string{"release": vals})
	if err != nil || !strings.Contains(result, "scanJobId: \"21\"") || !strings.Contains(result, "untouched:\n  value: true") || !strings.Contains(result, "image:\n  digest: old") {
		t.Fatalf("bad run insertion: %v\n%s", err, result)
	}
	for _, bad := range []string{"image:\n  digest: old", "release:\n  sourceCommit: old\nrelease:\n  sourceCommit: other", "release:\n  sourceCommit: old\n  sourceCommit: other"} {
		if _, err := addPhase2ReleaseKeys(bad, vals); err == nil {
			t.Fatal("ambiguous or missing release accepted")
		}
	}
}

func TestPhase2ReleaseRemovesLegacyPipelineAuthorityWithExplicitNull(t *testing.T) {
	old := "image:\n  tag: old\nrelease:\n  sourceProjectId: \"86247033\"\n  pipelineId: \"10\"\n  pipelineUrl: \"old-url\"\n"
	got, err := addPhase2ReleaseKeys(old, map[string]string{"schemaVersion": "2", "scanPipelineId": "20"})
	if err != nil || !strings.Contains(got, "  pipelineId: null") || !strings.Contains(got, "  pipelineUrl: null") {
		t.Fatal("Legacy Helm inheritance not explicitly cleared")
	}
	if _, err = addPhase2ReleaseKeys(old+"  pipelineId: \"11\"\n", map[string]string{"schemaVersion": "2"}); err == nil {
		t.Fatal("Duplicate legacy pipeline key accepted")
	}
}
