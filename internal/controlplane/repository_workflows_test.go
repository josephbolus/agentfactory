package controlplane

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestGitHubWorkflowSourceUsesRESTRepositorySlug(t *testing.T) {
	var calls [][]string
	var runCalls [][]string
	source := githubCLI{
		runJSON: func(_ context.Context, args []string, target any) error {
			calls = append(calls, append([]string(nil), args...))
			switch args[1] {
			case "repos/acme/api":
				return json.Unmarshal([]byte(`{"default_branch":"main"}`), target)
			case "repos/acme/api/git/ref/heads/main":
				return json.Unmarshal([]byte(`{"object":{"sha":"commit-1"}}`), target)
			case "repos/acme/api/git/trees/commit-1?recursive=1":
				return json.Unmarshal([]byte(`{"tree":[{"path":".factory/workflows/qa.md","type":"blob","sha":"blob-1"}]}`), target)
			case "repos/acme/api/git/blobs/blob-1":
				content := base64.StdEncoding.EncodeToString([]byte("---\nid: qa\ntitle: QA\n---\nCheck QA."))
				return json.Unmarshal([]byte(`{"encoding":"base64","content":"`+content+`"}`), target)
			case "repos/acme/api/issues/7/comments":
				return json.Unmarshal([]byte(`[[{"id":9,"body":"<!-- factory-workflow-routing -->"}]]`), target)
			default:
				t.Fatalf("unexpected GitHub API path %q", args[1])
				return nil
			}
		},
		run: func(_ context.Context, args []string) error {
			runCalls = append(runCalls, append([]string(nil), args...))
			return nil
		},
	}
	commit, files, err := source.ListWorkflowFiles(context.Background(), "github.com/acme/api")
	if err != nil {
		t.Fatal(err)
	}
	if commit != "commit-1" || string(files[".factory/workflows/qa.md"]) == "" {
		t.Fatalf("workflow response = %q, %#v", commit, files)
	}
	if err := source.UpsertWorkflowComment(context.Background(), "github.com/acme/api", 7, "selected"); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call[1], "repos/github.com/") {
			t.Fatalf("GitHub REST path retained host prefix: %#v", call)
		}
	}
	if len(runCalls) != 1 || runCalls[0][0] != "api" || runCalls[0][1] != "--method" ||
		runCalls[0][2] != "PATCH" || runCalls[0][3] != "repos/acme/api/issues/comments/9" {
		t.Fatalf("comment API call = %#v", runCalls)
	}
}

func TestGitHubWorkflowSourceRejectsMalformedRepositoryIdentity(t *testing.T) {
	for _, identity := range []string{
		"gitlab.com/acme/api", "github.com/acme", "github.com/acme/api/extra", "https://github.com/acme/api",
	} {
		source := githubCLI{}
		if _, _, err := source.ListWorkflowFiles(context.Background(), identity); err == nil {
			t.Fatalf("accepted malformed repository identity %q", identity)
		}
		if err := source.UpsertWorkflowComment(context.Background(), identity, 7, "selected"); err == nil {
			t.Fatalf("accepted malformed comment repository identity %q", identity)
		}
	}
}

func TestGitHubWorkflowSourceRejectsTruncatedTree(t *testing.T) {
	source := githubCLI{
		runJSON: func(_ context.Context, args []string, target any) error {
			switch args[1] {
			case "repos/acme/api":
				return json.Unmarshal([]byte(`{"default_branch":"main"}`), target)
			case "repos/acme/api/git/ref/heads/main":
				return json.Unmarshal([]byte(`{"object":{"sha":"commit-1"}}`), target)
			case "repos/acme/api/git/trees/commit-1?recursive=1":
				return json.Unmarshal([]byte(`{"truncated":true,"tree":[]}`), target)
			default:
				t.Fatalf("unexpected GitHub API path %q", args[1])
				return nil
			}
		},
	}
	_, _, err := source.ListWorkflowFiles(context.Background(), "github.com/acme/api")
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated tree error = %v", err)
	}
}

