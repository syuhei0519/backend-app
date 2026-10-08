// Trivy生成CycloneDXのschemaとscan部品対応を検証する。ci/verify-oci-policy.shのsbom-checkが呼ぶ。
// 同梱schemasを用い、検証中に任意の外部schemaを取得しない。失敗は固定categoryとして返す。
package release

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/*.json
var sbomSchemas embed.FS

var schemaOnce sync.Once
var compiledSBOM *jsonschema.Schema
var schemaErr error

type refuseSchemaLoader struct{}

func (refuseSchemaLoader) Load(string) (any, error) { return nil, ErrRefused }

func schemaSBOM() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.UseLoader(refuseSchemaLoader{}) // No runtime URL, filesystem or network loads.
		c.AssertFormat()
		for name, checksum := range map[string]string{
			"bom-1.7.schema.json":           "df472ef4aaf593904c479293723a1a5c191d6672715c93b3c0b5c318f3914221",
			"spdx.schema.json":              "54a6288292bc6c90b0d3952f5f939f17436fa76704ffe68a46e5b78539c7cc1b",
			"jsf-0.82.schema.json":          "8bae002c25e723db7ee1f26afde680ae1a2b1a8f6b4b4b0fd65dc3becb090aae",
			"cryptography-defs.schema.json": "018ea7f78b5208ec647cfd10f669cc9c26aba6aceb79c4da7f9c0ef4c99b60de",
		} {
			b, e := sbomSchemas.ReadFile("schemas/" + name)
			if e != nil || Checksum(b) != checksum {
				schemaErr = ErrRefused
				return
			}
			doc, e := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			if e != nil || c.AddResource("http://cyclonedx.org/schema/"+name, doc) != nil {
				schemaErr = ErrRefused
				return
			}
		}
		compiledSBOM, schemaErr = c.Compile("http://cyclonedx.org/schema/bom-1.7.schema.json")
	})
	if schemaErr != nil {
		return nil, ErrRefused
	}
	return compiledSBOM, nil
}

// SBOM失敗は固定検証stageだけを公開し、schema詳細や入力内容を出さない。
type sbomValidationError struct{ category string }

func (e *sbomValidationError) Error() string { return ErrRefused.Error() }
func (e *sbomValidationError) Unwrap() error { return ErrRefused }
func sbomRefusal(category string) error      { return &sbomValidationError{category} }

func SBOMFailureCategory(err error) string {
	var failure *sbomValidationError
	if errors.As(err, &failure) {
		return failure.category
	}
	return "input-unavailable"
}

type bomComponent struct {
	Ref        string                         `json:"bom-ref"`
	Type       string                         `json:"type"`
	Name       string                         `json:"name"`
	Version    string                         `json:"version"`
	Properties []struct{ Name, Value string } `json:"properties"`
}

type SBOMProof struct {
	Format              string `json:"format"`
	SpecVersion         string `json:"specVersion"`
	SHA256              string `json:"sha256"`
	ConfigDigest        string `json:"configDigest"`
	ComponentCount      int    `json:"componentCount"`
	ScannedPackageCount int    `json:"scannedPackageCount"`
}

// ValidateSBOM checks the entire official schema and runtime inventory against
// the private Trivy report of this same input. Run/source binding is additional
// release-record authority, not inferred from this standalone content check.
// JSONとして読めるだけで成功にしない。固定schema・時刻・image configとraw scanの部品対応を確認する。
func ValidateSBOM(b, report []byte, configDigest string, now time.Time) (SBOMProof, error) {
	var proof SBOMProof
	if len(b) == 0 || len(b) > maxEvidenceBytes || len(report) == 0 || len(report) > maxEvidenceBytes || uniqueKeys(b) != nil || uniqueKeys(report) != nil || !digestPattern.MatchString(configDigest) {
		return proof, sbomRefusal("input-boundary")
	}
	sch, e := schemaSBOM()
	if e != nil {
		return proof, sbomRefusal("embedded-schema")
	}
	value, e := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if e != nil || sch.Validate(value) != nil {
		return proof, sbomRefusal("official-schema")
	}
	var bom struct {
		Format      string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Metadata    struct {
			Timestamp time.Time    `json:"timestamp"`
			Component bomComponent `json:"component"`
			Tools     struct {
				Components []bomComponent `json:"components"`
			} `json:"tools"`
		} `json:"metadata"`
		Components   []bomComponent `json:"components"`
		Dependencies []struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"dependsOn"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(b, &bom) != nil || bom.Format != "CycloneDX" || bom.SpecVersion != "1.7" || bom.Metadata.Component.Type != "container" || bom.Metadata.Component.Name != ".oci/layout" || bom.Metadata.Component.Ref == "" || len(bom.Components) == 0 || bom.Metadata.Timestamp.IsZero() || bom.Metadata.Timestamp.After(now) || now.Sub(bom.Metadata.Timestamp) > 24*time.Hour {
		return proof, sbomRefusal("metadata-identity")
	}
	imageIDs := 0
	for _, p := range bom.Metadata.Component.Properties {
		if p.Name == "aquasecurity:trivy:ImageID" {
			imageIDs++
			if p.Value != configDigest {
				return proof, sbomRefusal("config-identity")
			}
		}
	}
	if imageIDs != 1 {
		return proof, sbomRefusal("config-property-count")
	}
	tools := 0
	for _, tool := range bom.Metadata.Tools.Components {
		if tool.Name == "trivy" && tool.Version == "0.75.0" {
			tools++
		}
	}
	if tools != 1 {
		return proof, sbomRefusal("tool-identity")
	}
	refs := map[string]bool{bom.Metadata.Component.Ref: true}
	inventory := map[string]bool{}
	for _, component := range bom.Components {
		if component.Ref == "" || refs[component.Ref] || strings.TrimSpace(component.Name) == "" {
			return proof, sbomRefusal("component-identity")
		}
		refs[component.Ref] = true
		if component.Type == "library" {
			inventory[component.Name+"\x00"+component.Version] = true
		}
	}
	graph := map[string]bool{}
	edges := map[string][]string{}
	for _, dep := range bom.Dependencies {
		if !refs[dep.Ref] || graph[dep.Ref] {
			return proof, sbomRefusal("dependency-identity")
		}
		graph[dep.Ref] = true
		edges[dep.Ref] = dep.DependsOn
		for _, ref := range dep.DependsOn {
			if !refs[ref] {
				return proof, sbomRefusal("dependency-reference")
			}
		}
	}
	for ref := range refs {
		if !graph[ref] {
			return proof, sbomRefusal("dependency-coverage")
		}
	}
	visited := map[string]bool{}
	queue := []string{bom.Metadata.Component.Ref}
	for len(queue) != 0 {
		ref := queue[0]
		queue = queue[1:]
		if visited[ref] {
			continue
		}
		visited[ref] = true
		queue = append(queue, edges[ref]...)
	}
	if len(visited) != len(refs) {
		return proof, sbomRefusal("dependency-reachability")
	}
	var raw struct {
		SchemaVersion              int
		ArtifactType, ArtifactName string
		Metadata                   struct{ ImageID string }
		Trivy                      struct{ Version string }
		Results                    []struct {
			Packages []struct {
				Name, Version, Release string
				Epoch                  int
			}
		}
	}
	if json.Unmarshal(report, &raw) != nil || raw.SchemaVersion != 2 || raw.ArtifactType != "container_image" || raw.ArtifactName != ".oci/layout" || raw.Metadata.ImageID != configDigest || raw.Trivy.Version != "0.75.0" {
		return proof, sbomRefusal("report-identity")
	}
	packages := map[string]bool{}
	for _, result := range raw.Results {
		for _, pkg := range result.Packages {
			// Trivy 0.75.0のFormatVersionへ合わせる。CycloneDXのversionには、raw JSONでは別fieldのrelease/epochも含まれる。
			version := pkg.Version
			if pkg.Release != "" {
				version += "-" + pkg.Release
			}
			if pkg.Epoch != 0 {
				version = strconv.Itoa(pkg.Epoch) + ":" + version
			}
			key := pkg.Name + "\x00" + version
			if pkg.Name == "" {
				return proof, sbomRefusal("package-name-empty")
			}
			if pkg.Version == "" {
				return proof, sbomRefusal("package-version-empty")
			}
			if pkg.Epoch < 0 {
				return proof, sbomRefusal("package-epoch-negative")
			}
			if !inventory[key] {
				// 失敗分類のみ。別綴りの入力を許可する処理ではない。
				if inventory[pkg.Name+"\x00"+strings.TrimPrefix(version, "v")] || inventory[pkg.Name+"\x00v"+version] {
					return proof, sbomRefusal("package-version-prefix")
				}
				for candidate := range inventory {
					if strings.HasPrefix(candidate, pkg.Name+"\x00") {
						return proof, sbomRefusal("package-version-unmatched")
					}
				}
				return proof, sbomRefusal("package-name-unmatched")
			}
			packages[key] = true
		}
	}
	if len(packages) == 0 {
		return proof, sbomRefusal("empty-inventory")
	}
	if len(packages) != len(inventory) {
		return proof, sbomRefusal("inventory-cardinality")
	}
	return SBOMProof{"CycloneDX", "1.7", Checksum(b), configDigest, len(bom.Components), len(packages)}, nil
}
