// 安全な一時OCI fixtureでchecksum/構造/展開上限・危険入力拒否を確認する。
// 実registry pull・BuildKit build・scanner実行の互換性受入とは別の単体試験。
package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/validate"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const source = "https://gitlab.com/syuhei-platform-engineering-lab/backend-app"

var revision = strings.Repeat("a", 40)

func digest(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }
func encoded(x any) []byte {
	b, e := json.Marshal(x)
	if e != nil {
		panic(e)
	}
	return b
}
func descriptor(kind string, b []byte) map[string]any {
	return map[string]any{"mediaType": kind, "digest": digest(b), "size": len(b)}
}

func fixture(change string) map[string][]byte {
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	_ = tw.WriteHeader(&tar.Header{Name: "hello", Mode: 0644, Size: 5})
	_, _ = tw.Write([]byte("hello"))
	_ = tw.Close()
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(layer.Bytes())
	_ = zw.Close()
	lb := compressed.Bytes()
	diff := digest(layer.Bytes())
	labels := map[string]string{"org.opencontainers.image.revision": revision, "org.opencontainers.image.source": source}
	arch := "amd64"
	if change == "arm" {
		arch = "arm64"
	}
	if change == "github" {
		labels["org.opencontainers.image.source"] = "https://github.com/example-org/backend-app"
	}
	if change == "source" {
		labels["org.opencontainers.image.source"] = "https://example.org/other"
	}
	if change == "revision" {
		labels["org.opencontainers.image.revision"] = strings.Repeat("b", 40)
	}
	if change == "diff" {
		diff = "sha256:" + strings.Repeat("0", 64)
	}
	cb := encoded(map[string]any{"architecture": arch, "os": "linux", "config": map[string]any{"Labels": labels}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	ld := descriptor("application/vnd.oci.image.layer.v1.tar+gzip", lb)
	if change == "size" {
		ld["size"] = len(lb) + 1
	}
	if change == "foreign" {
		ld["mediaType"] = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
	}
	mb := encoded(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": descriptor("application/vnd.oci.image.config.v1+json", cb), "layers": []any{ld}})
	d := descriptor("application/vnd.oci.image.manifest.v1+json", mb)
	if change == "nested" {
		d["mediaType"] = "application/vnd.oci.image.index.v1+json"
	}
	manifests := []any{d}
	if change == "multi" {
		manifests = append(manifests, d)
	}
	files := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": encoded(map[string]any{"schemaVersion": 2, "manifests": manifests}), "blobs/sha256/" + strings.TrimPrefix(digest(cb), "sha256:"): cb, "blobs/sha256/" + strings.TrimPrefix(digest(mb), "sha256:"): mb, "blobs/sha256/" + strings.TrimPrefix(digest(lb), "sha256:"): lb}
	if change == "corrupt" {
		files["blobs/sha256/"+strings.TrimPrefix(digest(lb), "sha256:")] = []byte("corrupt")
	}
	if change == "missing" {
		delete(files, "blobs/sha256/"+strings.TrimPrefix(digest(lb), "sha256:"))
	}
	if change == "duplicate-json" {
		files["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0","imageLayoutVersion":"1.0.0"}`)
	}
	if change == "trailing-json" {
		files["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0"}{}`)
	}
	return files
}

func archive(t *testing.T, dir string, files map[string][]byte, extra *tar.Header) (string, string) {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for n, data := range files {
		if e := tw.WriteHeader(&tar.Header{Name: n, Mode: 0644, Size: int64(len(data))}); e != nil {
			t.Fatal(e)
		}
		if _, e := tw.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if extra != nil {
		if e := tw.WriteHeader(extra); e != nil {
			t.Fatal(e)
		}
		if extra.Size > 0 {
			_, _ = tw.Write(bytes.Repeat([]byte{'x'}, int(extra.Size)))
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "input.tar")
	if e := os.WriteFile(p, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return p, strings.TrimPrefix(digest(b.Bytes()), "sha256:")
}

func TestValidLayoutBindsSourceAndArchiveAndManifest(t *testing.T) {
	dir := t.TempDir()
	files := fixture("")
	p, sum := archive(t, dir, files, nil)
	out := filepath.Join(dir, "layout")
	proof, e := Validate(p, sum, out, revision)
	if e != nil {
		t.Fatal("valid OCI refused", e)
	}
	var index struct {
		Manifests []Descriptor `json:"manifests"`
	}
	_ = json.Unmarshal(files["index.json"], &index)
	if proof.Digest != index.Manifests[0].Digest || proof.ArchiveSHA256 != sum || proof.Digest == "sha256:"+sum || proof.Platform != "linux/amd64" || proof.LayerCount != 1 || proof.RegistryPush {
		t.Fatal("archive and image provenance conflated")
	}
	if _, e = Validate(p, sum, out, revision); e == nil {
		t.Fatal("existing output overwritten")
	}
}

func TestCorruptAndSubstitutedLayoutsNeverPublishDirectory(t *testing.T) {
	for _, bad := range []string{"arm", "source", "revision", "diff", "size", "foreign", "nested", "multi", "corrupt", "missing", "duplicate-json", "trailing-json"} {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			p, sum := archive(t, dir, fixture(bad), nil)
			out := filepath.Join(dir, "layout")
			if _, e := Validate(p, sum, out, revision); e != ErrInput {
				t.Fatal("bad OCI accepted")
			}
			if _, e := os.Stat(out); !os.IsNotExist(e) {
				t.Fatal("failed input published")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatal("partial extraction retained")
			}
		})
	}
}

func TestArchivePathsLinksDuplicateAndChecksumRefused(t *testing.T) {
	for _, hdr := range []*tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Size: 1}, {Name: "/escape", Typeflag: tar.TypeReg, Size: 1}, {Name: `blobs\escape`, Typeflag: tar.TypeReg, Size: 1},
		{Name: "blobs/sha256/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}, {Name: "index.json", Typeflag: tar.TypeReg, Size: 1},
		{Name: "hard", Typeflag: tar.TypeLink, Linkname: "index.json"},
	} {
		dir := t.TempDir()
		p, sum := archive(t, dir, fixture(""), hdr)
		if _, e := Validate(p, sum, filepath.Join(dir, "layout"), revision); e == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
	dir := t.TempDir()
	p, _ := archive(t, dir, fixture(""), nil)
	if _, e := Validate(p, strings.Repeat("0", 64), filepath.Join(dir, "layout"), revision); e == nil {
		t.Fatal("checksum substitution accepted")
	}
	if _, e := Validate(filepath.Join(dir, "expired.tar"), strings.Repeat("0", 64), filepath.Join(dir, "layout"), revision); e == nil {
		t.Fatal("expired/missing input rescued")
	}
}

func TestArchiveSizeLimitBeforeExtraction(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "too-large.tar")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(MaxArchive + 1); e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	if _, e = Validate(p, strings.Repeat("0", 64), filepath.Join(dir, "layout"), revision); e == nil {
		t.Fatal("oversize input accepted")
	}
}

func TestHarmlessRootDirectoryHeader(t *testing.T) {
	for _, root := range []string{".", "./"} {
		dir := t.TempDir()
		p, sum := archive(t, dir, fixture(""), &tar.Header{Name: root, Typeflag: tar.TypeDir, Mode: 0700})
		if _, e := Validate(p, sum, filepath.Join(dir, "layout"), revision); e != nil {
			t.Fatal("OCI root directory header refused")
		}
	}
}

func TestPinnedCraneReaderAcceptsExactValidatedDirectory(t *testing.T) {
	dir := t.TempDir()
	p, sum := archive(t, dir, fixture(""), nil)
	out := filepath.Join(dir, "layout")
	proof, e := Validate(p, sum, out, revision)
	if e != nil {
		t.Fatal(e)
	}
	index, e := layout.ImageIndexFromPath(out)
	if e != nil {
		t.Fatal("pinned crane cannot read layout")
	}
	m, e := index.IndexManifest()
	if e != nil || len(m.Manifests) != 1 {
		t.Fatal("pinned crane cannot read manifest")
	}
	image, e := index.Image(m.Manifests[0].Digest)
	if e != nil {
		t.Fatal("pinned crane cannot read image")
	}
	h, e := image.Digest()
	if e != nil || h.String() != proof.Digest {
		t.Fatal("pinned crane sees a different digest")
	}
	if e = validate.Image(image); e != nil {
		t.Fatal("pinned crane full validation failed", e)
	}
}
