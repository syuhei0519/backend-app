package oci

import (
	"context"
	"errors"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryTagPreflightDistinguishesManifest404FromAuthFailure(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, kind := range []string{"present", "missing", "auth404", "network", "wrong-source", "wrong-platform", "invalid-manifest", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			cfg, _ := empty.Image.ConfigFile()
			cfg.OS = "linux"
			cfg.Architecture = "amd64"
			cfg.Config.Labels = map[string]string{"org.opencontainers.image.revision": sha, "org.opencontainers.image.source": "https://gitlab.com/syuhei-platform-engineering-lab/frontend-app"}
			if kind == "wrong-source" {
				cfg.Config.Labels["org.opencontainers.image.revision"] = strings.Repeat("b", 40)
			}
			if kind == "wrong-platform" {
				cfg.Architecture = "arm64"
			}
			image, _ := mutate.ConfigFile(empty.Image, cfg)
			manifest, _ := image.RawManifest()
			config, _ := image.RawConfigFile()
			mt, _ := image.MediaType()
			blobReads := 0
			transport := registryRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" && r.Method != "HEAD" {
					t.Fatal("Tag preflight attempted write")
				}
				if kind == "network" {
					return nil, errors.New("synthetic network unavailable")
				}
				if r.URL.Path == "/v2/" {
					if kind == "auth404" {
						resp := registryResponse(401, "")
						resp.Header.Set("WWW-Authenticate", `Bearer realm="https://gitlab.com/jwt/auth",service="container_registry"`)
						return resp, nil
					}
					return registryResponse(200, ""), nil
				}
				if r.URL.Host == "gitlab.com" && r.URL.Path == "/jwt/auth" {
					return registryResponse(404, `{"errors":[{"code":"DENIED"}]}`), nil
				}
				if r.URL.Path == "/v2/syuhei-platform-engineering-lab/frontend-app/manifests/"+sha {
					if kind == "missing" {
						return registryResponse(404, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`), nil
					}
					if kind == "redirect" {
						resp := registryResponse(302, "")
						resp.Header.Set("Location", "https://invalid.example.invalid/private")
						return resp, nil
					}
					if kind == "invalid-manifest" {
						return registryResponse(200, "not a manifest"), nil
					}
					resp := registryResponse(200, string(manifest))
					resp.Header.Set("Content-Type", string(mt))
					return resp, nil
				}
				if strings.Contains(r.URL.Path, "/blobs/sha256:") {
					blobReads++
					return registryResponse(200, string(config)), nil
				}
				t.Fatal("Unrecognized registry endpoint", r.URL.Host, r.URL.Path)
				return nil, ErrInput
			})
			exists, err := RegistrySHAExists(context.Background(), "frontend", sha, "gitlab-ci-token", "synthetic-only", transport)
			if kind == "present" {
				if !exists || err != nil || blobReads != 1 {
					t.Fatal("Existing valid tag not recognized")
				}
			} else if kind == "missing" {
				if exists || err != nil || blobReads != 0 {
					t.Fatal("Exact manifest404 did not allow new build")
				}
			} else if err == nil {
				t.Fatal("Uncertain or invalid state allowed build")
			}
		})
	}
}
