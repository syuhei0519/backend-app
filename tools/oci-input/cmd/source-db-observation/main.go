// Records allowlisted source-scan DB diagnostics, never a policy decision.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var repositories = []string{"ghcr.io/aquasecurity/trivy-db:2", "public.ecr.aws/aquasecurity/trivy-db:2"}
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type metadata struct {
	Version      int
	UpdatedAt    time.Time
	DownloadedAt time.Time
}

type safeMetadata struct {
	Version      int     `json:"version"`
	UpdatedAt    string  `json:"updatedAt"`
	DownloadedAt string  `json:"downloadedAt"`
	AgeSeconds   float64 `json:"ageSeconds"`
	GateReason   string  `json:"gateReason"`
}

type observation struct {
	ObservedAt           string        `json:"observedAt"`
	CacheMetadataExisted bool          `json:"cacheMetadataExistedBeforeDownload"`
	MetadataStatus       string        `json:"metadataStatus"`
	Metadata             *safeMetadata `json:"metadata,omitempty"`
	MaxAgeHours          int           `json:"maxAgeHours"`
	DownloadSource       string        `json:"downloadSource,omitempty"`
	SourceStatus         string        `json:"sourceStatus"`
	RegistryTagDigest    string        `json:"registryTagDigest,omitempty"`
	RegistryTagCheckedAt string        `json:"registryTagCheckedAt,omitempty"`
	DigestStatus         string        `json:"digestStatus"`
	DigestBinding        string        `json:"digestBinding"`
}

func selectedSource(log []byte) string {
	selected := ""
	scanner := bufio.NewScanner(strings.NewReader(string(log)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "Artifact successfully downloaded") {
			continue
		}
		for _, repo := range repositories {
			if strings.HasSuffix(strings.TrimSpace(line), `repo="`+repo+`"`) || strings.HasSuffix(strings.TrimSpace(line), "repo="+repo) {
				if selected != "" {
					return "" // Ambiguous log; do not invent a selected source.
				}
				selected = repo
			}
		}
	}
	if scanner.Err() != nil {
		return ""
	}
	return selected
}

func observe(data, log []byte, cached bool, now time.Time, lookup func(string) (string, error)) observation {
	o := observation{ObservedAt: now.UTC().Format(time.RFC3339Nano), CacheMetadataExisted: cached,
		MetadataStatus: "unavailable_or_invalid", MaxAgeHours: 24, SourceStatus: "unobserved_or_cache_reused",
		DigestStatus: "not_observed", DigestBinding: "post-download registry tag observation; not proof of downloaded DB identity"}
	var m metadata
	if json.Unmarshal(data, &m) == nil {
		reason := "pass"
		switch {
		case m.Version != 2:
			reason = "unsupported_version"
		case m.UpdatedAt.IsZero():
			reason = "missing_updated_at"
		case m.UpdatedAt.After(now):
			reason = "future_updated_at"
		case now.Sub(m.UpdatedAt) > 24*time.Hour:
			reason = "expired"
		}
		o.MetadataStatus = "decoded"
		o.Metadata = &safeMetadata{m.Version, m.UpdatedAt.UTC().Format(time.RFC3339Nano),
			m.DownloadedAt.UTC().Format(time.RFC3339Nano), now.Sub(m.UpdatedAt).Seconds(), reason}
	}
	if source := selectedSource(log); source != "" {
		o.DownloadSource, o.SourceStatus = source, "observed_trivy_download_success"
		digest, err := lookup(source)
		o.RegistryTagCheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
		o.DigestStatus = "registry_lookup_unavailable"
		if err == nil && digestPattern.MatchString(digest) {
			o.RegistryTagDigest, o.DigestStatus = digest, "observed_tag_digest"
		}
	}
	return o
}

func lookup(source string) (string, error) {
	allowed := false
	for _, repo := range repositories {
		allowed = allowed || source == repo
	}
	if !allowed {
		return "", errors.New("unapproved source")
	}
	ref, err := name.NewTag(source, name.StrictValidation)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desc, err := remote.Head(ref, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

func limitedRead(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return nil
	}
	return b
}

func main() {
	db := flag.String("metadata", ".cache/trivy/db/metadata.json", "DB metadata")
	log := flag.String("download-log", ".security/private/db.log", "private download log")
	cached := flag.Bool("cache-metadata-existed", false, "metadata existed before download")
	flag.Parse()
	o := observe(limitedRead(*db), limitedRead(*log), *cached, time.Now().UTC(), lookup)
	if json.NewEncoder(os.Stdout).Encode(o) != nil {
		os.Exit(1)
	}
}
