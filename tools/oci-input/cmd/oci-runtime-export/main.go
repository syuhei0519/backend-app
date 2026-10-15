// Convert the validated image to Docker transport without changing config/layers.
package main

import (
	"fmt"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/validate"
	"os"
)

func check() error {
	if len(os.Args) != 5 {
		return fmt.Errorf("arguments")
	}
	idx, err := layout.ImageIndexFromPath(os.Args[1])
	if err != nil {
		return err
	}
	manifest, err := idx.IndexManifest()
	if err != nil {
		return err
	}
	if len(manifest.Manifests) != 1 || !manifest.Manifests[0].MediaType.IsImage() {
		return fmt.Errorf("manifest")
	}
	img, err := idx.Image(manifest.Manifests[0].Digest)
	if err != nil {
		return err
	}
	digest, err := img.Digest()
	if err != nil || digest.String() != os.Args[2] {
		return fmt.Errorf("digest")
	}
	if err = validate.Image(img); err != nil {
		return err
	}
	ref, err := name.NewTag(os.Args[4], name.StrictValidation)
	if err != nil {
		return err
	}
	return tarball.WriteToFile(os.Args[3], ref, img)
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "OCI runtime transport refused")
		os.Exit(1)
	}
}
