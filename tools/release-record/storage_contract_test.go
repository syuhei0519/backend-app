package release

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStorageContractProvesServerRefusalAndPreservedBytes(t *testing.T) {
	c, _ := NewClient("synthetic-nonissued-job-token")
	stored := map[string]string{}
	puts, denied := 0, 0
	c.http.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == "PUT" {
			puts++
			if _, ok := stored[req.URL.String()]; ok {
				denied++
				return response(400, "PRIVATE_BODY_NOT_OUTPUT"), nil
			}
			b, _ := io.ReadAll(req.Body)
			stored[req.URL.String()] = string(b)
			return response(201, ""), nil
		}
		if v, ok := stored[req.URL.String()]; ok {
			return response(200, v), nil
		}
		return response(404, "absent"), nil
	})
	d, _ := StorageFixtureDigest("frontend")
	p, err := c.StorageContract(Run{"frontend", d, 1, 2}, strings.Repeat("a", 40))
	if err != nil || p.Phase2Adoptable || !p.Fixture || !p.StoredBytesUnchanged || p.ServerDuplicateHTTP != 400 || len(p.Artifacts) != 3 || puts != 4 || denied != 1 {
		t.Fatal("actual server duplicate refusal/fixture boundaries not proven")
	}
	for _, data := range stored {
		if _, err := DecodeRecord([]byte(data)); err == nil {
			t.Fatal("package-auth fixture adopted as image release")
		}
	}
	if _, err := c.StorageContract(run(), strings.Repeat("a", 40)); err == nil || puts != 4 {
		t.Fatal("duplicate probe targeted a real image digest")
	}
}
