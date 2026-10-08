package oci

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type registryRoundTripFunc func(*http.Request) (*http.Response, error)

func (f registryRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func registryResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}

const registryTestRepo = "syuhei-platform-engineering-lab/frontend-app"

func TestRegistryReadRejectsAuthorityAndWriteBeforeNetwork(t *testing.T) {
	hash := strings.Repeat("a", 64)
	scope := url.QueryEscape("repository:" + registryTestRepo + ":pull")
	goodAuth := "https://gitlab.com/jwt/auth?service=container_registry&scope=" + scope
	cases := []struct{ method, target string }{
		{"PUT", "https://registry.gitlab.com/v2/"},
		{"GET", "http://registry.gitlab.com/v2/"},
		{"GET", "https://other.invalid/v2/"},
		{"GET", "https://user@registry.gitlab.com/v2/"},
		{"GET", "https://registry.gitlab.com/v2/?scope=write"},
		{"GET", "https://registry.gitlab.com/v2/" + registryTestRepo + "/manifests/latest"},
		{"GET", "https://registry.gitlab.com/v2/other/blobs/sha256:" + hash},
		{"GET", "https://registry.gitlab.com/%76%32/"},
		{"GET", goodAuth + "&scope=" + scope},
		{"GET", goodAuth + "&arbitrary=value"},
		{"GET", "https://gitlab.com/jwt/auth?service=container_registry&scope=" + url.QueryEscape("repository:"+registryTestRepo+":pull,push")},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			called := false
			tr := RegistryReadTransport{Repository: registryTestRepo, Base: registryRoundTripFunc(func(*http.Request) (*http.Response, error) { called = true; return registryResponse(200, ""), nil })}
			req, _ := http.NewRequest(c.method, c.target, nil)
			if _, err := tr.RoundTrip(req); err == nil || called {
				t.Fatal("invalid request reached transport")
			}
		})
	}
}

func TestRegistryBlobCDNBoundAndCredentialFree(t *testing.T) {
	hash := strings.Repeat("a", 64)
	location := "https://cdn.registry.gitlab-static.net/gitlab/docker/registry/v2/blobs/sha256/aa/" + hash + "/data?signature=opaque"
	calls := 0
	tr := RegistryReadTransport{Repository: registryTestRepo, Base: registryRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			res := registryResponse(307, "")
			res.Header.Set("Location", location)
			return res, nil
		}
		if r.URL.String() != location || len(r.Header) != 0 || r.Method != "GET" {
			t.Fatal("CDN hop leaked headers or changed target")
		}
		return registryResponse(200, "blob"), nil
	})}
	req, _ := http.NewRequest("GET", "https://registry.gitlab.com/v2/"+registryTestRepo+"/blobs/sha256:"+hash, nil)
	req.Header.Set("Authorization", "Bearer fixture")
	req.Header.Set("Cookie", "fixture")
	req.Header.Set("Referer", "fixture")
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil || string(b) != "blob" || calls != 2 {
		t.Fatal("valid fixed blob hop failed")
	}
}

func TestRegistryRedirectRefusals(t *testing.T) {
	hash := strings.Repeat("a", 64)
	base := "https://cdn.registry.gitlab-static.net/gitlab/docker/registry/v2/blobs/sha256/aa/" + hash + "/data"
	for _, location := range []string{"https://other.invalid/blob", strings.Replace(base, hash, strings.Repeat("b", 64), 1), strings.Replace(base, "/data", "/%64ata", 1), base + "#fragment"} {
		calls := 0
		tr := RegistryReadTransport{Repository: registryTestRepo, Base: registryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			r := registryResponse(307, "")
			r.Header.Set("Location", location)
			return r, nil
		})}
		req, _ := http.NewRequest("GET", "https://registry.gitlab.com/v2/"+registryTestRepo+"/blobs/sha256:"+hash, nil)
		if _, err := tr.RoundTrip(req); err == nil || calls != 1 {
			t.Fatal("unexpected redirect followed")
		}
	}
	calls := 0
	tr := RegistryReadTransport{Repository: registryTestRepo, Base: registryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := registryResponse(307, "")
		r.Header.Set("Location", base)
		return r, nil
	})}
	req, _ := http.NewRequest("GET", "https://registry.gitlab.com/v2/"+registryTestRepo+"/blobs/sha256:"+hash, nil)
	if _, err := tr.RoundTrip(req); err == nil || calls != 2 {
		t.Fatal("second redirect not refused")
	}
}

func TestRegistryBodyLimits(t *testing.T) {
	for _, declared := range []bool{true, false} {
		tr := RegistryReadTransport{Repository: registryTestRepo, Base: registryRoundTripFunc(func(*http.Request) (*http.Response, error) {
			r := registryResponse(200, strings.Repeat("x", 1024*1024+1))
			if declared {
				r.ContentLength = 1024*1024 + 1
			}
			return r, nil
		})}
		req, _ := http.NewRequest("GET", "https://gitlab.com/jwt/auth?service=container_registry&scope="+url.QueryEscape("repository:"+registryTestRepo+":pull"), nil)
		r, err := tr.RoundTrip(req)
		if declared {
			if err == nil {
				t.Fatal("oversized declared body accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err == nil {
			t.Fatal("oversized streamed body accepted")
		}
	}
}
