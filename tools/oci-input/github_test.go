package oci

import (
	"path/filepath"
	"testing"
)

func TestGitHubSourceBoundary(t *testing.T) {
	for _, tc := range []struct {
		kind, repo string
		pass       bool
	}{
		{"github", "example-org/backend-app", true},
		{"github", "other-org/backend-app", false},
		{"github", "example-org/other", false},
		{"", "example-org/backend-app", false},
	} {
		t.Run(tc.kind+tc.repo, func(t *testing.T) {
			dir := t.TempDir()
			file, hash := archive(t, dir, fixture(tc.kind), nil)
			proof, err := ValidateGitHub(file, hash, filepath.Join(dir, "out"), revision, tc.repo)
			if (err == nil) != tc.pass {
				t.Fatal("source identity boundary")
			}
			if err == nil && proof.SourceProject != 0 {
				t.Fatal("GitLab identity leaked into native proof")
			}
		})
	}
}
