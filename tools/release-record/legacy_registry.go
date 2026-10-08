package release

import "fmt"

// LegacyRegistryOrigin identifies an actual Phase 0 build. It contains no scan
// decision or invented historical SBOM. ValidateLegacyRegistryBuild must prove
// the original successful job and its nonempty BuildKit metadata before use.
type LegacyRegistryOrigin struct {
	Service         string `json:"service"`
	SourceProjectID int64  `json:"sourceProjectId"`
	SourceCommit    string `json:"sourceCommit"`
	BuildPipelineID int64  `json:"buildPipelineId"`
	BuildJobID      int64  `json:"buildJobId"`
	ImageDigest     string `json:"imageDigest"`
}

func (o LegacyRegistryOrigin) validate() error {
	project := int64(86247025)
	if o.Service == "backend" {
		project = 86247033
	} else if o.Service != "frontend" {
		return ErrRefused
	}
	if o.SourceProjectID != project || !shaPattern.MatchString(o.SourceCommit) || o.BuildPipelineID <= 0 || o.BuildJobID <= 0 || !digestPattern.MatchString(o.ImageDigest) {
		return ErrRefused
	}
	return nil
}

func DecodeLegacyRegistryOrigin(b []byte) (LegacyRegistryOrigin, error) {
	var o LegacyRegistryOrigin
	if len(b) > 4096 || requiredObject(b, []string{"service", "sourceProjectId", "sourceCommit", "buildPipelineId", "buildJobId", "imageDigest"}) != nil || strictData(b, &o) != nil || o.validate() != nil {
		return LegacyRegistryOrigin{}, ErrRefused
	}
	return o, nil
}

// A well-formed selector permits a failed input record, never a positive build
// claim. The protected writer checks the real failed scan before storage.
func FailedRegistryInput(c ScanContext, request []byte) (Record, error) {
	return failedRegistryInput(c, request, false)
}

func FailedHistoricalRegistryInput(c ScanContext, request []byte) (Record, error) {
	return failedRegistryInput(c, request, true)
}

func failedRegistryInput(c ScanContext, request []byte, historical bool) (Record, error) {
	q, e := DecodeRegistryRequest(request)
	project := int64(86247025)
	if q.Service == "backend" {
		project = 86247033
	}
	if e != nil || c.ProjectID != project || c.PipelineID <= 0 || c.JobID <= 0 || !shaPattern.MatchString(c.Commit) || (!historical && c.Commit != q.SourceCommit) || c.ProjectURL != "https://gitlab.com/syuhei-platform-engineering-lab/"+q.Service+"-app" {
		return Record{}, ErrRefused
	}
	return Record{Service: q.Service, SourceProjectID: project, SourceCommit: q.SourceCommit, ImageRepository: "registry.gitlab.com/syuhei-platform-engineering-lab/" + q.Service + "-app", ImageTag: q.SourceCommit, ImageDigest: q.ImageDigest, ScanPipelineID: c.PipelineID, ScanJobID: c.JobID, InputKind: "registry-retrieval", PolicyRevision: c.Commit}, nil
}

// A Phase 0 retry which merely reused a tag has empty image-metadata.json. It
// cannot be relabelled as the original build; unavailable/expired metadata fails.
func (a *SourceAPI) ValidateLegacyRegistryBuild(o LegacyRegistryOrigin) error {
	if a == nil || o.validate() != nil {
		return ErrRefused
	}
	j, e := a.job(o.SourceProjectID, o.BuildJobID)
	if e != nil || j.ID != o.BuildJobID || j.Name != "publish-image" || j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != o.SourceCommit || j.Pipeline.ProjectID != o.SourceProjectID || j.Pipeline.ID != o.BuildPipelineID || j.Pipeline.SHA != o.SourceCommit {
		return ErrRefused
	}
	base := fmt.Sprintf("/projects/%d", o.SourceProjectID)
	var p struct {
		ID        int64  `json:"id"`
		ProjectID int64  `json:"project_id"`
		SHA       string `json:"sha"`
		Ref       string `json:"ref"`
		Status    string `json:"status"`
		Source    string `json:"source"`
	}
	if a.object(base+fmt.Sprintf("/pipelines/%d", o.BuildPipelineID), &p) != nil || p.ID != o.BuildPipelineID || p.ProjectID != o.SourceProjectID || p.SHA != o.SourceCommit || p.Ref != "main" || p.Status != "success" || (p.Source != "push" && p.Source != "api" && p.Source != "web") {
		return ErrRefused
	}
	var metadata map[string]any
	if a.object(base+fmt.Sprintf("/jobs/%d/artifacts/image-metadata.json", o.BuildJobID), &metadata) != nil || metadata["containerimage.digest"] != o.ImageDigest {
		return ErrRefused
	}
	config, ok := metadata["containerimage.config.digest"].(string)
	if !ok || !digestPattern.MatchString(config) {
		return ErrRefused
	}
	return nil
}

// This input binding grants no adoption or write authority. Actual registry
// labels/layout are checked against the original image commit, and a fresh
// current-policy scan must produce the first real schema2 record for this image.
func BindLegacyRegistryIdentity(c ScanContext, origin LegacyRegistryOrigin, producer, consumer, crane []byte, archiveSHA string, archiveBytes int64) (Record, string, error) {
	if origin.validate() != nil || c.ProjectID != origin.SourceProjectID || c.PipelineID <= 0 || c.JobID <= 0 || c.JobID == origin.BuildJobID || !shaPattern.MatchString(c.Commit) || c.ProjectURL != "https://gitlab.com/syuhei-platform-engineering-lab/"+origin.Service+"-app" {
		return Record{}, "", ErrRefused
	}
	p, config, e := checkedOCIIdentity(origin.SourceProjectID, origin.SourceCommit, producer, consumer, crane, archiveSHA, archiveBytes)
	if e != nil || p.Digest != origin.ImageDigest {
		return Record{}, "", ErrRefused
	}
	r := Record{Service: origin.Service, SourceProjectID: origin.SourceProjectID, SourceCommit: origin.SourceCommit, BuildPipelineID: origin.BuildPipelineID, BuildJobID: origin.BuildJobID, ImageRepository: "registry.gitlab.com/syuhei-platform-engineering-lab/" + origin.Service + "-app", ImageTag: origin.SourceCommit, ImageDigest: origin.ImageDigest, ScanPipelineID: c.PipelineID, ScanJobID: c.JobID, InputKind: "registry-retrieval", InputArchiveSHA256: archiveSHA, PolicyRevision: c.Commit}
	return r, config, nil
}
