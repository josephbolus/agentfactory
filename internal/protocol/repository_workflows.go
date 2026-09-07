package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	RepositoryWorkflowDir          = ".factory/workflows/"
	RepositoryWorkflowFallback     = "implement"
	RepositoryWorkflowFallbackPath = ".factory/workflows/implement.md"
	RepositoryWorkflowHealthy      = "healthy"
	RepositoryWorkflowInvalid      = "invalid"
	RepositoryWorkflowUnavailable  = "unavailable"
	MaxRepositoryWorkflowFiles     = 100
	MaxRepositoryWorkflowBytes     = 48 << 10
	MaxRepositoryWorkflowCatalog   = 2 << 20
	MaxWorkflowIDBytes             = 100
	MaxWorkflowTitleBytes          = 200
	MaxWorkflowDescriptionBytes    = 500
	MaxWorkflowLabelBytes          = 100
	MaxWorkflowLabels              = 10
)

var workflowIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[a-z0-9-]*[a-z0-9])?(?:/[a-z0-9]+(?:[a-z0-9-]*[a-z0-9])?)*$`)

type workflowFrontmatter struct {
	ID          string               `yaml:"id"`
	Title       string               `yaml:"title"`
	Description string               `yaml:"description"`
	GitHubIssue *workflowGitHubIssue `yaml:"github_issue"`
}

type workflowGitHubIssue struct {
	LabelsAll *[]string `yaml:"labels_all"`
}

type RepositoryWorkflow struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	Path         string   `json:"path"`
	CommitSHA    string   `json:"commit_sha"`
	Digest       string   `json:"digest"`
	LabelsAll    []string `json:"labels_all,omitempty"`
	Instructions string   `json:"-"`
}

type RepositoryWorkflowSnapshot struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Path         string `json:"path"`
	CommitSHA    string `json:"commit_sha"`
	Digest       string `json:"digest"`
	Instructions string `json:"instructions"`
}

type RepositoryWorkflowCatalog struct {
	RepositoryID       string                         `json:"repository_id"`
	RepositoryIdentity string                         `json:"repository_identity"`
	CommitSHA          string                         `json:"commit_sha"`
	Status             string                         `json:"status"`
	Error              string                         `json:"error,omitempty"`
	SyncedAt           time.Time                      `json:"synced_at"`
	Workflows          []RepositoryWorkflow           `json:"workflows"`
	Diagnostics        []RepositoryWorkflowDiagnostic `json:"diagnostics,omitempty"`
}

type RepositoryWorkflowDiagnostic struct {
	IssueNumber int       `json:"issue_number"`
	IssueURL    string    `json:"issue_url"`
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	WorkflowIDs []string  `json:"workflow_ids,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type WorkflowMatchError struct {
	WorkflowIDs []string
}

func (e *WorkflowMatchError) Error() string {
	return fmt.Sprintf("multiple workflows match issue labels: %s", strings.Join(e.WorkflowIDs, ", "))
}

type RepositoryWorkflowOption struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	RepositoryCount int    `json:"repository_count"`
}

type RepositoryWorkflowOptionsRequest struct {
	RepositoryIDs []string `json:"repository_ids"`
}

type RepositoryWorkflowOptionsResponse struct {
	Workflows []RepositoryWorkflowOption `json:"workflows"`
}

func ParseRepositoryWorkflow(filePath, commitSHA string, content []byte) (RepositoryWorkflow, error) {
	workflow := RepositoryWorkflow{Path: filePath, CommitSHA: strings.TrimSpace(commitSHA)}
	if err := validateWorkflowPath(filePath); err != nil {
		return workflow, err
	}
	if len(content) > MaxRepositoryWorkflowBytes {
		return workflow, fmt.Errorf("workflow %q exceeds %d bytes", filePath, MaxRepositoryWorkflowBytes)
	}
	workflow.Digest = digestBytes(content)
	frontmatter, instructions, hasFrontmatter, err := splitWorkflowFrontmatter(content)
	if err != nil {
		return workflow, fmt.Errorf("workflow %q: %w", filePath, err)
	}
	if !hasFrontmatter {
		if filePath != RepositoryWorkflowFallbackPath {
			return workflow, errors.New("workflow requires YAML frontmatter")
		}
		workflow.ID = RepositoryWorkflowFallback
		workflow.Title = "Implementation"
		workflow.Instructions = strings.TrimSpace(string(content))
		if workflow.Instructions == "" {
			return workflow, errors.New("workflow instructions are empty")
		}
		return workflow, nil
	}
	var metadata workflowFrontmatter
	decoder := yaml.NewDecoder(bytes.NewReader(frontmatter))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		return workflow, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	if strings.TrimSpace(metadata.ID) == "" {
		return workflow, errors.New("workflow id is required")
	}
	workflowID, err := NormalizeRepositoryWorkflowID(metadata.ID)
	if err != nil {
		return workflow, fmt.Errorf("workflow id %q is invalid", metadata.ID)
	}
	metadata.Title = strings.TrimSpace(metadata.Title)
	if metadata.Title == "" || len([]byte(metadata.Title)) > MaxWorkflowTitleBytes {
		return workflow, fmt.Errorf("workflow title is required and limited to %d bytes", MaxWorkflowTitleBytes)
	}
	metadata.Description = strings.TrimSpace(metadata.Description)
	if len([]byte(metadata.Description)) > MaxWorkflowDescriptionBytes {
		return workflow, fmt.Errorf("workflow description is limited to %d bytes", MaxWorkflowDescriptionBytes)
	}
	var labels []string
	if metadata.GitHubIssue != nil && metadata.GitHubIssue.LabelsAll != nil {
		if len(*metadata.GitHubIssue.LabelsAll) == 0 {
			return workflow, errors.New("github_issue.labels_all must contain at least one label")
		}
		labels, err = normalizeWorkflowLabels(*metadata.GitHubIssue.LabelsAll)
		if err != nil {
			return workflow, err
		}
	}
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return workflow, errors.New("workflow instructions are empty")
	}
	workflow.ID, workflow.Title, workflow.Description = workflowID, metadata.Title, metadata.Description
	workflow.LabelsAll, workflow.Instructions = labels, instructions
	return workflow, nil
}

