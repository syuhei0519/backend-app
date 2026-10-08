package main

import (
	"archive/tar"
	"bytes"
	oci "core-platform/oci-input"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFixedRegistryPullPreservesActualManifestAndArchive(t *testing.T) {
	for _, bad := range []string{"", "compressed", "wrong-digest", "wrong-source"} {
		t.Run(bad, func(t *testing.T) {
			t.Chdir(t.TempDir())
			source := strings.Repeat("a", 40)
			var layer bytes.Buffer
			tw := tar.NewWriter(&layer)
			body := []byte("fixture static content")
			if tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: int64(len(body))}) != nil {
				t.Fatal("fixture tar")
			}
			tw.Write(body)
			tw.Close()
			inputLayer := static.NewLayer(layer.Bytes(), types.OCIUncompressedLayer)
			if bad == "compressed" {
				var err error
				inputLayer, err = tarball.LayerFromReader(bytes.NewReader(layer.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
			}
			image, e := mutate.AppendLayers(empty.Image, inputLayer)
			if e != nil {
				t.Fatal(e)
			}
			config, e := image.ConfigFile()
			if e != nil {
				t.Fatal(e)
			}
			config.OS = "linux"
			config.Architecture = "amd64"
			config.Config.Labels = map[string]string{"org.opencontainers.image.revision": source, "org.opencontainers.image.source": "https://gitlab.com/syuhei-platform-engineering-lab/backend-app"}
			if bad == "wrong-source" {
				config.Config.Labels["org.opencontainers.image.revision"] = strings.Repeat("b", 40)
			}
			image, e = mutate.ConfigFile(image, config)
			if e != nil {
				t.Fatal(e)
			}
			digest, _ := image.Digest()
			mediaType, _ := image.MediaType()
			configDigest, _ := image.ConfigName()
			manifest, _ := image.RawManifest()
			configBytes, _ := image.RawConfigFile()
			layers, _ := image.Layers()
			layerDigest, _ := layers[0].Digest()
			layerBody, _ := layers[0].Compressed()
			compressedBytes, err := io.ReadAll(layerBody)
			layerBody.Close()
			if err != nil {
				t.Fatal(err)
			}
			blobs := map[string][]byte{configDigest.String(): configBytes, layerDigest.String(): compressedBytes}
			beforeTransport := http.DefaultTransport
			beforeArgs := os.Args
			t.Cleanup(func() { http.DefaultTransport = beforeTransport; os.Args = beforeArgs })
			calls := 0
			http.DefaultTransport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" && r.Method != "HEAD" {
					t.Fatal("write during pull")
				}
				if r.URL.Scheme != "https" || r.URL.Host != "registry.gitlab.com" {
					t.Fatal("unexpected registry target")
				}
				res := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(strings.NewReader(""))}
				p := r.URL.Path
				var data []byte
				switch {
				case p == "/v2/":
				case strings.HasPrefix(p, "/v2/syuhei-platform-engineering-lab/backend-app/manifests/"):
					data = manifest
					res.Header.Set("Content-Type", string(mediaType))
					res.Header.Set("Docker-Content-Digest", digest.String())
				case strings.HasPrefix(p, "/v2/syuhei-platform-engineering-lab/backend-app/blobs/"):
					data = blobs[strings.TrimPrefix(p, "/v2/syuhei-platform-engineering-lab/backend-app/blobs/")]
					if data == nil {
						t.Fatal("unexpected blob")
					}
				default:
					t.Fatal("unexpected registry path")
				}
				res.Body = io.NopCloser(bytes.NewReader(data))
				res.ContentLength = int64(len(data))
				return res, nil
			})
			t.Setenv("CI_REGISTRY_USER", "gitlab-ci-token")
			t.Setenv("CI_JOB_TOKEN", "fixture-only")
			t.Setenv("CI_REGISTRY_PASSWORD", "fixture-only")
			expected := digest.String()
			if bad == "wrong-digest" {
				expected = "sha256:" + strings.Repeat("0", 64)
			}
			os.Args = []string{"oci-registry-pull", "backend", source, expected}
			e = check()
			valid := bad == "" || bad == "compressed"
			if (e == nil) != valid {
				t.Fatal("actual image binding mismatch", bad, e)
			}
			if !valid {
				if _, e := os.Stat(".oci/image.tar"); e == nil {
					t.Fatal("positive archive survived rejected input")
				}
				return
			}
			archive, e := os.ReadFile(".oci/image.tar")
			if e != nil {
				t.Fatal(e)
			}
			sum := fmt.Sprintf("%x", sha256.Sum256(archive))
			proof, e := oci.Validate(".oci/image.tar", sum, "validated", source)
			if e != nil || proof.Digest != digest.String() || calls < 4 {
				t.Fatal("actual exported tar incompatible with strict scanner input", e)
			}
			// Another native job re-pulls rather than exporting private OCI bytes.
			// Its exact archive hash must be reproducible for compressed inputs too.
			if os.RemoveAll(".oci") != nil || check() != nil {
				t.Fatal("second fixture pull failed")
			}
			again, e := os.ReadFile(".oci/image.tar")
			if e != nil || !bytes.Equal(archive, again) {
				t.Fatal("immutable digest did not reproduce identical archive bytes")
			}
		})
	}
}
