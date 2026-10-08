// format/runtime検査のnative jobと安全proofを選択runへ結び付ける。consumer-scanとdelivery-checkから呼ぶ。
// 同じdigestでも検査jobやarchive checksumが別なら受け入れない。registry再取得とbuild由来を区別する。
package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// 検査失敗は固定check名だけを公開し、API応答や資格情報は出さない。
type inspectionFailure struct{ stage string }

func (e inspectionFailure) Error() string { return "Inspection refused" }
func (e inspectionFailure) Unwrap() error { return ErrRefused }
func InspectionFailureStage(err error) string {
	var failure inspectionFailure
	if errors.As(err, &failure) {
		return failure.stage
	}
	return "inspection-unspecified"
}

type inspectionIdentity struct {
	Schema        int    `json:"schemaVersion"`
	Project       int64  `json:"sourceProjectId"`
	Source        string `json:"sourceCommit"`
	BuildPipeline int64  `json:"buildPipelineId"`
	BuildJob      int64  `json:"buildJobId"`
	Archive       string `json:"archiveSha256"`
	Digest        string `json:"digest"`
	Push          *bool  `json:"registryPush"`
	Adoptable     *bool  `json:"phase2Adoptable"`
}

func explicitlyFalse(v *bool) bool { return v != nil && !*v }

func inspectionJobName(r Record, kind string) string {
	if kind != "format" && kind != "runtime" {
		return ""
	}
	switch r.InputKind {
	case "registry-retrieval":
		return "registry-" + kind + "-inspection"
	case "build-oci":
		if kind == "format" {
			return "oci-format-compatibility"
		}
		return "oci-isolated-runtime"
	}
	return ""
}

// Checks actual safe artifacts after their native successful jobs are bound.
// It cannot grant registry writes, deployment or a historical-source exception.
// 実job成功・保護main・policy・scan pipeline・build・archive・digestと、検査/資源proofを照合する。
// runtime fixture成功は実DB接続や配備成功を証明するものではない。
func checkInspection(r Record, j completedJob, kind string, proof, budget []byte, now time.Time) error {
	if r.Adopt(now) != nil || (r.Service != "frontend" && r.Service != "backend") || inspectionJobName(r, kind) == "" || j.ID <= 0 || j.Name != inspectionJobName(r, kind) || j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != r.PolicyRevision || j.Pipeline.ID != r.ScanPipelineID || j.Pipeline.ProjectID != r.SourceProjectID || j.Pipeline.SHA != r.PolicyRevision || uniqueKeys(proof) != nil || uniqueKeys(budget) != nil {
		return ErrRefused
	}
	var common inspectionIdentity
	if json.Unmarshal(proof, &common) != nil || common.Schema != 1 || common.Project != r.SourceProjectID || common.Source != r.SourceCommit || common.BuildPipeline != r.BuildPipelineID || common.BuildJob != r.BuildJobID || common.Archive != r.InputArchiveSHA256 || common.Digest != r.ImageDigest || !explicitlyFalse(common.Push) || !explicitlyFalse(common.Adoptable) {
		return ErrRefused
	}
	switch kind {
	case "runtime":
		var p struct {
			Pipeline           int64  `json:"runtimePipelineId"`
			Job                int64  `json:"runtimeJobId"`
			UID                int    `json:"runtimeUid"`
			Ready              *int   `json:"frontendReadyHttp"`
			Live               *int   `json:"frontendLiveHttp"`
			Index              *int   `json:"staticIndexHttp"`
			BackendReady       *int   `json:"backendReadyHttp"`
			BackendLive        *int   `json:"backendLiveHttp"`
			DBReadinessRefused *bool  `json:"databaseReadinessRefused"`
			Network            string `json:"network"`
			DB                 *bool  `json:"sourceDatabaseConnected"`
		}
		if json.Unmarshal(proof, &p) != nil || p.Pipeline != r.ScanPipelineID || p.Job != j.ID || p.UID != 10001 || p.Network != "none-loopback-only" || !explicitlyFalse(p.DB) {
			return ErrRefused
		}
		if r.Service == "backend" {
			if p.BackendReady == nil || *p.BackendReady != 503 || p.BackendLive == nil || *p.BackendLive != 200 || p.DBReadinessRefused == nil || !*p.DBReadinessRefused || p.Ready != nil || p.Live != nil || p.Index != nil {
				return ErrRefused
			}
		} else if p.Ready == nil || *p.Ready != 200 || p.Live == nil || *p.Live != 200 || p.Index == nil || *p.Index != 200 || p.BackendReady != nil || p.BackendLive != nil || p.DBReadinessRefused != nil {
			return ErrRefused
		}
	case "format":
		var p struct {
			Pipeline      int64     `json:"validationPipelineId"`
			Job           int64     `json:"validationJobId"`
			Trivy         string    `json:"trivyVersion"`
			Crane         string    `json:"craneReader"`
			Same          bool      `json:"sameDirectoryRead"`
			Full          bool      `json:"fullCraneLayerValidation"`
			Gate          *bool     `json:"securityGateApplied"`
			DB            time.Time `json:"dbUpdatedAt"`
			Schema        int       `json:"reportSchemaVersion"`
			ArchiveBytes  int64     `json:"archiveBytes"`
			ExpandedBytes int64     `json:"expandedBytes"`
		}
		if json.Unmarshal(proof, &p) != nil || p.Pipeline != r.ScanPipelineID || p.Job != j.ID || p.Trivy != "0.75.0" || p.Crane != "v0.21.7" || !p.Same || !p.Full || !explicitlyFalse(p.Gate) || p.DB.IsZero() || p.DB.After(now) || now.Sub(p.DB) > 24*time.Hour || p.Schema != 2 || p.ArchiveBytes <= 0 || p.ArchiveBytes > 100<<20 || p.ExpandedBytes <= 0 || p.ExpandedBytes > 1024<<20 {
			return ErrRefused
		}
	default:
		return ErrRefused
	}
	var b struct {
		Schema    int     `json:"schemaVersion"`
		Kind      string  `json:"kind"`
		Samples   int     `json:"samples"`
		Measured  bool    `json:"measurementAvailable"`
		Within    bool    `json:"withinBudget"`
		Workspace int64   `json:"maxWorkspaceKiB"`
		Buildkit  int64   `json:"maxBuildkitStoreKiB"`
		Free      float64 `json:"minFilesystemFreePercent"`
	}
	if json.Unmarshal(budget, &b) != nil || b.Schema != 1 || b.Kind != kind || b.Samples < 2 || !b.Measured || !b.Within || b.Workspace <= 0 || b.Workspace > 2097152 || b.Buildkit < 0 || b.Buildkit > 10485760 || b.Free < 20 || b.Free > 100 {
		return ErrRefused
	}
	return nil
}

