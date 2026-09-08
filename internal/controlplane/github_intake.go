package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

const githubIssuePollInterval = 30 * time.Second

// factoryProjectTitle is the GitHub Project whose Status field gates intake.
const factoryProjectTitle = "Factory"

type githubIssue struct {
	Number int                `json:"number"`
	Title  string             `json:"title"`
	URL    string             `json:"url"`
	Labels []githubIssueLabel `json:"labels,omitempty"`
}

type githubIssueLabel struct {
	Name string `json:"name"`
}

type githubPullRequest struct {
	URL string `json:"url"`
}

type githubIssueSource interface {
	ListIssues(context.Context, string) ([]githubIssue, error)
	ListPullRequests(context.Context, string) ([]githubPullRequest, error)
	IssueProjectStatus(context.Context, string, int) (string, error)
}

type githubCLI struct {
	runJSON func(context.Context, []string, any) error
	run     func(context.Context, []string) error
}

func (githubCLI) ListIssues(ctx context.Context, repository string) ([]githubIssue, error) {
	command := exec.CommandContext(ctx, "gh", "issue", "list", "--repo", repository,
		"--state", "open", "--label", "needs-agent", "--limit", "100", "--json", "number,title,url,labels")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list GitHub issues for %s: %w", repository, err)
	}
	var issues []githubIssue
	if err := json.Unmarshal(output, &issues); err != nil {
		return nil, fmt.Errorf("decode GitHub issues for %s: %w", repository, err)
	}
	return issues, nil
}

func (githubCLI) ListPullRequests(ctx context.Context, repository string) ([]githubPullRequest, error) {
	command := exec.CommandContext(ctx, "gh", "pr", "list", "--repo", repository,
		"--state", "open", "--label", "needs-agent", "--limit", "100", "--json", "url")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list GitHub pull requests for %s: %w", repository, err)
	}
	var pullRequests []githubPullRequest
	if err := json.Unmarshal(output, &pullRequests); err != nil {
		return nil, fmt.Errorf("decode GitHub pull requests for %s: %w", repository, err)
	}
	return pullRequests, nil
}

// splitGitHubRepository turns a remote identity such as
// "github.com/owner/name" into the owner and name GraphQL expects.
func splitGitHubRepository(repository string) (string, string, error) {
	slug := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(repository)), "github.com/")
	owner, name, found := strings.Cut(slug, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("invalid GitHub repository %q", repository)
	}
	return owner, name, nil
}

// IssueProjectStatus reads the issue's Status field from the Factory GitHub
// Project. A missing item or status returns an empty string.
func (githubCLI) IssueProjectStatus(ctx context.Context, repository string, issueNumber int) (string, error) {
	owner, name, err := splitGitHubRepository(repository)
	if err != nil {
		return "", err
	}
	const query = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){issue(number:$number){projectItems(first:10){nodes{project{title} fieldValueByName(name:"Status"){... on ProjectV2ItemFieldSingleSelectValue{name}}}}}}}`
	command := exec.CommandContext(ctx, "gh", "api", "graphql", "-f", "query="+query,
		"-F", "owner="+owner, "-F", "name="+name, "-F", "number="+strconv.Itoa(issueNumber))
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(strings.TrimSpace(string(exitErr.Stderr))) > 0 {
			return "", fmt.Errorf("read GitHub Project status for %s#%d: %s", repository, issueNumber, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("read GitHub Project status for %s#%d: %w", repository, issueNumber, err)
	}
	var response struct {
		Data struct {
			Repository struct {
				Issue struct {
					ProjectItems struct {
						Nodes []struct {
							Project struct {
								Title string `json:"title"`
							} `json:"project"`
							FieldValueByName *struct {
								Name string `json:"name"`
							} `json:"fieldValueByName"`
						} `json:"nodes"`
					} `json:"projectItems"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return "", fmt.Errorf("decode GitHub Project status for %s#%d: %w", repository, issueNumber, err)
	}
	fallback := ""
	for _, node := range response.Data.Repository.Issue.ProjectItems.Nodes {
		if node.FieldValueByName == nil || strings.TrimSpace(node.FieldValueByName.Name) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(node.Project.Title), factoryProjectTitle) {
			return strings.TrimSpace(node.FieldValueByName.Name), nil
		}
		if fallback == "" {
			fallback = strings.TrimSpace(node.FieldValueByName.Name)
		}
	}
	return fallback, nil
}

