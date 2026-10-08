package release

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var ErrInspectionPrincipalRequest = errors.New("inspection principal request refused")
var ErrInspectionPrincipalIdentity = errors.New("inspection principal identity refused")

// Authenticate only GET /job with the native job token. Native writer JSON is
// read independently through the existing Reporter endpoint; no private scan
// artifact request, archive or trace is reachable with this token.
func (c *Client) ValidateInspectionPrincipal(w WriterContext) error {
	if c == nil || (w.ProjectID != 86247025 && w.ProjectID != 86247033) || w.PipelineID <= 0 || w.JobID <= 0 || !shaPattern.MatchString(w.Commit) || (w.JobName != "registry-format-inspection" && w.JobName != "registry-runtime-inspection") {
		return ErrInspectionPrincipalIdentity
	}
	req, e := http.NewRequest(http.MethodGet, "https://gitlab.com/api/v4/job", nil)
	if e != nil {
		return ErrInspectionPrincipalRequest
	}
	req.Header.Set("JOB-TOKEN", c.token)
	res, e := c.http.Do(req)
	if e != nil {
		return ErrInspectionPrincipalRequest
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrInspectionPrincipalRequest
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	var j completedJob
	if e != nil || len(b) > 2*1024*1024 || uniqueKeys(b) != nil || json.Unmarshal(b, &j) != nil {
		return ErrInspectionPrincipalRequest
	}
	if j.ID != w.JobID || j.Name != w.JobName || j.Status != "running" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != w.Commit || j.Pipeline.ID != w.PipelineID || j.Pipeline.ProjectID != w.ProjectID || j.Pipeline.SHA != w.Commit {
		return ErrInspectionPrincipalIdentity
	}
	return nil
}
