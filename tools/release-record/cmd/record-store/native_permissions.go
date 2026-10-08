package main

import (
	"os"
)

// Only sanitized writer JSON is shared with a different native job UID.
// Private scan inventory, OCI archives and credentials retain their modes.
func exposeSafeNativeJSON() error {
	for _, path := range []string{".release-store", ".release-store/public"} {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return os.ErrPermission
		}
		if os.Chmod(path, 0755) != nil {
			return os.ErrPermission
		}
	}
	for _, path := range []string{".release-store/public/stored.json", ".release-store/public/record.json", ".release-store/public/rescan.json"} {
		info, e := os.Lstat(path)
		if os.IsNotExist(e) && path == ".release-store/public/rescan.json" {
			continue
		}
		if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return os.ErrPermission
		}
		if os.Chmod(path, 0644) != nil {
			return os.ErrPermission
		}
	}
	return nil
}