func TestGitHubWorkflowSourceRejectsLimitsBeforeBlobFetch(t *testing.T) {
	tests := []struct {
		name    string
		entries []map[string]any
		want    string
	}{
		{name: "file count", entries: workflowTreeEntries(protocol.MaxRepositoryWorkflowFiles+1, 1), want: "files"},
		{name: "file size", entries: workflowTreeEntries(1, protocol.MaxRepositoryWorkflowBytes+1), want: "bytes"},
		{name: "catalog size", entries: workflowTreeEntries(44, protocol.MaxRepositoryWorkflowBytes), want: "catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blobCalls := 0
			source := githubCLI{runJSON: func(_ context.Context, args []string, target any) error {
				switch args[1] {
				case "repos/acme/api":
					return json.Unmarshal([]byte(`{"default_branch":"main"}`), target)
				case "repos/acme/api/git/ref/heads/main":
					return json.Unmarshal([]byte(`{"object":{"sha":"commit-1"}}`), target)
				case "repos/acme/api/git/trees/commit-1?recursive=1":
					encoded, err := json.Marshal(map[string]any{"tree": test.entries})
					if err != nil {
						t.Fatal(err)
					}
					return json.Unmarshal(encoded, target)
				default:
					blobCalls++
					return errors.New("blob fetch must not occur")
				}
			}}
			_, _, err := source.ListWorkflowFiles(context.Background(), "github.com/acme/api")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("limit error = %v, want %q", err, test.want)
			}
			if blobCalls != 0 {
				t.Fatalf("blob fetches = %d", blobCalls)
			}
		})
	}
}

func workflowTreeEntries(count, size int) []map[string]any {
	entries := make([]map[string]any, count)
	for index := range entries {
		entries[index] = map[string]any{
			"path": ".factory/workflows/workflow-" + strconv.Itoa(index) + ".md",
			"type": "blob",
			"sha":  "blob-" + strconv.Itoa(index),
			"size": size,
		}
	}
	return entries
}

type fakeWorkflowSource struct {
	commit        string
	files         map[string][]byte
	issues        map[string][]githubIssue
	comments      []string
	workflowCalls int
	workflowErr   error
}

type workflowRevision struct {
	commit string
	files  map[string][]byte
}

type mappedWorkflowSource struct {
	revisions map[string]workflowRevision
}

func (f *mappedWorkflowSource) ListIssues(context.Context, string) ([]githubIssue, error) {
	return nil, nil
}

// IssueProjectStatus keeps routing tests focused on workflow selection.
func (f *mappedWorkflowSource) IssueProjectStatus(_ context.Context, _ string, _ int) (string, error) {
	return protocol.ProjectStatusReady, nil
}

func (*mappedWorkflowSource) ListPullRequests(context.Context, string) ([]githubPullRequest, error) {
	return nil, nil
}

func (f *mappedWorkflowSource) ListWorkflowFiles(_ context.Context, repository string) (string, map[string][]byte, error) {
	revision, ok := f.revisions[repository]
	if !ok {
		return "", nil, errors.New("workflow revision not found")
	}
	return revision.commit, revision.files, nil
}

func (*mappedWorkflowSource) UpsertWorkflowComment(context.Context, string, int, string) error {
	return nil
}

