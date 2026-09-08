package controlplane

import (
	"context"
	"strings"
	"testing"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

type fakeGitHubIssues struct {
	issues           map[string][]githubIssue
	pullRequests     map[string][]githubPullRequest
	projectStatus    map[int]string
	calls            []string
	pullRequestCalls []string
}

func (f *fakeGitHubIssues) IssueProjectStatus(_ context.Context, _ string, issueNumber int) (string, error) {
	return f.projectStatus[issueNumber], nil
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
	source := &fakeGitHubIssues{
		issues: map[string][]githubIssue{
			repository.RemoteIdentity: {{Number: 42, Title: "Fix case-insensitive search", URL: "https://github.com/acme/api/issues/42"}},
		},
		projectStatus: map[int]string{42: protocol.ProjectStatusReady},
	}
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

// TestGitHubIssueIntakeRequiresProjectReady proves the second admission
// condition: a needs-agent issue is admitted only while its Factory Project
// status is Ready. Todo and missing items wait; Ready admits exactly once.
func TestGitHubIssueIntakeRequiresProjectReady(t *testing.T) {
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
	source := &fakeGitHubIssues{
		issues: map[string][]githubIssue{
			repository.RemoteIdentity: {{Number: 42, Title: "Fix case-insensitive search", URL: "https://github.com/acme/api/issues/42"}},
		},
		projectStatus: map[int]string{42: protocol.ProjectStatusTodo},
	}
	store.githubIssues = source

	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("Todo issue admitted %d runs, want 0", runs)
	}

	source.projectStatus[42] = protocol.ProjectStatusReady
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("Ready issue admitted %d runs, want 1", runs)
	}

	// A second poll of the same ready visit must not double-start Work.
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("repeated poll admitted %d runs, want 1", runs)
	}
}

// TestGitHubIssueIntakeRearmsAfterLabelRemoval proves one visit is one Work:
// while the issue keeps needs-agent it runs once, removing the label ends the
// visit, and re-adding it starts exactly one new Work.
func TestGitHubIssueIntakeRearmsAfterLabelRemoval(t *testing.T) {
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
	issue := githubIssue{Number: 42, Title: "Fix case-insensitive search", URL: "https://github.com/acme/api/issues/42"}
	source := &fakeGitHubIssues{
		issues:        map[string][]githubIssue{repository.RemoteIdentity: {issue}},
		projectStatus: map[int]string{42: protocol.ProjectStatusReady},
	}
	store.githubIssues = source

	poll := func() int {
		t.Helper()
		if err := store.PollGitHubIssues(context.Background()); err != nil {
			t.Fatal(err)
		}
		var runs int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
			t.Fatal(err)
		}
		return runs
	}

	if runs := poll(); runs != 1 {
		t.Fatalf("first visit runs = %d, want 1", runs)
	}
	if runs := poll(); runs != 1 {
		t.Fatalf("second poll of one visit runs = %d, want 1", runs)
	}

	// Finish the visit before the human re-labels the issue.
	if _, err := store.db.Exec(`UPDATE sessions SET state = 'succeeded', terminal_at = ?`, store.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Removing needs-agent removes the issue from the poll and ends the visit.
	source.issues[repository.RemoteIdentity] = nil
	if runs := poll(); runs != 1 {
		t.Fatalf("runs after label removal = %d, want 1", runs)
	}
	source.issues[repository.RemoteIdentity] = []githubIssue{issue}
	if runs := poll(); runs != 2 {
		t.Fatalf("runs after re-adding the label = %d, want 2", runs)
	}

	var distinctKeys int
	if err := store.db.QueryRow(`SELECT COUNT(DISTINCT request_key) FROM runs`).Scan(&distinctKeys); err != nil {
		t.Fatal(err)
	}
	if distinctKeys != 2 {
		t.Fatalf("distinct request keys = %d, want 2", distinctKeys)
	}
}

// TestSplitGitHubRepository pins the remote-identity parsing that the live
// GraphQL gate depends on: managed repositories store "github.com/owner/name".
func TestSplitGitHubRepository(t *testing.T) {
	for _, test := range []struct {
		repository string
		owner      string
		name       string
		invalid    bool
	}{
		{repository: "github.com/josephbolus/agentfactory", owner: "josephbolus", name: "agentfactory"},
		{repository: "josephbolus/agentfactory", owner: "josephbolus", name: "agentfactory"},
		{repository: "GitHub.com/JosephBolus/AgentFactory", owner: "josephbolus", name: "agentfactory"},
		{repository: "bad", invalid: true},
		{repository: "github.com/", invalid: true},
		{repository: "github.com/owner/name/extra", invalid: true},
	} {
		owner, name, err := splitGitHubRepository(test.repository)
		if test.invalid {
			if err == nil {
				t.Fatalf("splitGitHubRepository(%q) accepted an invalid identity", test.repository)
			}
			continue
		}
		if err != nil || owner != test.owner || name != test.name {
			t.Fatalf("splitGitHubRepository(%q) = %q, %q, %v", test.repository, owner, name, err)
		}
	}
}
