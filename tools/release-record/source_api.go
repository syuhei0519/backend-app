// GitLabの実job/pipeline/mainから実行主体を検証するread API層。
// writer/公開側の自己申告変数だけで許可せず、固定projectと保護mainの実体を照合する。consumer.goとstore_bundle.goが呼ぶ。
package release

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type SourceAPI struct {
	token string
	http  *http.Client
}

func NewSourceAPI(readToken string) (*SourceAPI, error) {
	if readToken == "" {
		return nil, ErrRefused
	}
	return &SourceAPI{readToken, &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type completedJob struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	Ref          string `json:"ref"`
	Commit       struct {
		ID string `json:"id"`
	} `json:"commit"`
	Pipeline struct {
		ID        int64  `json:"id"`
		SHA       string `json:"sha"`
		ProjectID int64  `json:"project_id"`
	} `json:"pipeline"`
}

type WriterContext struct {
	ProjectID, PipelineID, JobID int64
	Commit                       string
	JobName                      string
}

// writer権限はlive APIの実体から読む。CI環境変数/rulesだけでは未保護jobに証跡公開権限があるか証明できない。
func (a *SourceAPI) ValidateWriter(w WriterContext, r Record) error {
	if w.ProjectID != r.SourceProjectID || w.PipelineID != r.ScanPipelineID || w.JobID <= 0 || w.JobID == r.ScanJobID || w.Commit != r.PolicyRevision || (w.JobName != "release-record-store" && w.JobName != "release-record-store-rescan") || r.ValidateStored() != nil {
		return ErrRefused
	}
	scan, e := a.job(w.ProjectID, r.ScanJobID)
	if e != nil || (w.JobName == "release-record-store-rescan" && scan.Name != "oci-image-rescan-validation") || (w.JobName == "release-record-store" && scan.Name != "oci-image-policy-validation" && scan.Name != "image-scan") {
		return ErrRefused
	}
	return a.validateProtectedMainActor(w, []string{"source-scan", "security-policy-test"})
}

// 公開権限はscan成功・不変bytesとは別に確認する。現main公開だけが対象で、古いsourceのrollback取得はデータ読取に限り、SHA tag上書き権限を与えない。
func (a *SourceAPI) ValidatePublisher(w WriterContext, r Record) error {
	if a == nil || w.JobName != "publish-oci" || w.ProjectID != r.SourceProjectID || w.PipelineID != r.ScanPipelineID || w.JobID <= 0 || w.JobID == r.ScanJobID || w.JobID == r.BuildJobID || w.Commit != r.PolicyRevision || r.SourceCommit != w.Commit || r.ValidateStored() != nil || r.Decision != "passed" || a.ValidateCompletedJobs(r) != nil {
		return ErrRefused
	}
	return a.validateProtectedMainActor(w, []string{"source-scan", "security-policy-test", "oci-format-compatibility", "oci-isolated-runtime", "release-record-store"})
}

// 再取得は現保護mainでのscanで、新規buildやregistry書込ではない。古いimage選択には後段のrollback consumerが必要。
func (a *SourceAPI) ValidateRetrievalScan(w WriterContext) error {
	if a == nil || (w.ProjectID != 86247025 && w.ProjectID != 86247033) || w.PipelineID <= 0 || w.JobID <= 0 || !shaPattern.MatchString(w.Commit) || w.JobName != "image-scan" {
		return ErrRefused
	}
	return a.validateProtectedMainActor(w, []string{"source-scan", "security-policy-test"})
}

func (a *SourceAPI) validateHistoricalPipeline(w WriterContext) error {
	var p struct {
		ID        int64  `json:"id"`
		ProjectID int64  `json:"project_id"`
		SHA       string `json:"sha"`
		Ref       string `json:"ref"`
		Source    string `json:"source"`
	}
	if a == nil || a.object(fmt.Sprintf("/projects/%d/pipelines/%d", w.ProjectID, w.PipelineID), &p) != nil || p.ID != w.PipelineID || p.ProjectID != w.ProjectID || p.SHA != w.Commit || p.Ref != "main" || p.Source != "api" {
		return ErrRefused
	}
	return nil
}

func (a *SourceAPI) validateProtectedMainActor(w WriterContext, requiredJobs []string) error {
	j, e := a.job(w.ProjectID, w.JobID)
	if e != nil || j.ID != w.JobID || j.Name != w.JobName || j.Status != "running" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != w.Commit || j.Pipeline.ProjectID != w.ProjectID || j.Pipeline.ID != w.PipelineID || j.Pipeline.SHA != w.Commit {
		return ErrRefused
	}
	base := fmt.Sprintf("/projects/%d", w.ProjectID)

	// 呼出側のCI flagではなく、実pipelineと保護branchを照合する。
	var pipe struct {
		ID        int64  `json:"id"`
		ProjectID int64  `json:"project_id"`
		SHA       string `json:"sha"`
		Ref       string `json:"ref"`
		Source    string `json:"source"`
	}
	if a.object(base+fmt.Sprintf("/pipelines/%d", w.PipelineID), &pipe) != nil {
		return ErrRefused
	}

	if pipe.ID != w.PipelineID || pipe.ProjectID != w.ProjectID || pipe.SHA != w.Commit || pipe.Ref != "main" || (pipe.Source != "push" && pipe.Source != "api" && pipe.Source != "web") {
		return ErrRefused
	}
	var branch struct {
		Name      string
		Protected bool
		Commit    struct{ ID string }
	}
	if a.object(base+"/repository/branches/main", &branch) != nil || branch.Name != "main" || !branch.Protected || branch.Commit.ID != w.Commit {
		return ErrRefused
	}
	var jobs []completedJob
	if a.object(base+fmt.Sprintf("/pipelines/%d/jobs?per_page=100", w.PipelineID), &jobs) != nil || len(jobs) >= 100 {
		return ErrRefused
	}
	for _, name := range requiredJobs {
		count := 0
		for _, job := range jobs {
			if job.Name == name {
				count++
				if job.Status != "success" || job.AllowFailure || job.Commit.ID != w.Commit || job.Pipeline.ProjectID != w.ProjectID || job.Pipeline.ID != w.PipelineID || job.Pipeline.SHA != w.Commit {
					return ErrRefused
				}
			}
		}
		if count != 1 {
			return ErrRefused
		}
	}
	return nil
}

// この固定Source API pathは内部callerだけが構成する。
func (a *SourceAPI) object(path string, out any) error {
	req, e := http.NewRequest(http.MethodGet, "https://gitlab.com/api/v4"+path, nil)
	if e != nil {
		return ErrRefused
	}
	req.Header.Set("PRIVATE-TOKEN", a.token)
	res, e := a.http.Do(req)
	if e != nil {
		return ErrRefused
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if e != nil || len(b) > 2*1024*1024 || uniqueKeys(b) != nil || json.Unmarshal(b, out) != nil {
		return ErrRefused
	}
	return nil
}

func (a *SourceAPI) job(project, id int64) (completedJob, error) {
	var job completedJob
	if (project != 86247025 && project != 86247033) || id <= 0 {
		return job, ErrRefused
	}
	u := fmt.Sprintf("https://gitlab.com/api/v4/projects/%d/jobs/%d", project, id)
	req, e := http.NewRequest(http.MethodGet, u, nil)
	if e != nil {
		return job, ErrRefused
	}
	req.Header.Set("PRIVATE-TOKEN", a.token)
	res, e := a.http.Do(req)
	if e != nil {
		return job, ErrRefused
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return job, ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if e != nil || len(b) > 2*1024*1024 || uniqueKeys(b) != nil || json.Unmarshal(b, &job) != nil {
		return job, ErrRefused
	}
	return job, nil
}

// A record's declared terminal status is checked after the scan job finishes.
// No pipeline/job trace or arbitrary artifact URL is used by this authority.
// buildとscanの実完了jobを照合する。実行条件rulesと、APIで検証する権限・成功状態を混同しない。
func (a *SourceAPI) ValidateCompletedJobs(r Record) error {
	if r.ValidateStored() != nil {
		return ErrRefused
	}
	s, e := a.job(r.SourceProjectID, r.ScanJobID)
	if e != nil || s.ID != r.ScanJobID || s.Ref != "main" || (s.Name != "image-scan" && s.Name != "oci-image-policy-validation" && s.Name != "oci-image-rescan-validation") || s.AllowFailure || s.Status != r.ScanJobStatus || s.Pipeline.ID != r.ScanPipelineID || s.Pipeline.ProjectID != r.SourceProjectID || s.Commit.ID != r.PolicyRevision || s.Pipeline.SHA != r.PolicyRevision {
		return ErrRefused
	}
	if r.BuildPipelineID == 0 && r.BuildJobID == 0 {
		return nil
	} // Failed unknown-build evidence only, already checked by schema.
	b, e := a.job(r.SourceProjectID, r.BuildJobID)
	if e != nil || b.ID != r.BuildJobID || b.Ref != "main" || b.ID == s.ID || (b.Name != "container-build-oci" && b.Name != "publish-image") || b.AllowFailure || b.Status != "success" || b.Pipeline.ID != r.BuildPipelineID || b.Pipeline.ProjectID != r.SourceProjectID || b.Commit.ID != r.SourceCommit || b.Pipeline.SHA != r.SourceCommit {
		return ErrRefused
	}
	return nil
}