func TestRepositoryWorkflowHTTPAPI(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/qa.md":        []byte("---\nid: qa\ntitle: QA\n---\nqa"),
	}}
	server := httptest.NewServer(NewHandler(store, nil))
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/repositories/"+repository.ID+"/workflows/refresh", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", response.StatusCode)
	}
	var catalog protocol.RepositoryWorkflowCatalog
	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Status != protocol.RepositoryWorkflowHealthy {
		t.Fatalf("catalog = %#v", catalog)
	}
	response, err = http.Get(server.URL + "/api/v1/repositories/" + repository.ID + "/workflows")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", response.StatusCode)
	}
	response.Body.Close()
	request, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/repository-workflows/options", strings.NewReader(`{"repository_ids":["`+repository.ID+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("options status = %d", response.StatusCode)
	}
	var options protocol.RepositoryWorkflowOptionsResponse
	if err := json.NewDecoder(response.Body).Decode(&options); err != nil || len(options.Workflows) != 2 {
		t.Fatalf("options = %#v, err %v", options, err)
	}
}

func (f *fakeWorkflowSource) ListIssues(_ context.Context, repository string) ([]githubIssue, error) {
	return f.issues[repository], nil
}

// IssueProjectStatus keeps routing tests focused on workflow selection.
func (f *fakeWorkflowSource) IssueProjectStatus(_ context.Context, _ string, _ int) (string, error) {
	return protocol.ProjectStatusReady, nil
}

func (fakeWorkflowSource) ListPullRequests(context.Context, string) ([]githubPullRequest, error) {
	return nil, nil
}

func (f *fakeWorkflowSource) ListWorkflowFiles(context.Context, string) (string, map[string][]byte, error) {
	f.workflowCalls++
	if f.workflowErr != nil {
		return "", nil, f.workflowErr
	}
	return f.commit, f.files, nil
}

func (f *fakeWorkflowSource) UpsertWorkflowComment(_ context.Context, _ string, _ int, body string) error {
	f.comments = append(f.comments, body)
	return nil
}

type failingWorkflowSource struct {
	issues   map[string][]githubIssue
	comments []string
}

func (f *failingWorkflowSource) ListIssues(_ context.Context, repository string) ([]githubIssue, error) {
	return f.issues[repository], nil
}

// IssueProjectStatus keeps routing tests focused on workflow selection.
func (f *failingWorkflowSource) IssueProjectStatus(_ context.Context, _ string, _ int) (string, error) {
	return protocol.ProjectStatusReady, nil
}

func (*failingWorkflowSource) ListPullRequests(context.Context, string) ([]githubPullRequest, error) {
	return nil, nil
}

func (*failingWorkflowSource) ListWorkflowFiles(context.Context, string) (string, map[string][]byte, error) {
	return "", nil, errors.New("GitHub workflow API unavailable")
}

func (f *failingWorkflowSource) UpsertWorkflowComment(_ context.Context, _ string, _ int, body string) error {
	f.comments = append(f.comments, body)
	return nil
}

func TestGitHubIssueIntakeRoutesByRepositoryWorkflowLabels(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.setManagedRepositoryIssueIntake(context.Background(), repository.ID, true); err != nil {
		t.Fatal(err)
	}
	source := &fakeWorkflowSource{
		commit: "commit-1",
		files: map[string][]byte{
			".factory/workflows/implement.md": []byte("implement"),
			".factory/workflows/dba.md":       []byte("---\nid: dba/index-review\ntitle: DBA\ngithub_issue:\n  labels_all: [team:dba]\n---\nReview indexes."),
		},
		issues: map[string][]githubIssue{repository.RemoteIdentity: {{
			Number: 11, Title: "Index review", URL: "https://github.com/acme/api/issues/11",
			Labels: []githubIssueLabel{{Name: "TEAM:DBA"}},
		}}},
	}
	store.githubIssues = source
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	var workflowID string
	if err := store.db.QueryRow(`SELECT json_extract(task_snapshot, '$.workflow_id') FROM runs`).Scan(&workflowID); err != nil {
		t.Fatal(err)
	}
	if workflowID != "dba/index-review" || len(source.comments) != 1 ||
		!strings.Contains(source.comments[0], "dba/index-review") ||
		strings.Contains(source.comments[0], "pending") || !strings.Contains(source.comments[0], "Work `") {
		t.Fatalf("workflow route = %q, comments = %#v", workflowID, source.comments)
	}
}

