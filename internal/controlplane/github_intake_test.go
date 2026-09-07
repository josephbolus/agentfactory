package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/jbolus-owens/factory/internal/protocol"
)

type fakeGitHubIssues struct {
	issues           map[string][]githubIssue
	pullRequests     map[string][]githubPullRequest
	calls            []string
	pullRequestCalls []string
}

func (f *fakeGitHubIssues) ListPullRequests(_ context.Context, repository string) ([]githubPullRequest, error) {
	f.pullRequestCalls = append(f.pullRequestCalls, repository)
	return f.pullRequests[repository], nil
}

func (f *fakeGitHubIssues) ListIssues(_ context.Context, repository string) ([]githubIssue, error) {
	f.calls = append(f.calls, repository)
	return f.issues[repository], nil
}

func TestGitHubIssueIntakeAdmitsEachIssueOnce(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.setManagedRepositoryIssueIntake(context.Background(), repository.ID, true); err != nil {
		t.Fatal(err)
	}
	source := &fakeGitHubIssues{issues: map[string][]githubIssue{
		repository.RemoteIdentity: {{Number: 42, Title: "Fix case-insensitive search", URL: "https://github.com/acme/api/issues/42"}},
	}}
	store.githubIssues = source

	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 2 {
		t.Fatalf("GitHub issue calls = %d, want 2", len(source.calls))
	}
	var runCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("runs = %d, want 1", runCount)
	}
	var runID string
	if err := store.db.QueryRow(`SELECT id FROM runs`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	run, err := store.Run(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Run.OutcomeContract != protocol.OutcomeProcessExit ||
		strings.Contains(run.Sessions[0].ResolvedPrompt, "factory update") {
		t.Fatalf("GitHub intake Run = %#v", run)
	}
	if run.Run.Targets[0].SourceTitle != "Fix case-insensitive search" ||
		run.Sessions[0].Target.SourceTitle != "Fix case-insensitive search" {
		t.Fatalf("GitHub intake title = %#v, %#v", run.Run.Targets[0], run.Sessions[0].Target)
	}
	var sourceReference string
	if err := store.db.QueryRow(`SELECT source_reference FROM sessions`).Scan(&sourceReference); err != nil {
		t.Fatal(err)
	}
	if sourceReference != "https://github.com/acme/api/issues/42" {
		t.Fatalf("source reference = %q", sourceReference)
	}
}

func TestGitHubIssueIntakeRequiresRepositoryOptIn(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeGitHubIssues{issues: map[string][]githubIssue{
		repository.RemoteIdentity: {{Number: 42, URL: "https://github.com/acme/api/issues/42"}},
	}}
	store.githubIssues = source

	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.calls) != 0 {
		t.Fatalf("GitHub issue calls = %d, want 0", len(source.calls))
	}
}

func TestGitHubPullRequestWakeRequiresLinkedTerminalWork(t *testing.T) {
	store := newTestStore(t)
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/acme/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.setManagedRepositoryIssueIntake(context.Background(), repository.ID, true); err != nil {
		t.Fatal(err)
	}
	admission, err := store.admitGitHubIssueBuild(context.Background(), protocol.BuildRequest{
		RequestKey: "issue-42", References: []string{"https://github.com/acme/api/issues/42"},
	}, "Fix case-insensitive search")
	if err != nil {
		t.Fatal(err)
	}
	work := admission.Run.Sessions[0]
	pullRequestURL := "https://github.com/acme/api/pull/7"
	if _, err := store.db.Exec(`
		UPDATE sessions SET state = 'ready', terminal_at = ?, pull_request_url = ? WHERE id = ?
	`, store.now().UnixMilli(), pullRequestURL, work.ID); err != nil {
		t.Fatal(err)
	}
	source := &fakeGitHubIssues{pullRequests: map[string][]githubPullRequest{
		repository.RemoteIdentity: {{URL: pullRequestURL}},
	}}
	store.githubIssues = source

	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	var replacementCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE predecessor_work_id = ?`, work.ID).Scan(&replacementCount); err != nil {
		t.Fatal(err)
	}
	if replacementCount != 1 {
		t.Fatalf("replacement Work = %d, want 1", replacementCount)
	}
	var replacementTitle string
	if err := store.db.QueryRow(`SELECT source_title FROM sessions WHERE predecessor_work_id = ?`, work.ID).Scan(&replacementTitle); err != nil {
		t.Fatal(err)
	}
	if replacementTitle != "Fix case-insensitive search" {
		t.Fatalf("replacement source title = %q", replacementTitle)
	}
	var replacementID string
	if err := store.db.QueryRow(`SELECT id FROM sessions WHERE predecessor_work_id = ?`, work.ID).Scan(&replacementID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		UPDATE sessions SET state = 'ready', terminal_at = ?, pull_request_url = ? WHERE id = ?
	`, store.now().UnixMilli(), pullRequestURL, replacementID); err != nil {
		t.Fatal(err)
	}
	source.pullRequests[repository.RemoteIdentity] = nil
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.pullRequests[repository.RemoteIdentity] = []githubPullRequest{{URL: pullRequestURL}}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&replacementCount); err != nil {
		t.Fatal(err)
	}
	if replacementCount != 3 {
		t.Fatalf("sessions after re-labelling PR = %d, want 3", replacementCount)
	}

	source.pullRequests[repository.RemoteIdentity] = []githubPullRequest{{URL: "https://github.com/acme/api/pull/99"}}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&replacementCount); err != nil {
		t.Fatal(err)
	}
	if replacementCount != 3 {
		t.Fatalf("sessions after unrelated PR = %d, want 3", replacementCount)
	}
}
