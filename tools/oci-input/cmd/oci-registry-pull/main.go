package main

import (
	"archive/tar"
	"context"
	oci "core-platform/oci-input"
	"fmt"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func check() error {
	if len(os.Args) != 4 || os.Args[1] != "backend" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(os.Args[2]) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(os.Args[3]) || os.Getenv("CI_REGISTRY_USER") != "gitlab-ci-token" || os.Getenv("CI_JOB_TOKEN") == "" || os.Getenv("CI_REGISTRY_PASSWORD") != os.Getenv("CI_JOB_TOKEN") {
		return oci.ErrInput
	}
	if _, e := os.Stat(".oci"); !os.IsNotExist(e) {
		return oci.ErrInput
	}
	if e := os.Mkdir(".oci", 0700); e != nil {
		return oci.ErrInput
	}
	repo := "syuhei-platform-engineering-lab/" + os.Args[1] + "-app"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ref, e := name.NewTag("registry.gitlab.com/"+repo+":"+os.Args[2], name.StrictValidation)
	if e != nil {
		return oci.ErrInput
	}
	d, e := remote.Get(ref, remote.WithContext(ctx), remote.WithAuth(&authn.Basic{Username: os.Getenv("CI_REGISTRY_USER"), Password: os.Getenv("CI_REGISTRY_PASSWORD")}), remote.WithTransport(oci.RegistryReadTransport{Base: http.DefaultTransport, Repository: repo}))
	if e != nil || d.Digest.String() != os.Args[3] || !d.MediaType.IsImage() {
		return oci.ErrInput
	}
	image, e := d.Image()
	if e != nil {
		return oci.ErrInput
	}
	m, e := image.Manifest()
	if e != nil || len(m.Layers) == 0 || len(m.Layers) > 256 || m.Config.Size <= 0 || m.Config.Size > 8*1024*1024 {
		return oci.ErrInput
	}
	total := m.Config.Size
	for _, layer := range m.Layers {
		if layer.Size <= 0 || layer.Size > oci.MaxArchive {
			return oci.ErrInput
		}
		total += layer.Size
		if total > oci.MaxArchive {
			return oci.ErrInput
		}
	}
	config, e := image.ConfigFile()
	if e != nil || config.OS != "linux" || config.Architecture != "amd64" || config.Config.Labels["org.opencontainers.image.revision"] != os.Args[2] || config.Config.Labels["org.opencontainers.image.source"] != "https://gitlab.com/"+repo {
		return oci.ErrInput
	}
	p, e := layout.Write(".oci/pulled-layout", empty.Index)
	if e != nil || p.AppendImage(image) != nil {
		return oci.ErrInput
	}
	file, e := os.OpenFile(".oci/image.tar", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return oci.ErrInput
	}
	tw := tar.NewWriter(file)
	e = filepath.Walk(".oci/pulled-layout", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return oci.ErrInput
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return oci.ErrInput
		}
		rel, e := filepath.Rel(".oci/pulled-layout", path)
		if e != nil || strings.Contains(rel, "..") {
			return oci.ErrInput
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}
		if tw.WriteHeader(h) != nil {
			return oci.ErrInput
		}
		f, e := os.Open(path)
		if e != nil {
			return oci.ErrInput
		}
		_, e = io.Copy(tw, f)
		f.Close()
		return e
	})
	closeTar := tw.Close()
	closeFile := file.Close()
	if e != nil || closeTar != nil || closeFile != nil {
		return oci.ErrInput
	}
	info, e := os.Stat(".oci/image.tar")
	if e != nil || info.Size() > oci.MaxArchive {
		return oci.ErrInput
	}
	return nil
}

func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "Fixed registry OCI retrieval refused; private diagnostics suppressed")
		os.Exit(1)
	}
}
