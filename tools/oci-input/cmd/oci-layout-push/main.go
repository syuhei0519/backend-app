package main

import (
	"fmt"
	"os"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func noRetry(error) bool { return false }

func main() {
	if len(os.Args) != 4 { fail() }
	path, err := layout.FromPath(os.Args[1]); if err != nil { fail() }
	index, err := path.ImageIndex(); if err != nil { fail() }
	manifest, err := index.IndexManifest()
	if err != nil || len(manifest.Manifests) != 1 || manifest.Manifests[0].Digest.String() != os.Args[3] { fail() }
	image, err := index.Image(manifest.Manifests[0].Digest); if err != nil { fail() }
	tag, err := name.NewTag(os.Args[2], name.StrictValidation); if err != nil { fail() }
	opts := []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithRetryPredicate(noRetry), remote.WithRetryStatusCodes()}
	if err = remote.Write(tag, image, opts...); err != nil { fail() }
	fmt.Println(os.Args[3])
}

func fail() { fmt.Fprintln(os.Stderr, "registry write failed or outcome uncertain"); os.Exit(1) }