type rotatingWorkflowSource struct {
	issues       map[string][]githubIssue
	workflowCall int
}

func (f *rotatingWorkflowSource) ListIssues(_ context.Context, repository string) ([]githubIssue, error) {
	return f.issues[repository], nil
}

// IssueProjectStatus keeps routing tests focused on workflow selection.
func (f *rotatingWorkflowSource) IssueProjectStatus(_ context.Context, _ string, _ int) (string, error) {
	return protocol.ProjectStatusReady, nil
}

func (*rotatingWorkflowSource) ListPullRequests(context.Context, string) ([]githubPullRequest, error) {
	return nil, nil
}

func (f *rotatingWorkflowSource) ListWorkflowFiles(context.Context, string) (string, map[string][]byte, error) {
	f.workflowCall++
	body := "first revision"
	commit := "commit-1"
	if f.workflowCall > 1 {
		body = "second revision"
		commit = "commit-2"
	}
	return commit, map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\ngithub_issue:\n  labels_all: [team:dba]\n---\n" + body),
	}, nil
}

func (*rotatingWorkflowSource) UpsertWorkflowComment(context.Context, string, int, string) error {
	return nil
}

func TestGitHubIssueIntakeFreezesSelectedCatalogRevision(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.setManagedRepositoryIssueIntake(context.Background(), repository.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.recordGitHubWorkflowDiagnostic(context.Background(), repository.ID, 13,
		"https://github.com/acme/api/issues/13", "workflow_route_unavailable", "retry", nil); err != nil {
		t.Fatal(err)
	}
	source := &rotatingWorkflowSource{issues: map[string][]githubIssue{repository.RemoteIdentity: {{
		Number: 13, Title: "Index review", URL: "https://github.com/acme/api/issues/13",
		Labels: []githubIssueLabel{{Name: "team:dba"}},
	}}}}
	store.githubIssues = source
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if source.workflowCall != 1 {
		t.Fatalf("workflow refreshes = %d, want 1", source.workflowCall)
	}
	page, err := store.RunPage(context.Background(), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(page.Runs))
	}
	detail, err := store.Run(context.Background(), page.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	work := detail.Sessions[0]
	if work.Workflow.CommitSHA != "commit-1" || !strings.Contains(work.ResolvedPrompt, "first revision") ||
		strings.Contains(work.ResolvedPrompt, "second revision") {
		t.Fatalf("selected workflow revision = %#v", work.Workflow)
	}
	var diagnostics int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM repository_workflow_diagnostics WHERE repository_id = ? AND issue_number = ?`, repository.ID, 13).Scan(&diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics != 0 {
		t.Fatalf("successful admission left %d routing diagnostics", diagnostics)
	}
}

func TestGitHubIssueIntakeRecordsCatalogFailurePerIssue(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.setManagedRepositoryIssueIntake(context.Background(), repository.ID, true); err != nil {
		t.Fatal(err)
	}
	source := &failingWorkflowSource{issues: map[string][]githubIssue{repository.RemoteIdentity: {{
		Number: 12, URL: "https://github.com/acme/api/issues/12",
	}}}}
	store.githubIssues = source
	if err := store.PollGitHubIssues(context.Background()); err == nil {
		t.Fatal("catalog failure was not reported")
	}
	var code string
	if err := store.db.QueryRow(`SELECT code FROM repository_workflow_diagnostics WHERE repository_id = ? AND issue_number = ?`, repository.ID, 12).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "workflow_catalog_unavailable" || len(source.comments) != 1 {
		t.Fatalf("catalog failure = code %q, comments %#v", code, source.comments)
	}
}

func TestRepositoryWorkflowCatalogRefreshAndSnapshot(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{
		commit: "commit-1",
		files: map[string][]byte{
			".factory/workflows/implement.md":        []byte("implement instructions"),
			".factory/workflows/dba/index-review.md": []byte("---\nid: dba/index-review\ntitle: DBA index review\ngithub_issue:\n  labels_all: [team:dba]\n---\nReview indexes."),
		},
	}
	catalog, err := store.RefreshRepositoryWorkflowCatalog(context.Background(), repository.ID)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Status != protocol.RepositoryWorkflowHealthy || catalog.CommitSHA != "commit-1" || len(catalog.Workflows) != 2 {
		t.Fatalf("catalog = %#v", catalog)
	}
	workflow, err := catalog.MatchIssueLabels([]string{"TEAM:DBA"})
	if err != nil || workflow.ID != "dba/index-review" {
		t.Fatalf("workflow = %#v, %v", workflow, err)
	}
	loaded, err := store.WorkflowCatalog(context.Background(), repository.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workflows[0].Instructions != "" {
		t.Fatal("catalog API model exposed workflow instructions")
	}
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.WorkflowSnapshot(context.Background(), tx, repository.ID, "dba/index-review")
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if snapshot.Instructions != "Review indexes." || snapshot.CommitSHA != "commit-1" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := store.recordGitHubWorkflowDiagnostic(context.Background(), repository.ID, 12,
		"https://github.com/acme/api/issues/12", "workflow_route_ambiguous",
		"multiple workflows match issue labels", []string{"dba", "qa"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.WorkflowCatalog(context.Background(), repository.ID)
	if err != nil || len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].IssueNumber != 12 {
		t.Fatalf("diagnostics = %#v, err %v", loaded.Diagnostics, err)
	}
	if len(loaded.Diagnostics[0].WorkflowIDs) != 2 || loaded.Diagnostics[0].WorkflowIDs[0] != "dba" {
		t.Fatalf("diagnostic candidates = %#v", loaded.Diagnostics[0].WorkflowIDs)
	}
}

func TestRepositoryWorkflowOptionsReturnIntersection(t *testing.T) {
	store := newTestStore(t)
	first, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/one"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/two"})
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/qa.md":        []byte("---\nid: qa\ntitle: QA\n---\nqa"),
	}}
	if _, err := store.RefreshRepositoryWorkflowCatalog(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{commit: "commit-2", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/qa.md":        []byte("---\nid: qa\ntitle: QA\n---\nqa two"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\n---\ndba"),
	}}
	if _, err := store.RefreshRepositoryWorkflowCatalog(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	options, err := store.WorkflowOptions(context.Background(), []string{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 || options[0].ID != "implement" || options[1].ID != "qa" {
		t.Fatalf("options = %#v", options)
	}
}

func TestLegacyRepositoryUsesSyntheticWorkflowFallback(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE repositories SET centrally_managed = 0, enabled = 0 WHERE id = ?`, repository.ID); err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{}
	catalog, err := store.WorkflowCatalog(context.Background(), repository.ID)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Status != protocol.RepositoryWorkflowHealthy || len(catalog.Workflows) != 1 || catalog.Workflows[0].ID != protocol.RepositoryWorkflowFallback {
		t.Fatalf("legacy catalog = %#v", catalog)
	}
	options, err := store.WorkflowOptions(context.Background(), []string{repository.ID})
	if err != nil || len(options) != 1 || options[0].ID != protocol.RepositoryWorkflowFallback {
		t.Fatalf("legacy options = %#v, err %v", options, err)
	}
}

