// 配信CIからmanifest更新MRを提案するGo helper。ci/propose-manifest-update.shが起動する。
// 検証済みdotenv・CI identityを入力に、environments/localのimage/releaseを同時更新する。
// 採否はapplication-manifest/ci/trusted-consumer.pyとapplication-manifest/ci/pre_merge_check.pyが独立判定し、mergeは別操作。
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type client struct {
	base, token string
	http        *http.Client
}
type branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		ID string `json:"id"`
	} `json:"commit"`
}
type repoFile struct {
	Content      string `json:"content"`
	LastCommitID string `json:"last_commit_id"`
}
type mergeRequest struct {
	IID          int    `json:"iid"`
	SourceBranch string `json:"source_branch"`
	Author       struct {
		Username string `json:"username"`
	} `json:"author"`
}

func required(name string) string {
	v := os.Getenv(name)
	if v == "" {
		panic(name + " is required")
	}
	return v
}

// API失敗は最大3試行。429はRetry-After、その他は指数待機+jitter。
// HTTP応答bodyは閉じ、非再試行4xxは停止する。Retry-After値自体の上限はここでは設けていない。
func (c *client) call(method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(payload))
		req.Header.Set("PRIVATE-TOKEN", c.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			if out != nil {
				return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
			}
			return nil
		}
		wait := time.Duration(2<<attempt)*time.Second + time.Duration(rand.Intn(500))*time.Millisecond
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 429 {
				if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil {
					wait = time.Duration(n) * time.Second
				}
			}
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return fmt.Errorf("GitLab API operation refused: status %d", resp.StatusCode)
			}
		}
		time.Sleep(wait)
	}
	return errors.New("GitLab API operation failed after bounded retries")
}

// HTTPは1回20秒上限、redirect拒否で認証headerの別宛先送信を防ぐ。
func proposalHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func projectPath(id string) string { return "/projects/" + url.PathEscape(id) }
func (c *client) head(id string) (string, error) {
	var b branch
	err := c.call("GET", projectPath(id)+"/repository/branches/main", nil, &b)
	if err == nil && (b.Name != "main" || !b.Protected) {
		return "", errors.New("protected main required")
	}
	return b.Commit.ID, err
}

