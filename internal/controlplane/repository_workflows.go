package controlplane

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

const workflowCommentMarker = "<!-- factory-workflow-routing -->"

type githubWorkflowSource interface {
	ListWorkflowFiles(context.Context, string) (string, map[string][]byte, error)
	UpsertWorkflowComment(context.Context, string, int, string) error
}

type githubRepositoryInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type githubGitRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type githubTreeResponse struct {
	Truncated bool              `json:"truncated"`
	Tree      []githubTreeEntry `json:"tree"`
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

type githubBlobResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type githubIssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func githubRepositorySlug(identity string) (string, error) {
	identity = strings.TrimSpace(identity)
	if strings.HasSuffix(strings.ToLower(identity), ".git") {
		return "", fmt.Errorf("repository identity %q must not use a .git suffix", identity)
	}
	parts := strings.Split(identity, "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "github.com") ||
		!validGitHubOwner(parts[1]) || !validGitHubRepository(parts[2]) {
		return "", fmt.Errorf("repository identity %q must use github.com/owner/repository", identity)
	}
	return strings.ToLower(parts[1] + "/" + parts[2]), nil
}

func (c githubCLI) ListWorkflowFiles(ctx context.Context, repository string) (string, map[string][]byte, error) {
	slug, err := githubRepositorySlug(repository)
	if err != nil {
		return "", nil, err
	}
	apiRepository := "repos/" + slug
	var info githubRepositoryInfo
	if err := c.json(ctx, []string{"api", apiRepository}, &info); err != nil {
		return "", nil, fmt.Errorf("read default branch for %s: %w", repository, err)
	}
	var ref githubGitRef
	refPath := apiRepository + "/git/ref/heads/" + url.PathEscape(info.DefaultBranch)
	if err := c.json(ctx, []string{"api", refPath}, &ref); err != nil {
		return "", nil, fmt.Errorf("read default branch commit for %s: %w", repository, err)
	}
	if strings.TrimSpace(ref.Object.SHA) == "" {
		return "", nil, errors.New("GitHub returned an empty default branch commit")
	}
	var tree githubTreeResponse
	treePath := apiRepository + "/git/trees/" + url.PathEscape(ref.Object.SHA) + "?recursive=1"
	if err := c.json(ctx, []string{"api", treePath}, &tree); err != nil {
		return "", nil, fmt.Errorf("read workflow tree for %s: %w", repository, err)
	}
	if tree.Truncated {
		return "", nil, fmt.Errorf("GitHub workflow tree for %s is truncated", repository)
	}
	entries := make([]githubTreeEntry, 0)
	var totalBytes int64
	for _, entry := range tree.Tree {
		if entry.Type != "blob" || !strings.HasPrefix(entry.Path, protocol.RepositoryWorkflowDir) || !strings.HasSuffix(entry.Path, ".md") {
			continue
		}
		entries = append(entries, entry)
		if len(entries) > protocol.MaxRepositoryWorkflowFiles {
			return "", nil, fmt.Errorf("workflow catalog exceeds %d files", protocol.MaxRepositoryWorkflowFiles)
		}
		if entry.Size < 0 || entry.Size > int64(protocol.MaxRepositoryWorkflowBytes) {
			return "", nil, fmt.Errorf("workflow %q exceeds %d bytes", entry.Path, protocol.MaxRepositoryWorkflowBytes)
		}
		totalBytes += entry.Size
		if totalBytes > int64(protocol.MaxRepositoryWorkflowCatalog) {
			return "", nil, fmt.Errorf("workflow catalog exceeds %d bytes", protocol.MaxRepositoryWorkflowCatalog)
		}
	}
	files := make(map[string][]byte)
	for _, entry := range entries {
		var blob githubBlobResponse
		if err := c.json(ctx, []string{"api", apiRepository + "/git/blobs/" + url.PathEscape(entry.SHA)}, &blob); err != nil {
			return "", nil, fmt.Errorf("read workflow %s: %w", entry.Path, err)
		}
		if blob.Encoding != "base64" {
			return "", nil, fmt.Errorf("workflow %s has unsupported GitHub encoding %q", entry.Path, blob.Encoding)
		}
		content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
		if err != nil {
			return "", nil, fmt.Errorf("decode workflow %s: %w", entry.Path, err)
		}
		files[entry.Path] = content
	}
	return ref.Object.SHA, files, nil
}

func (c githubCLI) UpsertWorkflowComment(ctx context.Context, repository string, issueNumber int, body string) error {
	slug, err := githubRepositorySlug(repository)
	if err != nil {
		return err
	}
	var pages [][]githubIssueComment
	path := fmt.Sprintf("repos/%s/issues/%d/comments", slug, issueNumber)
	if err := c.json(ctx, []string{"api", path, "--paginate", "--slurp"}, &pages); err != nil {
		return fmt.Errorf("list issue comments: %w", err)
	}
	for _, page := range pages {
		for _, comment := range page {
			if strings.Contains(comment.Body, workflowCommentMarker) {
				return c.command(ctx, []string{"api", "--method", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", slug, comment.ID), "-f", "body=" + body})
			}
		}
	}
	return c.command(ctx, []string{"api", "--method", "POST", path, "-f", "body=" + body})
}

func (c githubCLI) json(ctx context.Context, args []string, target any) error {
	if c.runJSON != nil {
		return c.runJSON(ctx, args, target)
	}
	return runGHJSON(ctx, args, target)
}

func (c githubCLI) command(ctx context.Context, args []string) error {
	if c.run != nil {
		return c.run(ctx, args)
	}
	return runGH(ctx, args)
}

func runGHJSON(ctx context.Context, args []string, target any) error {
	output, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, target); err != nil {
		return err
	}
	return nil
}

func runGH(ctx context.Context, args []string) error {
	if output, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("gh %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Store) RefreshRepositoryWorkflowCatalog(ctx context.Context, repositoryID string) (protocol.RepositoryWorkflowCatalog, error) {
	var centrallyManaged int
	if err := s.db.QueryRowContext(ctx, `
		SELECT centrally_managed FROM repositories WHERE id = ?
	`, repositoryID).Scan(&centrallyManaged); errors.Is(err, sql.ErrNoRows) {
		return protocol.RepositoryWorkflowCatalog{}, ErrNotFound
	} else if err != nil {
		return protocol.RepositoryWorkflowCatalog{}, unavailable(err)
	}
	if centrallyManaged == 0 {
		catalog, buildErr := protocol.BuildRepositoryWorkflowCatalog(repositoryID, "", "", nil)
		catalog.SyncedAt = s.now()
		if buildErr != nil {
			catalog.Status = protocol.RepositoryWorkflowInvalid
			catalog.Error = buildErr.Error()
		}
		return catalog, s.saveWorkflowCatalog(ctx, catalog, buildErr)
	}
	identity, err := s.managedRepositoryIdentity(ctx, repositoryID)
	if err != nil {
		return protocol.RepositoryWorkflowCatalog{}, err
	}
	source, supportsCatalog := s.githubIssues.(githubWorkflowSource)
	if !supportsCatalog {
		catalog, buildErr := protocol.BuildRepositoryWorkflowCatalog(repositoryID, identity, "", nil)
		if buildErr != nil {
			return catalog, s.saveWorkflowCatalog(ctx, catalog, buildErr)
		}
		return catalog, s.saveWorkflowCatalog(ctx, catalog, nil)
	}
	commitSHA, files, err := source.ListWorkflowFiles(ctx, identity)
	if err != nil {
		catalog := protocol.RepositoryWorkflowCatalog{
			RepositoryID: repositoryID, RepositoryIdentity: identity,
			Status: protocol.RepositoryWorkflowUnavailable, Error: err.Error(),
			SyncedAt: s.now(), Workflows: []protocol.RepositoryWorkflow{},
		}
		return catalog, s.saveWorkflowCatalog(ctx, catalog, err)
	}
	catalog, buildErr := protocol.BuildRepositoryWorkflowCatalog(repositoryID, identity, commitSHA, files)
	catalog.SyncedAt = s.now()
	if buildErr != nil {
		catalog.Status = protocol.RepositoryWorkflowInvalid
		catalog.Error = buildErr.Error()
	}
	return catalog, s.saveWorkflowCatalog(ctx, catalog, buildErr)
}

func (s *Store) managedRepositoryIdentity(ctx context.Context, repositoryID string) (string, error) {
	var identity string
	err := s.db.QueryRowContext(ctx, `
		SELECT remote_identity FROM repositories
		WHERE id = ? AND centrally_managed = 1 AND enabled = 1
	`, repositoryID).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		return "", conflict("repository_not_managed", "repository is not an enabled managed repository")
	}
	if err != nil {
		return "", unavailable(err)
	}
	return identity, nil
}

func (s *Store) saveWorkflowCatalog(ctx context.Context, catalog protocol.RepositoryWorkflowCatalog, syncErr error) error {
	if catalog.SyncedAt.IsZero() {
		catalog.SyncedAt = s.now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repository_workflow_catalogs(repository_id, commit_sha, status, error, synced_at, total_bytes)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository_id) DO UPDATE SET
			commit_sha = excluded.commit_sha, status = excluded.status, error = excluded.error,
			synced_at = excluded.synced_at, total_bytes = excluded.total_bytes
	`, catalog.RepositoryID, catalog.CommitSHA, catalog.Status, catalog.Error, catalog.SyncedAt.UnixMilli(), workflowCatalogBytes(catalog)); err != nil {
		return unavailable(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM repository_workflow_entries WHERE repository_id = ?`, catalog.RepositoryID); err != nil {
		return unavailable(err)
	}
	if catalog.Status == protocol.RepositoryWorkflowHealthy {
		for _, workflow := range catalog.Workflows {
			labelsValue := workflow.LabelsAll
			if labelsValue == nil {
				labelsValue = []string{}
			}
			labels, err := json.Marshal(labelsValue)
			if err != nil {
				return unavailable(err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO repository_workflow_entries(
					repository_id, workflow_id, title, description, path, commit_sha, digest, labels_json, instructions
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, catalog.RepositoryID, workflow.ID, workflow.Title, workflow.Description, workflow.Path,
				workflow.CommitSHA, workflow.Digest, labels, workflow.Instructions); err != nil {
				return unavailable(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return unavailable(err)
	}
	if syncErr != nil {
		return syncErr
	}
	return nil
}

func workflowCatalogBytes(catalog protocol.RepositoryWorkflowCatalog) int {
	total := 0
	for _, workflow := range catalog.Workflows {
		total += len([]byte(workflow.Instructions))
	}
	return total
}

func (s *Store) WorkflowCatalog(ctx context.Context, repositoryID string) (protocol.RepositoryWorkflowCatalog, error) {
	var catalog protocol.RepositoryWorkflowCatalog
	var synced int64
	err := s.db.QueryRowContext(ctx, `
		SELECT repository_id, commit_sha, status, error, synced_at
		FROM repository_workflow_catalogs WHERE repository_id = ?
	`, repositoryID).Scan(&catalog.RepositoryID, &catalog.CommitSHA, &catalog.Status, &catalog.Error, &synced)
	if errors.Is(err, sql.ErrNoRows) {
		return s.RefreshRepositoryWorkflowCatalog(ctx, repositoryID)
	}
	if err != nil {
		return catalog, unavailable(err)
	}
	catalog.SyncedAt = fromMillis(synced)
	if err := s.db.QueryRowContext(ctx, `SELECT remote_identity FROM repositories WHERE id = ?`, repositoryID).Scan(&catalog.RepositoryIdentity); err != nil {
		return catalog, unavailable(err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT workflow_id, title, description, path, commit_sha, digest, labels_json
		FROM repository_workflow_entries WHERE repository_id = ? ORDER BY path, workflow_id
	`, repositoryID)
	if err != nil {
		return catalog, unavailable(err)
	}
	defer rows.Close()
	catalog.Workflows = make([]protocol.RepositoryWorkflow, 0)
	for rows.Next() {
		var workflow protocol.RepositoryWorkflow
		var labelsJSON []byte
		if err := rows.Scan(&workflow.ID, &workflow.Title, &workflow.Description, &workflow.Path, &workflow.CommitSHA, &workflow.Digest, &labelsJSON); err != nil {
			return catalog, unavailable(err)
		}
		if err := json.Unmarshal(labelsJSON, &workflow.LabelsAll); err != nil {
			return catalog, unavailable(err)
		}
		catalog.Workflows = append(catalog.Workflows, workflow)
	}
	if err := rows.Err(); err != nil {
		return catalog, unavailable(err)
	}
	diagnostics, err := s.workflowDiagnostics(ctx, repositoryID)
	if err != nil {
		return catalog, err
	}
	catalog.Diagnostics = diagnostics
	return catalog, nil
}

func (s *Store) workflowDiagnostics(ctx context.Context, repositoryID string) ([]protocol.RepositoryWorkflowDiagnostic, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT issue_number, issue_url, code, message, workflow_ids_json, updated_at
		FROM repository_workflow_diagnostics
		WHERE repository_id = ? ORDER BY issue_number
	`, repositoryID)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	diagnostics := make([]protocol.RepositoryWorkflowDiagnostic, 0)
	for rows.Next() {
		var diagnostic protocol.RepositoryWorkflowDiagnostic
		var workflowIDsJSON []byte
		var updatedAt int64
		if err := rows.Scan(&diagnostic.IssueNumber, &diagnostic.IssueURL, &diagnostic.Code, &diagnostic.Message, &workflowIDsJSON, &updatedAt); err != nil {
			return nil, unavailable(err)
		}
		if err := json.Unmarshal(workflowIDsJSON, &diagnostic.WorkflowIDs); err != nil {
			return nil, unavailable(err)
		}
		diagnostic.UpdatedAt = fromMillis(updatedAt)
		diagnostics = append(diagnostics, diagnostic)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return diagnostics, nil
}

func (s *Store) WorkflowOptions(ctx context.Context, repositoryIDs []string) ([]protocol.RepositoryWorkflowOption, error) {
	if len(repositoryIDs) == 0 {
		return []protocol.RepositoryWorkflowOption{}, nil
	}
	if len(repositoryIDs) > protocol.MaxTaskRepositories {
		return nil, invalid("too_many_task_repositories", "a Task is limited to 100 repositories")
	}
	seenRepositories := make(map[string]struct{}, len(repositoryIDs))
	for _, repositoryID := range repositoryIDs {
		if _, exists := seenRepositories[repositoryID]; exists {
			return nil, invalid("duplicate_task_repository", "each repository may be selected once")
		}
		seenRepositories[repositoryID] = struct{}{}
	}
	counts := make(map[string]protocol.RepositoryWorkflowOption)
	for index, repositoryID := range repositoryIDs {
		catalog, err := s.WorkflowCatalog(ctx, repositoryID)
		if err != nil {
			return nil, err
		}
		if catalog.Status != protocol.RepositoryWorkflowHealthy {
			return nil, conflict("workflow_catalog_unavailable", "every selected repository must have a healthy workflow catalog")
		}
		seen := make(map[string]bool)
		for _, workflow := range catalog.Workflows {
			if index == 0 {
				counts[workflow.ID] = protocol.RepositoryWorkflowOption{ID: workflow.ID, Title: workflow.Title, Description: workflow.Description, RepositoryCount: 1}
				seen[workflow.ID] = true
				continue
			}
			if existing, ok := counts[workflow.ID]; ok {
				existing.RepositoryCount++
				counts[workflow.ID] = existing
				seen[workflow.ID] = true
			}
		}
		if index > 0 {
			for id, option := range counts {
				if option.RepositoryCount != index+1 || !seen[id] {
					delete(counts, id)
				}
			}
		}
	}
	options := make([]protocol.RepositoryWorkflowOption, 0, len(counts))
	for _, option := range counts {
		options = append(options, option)
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	return options, nil
}

func (s *Store) WorkflowSnapshot(ctx context.Context, tx *sql.Tx, repositoryID, workflowID string) (protocol.RepositoryWorkflowSnapshot, error) {
	workflowID, err := protocol.NormalizeRepositoryWorkflowID(workflowID)
	if err != nil {
		return protocol.RepositoryWorkflowSnapshot{}, invalid("invalid_workflow_id", err.Error())
	}
	var workflow protocol.RepositoryWorkflowSnapshot
	err = tx.QueryRowContext(ctx, `
		SELECT workflow_id, title, description, path, commit_sha, digest, instructions
		FROM repository_workflow_entries
		WHERE repository_id = ? AND workflow_id = ?
	`, repositoryID, workflowID).Scan(&workflow.ID, &workflow.Title, &workflow.Description, &workflow.Path, &workflow.CommitSHA, &workflow.Digest, &workflow.Instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow, conflict("workflow_not_found", fmt.Sprintf("workflow %s is not available for this repository", workflowID))
	}
	if err != nil {
		return workflow, unavailable(err)
	}
	return workflow, nil
}

func loadWorkflowSnapshot(ctx context.Context, tx *sql.Tx, repositoryID, workflowID string) (protocol.RepositoryWorkflowSnapshot, error) {
	if workflowID == protocol.RepositoryWorkflowFallback {
		var workflow protocol.RepositoryWorkflowSnapshot
		err := tx.QueryRowContext(ctx, `
			SELECT workflow_id, title, description, path, commit_sha, digest, instructions
			FROM repository_workflow_entries
			WHERE repository_id = ? AND workflow_id = ?
		`, repositoryID, workflowID).Scan(&workflow.ID, &workflow.Title, &workflow.Description, &workflow.Path, &workflow.CommitSHA, &workflow.Digest, &workflow.Instructions)
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.RepositoryWorkflowSnapshot{
				ID: protocol.RepositoryWorkflowFallback, Title: "Implementation",
				Path: protocol.RepositoryWorkflowFallbackPath, Instructions: protocol.StandardBuildProcedurePrompt,
			}, nil
		}
		if err != nil {
			return workflow, unavailable(err)
		}
		return workflow, nil
	}
	return workflowSnapshot(ctx, tx, repositoryID, workflowID)
}

func workflowSnapshot(ctx context.Context, tx *sql.Tx, repositoryID, workflowID string) (protocol.RepositoryWorkflowSnapshot, error) {
	workflowID, err := protocol.NormalizeRepositoryWorkflowID(workflowID)
	if err != nil {
		return protocol.RepositoryWorkflowSnapshot{}, invalid("invalid_workflow_id", err.Error())
	}
	var workflow protocol.RepositoryWorkflowSnapshot
	err = tx.QueryRowContext(ctx, `
		SELECT workflow_id, title, description, path, commit_sha, digest, instructions
		FROM repository_workflow_entries
		WHERE repository_id = ? AND workflow_id = ?
	`, repositoryID, workflowID).Scan(&workflow.ID, &workflow.Title, &workflow.Description, &workflow.Path, &workflow.CommitSHA, &workflow.Digest, &workflow.Instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow, conflict("workflow_not_found", fmt.Sprintf("workflow %s is not available for this repository", workflowID))
	}
	if err != nil {
		return workflow, unavailable(err)
	}
	return workflow, nil
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func workflowRoutingComment(workflow protocol.RepositoryWorkflow, workID string) string {
	return workflowCommentMarker + "\nFactory selected workflow **" + workflow.ID + "** for Work `" + workID + "`.\n"
}

func taskRepositoryIDs(repositories []protocol.TaskRepository) []string {
	ids := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		ids = append(ids, repository.ID)
	}
	return ids
}

func resolveTaskWorkflowPrompt(prompt string, workflow protocol.RepositoryWorkflowSnapshot, selected bool) string {
	if !selected {
		return prompt
	}
	return prompt + "\n\nTrusted repository workflow " + workflow.ID + " (" + workflow.Path + " @ " + workflow.CommitSHA + "):\n\n" + workflow.Instructions
}
