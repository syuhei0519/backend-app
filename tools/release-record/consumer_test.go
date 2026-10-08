// 選択tupleとrecord/scan/SBOM/native bytes・policyの対応をfixtureで確認する。
// 通信fixture成功と現GitLabの実権限監査は別。後続attemptはmanifest側で検証する。
package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConsumerRejectsPackageNativeAndAnnotationSwaps(t *testing.T) {
	r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
	r, e := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r)
	u, _ := r.Run().URL("record")
	s := ConsumerSelection{r.Service, r.SourceProjectID, r.SourceCommit, r.ImageDigest, r.BuildPipelineID, r.BuildJobID, r.ScanPipelineID, r.ScanJobID, u, Checksum(b), r.SBOM.URL, r.SBOM.SHA256}
	if _, e := CheckConsumerBundle(s, b, scan, sbom, b, r.SourceCommit, false, now); e != nil {
		t.Fatal("valid actual bundle refused", e)
	}
	for _, change := range []func(*ConsumerSelection){func(x *ConsumerSelection) { x.ScanJobID++ }, func(x *ConsumerSelection) { x.BuildJobID++ }, func(x *ConsumerSelection) { x.SBOMSHA256 = strings.Repeat("0", 64) }, func(x *ConsumerSelection) { x.RecordURL += "?latest=true" }, func(x *ConsumerSelection) { x.SourceProjectID = 86247033 }, func(x *ConsumerSelection) { x.ImageDigest = "sha256:" + strings.Repeat("0", 64) }} {
		changed := s
		change(&changed)
		if _, e := CheckConsumerBundle(changed, b, scan, sbom, b, r.SourceCommit, false, now); e == nil {
			t.Fatal("annotation swap accepted")
		}
	}
	if _, e := CheckConsumerBundle(s, b, scan, sbom, append(append([]byte{}, b...), '\n'), r.SourceCommit, false, now); e == nil {
		t.Fatal("Package differs from native job bytes accepted")
	}
	if _, e := CheckConsumerBundle(s, b, scan, sbom, b, strings.Repeat("0", 40), true, now); e == nil {
		t.Fatal("rollback adopted old policy")
	}
	if _, e := CheckConsumerBundle(s, b, scan, sbom, b, r.SourceCommit, false, now.Add(25*time.Hour)); e == nil {
		t.Fatal("expired scan adopted")
	}
}

func TestNativeRecordUsesOnlyCompletedJobSafeJSON(t *testing.T) {
	for _, bad := range []string{"", "changed-bytes", "missing", "redirect", "oversized", "unfinished"} {
		t.Run(bad, func(t *testing.T) {
			r := fixtureRecord()
			b, _ := json.Marshal(r)
			a, _ := NewSourceAPI("fixture-read-token")
			a.http.Transport = sourceTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Host != "gitlab.com" || req.Header.Get("PRIVATE-TOKEN") != "fixture-read-token" {
					t.Fatal("unsafe native record request")
				}
				status := 200
				var body []byte
				switch req.URL.Path {
				case fmt.Sprintf("/api/v4/projects/%d/jobs/%d", r.SourceProjectID, r.ScanJobID):
					j := completedJob{ID: r.ScanJobID, Name: "image-scan", Status: "success", Ref: "main"}
					j.Commit.ID = r.PolicyRevision
					j.Pipeline.ID = r.ScanPipelineID
					j.Pipeline.ProjectID = r.SourceProjectID
					j.Pipeline.SHA = r.PolicyRevision
					if bad == "unfinished" {
						j.Status = "running"
					}
					body, _ = json.Marshal(j)
				case fmt.Sprintf("/api/v4/projects/%d/jobs/%d", r.SourceProjectID, r.BuildJobID):
					j := completedJob{ID: r.BuildJobID, Name: "container-build-oci", Status: "success", Ref: "main"}
					j.Commit.ID = r.SourceCommit
					j.Pipeline.ID = r.BuildPipelineID
					j.Pipeline.ProjectID = r.SourceProjectID
					j.Pipeline.SHA = r.SourceCommit
					body, _ = json.Marshal(j)
				case fmt.Sprintf("/api/v4/projects/%d/jobs/%d/artifacts/.image-policy/public/record.json", r.SourceProjectID, r.ScanJobID):
					body = b
					if bad == "changed-bytes" {
						body = append(append([]byte{}, b...), '\n')
					}
					if bad == "missing" {
						status = 404
					}
					if bad == "redirect" {
						status = 302
					}
					if bad == "oversized" {
						body = []byte(strings.Repeat("x", 2*1024*1024+1))
					}
				default:
					t.Fatal("unexpected native path")
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			got, e := a.ReadNativeRecord(r, Checksum(b))
			if (e == nil) != (bad == "") {
				t.Fatal("native record authority mismatch", bad)
			}
			if e == nil && string(got) != string(b) {
				t.Fatal("native bytes changed")
			}
		})
	}
}

func TestConsumerRollbackRequiresNewPolicyRunForOldImage(t *testing.T) {
	r, config, policy, db, scan, raw, sbom, now := scanRecordFixture()
	current := strings.Repeat("e", 40)
	r.PolicyRevision = current
	r, e := CompleteScanRecord(r, "", config, policy, db, scan, raw, sbom, now)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r)
	u, _ := r.Run().URL("record")
	s := ConsumerSelection{r.Service, r.SourceProjectID, r.SourceCommit, r.ImageDigest, r.BuildPipelineID, r.BuildJobID, r.ScanPipelineID, r.ScanJobID, u, Checksum(b), r.SBOM.URL, r.SBOM.SHA256}
	if _, e := CheckConsumerBundle(s, b, scan, sbom, b, current, false, now); e == nil {
		t.Fatal("ordinary proposal accepted historical image")
	}
	got, e := CheckConsumerBundle(s, b, scan, sbom, b, current, true, now)
	if e != nil || got.SourceCommit != r.SourceCommit || got.PolicyRevision != current {
		t.Fatal("fresh rollback scan did not preserve old image and current policy")
	}
}

func TestConsumerSelectionRejectsAmbiguousData(t *testing.T) {
	r := fixtureRecord()
	u, _ := r.Run().URL("record")
	s := ConsumerSelection{r.Service, r.SourceProjectID, r.SourceCommit, r.ImageDigest, r.BuildPipelineID, r.BuildJobID, r.ScanPipelineID, r.ScanJobID, u, strings.Repeat("a", 64), r.SBOM.URL, r.SBOM.SHA256}
	b, _ := json.Marshal(s)
	if _, e := DecodeConsumerSelection(b); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{string(b) + "{}", strings.Replace(string(b), `"service":"frontend"`, `"service":null`, 1), strings.Replace(string(b), `"sourceProjectId":86247025`, `"sourceProjectId":86247033`, 1), strings.Replace(string(b), `"service":"frontend"`, `"service":"frontend","service":"frontend"`, 1), strings.Replace(string(b), `"service":"frontend"`, `"service":"frontend","latest":true`, 1), strings.Replace(string(b), `"buildJobId":11`, `"buildJobId":0`, 1)} {
		if _, e := DecodeConsumerSelection([]byte(bad)); e == nil {
			t.Fatal("ambiguous selection accepted")
		}
	}
}