func (s *Store) RunGitHubIssueIntake(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = githubIssuePollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := s.PollGitHubIssues(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("github_issue_intake_failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) PollGitHubIssues(ctx context.Context) error {
	repositories, err := s.issueIntakeRepositories(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, repository := range repositories {
		issues, err := s.githubIssues.ListIssues(ctx, repository.RemoteIdentity)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
		pollKey, err := newID()
		if err != nil {
			result = errors.Join(result, unavailable(err))
			continue
		}
		var catalog protocol.RepositoryWorkflowCatalog
		workflowSource, hasWorkflowSource := s.githubIssues.(githubWorkflowSource)
		if hasWorkflowSource {
			catalog, err = s.RefreshRepositoryWorkflowCatalog(ctx, repository.ID)
			if err != nil {
				result = errors.Join(result, err)
				code := "workflow_catalog_unavailable"
				if catalog.Status == protocol.RepositoryWorkflowInvalid {
					code = "workflow_catalog_invalid"
				}
				for _, issue := range issues {
					if blockErr := s.blockGitHubWorkflowCatalog(ctx, repository, issue, code, err); blockErr != nil {
						result = errors.Join(result, blockErr)
					}
				}
				continue
			}
		}
		for _, issue := range issues {
			reference, err := protocol.NormalizeBuildReference(issue.URL)
			if err != nil || reference.SourceKind != "github_issue" ||
				reference.RepositoryIdentity != repository.RemoteIdentity {
				result = errors.Join(result, invalid("invalid_github_issue", "GitHub returned an invalid issue reference"))
				continue
			}
			workflowID := ""
			var selectedWorkflow protocol.RepositoryWorkflow
			if hasWorkflowSource {
				labels := make([]string, 0, len(issue.Labels))
				for _, label := range issue.Labels {
					labels = append(labels, label.Name)
				}
				workflow, matchErr := catalog.MatchIssueLabels(labels)
				if matchErr != nil {
					result = errors.Join(result, s.blockGitHubWorkflowRoute(ctx, repository, issue, matchErr))
					continue
				}
				workflowID = workflow.ID
				selectedWorkflow = workflow
			}
			projectStatus, err := s.githubIssues.IssueProjectStatus(ctx, repository.RemoteIdentity, issue.Number)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			if !strings.EqualFold(projectStatus, protocol.ProjectStatusReady) {
				continue
			}
			_, active, err := s.githubIssueVisit(ctx, repository.ID, issue.Number)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			if active {
				if err := s.touchGitHubIssueVisit(ctx, repository.ID, issue.Number, pollKey); err != nil {
					result = errors.Join(result, err)
				}
				continue
			}
			visitKey, err := s.startGitHubIssueVisit(ctx, repository.ID, issue.Number, pollKey)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			rebuild, err := s.githubIssueNeedsRebuild(ctx, repository.ID, reference.SourceKey)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			request := protocol.BuildRequest{
				RequestKey: "github-issue:" + reference.SourceKey + ":" + visitKey,
				References: []string{reference.Reference}, Workflow: workflowID,
				WorkflowSpecified: workflowID != "", Rebuild: rebuild,
			}
			var admission protocol.BuildAdmission
			if hasWorkflowSource {
				admission, err = s.admitGitHubIssueBuildWithWorkflow(ctx, request, issue.Title, repository.ID, selectedWorkflow)
			} else {
				admission, err = s.admitGitHubIssueBuild(ctx, request, issue.Title)
			}
			if err != nil {
				if releaseErr := s.clearGitHubIssueVisit(ctx, repository.ID, issue.Number); releaseErr != nil {
					result = errors.Join(result, releaseErr)
				}
				result = errors.Join(result, err)
				continue
			}
			if hasWorkflowSource {
				if err := s.clearGitHubWorkflowDiagnostic(ctx, repository.ID, issue.Number); err != nil {
					result = errors.Join(result, err)
				}
			}
			if hasWorkflowSource && len(admission.Run.Sessions) > 0 {
				if err := workflowSource.UpsertWorkflowComment(ctx, repository.RemoteIdentity, issue.Number,
					workflowRoutingComment(selectedWorkflow, admission.Run.Sessions[0].ID)); err != nil {
					result = errors.Join(result, err)
				}
			}
		}
		if err := s.clearMissingGitHubIssueVisits(ctx, repository.ID, pollKey); err != nil {
			result = errors.Join(result, err)
		}
		if err := s.pollGitHubPullRequests(ctx, repository); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (s *Store) blockGitHubWorkflowCatalog(
	ctx context.Context,
	repository protocol.ManagedRepository,
	issue githubIssue,
	code string,
	catalogErr error,
) error {
	if err := s.recordGitHubWorkflowDiagnostic(ctx, repository.ID, issue.Number, issue.URL,
		code, catalogErr.Error(), nil); err != nil {
		return err
	}
	source, ok := s.githubIssues.(githubWorkflowSource)
	if !ok {
		return nil
	}
	return source.UpsertWorkflowComment(ctx, repository.RemoteIdentity, issue.Number,
		workflowCommentMarker+"\nFactory could not load repository workflows: "+catalogErr.Error()+"\n")
}

func (s *Store) blockGitHubWorkflowRoute(
	ctx context.Context,
	repository protocol.ManagedRepository,
	issue githubIssue,
	routeErr error,
) error {
	code := "workflow_route_ambiguous"
	workflowIDs := []string(nil)
	var matchErr *protocol.WorkflowMatchError
	if errors.As(routeErr, &matchErr) {
		workflowIDs = matchErr.WorkflowIDs
	}
	if strings.Contains(routeErr.Error(), "unavailable") || strings.Contains(routeErr.Error(), "implementation workflow") {
		code = "workflow_route_unavailable"
	}
	if err := s.recordGitHubWorkflowDiagnostic(ctx, repository.ID, issue.Number, issue.URL, code, routeErr.Error(), workflowIDs); err != nil {
		return errors.Join(routeErr, err)
	}
	if source, ok := s.githubIssues.(githubWorkflowSource); ok {
		if err := source.UpsertWorkflowComment(ctx, repository.RemoteIdentity, issue.Number, workflowCommentMarker+"\nFactory could not route this Issue: "+routeErr.Error()+"\n"); err != nil {
			return errors.Join(routeErr, err)
		}
	}
	return routeErr
}

func (s *Store) recordGitHubWorkflowDiagnostic(
	ctx context.Context,
	repositoryID string,
	issueNumber int,
	issueURL, code, message string,
	workflowIDs []string,
) error {
	if workflowIDs == nil {
		workflowIDs = []string{}
	}
	encoded, err := json.Marshal(workflowIDs)
	if err != nil {
		return unavailable(err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO repository_workflow_diagnostics(
			repository_id, issue_number, issue_url, code, message, workflow_ids_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository_id, issue_number) DO UPDATE SET
			issue_url = excluded.issue_url, code = excluded.code, message = excluded.message,
			workflow_ids_json = excluded.workflow_ids_json, updated_at = excluded.updated_at
	`, repositoryID, issueNumber, issueURL, code, message, encoded, s.now().UnixMilli())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) clearGitHubWorkflowDiagnostic(ctx context.Context, repositoryID string, issueNumber int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repository_workflow_diagnostics WHERE repository_id = ? AND issue_number = ?`, repositoryID, issueNumber)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) pollGitHubPullRequests(ctx context.Context, repository protocol.ManagedRepository) error {
	pullRequests, err := s.githubIssues.ListPullRequests(ctx, repository.RemoteIdentity)
	if err != nil {
		return err
	}
	pollKey, err := newID()
	if err != nil {
		return unavailable(err)
	}
	var result error
	for _, pullRequest := range pullRequests {
		workID, found, err := s.linkedTerminalWork(ctx, repository.ID, pullRequest.URL)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !found {
			continue
		}
		needed, err := s.githubPRWakeNeeded(ctx, repository.ID, pullRequest.URL)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if needed {
			request := protocol.ReplaceWorkRequest{
				RequestKey: "github-pr-wake:" + workID, WorkID: workID,
			}
			if _, err := s.ReplaceWork(ctx, request); err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		if err := s.recordGitHubPRWake(ctx, repository.ID, pullRequest.URL, pollKey); err != nil {
			result = errors.Join(result, err)
		}
	}
	if err := s.clearMissingGitHubPRWakes(ctx, repository.ID, pollKey); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (s *Store) linkedTerminalWork(ctx context.Context, repositoryID, pullRequestURL string) (string, bool, error) {
	var sourceKey string
	err := s.db.QueryRowContext(ctx, `
		SELECT source_key FROM sessions
		WHERE repository_id = ? AND source_kind = 'github_issue' AND pull_request_url = ?
		ORDER BY admitted_at DESC, id DESC LIMIT 1
	`, repositoryID, pullRequestURL).Scan(&sourceKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, unavailable(err)
	}
	var workID, state string
	err = s.db.QueryRowContext(ctx, `
		SELECT id, state FROM sessions
		WHERE repository_id = ? AND source_kind = 'github_issue' AND source_key = ?
		ORDER BY admitted_at DESC, id DESC LIMIT 1
	`, repositoryID, sourceKey).Scan(&workID, &state)
	if err != nil {
		return "", false, unavailable(err)
	}
	if state != string(protocol.WorkReady) && state != string(protocol.WorkSucceeded) &&
		state != string(protocol.WorkFailed) && state != string(protocol.WorkNoChange) &&
		state != string(protocol.WorkCancelled) {
		return "", false, nil
	}
	return workID, true, nil
}

func (s *Store) githubPRWakeNeeded(ctx context.Context, repositoryID, pullRequestURL string) (bool, error) {
	var active int
	err := s.db.QueryRowContext(ctx, `
		SELECT active FROM github_pr_wakes WHERE repository_id = ? AND pull_request_url = ?
	`, repositoryID, pullRequestURL).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, unavailable(err)
	}
	return active == 0, nil
}

func (s *Store) recordGitHubPRWake(ctx context.Context, repositoryID, pullRequestURL, pollKey string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO github_pr_wakes(repository_id, pull_request_url, active, last_seen_key)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(repository_id, pull_request_url) DO UPDATE SET
			active = 1, last_seen_key = excluded.last_seen_key
	`, repositoryID, pullRequestURL, pollKey)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) clearMissingGitHubPRWakes(ctx context.Context, repositoryID, pollKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE github_pr_wakes SET active = 0
		WHERE repository_id = ? AND last_seen_key != ?
	`, repositoryID, pollKey)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// githubIssueVisit reports the active visit for an issue. An inactive or
// missing visit means the issue left the needs-agent label set and may start
// new Work on its next match.
func (s *Store) githubIssueVisit(ctx context.Context, repositoryID string, issueNumber int) (string, bool, error) {
	var visitKey string
	var active int
	err := s.db.QueryRowContext(ctx, `
		SELECT visit_key, active FROM github_issue_visits
		WHERE repository_id = ? AND issue_number = ?
	`, repositoryID, issueNumber).Scan(&visitKey, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, unavailable(err)
	}
	return visitKey, active == 1, nil
}

func (s *Store) startGitHubIssueVisit(ctx context.Context, repositoryID string, issueNumber int, pollKey string) (string, error) {
	visitKey, err := newID()
	if err != nil {
		return "", unavailable(err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO github_issue_visits(repository_id, issue_number, visit_key, active, last_seen_key)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(repository_id, issue_number) DO UPDATE SET
			visit_key = excluded.visit_key, active = 1, last_seen_key = excluded.last_seen_key
	`, repositoryID, issueNumber, visitKey, pollKey)
	if err != nil {
		return "", unavailable(err)
	}
	return visitKey, nil
}

func (s *Store) touchGitHubIssueVisit(ctx context.Context, repositoryID string, issueNumber int, pollKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE github_issue_visits SET last_seen_key = ?
		WHERE repository_id = ? AND issue_number = ?
	`, pollKey, repositoryID, issueNumber)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) clearGitHubIssueVisit(ctx context.Context, repositoryID string, issueNumber int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM github_issue_visits WHERE repository_id = ? AND issue_number = ?
	`, repositoryID, issueNumber)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// clearMissingGitHubIssueVisits rearms issues that left the label set.
func (s *Store) clearMissingGitHubIssueVisits(ctx context.Context, repositoryID, pollKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE github_issue_visits SET active = 0
		WHERE repository_id = ? AND last_seen_key != ?
	`, repositoryID, pollKey)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// githubIssueNeedsRebuild reports whether this source already has a terminal
// Work with no successor, which is what a new visit must chain from.
func (s *Store) githubIssueNeedsRebuild(ctx context.Context, repositoryID, sourceKey string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sessions AS candidate
			WHERE candidate.repository_id = ? AND candidate.source_kind = 'github_issue' AND candidate.source_key = ?
			  AND candidate.state IN ('ready', 'succeeded', 'failed', 'no-change', 'cancelled')
			  AND NOT EXISTS (
				SELECT 1 FROM sessions AS child WHERE child.predecessor_work_id = candidate.id
			  )
		)
	`, repositoryID, sourceKey).Scan(&exists)
	if err != nil {
		return false, unavailable(err)
	}
	return exists == 1, nil
}

func (s *Store) issueIntakeRepositories(ctx context.Context) ([]protocol.ManagedRepository, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remote_identity, enabled, issue_intake_enabled, created_at, updated_at
		FROM repositories
		WHERE centrally_managed = 1 AND enabled = 1 AND issue_intake_enabled = 1
		ORDER BY remote_identity
	`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	var repositories []protocol.ManagedRepository
	for rows.Next() {
		repository, err := scanManagedRepository(rows)
		if err != nil {
			return nil, unavailable(err)
		}
		repositories = append(repositories, repository)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return repositories, nil
}
