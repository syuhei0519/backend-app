package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSourceDBObservation(t *testing.T) {
	now := time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC)
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, repo := range repositories {
		t.Run(repo, func(t *testing.T) {
			calls := 0
			log := []byte("INFO Failed to download artifact repo=untrusted.invalid/db:2\nINFO Artifact successfully downloaded repo=\"" + repo + "\"\n")
			o := observe([]byte(`{"Version":2,"UpdatedAt":"2026-10-16T01:00:00Z","DownloadedAt":"2026-10-17T15:59:00Z","Secret":"DO_NOT_EXPORT"}`), log, false, now, func(s string) (string, error) {
				calls++
				if s != repo {
					t.Fatal("wrong source")
				}
				return digest, nil
			})
			if calls != 1 || o.DownloadSource != repo || o.RegistryTagDigest != digest || o.Metadata.GateReason != "expired" || o.Metadata.AgeSeconds != 39*3600 || o.CacheMetadataExisted {
				t.Fatalf("wrong observation: %+v", o)
			}
			b, _ := json.Marshal(o)
			if strings.Contains(string(b), "DO_NOT_EXPORT") || strings.Contains(string(b), "untrusted.invalid") || !strings.Contains(o.DigestBinding, "not proof") {
				t.Fatal("unsafe output or overstated binding")
			}
		})
	}
}

func TestSourceDBObservationUnknownAndUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 17, 16, 0, 0, 0, time.UTC)
	for _, log := range []string{"", "Artifact successfully downloaded repo=https://user:secret@evil.invalid/db:2", "Artifact successfully downloaded repo=" + repositories[0] + "\nArtifact successfully downloaded repo=" + repositories[1]} {
		o := observe([]byte(`{"Version":2,"UpdatedAt":"2026-10-17T15:00:00Z"}`), []byte(log), true, now, func(string) (string, error) { t.Fatal("unobserved source looked up"); return "", nil })
		if o.DownloadSource != "" || o.RegistryTagDigest != "" || !o.CacheMetadataExisted || o.Metadata.GateReason != "pass" {
			t.Fatal("unobserved/cache result invented source")
		}
	}
	o := observe([]byte(`not metadata`), []byte("Artifact successfully downloaded repo="+repositories[0]), false, now, func(string) (string, error) { return "", errors.New("private credential-bearing error") })
	b, _ := json.Marshal(o)
	if o.Metadata != nil || o.DigestStatus != "registry_lookup_unavailable" || strings.Contains(string(b), "credential") {
		t.Fatal("failure leaked input or invented metadata")
	}
}
