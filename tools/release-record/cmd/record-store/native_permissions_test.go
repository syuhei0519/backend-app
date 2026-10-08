package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeHandoffOnlyChangesSafeJSONModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Native UID handoff permissions require Linux")
	}
	t.Chdir(t.TempDir())
	if os.MkdirAll(".release-store/public", 0700) != nil {
		t.Fatal("mkdir")
	}
	for _, p := range []string{"stored.json", "record.json"} {
		if os.WriteFile(filepath.Join(".release-store/public", p), []byte("{}\n"), 0600) != nil {
			t.Fatal("fixture")
		}
	}
	if os.WriteFile("private-inventory", []byte("synthetic private fixture"), 0600) != nil {
		t.Fatal("fixture")
	}
	if exposeSafeNativeJSON() != nil {
		t.Fatal("Safe native JSON handoff refused")
	}
	for _, p := range []string{".release-store", ".release-store/public"} {
		i, _ := os.Stat(p)
		if i.Mode().Perm() != 0755 {
			t.Fatal("Safe traversal missing")
		}
	}
	for _, p := range []string{"stored.json", "record.json"} {
		i, _ := os.Stat(filepath.Join(".release-store/public", p))
		if i.Mode().Perm() != 0644 {
			t.Fatal("Other native UID cannot read sanitized JSON")
		}
	}
	i, _ := os.Stat("private-inventory")
	if i.Mode().Perm() != 0600 {
		t.Fatal("Private input mode changed")
	}
	if os.Remove(".release-store/public/record.json") != nil || os.Symlink("../../private-inventory", ".release-store/public/record.json") != nil {
		t.Fatal("symlink fixture")
	}
	if exposeSafeNativeJSON() == nil {
		t.Fatal("Symlink handoff accepted")
	}
}