func (a *SourceAPI) ReadConsumerInspection(r Record, now time.Time) error {
	return a.readInspection(r, now, false)
}

// 実行中delivery pipelineを検査できるのは、実際に動いている保護verifierだけ。通常の配備readerはpipeline全体成功を必要とする。
func (a *SourceAPI) ReadDeliveryInspection(w WriterContext, r Record, now time.Time) error {
	if a == nil || w.JobName != "verify-oci-release" || w.ProjectID != r.SourceProjectID || w.PipelineID != r.ScanPipelineID || w.Commit != r.PolicyRevision || r.SourceCommit != w.Commit || r.Adopt(now) != nil || inspectionJobName(r, "format") == "" {
		return ErrRefused
	}
	needs := []string{"source-scan", "security-policy-test", "release-record-store", "publish-oci", inspectionJobName(r, "format"), inspectionJobName(r, "runtime")}
	if a.validateProtectedMainActor(w, needs) != nil {
		return inspectionFailure{"inspection-actor-before"}
	}
	if err := a.readInspection(r, now, true); err != nil {
		return err
	}
	if a.validateProtectedMainActor(w, needs) != nil {
		return inspectionFailure{"inspection-actor-after"}
	}
	return nil
}

// 既存tagの読取検証だけを許し、registry pushは許可しない。古いsourceのrollback scanは通常の自動提案経路へ入れない。
func (a *SourceAPI) ValidateRegistryReuse(w WriterContext, r Record, now time.Time) error {
	if a == nil || w.JobName != "publish-oci" || r.InputKind != "registry-retrieval" || w.ProjectID != r.SourceProjectID || w.PipelineID != r.ScanPipelineID || w.JobID <= 0 || w.JobID == r.ScanJobID || w.JobID == r.BuildJobID || w.Commit != r.PolicyRevision || r.SourceCommit != w.Commit || r.Adopt(now) != nil {
		return ErrRefused
	}
	needs := []string{"source-scan", "security-policy-test", "release-record-store", "registry-format-inspection", "registry-runtime-inspection"}
	if a.validateProtectedMainActor(w, needs) != nil || a.readInspection(r, now, true) != nil || a.validateProtectedMainActor(w, needs) != nil {
		return ErrRefused
	}
	return nil
}

