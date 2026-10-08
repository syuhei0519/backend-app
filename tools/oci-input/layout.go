// OCI archiveを安全に検証・展開する共通入力境界。ci/build-oci.shと各scan/runtime/publication jobが呼ぶ。
// tar checksum、manifest/config/layerのdigest、sourceラベルとplatformを照合し、上限付きProofを返す。
package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxArchive int64 = 100 * 1024 * 1024
const MaxExpanded int64 = 1024 * 1024 * 1024
const maxFile int64 = 512 * 1024 * 1024
const maxJSON int64 = 8 * 1024 * 1024

var ErrInput = errors.New("OCI input refused")
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commit40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type Proof struct {
	SchemaVersion int    `json:"schemaVersion"`
	SourceCommit  string `json:"sourceCommit"`
	SourceProject int64  `json:"sourceProjectId"`
	ArchiveSHA256 string `json:"archiveSha256"`
	ArchiveBytes  int64  `json:"archiveBytes"`
	ExpandedBytes int64  `json:"expandedBytes"`
	Digest        string `json:"digest"`
	LayerCount    int    `json:"layerCount"`
	Platform      string `json:"platform"`
	RegistryPush  bool   `json:"registryPush"`
}

// エラー文は固定値のみ。入力名/設定値/サーバーデータを診断へ混ぜない。検証済みsource/digestだけを公開する。
func fail() error { return ErrInput }

func noDuplicateJSON(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return fail()
		}
		t, e := d.Token()
		if e != nil {
			return fail()
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return fail()
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return fail()
				}
				seen[s] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			t, e = d.Token()
			if e != nil || t != json.Delim('}') {
				return fail()
			}
		case json.Delim('['):
			for d.More() {
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			t, e = d.Token()
			if e != nil || t != json.Delim(']') {
				return fail()
			}
		case json.Delim(']'), json.Delim('}'):
			return fail()
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fail()
	}
	return nil
}

func readJSON(p string, out any) error {
	f, e := os.Open(p)
	if e != nil {
		return fail()
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maxJSON+1))
	if e != nil || int64(len(b)) > maxJSON || noDuplicateJSON(b) != nil {
		return fail()
	}
	if json.Unmarshal(b, out) != nil {
		return fail()
	}
	return nil
}

func blobName(digest string) (string, error) {
	if !strings.HasPrefix(digest, "sha256:") || !hex64.MatchString(strings.TrimPrefix(digest, "sha256:")) {
		return "", fail()
	}
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:"), nil
}

