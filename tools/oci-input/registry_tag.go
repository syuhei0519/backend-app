package oci

import (
	"context"
	"errors"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"net/http"
	"strings"
)

type tagObservation struct {
	base    http.RoundTripper
	target  string
	missing bool
}

func (t *tagObservation) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err == nil && response != nil && response.StatusCode == http.StatusNotFound && r.URL.Host == "registry.gitlab.com" && r.URL.Path == t.target && (r.Method == "GET" || r.Method == "HEAD") {
		t.missing = true
	}
	return response, err
}

// A missing auth endpoint or transport failure never means an absent SHA tag.
// Existing images are checked without retrieving any layer or building again.
func RegistrySHAExists(ctx context.Context, service, sha, user, password string, base http.RoundTripper) (bool, error) {
	if (service != "frontend" && service != "backend") || !commit40.MatchString(sha) || user != "gitlab-ci-token" || password == "" || base == nil {
		return false, ErrInput
	}
	repo := "syuhei-platform-engineering-lab/" + service + "-app"
	observation := &tagObservation{base: base, target: "/v2/" + repo + "/manifests/" + sha}
	ref, err := name.NewTag("registry.gitlab.com/"+repo+":"+sha, name.StrictValidation)
	if err != nil {
		return false, ErrInput
	}
	d, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuth(&authn.Basic{Username: user, Password: password}), remote.WithTransport(RegistryReadTransport{Base: observation, Repository: repo}))
	if err != nil {
		var response *transport.Error
		if observation.missing && errors.As(err, &response) && response.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, ErrInput
	}
	if !d.MediaType.IsImage() || !strings.HasPrefix(d.Digest.String(), "sha256:") {
		return false, ErrInput
	}
	image, err := d.Image()
	if err != nil {
		return false, ErrInput
	}
	manifest, err := image.Manifest()
	if err != nil || manifest.Config.Size <= 0 || manifest.Config.Size > 8<<20 {
		return false, ErrInput
	}
	config, err := image.ConfigFile()
	if err != nil || config.OS != "linux" || config.Architecture != "amd64" || config.Config.Labels["org.opencontainers.image.revision"] != sha || config.Config.Labels["org.opencontainers.image.source"] != "https://gitlab.com/"+repo {
		return false, ErrInput
	}
	return true, nil
}
