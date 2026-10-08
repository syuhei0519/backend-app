package main

import (
	"encoding/json"
	"strings"
	"testing"
)

var imageConfig = "sha256:" + strings.Repeat("a", 64)

func imageReport(results string) []byte {
	return []byte(`{"SchemaVersion":2,"Trivy":{"Version":"0.75.0"},"ArtifactType":"container_image","ArtifactName":".oci/layout","Metadata":{"ImageID":"` + imageConfig + `"},"Results":[` + results + `]}`)
}

func TestImageGateBindsConfigAndKeepsSourceBoundary(t *testing.T) {
	r := imageReport(`{"Target":".oci/layout (alpine 3.23.0)"}`)
	if s, e := evaluateImage(policy, metadata, r, at, imageConfig); e != nil || !s.Pass {
		t.Fatal("valid image rejected")
	}
	if _, e := evaluate(policy, metadata, r, at); e == nil {
		t.Fatal("source mode accepted image")
	}
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(r), imageConfig, "sha256:"+strings.Repeat("b", 64), 1)),
		[]byte(strings.Replace(string(r), `"ArtifactName":".oci/layout"`, `"ArtifactName":"other/layout"`, 1)),
		[]byte(strings.Replace(string(r), "container_image", "filesystem", 1)),
		imageReport(""),
		imageReport(`{"Target":"/../../escape"}`),
		imageReport(`{"Target":"https://external/path"}`),
		[]byte(strings.Replace(string(r), `"ImageID":`, `"ImageID":"wrong","ImageID":`, 1)),
	} {
		if _, e := evaluateImage(policy, metadata, bad, at, imageConfig); e == nil {
			t.Fatal("unbound or unsafe image accepted")
		}
	}
	if _, e := evaluateImage(policy, metadata, r, at, ""); e == nil {
		t.Fatal("empty image config accepted")
	}
}

func TestImageFindingsUseSamePolicyAndRedaction(t *testing.T) {
	for _, c := range []struct {
		severity, fix string
		pass          bool
	}{
		{"CRITICAL", "2.0", false}, {"CRITICAL", "", true}, {"HIGH", "2.0", true}, {"UNKNOWN", "", true},
	} {
		r := imageReport(`{"Target":".oci/layout (alpine 3.23.0)","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-12345","PkgName":"demo","InstalledVersion":"1.0","FixedVersion":"` + c.fix + `","Severity":"` + c.severity + `"}]}`)
		s, e := evaluateImage(policy, metadata, r, at, imageConfig)
		if e != nil || s.Pass != c.pass || len(s.Findings) != 1 || s.Findings[0].Path != "image/os" {
			t.Fatal("image severity or target decision incorrect")
		}
	}
	const sentinel = "PRIVATE_MATCH_MUST_NOT_ESCAPE"
	r := imageReport(`{"Target":"/etc/example.txt","Secrets":[{"RuleID":"fixture-rule","Severity":"HIGH","Match":"` + sentinel + `","Title":"` + sentinel + `"}]}`)
	s, e := evaluateImage(policy, metadata, r, at, imageConfig)
	b, _ := json.Marshal(s)
	if e != nil || s.Pass || strings.Contains(string(b), sentinel) || s.Findings[0].Path != "etc/example.txt" {
		t.Fatal("image secret redaction or rejection failed")
	}
	if _, e := evaluateImage(policy, []byte(`{"Version":2,"UpdatedAt":"2000-01-01T00:00:00Z"}`), r, at, imageConfig); e == nil {
		t.Fatal("image gate accepted expired DB")
	}
}
