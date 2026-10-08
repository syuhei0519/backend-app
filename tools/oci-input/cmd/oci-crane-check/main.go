// Reads an OCI directory using the same pinned reader as crane v0.21.7 push.
// No registry client, credentials, or write operation is used here.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/validate"
	"os"
	"runtime/debug"
)

func check() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("input")
	}
	b, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("version")
	}
	pinned := false
	for _, d := range b.Deps {
		if d.Path == "github.com/google/go-containerregistry" && d.Version == "v0.21.7" && d.Replace == nil {
			pinned = true
		}
	}
	if !pinned {
		return fmt.Errorf("version")
	}
	l, e := layout.ImageIndexFromPath(os.Args[1])
	if e != nil {
		return e
	}
	m, e := l.IndexManifest()
	if e != nil {
		return e
	}
	if len(m.Manifests) != 1 || !m.Manifests[0].MediaType.IsImage() {
		return fmt.Errorf("manifest")
	}
	i, e := l.Image(m.Manifests[0].Digest)
	if e != nil {
		return e
	}
	h, e := i.Digest()
	if e != nil || h.String() != os.Args[2] {
		return fmt.Errorf("digest")
	}
	if e = validate.Image(i); e != nil {
		return e
	}
	config, e := i.ConfigName()
	if e != nil || config.Algorithm != "sha256" {
		return fmt.Errorf("config")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"schemaVersion": 1, "reader": "crane-v0.21.7-layout-reader", "digest": h.String(), "configDigest": config.String(), "fullLayerValidation": true, "registryPush": false})
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "OCI crane compatibility refused")
		os.Exit(1)
	}
}
