package oci

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Read-only fixed registry/auth transport. One known CDN blob hop is handled
// here with no credentials; the HTTP client never sees an arbitrary redirect.
type RegistryReadTransport struct {
	Base       http.RoundTripper
	Repository string
}

func (t RegistryReadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || t.Base == nil || (t.Repository != "syuhei-platform-engineering-lab/frontend-app" && t.Repository != "syuhei-platform-engineering-lab/backend-app") || req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Fragment != "" || req.URL.EscapedPath() != req.URL.Path || (req.Method != http.MethodGet && req.Method != http.MethodHead) {
		return nil, ErrInput
	}
	limit := int64(8 * 1024 * 1024)
	blobDigest := ""
	switch req.URL.Host {
	case "gitlab.com":
		q, err := url.ParseQuery(req.URL.RawQuery)
		if err != nil || req.URL.Path != "/jwt/auth" || len(q["service"]) != 1 || len(q["scope"]) != 1 || q.Get("service") != "container_registry" || q.Get("scope") != "repository:"+t.Repository+":pull" {
			return nil, ErrInput
		}
		for key, values := range q {
			if (key != "service" && key != "scope" && key != "account" && key != "client_id") || len(values) != 1 {
				return nil, ErrInput
			}
		}
		limit = 1024 * 1024
	case "registry.gitlab.com":
		if req.URL.RawQuery != "" {
			return nil, ErrInput
		}
		if req.URL.Path != "/v2/" {
			base := "/v2/" + t.Repository + "/"
			if !strings.HasPrefix(req.URL.Path, base) || req.URL.RawQuery != "" {
				return nil, ErrInput
			}
			p := strings.TrimPrefix(req.URL.Path, base)
			if strings.HasPrefix(p, "manifests/") {
				v := strings.TrimPrefix(p, "manifests/")
				if !commit40.MatchString(v) && !(strings.HasPrefix(v, "sha256:") && hex64.MatchString(strings.TrimPrefix(v, "sha256:"))) {
					return nil, ErrInput
				}
			} else if strings.HasPrefix(p, "blobs/sha256:") && hex64.MatchString(strings.TrimPrefix(p, "blobs/sha256:")) {
				blobDigest = strings.TrimPrefix(p, "blobs/")
				limit = MaxArchive
			} else {
				return nil, ErrInput
			}
		}
	default:
		return nil, ErrInput
	}
	res, e := t.Base.RoundTrip(req)
	if e != nil {
		return nil, ErrInput
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		location := res.Header.Get("Location")
		res.Body.Close()
		if blobDigest == "" || len(location) > 8192 || strings.ContainsAny(location, " \t\r\n") {
			return nil, ErrInput
		}
		u, e := url.Parse(location)
		hash := strings.TrimPrefix(blobDigest, "sha256:")
		if e != nil || u.Scheme != "https" || u.Host != "cdn.registry.gitlab-static.net" || u.User != nil || u.Fragment != "" || u.EscapedPath() != u.Path || u.Path != "/gitlab/docker/registry/v2/blobs/sha256/"+hash[:2]+"/"+hash+"/data" {
			return nil, ErrInput
		}
		cdn, e := http.NewRequestWithContext(req.Context(), req.Method, location, nil)
		if e != nil {
			return nil, ErrInput
		}
		// No Authorization, cookies, referer, or registry headers on this hop.
		res, e = t.Base.RoundTrip(cdn)
		if e != nil {
			return nil, ErrInput
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			res.Body.Close()
			return nil, ErrInput
		}
	}
	if res.ContentLength > limit {
		res.Body.Close()
		return nil, ErrInput
	}
	res.Body = &registryBoundBody{ReadCloser: res.Body, left: limit}
	return res, nil
}

type registryBoundBody struct {
	io.ReadCloser
	left int64
}

func (b *registryBoundBody) Read(p []byte) (int, error) {
	if b.left == 0 {
		var one [1]byte
		n, e := b.ReadCloser.Read(one[:])
		if n != 0 {
			return 0, ErrInput
		}
		return 0, e
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, e := b.ReadCloser.Read(p)
	b.left -= int64(n)
	return n, e
}