// 渡されたjob IDだけを信用せず、実APIでverify-oci-releaseの名前・成功・main・SHA・pipelineを確認する。
func checkDeliveryJob(c *client, get func(string) string) error {
	refused := errors.New("actual completed delivery verifier required")
	idText := get("VERIFY_RELEASE_JOB_ID")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != idText || idText == get("SCAN_JOB_ID") || idText == get("BUILD_JOB_ID") {
		return refused
	}
	var j struct {
		ID                int64 `json:"id"`
		Name, Status, Ref string
		AllowFailure      bool `json:"allow_failure"`
		Commit            struct{ ID string }
		Pipeline          struct {
			ID        int64
			ProjectID int64 `json:"project_id"`
			SHA       string
		}
	}
	if c.call("GET", projectPath(get("CI_PROJECT_ID"))+"/jobs/"+idText, nil, &j) != nil || j.ID != id || j.Name != "verify-oci-release" || j.Status != "success" || j.AllowFailure || j.Ref != "main" || j.Commit.ID != get("CI_COMMIT_SHA") || strconv.FormatInt(j.Pipeline.ID, 10) != get("CI_PIPELINE_ID") || strconv.FormatInt(j.Pipeline.ProjectID, 10) != get("CI_PROJECT_ID") || j.Pipeline.SHA != get("CI_COMMIT_SHA") {
		return refused
	}
	return nil
}
func (c *client) file(project, file, ref string) (repoFile, string, error) {
	var f repoFile
	p := projectPath(project) + "/repository/files/" + url.PathEscape(file) + "?ref=" + url.QueryEscape(ref)
	if err := c.call("GET", p, nil, &f); err != nil {
		return f, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(f.Content)
	return f, string(raw), err
}
func value(content, section, key string) (string, error) {
	in := false
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, " ") {
			in = strings.TrimSpace(line) == section+":"
			continue
		}
		if in && strings.HasPrefix(line, "  "+key+":") {
			return strings.Trim(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), "\"'"), nil
		}
	}
	return "", errors.New("missing " + section + "." + key)
}
func update(content string, vals map[string]map[string]string) (string, error) {
	lines := strings.Split(content, "\n")
	section := ""
	seen := map[string]bool{}
	for i, line := range lines {
		if !strings.HasPrefix(line, " ") {
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		for key, v := range vals[section] {
			if strings.HasPrefix(line, "  "+key+":") {
				lines[i] = "  " + key + ": \"" + v + "\""
				seen[section+"."+key] = true
			}
		}
	}
	for s, m := range vals {
		for k := range m {
			if !seen[s+"."+k] {
				return "", errors.New("missing " + s + "." + k)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// Phase2 proposals carry one immutable scan run. The protected delivery checker
// produces these fields; the fixed manifest consumer independently checks the
// full native scan/runtime bundle before anyone can merge the proposal.
// 元buildと今回scanを分け、digest・record/SBOM URL/checksumを一つのrelease tupleへ結合する。
// 同じcommitでも再buildのbytesは同一とは限らないため、SHAだけでimage同一性を決めない。
func phase2Release(get func(string) string) (map[string]string, error) {
	refused := errors.New("Phase2 proposal identity refused")
	hex40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	project := map[string]string{"frontend": "86247025", "backend": "86247033"}[get("SERVICE")]
	sha := get("CI_COMMIT_SHA")
	if project == "" || get("CI_PROJECT_ID") != project || get("SOURCE_PROJECT_ID") != project || get("SOURCE_COMMIT") != sha || get("IMAGE_TAG") != sha || !hex40.MatchString(sha) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(get("IMAGE_DIGEST")) || get("CI_COMMIT_BRANCH") != "main" || get("CI_COMMIT_REF_PROTECTED") != "true" {
		return nil, refused
	}
	for _, key := range []string{"CI_PIPELINE_ID", "BUILD_PIPELINE_ID", "BUILD_JOB_ID", "SCAN_PIPELINE_ID", "SCAN_JOB_ID"} {
		value := get(key)
		n, e := strconv.ParseInt(value, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
			return nil, refused
		}
	}
	if get("SCAN_PIPELINE_ID") != get("CI_PIPELINE_ID") || get("BUILD_JOB_ID") == get("SCAN_JOB_ID") || !hex64.MatchString(get("RELEASE_RECORD_SHA256")) || !hex64.MatchString(get("SBOM_SHA256")) {
		return nil, refused
	}
	base := "https://gitlab.com/api/v4/projects/" + project + "/packages/generic/core-platform-" + get("SERVICE") + "/sha256-" + strings.TrimPrefix(get("IMAGE_DIGEST"), "sha256:") + "/"
	run := get("SCAN_PIPELINE_ID") + "-" + get("SCAN_JOB_ID")
	if get("RELEASE_RECORD_URL") != base+"release-record-"+run+".json" || get("SBOM_URL") != base+"sbom-"+run+".cdx.json" {
		return nil, refused
	}
	return map[string]string{"schemaVersion": "2", "sourceProjectId": project, "sourceCommit": sha, "buildPipelineId": get("BUILD_PIPELINE_ID"), "buildJobId": get("BUILD_JOB_ID"), "scanPipelineId": get("SCAN_PIPELINE_ID"), "scanJobId": get("SCAN_JOB_ID"), "recordUrl": get("RELEASE_RECORD_URL"), "recordSha256": get("RELEASE_RECORD_SHA256"), "sbomUrl": get("SBOM_URL"), "sbomSha256": get("SBOM_SHA256")}, nil
}

func addPhase2ReleaseKeys(content string, vals map[string]string) (string, error) {
	lines := strings.Split(content, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if line == "release:" {
			if start != -1 {
				return "", errors.New("ambiguous release section")
			}
			start = i
		} else if start != -1 && end == len(lines) && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			end = i
		}
	}
	if start == -1 {
		return "", errors.New("missing release section")
	}
	seen := map[string]bool{}
	legacy := map[string]bool{}
	for index, line := range lines[start+1 : end] {
		for _, key := range []string{"pipelineId", "pipelineUrl"} {
			if strings.HasPrefix(line, "  "+key+":") {
				if legacy[key] {
					return "", errors.New("ambiguous legacy release key")
				}
				legacy[key] = true
				lines[start+1+index] = "  " + key + ": null"
			}
		}
		for key := range vals {
			if strings.HasPrefix(line, "  "+key+":") {
				if seen[key] {
					return "", errors.New("ambiguous release key")
				}
				seen[key] = true
			}
		}
	}
	keys := []string{}
	for key := range vals {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	added := []string{}
	for _, key := range []string{"pipelineId", "pipelineUrl"} {
		if !legacy[key] {
			added = append(added, "  "+key+": null")
		}
	}
	for _, key := range keys {
		added = append(added, "  "+key+": \"\"")
	}
	result := append([]string{}, lines[:end]...)
	result = append(result, added...)
	result = append(result, lines[end:]...)
	return strings.Join(result, "\n"), nil
}
func main() {
	if required("CI_API_V4_URL") != "https://gitlab.com/api/v4" {
		panic("fixed proposal API required")
	}
	c := client{"https://gitlab.com/api/v4", required("MANIFEST_UPDATE_TOKEN"), proposalHTTPClient()}
	service := required("SERVICE")
	sourceProject := required("CI_PROJECT_ID")
	sha := required("CI_COMMIT_SHA")
	digest := required("IMAGE_DIGEST")
	phase2 := os.Getenv("PHASE2_PROPOSAL_ENABLED") == "true"
	var scanRelease map[string]string
	if phase2 {
		var e error
		scanRelease, e = phase2Release(os.Getenv)
		if e != nil {
			panic(e)
		}
		if checkDeliveryJob(&c, os.Getenv) != nil {
			panic("actual completed delivery verifier refused")
		}
	}
	manifestProject := os.Getenv("MANIFEST_PROJECT")
	if manifestProject == "" {
		manifestProject = "syuhei-platform-engineering-lab/application-manifest"
	}
	if manifestProject != "syuhei-platform-engineering-lab/application-manifest" && manifestProject != "86247034" {
		panic("fixed manifest project required")
	}
	if h, e := c.head(sourceProject); e != nil || h != sha {
		panic("source main is no longer the candidate commit")
	}
	manifestHead, err := c.head(manifestProject)
	if err != nil {
		panic(err)
	}
	filePath := "environments/local/" + service + ".yaml"
	f, content, err := c.file(manifestProject, filePath, "main")
	if err != nil {
		panic(err)
	}
	oldCommit, _ := value(content, "release", "sourceCommit")
	oldDigest, _ := value(content, "image", "digest")
	if oldCommit == sha {
		if oldDigest != digest {
			panic("same source commit has a different digest")
		}
		if !phase2 {
			fmt.Println("manifest already contains this immutable image")
			return
		}
	}
	vals := map[string]map[string]string{"image": {"repository": required("CI_REGISTRY_IMAGE"), "tag": sha, "digest": digest}, "release": {"sourceProjectId": sourceProject, "sourceCommit": sha, "pipelineId": required("CI_PIPELINE_ID"), "pipelineUrl": required("CI_PIPELINE_URL")}}
	if phase2 {
		vals["release"] = scanRelease
		content, err = addPhase2ReleaseKeys(content, scanRelease)
		if err != nil {
			panic(err)
		}
	}
	updated, err := update(content, vals)
	if err != nil {
		panic(err)
	}
	branchName := "update/local/" + service + "/" + sha
	if phase2 {
		branchName += "/run-" + scanRelease["scanPipelineId"] + "-" + scanRelease["scanJobId"]
	}
	createBranch := map[string]string{"branch": branchName, "ref": "main"}
	var created any
	if err = c.call("POST", projectPath(manifestProject)+"/repository/branches", createBranch, &created); err != nil && !strings.Contains(err.Error(), "already exists") {
		panic(err)
	}
	commit := map[string]any{"branch": branchName, "commit_message": service + "の検証済み固定イメージを配備定義へ反映する: " + sha, "actions": []map[string]string{{"action": "update", "file_path": filePath, "content": updated, "last_commit_id": f.LastCommitID}}}
	var commitResult any
	if err = c.call("POST", projectPath(manifestProject)+"/repository/commits", commit, &commitResult); err != nil {
		_, branchContent, readErr := c.file(manifestProject, filePath, branchName)
		if readErr != nil || branchContent != updated {
			panic(err)
		}
	}
	title := service + "の検証済み固定イメージを配備する: " + sha
	body := "固定イメージの配備提案です。\n\nソースコミット: `" + sha + "`\nイメージdigest: `" + digest + "`\nCI: " + required("CI_PIPELINE_URL") + "\n\n固定mainの検証と直前SHA・treeの照合後に統合します。"
	if phase2 {
		body += "\n\n今回の検査run: " + scanRelease["scanPipelineId"] + "/" + scanRelease["scanJobId"] + "\n記録: " + scanRelease["recordUrl"] + "\nSBOM: " + scanRelease["sbomUrl"] + "\n両checksumと元buildを同じcommitで選択しています。"
	}
	mrBody := map[string]any{"source_branch": branchName, "target_branch": "main", "title": title, "description": body, "remove_source_branch": true, "draft": true}
	var mr mergeRequest
	if err = c.call("POST", projectPath(manifestProject)+"/merge_requests", mrBody, &mr); err != nil && !strings.Contains(err.Error(), "already exists") {
		panic(err)
	}
	botUser := os.Getenv("MANIFEST_BOT_USERNAME")
	if botUser == "" {
		botUser = "syuhei-platform-manifest-bot"
	}
	var opened []mergeRequest
	if err = c.call("GET", projectPath(manifestProject)+"/merge_requests?state=opened&per_page=100", nil, &opened); err != nil {
		panic(err)
	}
	prefix := "update/local/" + service + "/"
	for _, candidate := range opened {
		if candidate.SourceBranch != branchName && strings.HasPrefix(candidate.SourceBranch, prefix) && candidate.Author.Username == botUser {
			if err = c.call("PUT", projectPath(manifestProject)+"/merge_requests/"+strconv.Itoa(candidate.IID), map[string]string{"state_event": "close"}, nil); err != nil {
				panic(err)
			}
		}
	}
	after, err := c.head(sourceProject)
	if err != nil || after != sha {
		panic("source main changed while creating proposal")
	}
	manifestAfter, err := c.head(manifestProject)
	if err != nil || manifestAfter != manifestHead {
		panic("manifest main changed while creating proposal; rerun from latest main")
	}
	fmt.Println("manifest proposal ready:", branchName)
}