func (a *SourceAPI) readInspection(r Record, now time.Time, delivery bool) error {
	stage := "inspection-completed-jobs"
	if a == nil || a.ValidateCompletedJobs(r) != nil {
		return inspectionFailure{stage}
	}
	stage = "inspection-protected-main"
	main, e := a.ReadProtectedMain(r.SourceProjectID)
	if e != nil || main != r.PolicyRevision {
		return inspectionFailure{stage}
	}
	base := fmt.Sprintf("/projects/%d", r.SourceProjectID)
	var pipeline struct {
		ID      int64  `json:"id"`
		Project int64  `json:"project_id"`
		SHA     string `json:"sha"`
		Ref     string `json:"ref"`
		Source  string `json:"source"`
		Status  string `json:"status"`
	}
	status := "success"
	if delivery {
		status = "running"
	}
	stage = "inspection-pipeline"
	if a.object(base+fmt.Sprintf("/pipelines/%d", r.ScanPipelineID), &pipeline) != nil || pipeline.ID != r.ScanPipelineID || pipeline.Project != r.SourceProjectID || pipeline.SHA != r.PolicyRevision || pipeline.Ref != "main" || pipeline.Status != status {
		return inspectionFailure{stage}
	}
	if (r.InputKind == "registry-retrieval" && pipeline.Source != "api") || (r.InputKind == "build-oci" && (r.BuildPipelineID != r.ScanPipelineID || r.SourceCommit != r.PolicyRevision || (pipeline.Source != "push" && pipeline.Source != "api" && pipeline.Source != "web"))) || inspectionJobName(r, "format") == "" {
		return inspectionFailure{stage}
	}
	stage = "inspection-jobs"
	var jobs []completedJob
	if a.object(base+fmt.Sprintf("/pipelines/%d/jobs?per_page=100", r.ScanPipelineID), &jobs) != nil || len(jobs) >= 100 {
		return inspectionFailure{stage}
	}
	stage = "inspection-source-policy-jobs"
	for _, name := range []string{"source-scan", "security-policy-test"} {
		count := 0
		for _, j := range jobs {
			if j.Name == name {
				count++
				if j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != r.PolicyRevision || j.Pipeline.ID != r.ScanPipelineID || j.Pipeline.ProjectID != r.SourceProjectID || j.Pipeline.SHA != r.PolicyRevision {
					return inspectionFailure{stage}
				}
			}
		}
		if count != 1 {
			return inspectionFailure{stage}
		}
	}
	for _, kind := range []string{"format", "runtime"} {
		var selected completedJob
		count := 0
		for _, j := range jobs {
			if j.Name == inspectionJobName(r, kind) {
				count++
				selected = j
			}
		}
		stage = "inspection-" + kind + "-job-selection"
		if count != 1 {
			return inspectionFailure{stage}
		}
		folder, file := ".oci-compatibility", "compatibility.json"
		if kind == "runtime" {
			folder, file = ".oci-runtime", "runtime.json"
		}
		path := base + fmt.Sprintf("/jobs/%d/artifacts/", selected.ID)
		var proof, budget json.RawMessage
		stage = "inspection-" + kind + "-proof-read"
		if a.object(path+folder+"/public/"+file, &proof) != nil {
			return inspectionFailure{stage}
		}
		stage = "inspection-" + kind + "-budget-read"
		if a.object(path+".oci-budget-"+kind+"/public/disk.json", &budget) != nil {
			return inspectionFailure{stage}
		}
		stage = "inspection-" + kind + "-proof-check"
		if checkInspection(r, selected, kind, proof, budget, now) != nil {
			return inspectionFailure{stage}
		}
	}
	stage = "inspection-policy-freshness"
	fresh, e := a.ReadProtectedMain(r.SourceProjectID)
	if e != nil || fresh != main {
		return inspectionFailure{stage}
	}
	return nil
}