func NormalizeRepositoryWorkflowID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len([]byte(value)) > MaxWorkflowIDBytes || !workflowIDPattern.MatchString(value) {
		return "", fmt.Errorf("workflow id %q is invalid", value)
	}
	return value, nil
}

func BuildRepositoryWorkflowCatalog(repositoryID, repositoryIdentity, commitSHA string, files map[string][]byte) (RepositoryWorkflowCatalog, error) {
	catalog := RepositoryWorkflowCatalog{
		RepositoryID: repositoryID, RepositoryIdentity: repositoryIdentity,
		CommitSHA: strings.TrimSpace(commitSHA), Status: RepositoryWorkflowHealthy,
		Workflows: make([]RepositoryWorkflow, 0),
	}
	paths := make([]string, 0, len(files))
	for filePath := range files {
		if strings.HasSuffix(filePath, ".md") && strings.HasPrefix(filePath, RepositoryWorkflowDir) {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	if len(paths) > MaxRepositoryWorkflowFiles {
		return catalog, fmt.Errorf("workflow catalog exceeds %d files", MaxRepositoryWorkflowFiles)
	}
	total := 0
	seenIDs := make(map[string]string, len(paths))
	for _, filePath := range paths {
		content := files[filePath]
		total += len(content)
		if total > MaxRepositoryWorkflowCatalog {
			return catalog, fmt.Errorf("workflow catalog exceeds %d bytes", MaxRepositoryWorkflowCatalog)
		}
		workflow, err := ParseRepositoryWorkflow(filePath, commitSHA, content)
		if err != nil {
			return catalog, err
		}
		if previous, exists := seenIDs[workflow.ID]; exists {
			return catalog, fmt.Errorf("workflow id %q is duplicated by %q and %q", workflow.ID, previous, filePath)
		}
		seenIDs[workflow.ID] = filePath
		catalog.Workflows = append(catalog.Workflows, workflow)
	}
	if _, exists := seenIDs[RepositoryWorkflowFallback]; !exists {
		catalog.Workflows = append(catalog.Workflows, RepositoryWorkflow{
			ID: RepositoryWorkflowFallback, Title: "Implementation", Path: RepositoryWorkflowFallbackPath,
			CommitSHA: catalog.CommitSHA, Digest: digestBytes([]byte(StandardBuildProcedurePrompt)),
			Instructions: StandardBuildProcedurePrompt,
		})
	}
	return catalog, nil
}

func (catalog RepositoryWorkflowCatalog) MatchIssueLabels(labels []string) (RepositoryWorkflow, error) {
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[strings.ToLower(strings.TrimSpace(label))] = struct{}{}
	}
	var matches []RepositoryWorkflow
	for _, workflow := range catalog.Workflows {
		if len(workflow.LabelsAll) == 0 || workflow.ID == RepositoryWorkflowFallback {
			continue
		}
		matched := true
		for _, required := range workflow.LabelsAll {
			if _, exists := set[strings.ToLower(required)]; !exists {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, workflow)
		}
	}
	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for index := range matches {
			ids[index] = matches[index].ID
		}
		return RepositoryWorkflow{}, &WorkflowMatchError{WorkflowIDs: ids}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	for _, workflow := range catalog.Workflows {
		if workflow.ID == RepositoryWorkflowFallback {
			return workflow, nil
		}
	}
	return RepositoryWorkflow{}, errors.New("implementation workflow is unavailable")
}

func (workflow RepositoryWorkflow) Snapshot() RepositoryWorkflowSnapshot {
	return RepositoryWorkflowSnapshot{
		ID: workflow.ID, Title: workflow.Title, Description: workflow.Description,
		Path: workflow.Path, CommitSHA: workflow.CommitSHA, Digest: workflow.Digest,
		Instructions: workflow.Instructions,
	}
}

func splitWorkflowFrontmatter(content []byte) ([]byte, string, bool, error) {
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || strings.TrimSuffix(lines[0], "\r") != "---" {
		return nil, string(content), false, nil
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSuffix(lines[index], "\r") != "---" {
			continue
		}
		return []byte(strings.Join(lines[1:index], "\n")), strings.Join(lines[index+1:], "\n"), true, nil
	}
	return nil, "", false, errors.New("frontmatter closing delimiter is missing")
}

func validateWorkflowPath(filePath string) error {
	if !strings.HasPrefix(filePath, RepositoryWorkflowDir) || !strings.HasSuffix(filePath, ".md") || path.Clean(filePath) != filePath {
		return fmt.Errorf("workflow path %q is invalid", filePath)
	}
	return nil
}

func normalizeWorkflowLabels(labels []string) ([]string, error) {
	if len(labels) > MaxWorkflowLabels {
		return nil, fmt.Errorf("workflow may declare at most %d labels", MaxWorkflowLabels)
	}
	seen := make(map[string]struct{}, len(labels))
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label == "" || len([]byte(label)) > MaxWorkflowLabelBytes {
			return nil, fmt.Errorf("workflow labels are required and limited to %d bytes", MaxWorkflowLabelBytes)
		}
		if _, exists := seen[label]; exists {
			return nil, fmt.Errorf("workflow label %q is duplicated", label)
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	return result, nil
}

func digestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
