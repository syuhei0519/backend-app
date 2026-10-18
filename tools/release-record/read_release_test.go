package release

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseReaderActualBytesAndRunSubstitution(t *testing.T) {
	r := fixtureRecord()
	scan := []byte(`{"schemaVersion":1,"pass":true}`)
	sbom := []byte(`{"bomFormat":"CycloneDX"}`)
	r.ScanReport.SHA256 = Checksum(scan)
	r.SBOM.SHA256 = Checksum(sbom)
	record, _ := json.Marshal(r)
	u, _ := r.Run().URL("record")
	input := ReleaseReadInput{r.Service, r.ImageDigest, r.ScanPipelineID, r.ScanJobID, u, Checksum(record)}
	c, _ := NewClient("synthetic-nonissued-job-token")
	calls := 0
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" {
			t.Fatal("reader wrote source evidence")
		}
		switch req.URL.String() {
		case u:
			return response(200, string(record)), nil
		case r.ScanReport.URL:
			return response(200, string(scan)), nil
		case r.SBOM.URL:
			return response(200, string(sbom)), nil
		}
		return response(404, "private"), nil
	})
	p, e := c.ReadReleaseEvidence(input)
	if e != nil || p.FilesRead != 3 || !p.ChecksumsMatched || p.AdoptionAuthorized || calls != 3 {
		t.Fatal("actual byte read proof failed")
	}
	// A well-formed checksum and URL for another run cannot select this record.
	wrong := input
	wrong.JobID++
	wrong.RecordURL, _ = (Run{wrong.Service, wrong.Digest, wrong.PipelineID, wrong.JobID}).URL("record")
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) { return response(200, string(record)), nil })
	if _, e = c.ReadReleaseEvidence(wrong); e == nil {
		t.Fatal("other job's record adopted as requested run")
	}
	wrong = input
	wrong.RecordSHA256 = strings.Repeat("b", 64)
	if _, e = c.ReadReleaseEvidence(wrong); e == nil {
		t.Fatal("wrong external checksum accepted")
	}
	// A failed scan with unavailable SBOM is readable evidence, never adoption.
	r.Decision = "failed"
	r.ScanJobStatus = "failed"
	r.FailureReason = "sbom-unavailable"
	r.SBOMFailureReason = "sbom-unavailable"
	r.SBOM = nil
	record, _ = json.Marshal(r)
	input.RecordSHA256 = Checksum(record)
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == u {
			return response(200, string(record)), nil
		}
		return response(200, string(scan)), nil
	})
	p, e = c.ReadReleaseEvidence(input)
	if e != nil || p.FilesRead != 2 || p.Decision != "failed" || p.AdoptionAuthorized {
		t.Fatal("failed historical evidence misclassified")
	}
}

func TestReleaseReaderInputAndForbiddenWriteStatus(t *testing.T) {
	r := run()
	u, _ := r.URL("record")
	i := ReleaseReadInput{r.Service, r.Digest, r.PipelineID, r.JobID, u, strings.Repeat("a", 64)}
	b, _ := json.Marshal([]ReleaseReadInput{i})
	if _, e := DecodeReleaseReadInputs(b); e != nil {
		t.Fatal("fixed input refused")
	}
	for _, bad := range []string{string(b) + "{}", strings.Replace(string(b), `"jobId":220`, `"jobId":220,"jobId":220`, 1), strings.Replace(string(b), `"service":"frontend"`, `"service":null`, 1), strings.Replace(string(b), "gitlab.com", "other.invalid", 1), "[]"} {
		if _, e := DecodeReleaseReadInputs([]byte(bad)); e == nil {
			t.Fatal("ambiguous or foreign input accepted")
		}
	}
	c, _ := NewClient("synthetic-nonissued-job-token")
	for _, status := range []int{200, 201, 302, 400, 401, 403, 409} {
		c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			digest, _ := StorageFixtureDigest("frontend")
			expected, _ := (Run{"frontend", digest, 999, 1000}).URL("record")
			if req.Method != "PUT" || req.URL.String() != expected {
				t.Fatal("write probe touched real release or old filename")
			}
			return response(status, "private response"), nil
		})
		e := c.ConfirmSourceWriteForbidden("frontend", 999, 1000)
		if (e == nil) != (status == 403) {
			t.Fatal("duplicate or success mistaken for write authorization denial")
		}
	}
}