func TestProcedureAndScheduledRunsFreezeRepositoryWorkflow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	repository := createManagedRepositoryForProcedure(t, store, "github.com/acme/api")
	store.githubIssues = &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\n---\nRun the DBA checks."),
	}}
	if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
		t.Fatal(err)
	}
	procedure, err := store.CreateTask(ctx, protocol.SaveTaskRequest{
		Name: "DBA procedure", Prompt: "Review the repository.", Runtime: protocol.RuntimeCodex,
		RepositoryIDs: []string{repository.ID}, WorkflowID: "dba",
	})
	if err != nil {
		t.Fatal(err)
	}
	procedureRun, err := store.AdmitProcedureRun(ctx, protocol.ProcedureRunRequest{
		RequestKey: "dba-procedure", Procedure: procedure.Name,
		Repositories: []string{repository.RemoteIdentity},
	})
	if err != nil {
		t.Fatal(err)
	}
	procedureWork := procedureRun.Run.Sessions[0]
	if procedureWork.Workflow.ID != "dba" || !strings.Contains(procedureWork.ResolvedPrompt, "Run the DBA checks.") {
		t.Fatalf("procedure workflow = %#v", procedureWork)
	}

	now := time.Date(2026, time.August, 31, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	scheduled, err := store.CreateTask(ctx, protocol.SaveTaskRequest{
		Name: "Scheduled DBA", Prompt: "Review the repository.", Runtime: protocol.RuntimeCodex,
		RepositoryIDs: []string{repository.ID}, WorkflowID: "dba",
		Schedule: protocol.TaskSchedule{Enabled: true, Cron: "0 9 * * *", Timezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	if err := store.AdmitDueTasks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	page, err := store.RunPage(ctx, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	var scheduledWork protocol.Session
	for _, run := range page.Runs {
		if run.Task.Name == scheduled.Name {
			detail, err := store.Run(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			scheduledWork = detail.Sessions[0]
		}
	}
	if scheduledWork.Workflow.ID != "dba" || !strings.Contains(scheduledWork.ResolvedPrompt, "Run the DBA checks.") {
		t.Fatalf("scheduled workflow = %#v", scheduledWork)
	}
}

func TestScheduledRetryReusesClaimedRepositoryWorkflowSnapshots(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 31, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	first, _, err := store.CreateManagedRepository(ctx, protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/one"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.CreateManagedRepository(ctx, protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/two"})
	if err != nil {
		t.Fatal(err)
	}
	workflowFiles := func(body string) map[string][]byte {
		return map[string][]byte{
			".factory/workflows/implement.md": []byte("implement"),
			".factory/workflows/qa.md":        []byte("---\nid: qa\ntitle: QA\n---\n" + body),
		}
	}
	source := &mappedWorkflowSource{revisions: map[string]workflowRevision{
		first.RemoteIdentity:  {commit: "one-commit-1", files: workflowFiles("one-old")},
		second.RemoteIdentity: {commit: "two-commit-1", files: workflowFiles("two-old")},
	}}
	store.githubIssues = source
	for _, repository := range []protocol.ManagedRepository{first, second} {
		if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
			t.Fatal(err)
		}
	}
	worker := registerTestWorker(t, store, workerA, 10,
		protocol.RepositoryRegistration{Key: "one", RemoteIdentity: first.RemoteIdentity},
		protocol.RepositoryRegistration{Key: "two", RemoteIdentity: second.RemoteIdentity},
	)
	task, err := store.CreateTask(ctx, protocol.SaveTaskRequest{
		Name: "Scheduled QA retry", Prompt: "Run QA.", Runtime: protocol.RuntimeCodex,
		RepositoryIDs: []string{first.ID, second.ID}, WorkflowID: "qa",
		Schedule: protocol.TaskSchedule{Enabled: true, Cron: "0 9 * * *", Timezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var capabilities string
	if err := store.db.QueryRowContext(ctx, `SELECT capabilities_json FROM workers WHERE id = ?`, worker.ID).Scan(&capabilities); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, time.August, 31, 9, 1, 0, 0, time.UTC)
	if _, err := store.db.ExecContext(ctx, `UPDATE workers SET last_heartbeat = ?, capabilities_json = 'not-json' WHERE id = ?`, now.UnixMilli(), worker.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitDueTasks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var pending []byte
	if err := store.db.QueryRowContext(ctx, `SELECT pending_snapshot_json FROM tasks WHERE id = ?`, task.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pending), "one-old") || !strings.Contains(string(pending), "two-old") {
		t.Fatalf("pending workflow snapshots = %s", pending)
	}
	source.revisions[first.RemoteIdentity] = workflowRevision{commit: "one-commit-2", files: workflowFiles("one-new")}
	source.revisions[second.RemoteIdentity] = workflowRevision{commit: "two-commit-2", files: workflowFiles("two-new")}
	for _, repository := range []protocol.ManagedRepository{first, second} {
		if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
			t.Fatal(err)
		}
	}
	now = time.Date(2026, time.August, 31, 9, 3, 0, 0, time.UTC)
	if _, err := store.db.ExecContext(ctx, `UPDATE workers SET last_heartbeat = ?, capabilities_json = ? WHERE id = ?`, now.UnixMilli(), capabilities, worker.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitDueTasks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	page, err := store.RunPage(ctx, 10, "")
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("scheduled retry runs = %#v, err %v", page, err)
	}
	for repositoryID, workflow := range page.Runs[0].Task.RepositoryWorkflows {
		if workflow.Instructions != "" {
			t.Fatalf("Run page leaked workflow instructions for %s", repositoryID)
		}
	}
	detail, err := store.Run(ctx, page.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Sessions) != 2 || detail.Sessions[0].Workflow.CommitSHA != "one-commit-1" ||
		detail.Sessions[1].Workflow.CommitSHA != "two-commit-1" ||
		!strings.Contains(detail.Sessions[0].ResolvedPrompt, "one-old") ||
		!strings.Contains(detail.Sessions[1].ResolvedPrompt, "two-old") ||
		strings.Contains(detail.Sessions[0].ResolvedPrompt, "one-new") ||
		strings.Contains(detail.Sessions[1].ResolvedPrompt, "two-new") {
		t.Fatalf("scheduled retry workflow snapshots = %#v", detail.Sessions)
	}
}

func TestScheduledWorkflowFailureRecordsBlockedOccurrence(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 31, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	repository, _, err := store.CreateManagedRepository(ctx, protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/qa.md": []byte("---\nid: qa\ntitle: QA\n---\nRun QA."),
	}}
	store.githubIssues = source
	if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(ctx, protocol.SaveTaskRequest{
		Name: "Scheduled missing workflow", Prompt: "Run QA.", Runtime: protocol.RuntimeCodex,
		RepositoryIDs: []string{repository.ID}, WorkflowID: "qa",
		Schedule: protocol.TaskSchedule{Enabled: true, Cron: "0 9 * * *", Timezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.commit = "commit-2"
	source.files = map[string][]byte{
		".factory/workflows/broken.md": []byte("missing frontmatter"),
	}
	if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err == nil {
		t.Fatal("expected invalid catalog refresh")
	}
	now = time.Date(2026, time.August, 31, 9, 1, 0, 0, time.UTC)
	if err := store.AdmitDueTasks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var pending, retryAt sql.NullInt64
	var health, code, message string
	if err := store.db.QueryRowContext(ctx, `
		SELECT pending_due_at, schedule_retry_at, schedule_health_status,
		       schedule_health_code, schedule_health_message
		FROM tasks WHERE id = ?
	`, task.ID).Scan(&pending, &retryAt, &health, &code, &message); err != nil {
		t.Fatal(err)
	}
	if !pending.Valid || !retryAt.Valid || health != "error" || code != "transient_admission_error" ||
		!strings.Contains(message, "workflow qa") {
		t.Fatalf("scheduled failure = pending %v, retry %v, health %q, code %q, message %q",
			pending.Valid, retryAt.Valid, health, code, message)
	}
	source.commit = "commit-3"
	source.files = map[string][]byte{
		".factory/workflows/qa.md": []byte("---\nid: qa\ntitle: QA\n---\nRun repaired QA."),
	}
	if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
		t.Fatal(err)
	}
	now = fromMillis(retryAt.Int64).Add(time.Second)
	if err := store.AdmitDueTasks(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT pending_due_at, schedule_health_status, schedule_health_code
		FROM tasks WHERE id = ?
	`, task.ID).Scan(&pending, &health, &code); err != nil {
		t.Fatal(err)
	}
	if pending.Valid || health != "healthy" || code != "" {
		t.Fatalf("scheduled recovery = pending %v, health %q, code %q", pending.Valid, health, code)
	}
	page, err := store.RunPage(ctx, 10, "")
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("recovered runs = %#v, err %v", page, err)
	}
	detail, err := store.Run(ctx, page.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Sessions[0].Workflow.CommitSHA != "commit-3" ||
		!strings.Contains(detail.Sessions[0].ResolvedPrompt, "Run repaired QA.") {
		t.Fatalf("recovered workflow = %#v", detail.Sessions[0].Workflow)
	}
}

func TestReplacementPreservesRepositoryWorkflowSnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	repository, _, err := store.CreateManagedRepository(ctx, protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\n---\nRun the DBA checks."),
	}}
	if _, err := store.RefreshRepositoryWorkflowCatalog(ctx, repository.ID); err != nil {
		t.Fatal(err)
	}
	worker := registerTestWorker(t, store, workerA, 10, protocol.RepositoryRegistration{
		Key: "api", RemoteIdentity: repository.RemoteIdentity,
	})
	task, err := store.CreateTask(ctx, protocol.SaveTaskRequest{
		Name: "Replace DBA", Prompt: "Review the repository.", Runtime: protocol.RuntimeCodex,
		RepositoryIDs: []string{repository.ID}, WorkflowID: "dba",
		OutcomeContract: protocol.OutcomeAgentUpdate,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := store.RunTask(ctx, task.ID, protocol.RunTaskRequest{RequestKey: "replace-dba-first"})
	if err != nil {
		t.Fatal(err)
	}
	firstWork := first.Sessions[0]
	if _, err := store.db.ExecContext(ctx, `UPDATE sessions SET state = 'failed', terminal_at = admitted_at WHERE id = ?`, firstWork.ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ReplaceWork(ctx, protocol.ReplaceWorkRequest{
		RequestKey: "replace-dba", WorkID: firstWork.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Run.Sessions[0].Workflow.ID != "dba" ||
		!strings.Contains(replacement.Run.Sessions[0].ResolvedPrompt, "Run the DBA checks.") ||
		replacement.Run.Sessions[0].AssignedWorkerID != worker.ID {
		t.Fatalf("replacement workflow = %#v", replacement.Run.Sessions[0])
	}
}

func TestExplicitBuildWorkflowRefreshesRepositoryCatalog(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	repository, _, err := store.CreateManagedRepository(ctx, protocol.CreateManagedRepositoryRequest{RemoteIdentity: "github.com/acme/api"})
	if err != nil {
		t.Fatal(err)
	}
	store.githubIssues = &fakeWorkflowSource{commit: "commit-1", files: map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\n---\nRun the DBA checks."),
	}}
	admission, err := store.AdmitBuild(ctx, protocol.BuildRequest{
		RequestKey: "explicit-build-dba", References: []string{"https://github.com/acme/api/issues/7"},
		Workflow: "dba", WorkflowSpecified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if admission.Run.Run.Task.WorkflowID != "dba" || admission.Run.Sessions[0].Workflow.ID != "dba" ||
		!strings.Contains(admission.Run.Sessions[0].ResolvedPrompt, "Run the DBA checks.") {
		t.Fatalf("Build workflow = %#v", admission.Run)
	}
	var commit string
	if err := store.db.QueryRowContext(ctx, `SELECT commit_sha FROM repository_workflow_catalogs WHERE repository_id = ?`, repository.ID).Scan(&commit); err != nil || commit != "commit-1" {
		t.Fatalf("catalog commit = %q, err %v", commit, err)
	}
}