// descriptorのsizeとsha256を実blobへ照合。名前のhash文字列を信用して読み込まない。
func verifyBlob(dir string, d Descriptor) (string, error) {
	n, e := blobName(d.Digest)
	if e != nil || d.Size <= 0 || d.Size > maxFile {
		return "", fail()
	}
	f, e := os.Open(filepath.Join(dir, filepath.FromSlash(n)))
	if e != nil {
		return "", fail()
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Size() != d.Size {
		return "", fail()
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != strings.TrimPrefix(d.Digest, "sha256:") {
		return "", fail()
	}
	return f.Name(), nil
}

// 圧縮blobのdigestに加え、展開後diff IDとサイズを検証する。展開上限で過大入力を拒否する。
func verifyLayer(file, media, diff string) (int64, error) {
	f, e := os.Open(file)
	if e != nil {
		return 0, fail()
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(media, "gzip") {
		z, e := gzip.NewReader(f)
		if e != nil {
			return 0, fail()
		}
		defer z.Close()
		r = z
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, maxFile+1))
	if e != nil || n > maxFile || "sha256:"+hex.EncodeToString(h.Sum(nil)) != diff {
		return 0, fail()
	}
	return n, nil
}

func inspect(dir, commit, expectedSource string) (string, int, error) {
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	if readJSON(filepath.Join(dir, "oci-layout"), &layout) != nil || layout.Version != "1.0.0" {
		return "", 0, fail()
	}
	var index struct {
		Schema    int          `json:"schemaVersion"`
		Manifests []Descriptor `json:"manifests"`
	}
	if readJSON(filepath.Join(dir, "index.json"), &index) != nil || index.Schema != 2 || len(index.Manifests) != 1 {
		return "", 0, fail()
	}
	d := index.Manifests[0]
	if d.MediaType != "application/vnd.oci.image.manifest.v1+json" && d.MediaType != "application/vnd.docker.distribution.manifest.v2+json" {
		return "", 0, fail()
	}
	if d.Platform != nil && (d.Platform.OS != "linux" || d.Platform.Architecture != "amd64" || d.Platform.Variant != "") {
		return "", 0, fail()
	}
	p, e := verifyBlob(dir, d)
	if e != nil {
		return "", 0, e
	}
	var m struct {
		Schema    int          `json:"schemaVersion"`
		MediaType string       `json:"mediaType"`
		Config    Descriptor   `json:"config"`
		Layers    []Descriptor `json:"layers"`
	}
	if readJSON(p, &m) != nil || m.Schema != 2 || m.MediaType != d.MediaType || len(m.Layers) == 0 {
		return "", 0, fail()
	}
	if m.Config.MediaType != "application/vnd.oci.image.config.v1+json" && m.Config.MediaType != "application/vnd.docker.container.image.v1+json" {
		return "", 0, fail()
	}
	p, e = verifyBlob(dir, m.Config)
	if e != nil {
		return "", 0, e
	}
	var c struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
		Config       struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
		RootFS struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if readJSON(p, &c) != nil || c.OS != "linux" || c.Architecture != "amd64" || c.Variant != "" || c.Config.Labels["org.opencontainers.image.revision"] != commit || c.Config.Labels["org.opencontainers.image.source"] != expectedSource || c.RootFS.Type != "layers" || len(c.RootFS.DiffIDs) != len(m.Layers) {
		return "", 0, fail()
	}
	var unpacked int64
	for i, l := range m.Layers {
		switch l.MediaType {
		case "application/vnd.oci.image.layer.v1.tar", "application/vnd.oci.image.layer.v1.tar+gzip", "application/vnd.docker.image.rootfs.diff.tar.gzip":
		default:
			return "", 0, fail()
		}
		if _, e = blobName(c.RootFS.DiffIDs[i]); e != nil {
			return "", 0, e
		}
		p, e = verifyBlob(dir, l)
		if e != nil {
			return "", 0, e
		}
		n, e := verifyLayer(p, l.MediaType, c.RootFS.DiffIDs[i])
		if e != nil || unpacked > MaxExpanded-n {
			return "", 0, fail()
		}
		unpacked += n
	}
	return d.Digest, len(m.Layers), nil
}

// Validate extracts into a new temporary directory and publishes it only after
// checksum and complete layout validation. It never follows archive links and
// never overwrites an existing directory. The caller uses the resulting layout
// directory for both scanner and crane, not the input tar as a Docker tar.
// archive100MiB、展開合計1GiBなどの固定上限を適用。重複/不正pathやリンク等の危険なtar入力を拒否する。
// checksumとOCI構造が正しくても脆弱性検査・runtime互換性は別工程で必要。
func Validate(archive, checksum, destination, commit string) (Proof, error) {
	return validateSource(archive, checksum, destination, commit, "https://gitlab.com/syuhei-platform-engineering-lab/backend-app")
}

// ValidateGitHub keeps the archive, platform and layer boundary and requires
// the exact repository identity supplied by the trusted GitHub workflow.
func ValidateGitHub(archive, checksum, destination, commit, repository string) (Proof, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/backend-app$`).MatchString(repository) {
		return Proof{}, fail()
	}
	proof, err := validateSource(archive, checksum, destination, commit, "https://github.com/"+repository)
	proof.SourceProject = 0 // GitLab numeric project IDs have no GitHub meaning.
	return proof, err
}

func validateSource(archive, checksum, destination, commit, expectedSource string) (Proof, error) {
	var proof Proof
	if !hex64.MatchString(checksum) || !commit40.MatchString(commit) {
		return proof, fail()
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return proof, fail()
	}
	parent := filepath.Dir(destination)
	pi, e := os.Lstat(parent)
	if e != nil || !pi.IsDir() || pi.Mode()&os.ModeSymlink != 0 {
		return proof, fail()
	}
	ai, e := os.Lstat(archive)
	if e != nil || !ai.Mode().IsRegular() || ai.Size() <= 0 || ai.Size() > MaxArchive {
		return proof, fail()
	}
	f, e := os.Open(archive)
	if e != nil {
		return proof, fail()
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != checksum {
		return proof, fail()
	}
	if _, e = f.Seek(0, 0); e != nil {
		return proof, fail()
	}
	dir, e := os.MkdirTemp(parent, ".oci-layout-")
	if e != nil {
		return proof, fail()
	}
	defer os.RemoveAll(dir)
	tr := tar.NewReader(f)
	seen := map[string]bool{}
	var total int64
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return proof, fail()
		}
		n := strings.TrimPrefix(hdr.Name, "./")
		n = strings.TrimSuffix(n, "/")
		if (hdr.Name == "." || hdr.Name == "./") && hdr.Typeflag == tar.TypeDir {
			continue
		}
		if n == "" || strings.ContainsAny(n, "\\:") || strings.HasPrefix(n, "/") || path.Clean(n) != n || n == ".." || strings.HasPrefix(n, "../") || seen[n] {
			return proof, fail()
		}
		seen[n] = true
		if hdr.Typeflag == tar.TypeDir {
			if n != "blobs" && n != "blobs/sha256" {
				return proof, fail()
			}
			if os.MkdirAll(filepath.Join(dir, filepath.FromSlash(n)), 0700) != nil {
				return proof, fail()
			}
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return proof, fail()
		}
		if n != "index.json" && n != "oci-layout" && !(strings.HasPrefix(n, "blobs/sha256/") && hex64.MatchString(strings.TrimPrefix(n, "blobs/sha256/"))) {
			return proof, fail()
		}
		if hdr.Size <= 0 || hdr.Size > maxFile || total > MaxExpanded-hdr.Size {
			return proof, fail()
		}
		total += hdr.Size
		p := filepath.Join(dir, filepath.FromSlash(n))
		if os.MkdirAll(filepath.Dir(p), 0700) != nil {
			return proof, fail()
		}
		out, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return proof, fail()
		}
		bh := sha256.New()
		count, e := io.Copy(io.MultiWriter(out, bh), tr)
		ce := out.Close()
		if e != nil || ce != nil || count != hdr.Size {
			return proof, fail()
		}
		if strings.HasPrefix(n, "blobs/") && hex.EncodeToString(bh.Sum(nil)) != strings.TrimPrefix(n, "blobs/sha256/") {
			return proof, fail()
		}
	}
	digest, layers, e := inspect(dir, commit, expectedSource)
	if e != nil {
		return proof, e
	}
	if os.Rename(dir, destination) != nil {
		return proof, fail()
	}
	return Proof{1, commit, 86247033, checksum, ai.Size(), total, digest, layers, "linux/amd64", false}, nil
}
