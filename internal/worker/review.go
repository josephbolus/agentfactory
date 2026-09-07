package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jbolus-owens/factory/internal/protocol"
)

const (
	reviewApproved            = "approved"
	reviewFindings            = "findings"
	reviewApprovedLine        = "Verdict: approved"
	reviewFindingsLine        = "Verdict: findings"
	reviewFindingPrefix       = "## Finding: "
	reviewFindingLocationLine = "Location: "
	maxReviewPasses           = 3
)

type reviewFinding struct {
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Location string `json:"location,omitempty"`
}

type reviewResult struct {
	Verdict  string          `json:"verdict"`
	Findings []reviewFinding `json:"findings"`
	profile  ProfileConfig
}

func reviewArguments(profile ProfileConfig) ([]string, error) {
	return structuredOutputArguments(profile)
}

func repairArguments(profile ProfileConfig) ([]string, error) {
	if profile.Model == "" {
		return runtimeArguments(profile.Adapter, "", "", "")
	}
	profile.ReasoningEffort = "high"
	return runtimeArguments(profile.Adapter, profile.Provider, profile.Model, profile.ReasoningEffort)
}

func reviewPrompt(base string) string {
	return fmt.Sprintf(`Review only the changes from %q. Use git diff against that base branch. Do not edit files.
Factory-owned .factory/plans/*.md files are required audit artifacts; do not report them as findings.
Return Markdown only. The first line must be exactly %q or %q.
For findings, use one %q heading per finding, followed by its detail and an optional %q line.`,
		base, reviewApprovedLine, reviewFindingsLine, reviewFindingPrefix+"<title>", reviewFindingLocationLine+"path:line")
}

func parseReviewResult(value string) (reviewResult, error) {
	lines := strings.Split(unwrapReviewMarkdown(value), "\n")
	if len(lines) == 0 {
		return reviewResult{}, errors.New("review result is empty")
	}
	if strings.TrimSpace(lines[0]) == reviewApprovedLine {
		return reviewResult{Verdict: reviewApproved}, nil
	}
	if strings.TrimSpace(lines[0]) != reviewFindingsLine {
		return reviewResult{}, errors.New("review result has an invalid verdict")
	}
	findings, err := parseReviewFindings(lines[1:])
	if err != nil {
		return reviewResult{}, err
	}
	return reviewResult{Verdict: reviewFindings, Findings: findings}, nil
}

func unwrapReviewMarkdown(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "```") || !strings.HasSuffix(value, "```") {
		return value
	}
	_, body, found := strings.Cut(value, "\n")
	if !found {
		return value
	}
	return strings.TrimSpace(strings.TrimSuffix(body, "```"))
}

func parseReviewFindings(lines []string) ([]reviewFinding, error) {
	findings := make([]reviewFinding, 0)
	var current *reviewFinding
	detail := make([]string, 0)
	flush := func() error {
		if current == nil {
			return nil
		}
		current.Detail = strings.TrimSpace(strings.Join(detail, "\n"))
		if current.Title == "" || current.Detail == "" {
			return errors.New("review finding requires title and detail")
		}
		findings = append(findings, *current)
		current = nil
		detail = nil
		return nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, reviewFindingPrefix) {
			if err := flush(); err != nil {
				return nil, err
			}
			current = &reviewFinding{Title: strings.TrimSpace(strings.TrimPrefix(line, reviewFindingPrefix))}
			continue
		}
		if current == nil {
			continue
		}
		if strings.HasPrefix(line, reviewFindingLocationLine) {
			current.Location = strings.TrimSpace(strings.TrimPrefix(line, reviewFindingLocationLine))
			continue
		}
		detail = append(detail, line)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(findings) == 0 {
		return nil, errors.New("findings review result has no findings")
	}
	return findings, nil
}

func (manager *Manager) review(ctx context.Context, value worktree) (reviewResult, error) {
	profile, err := manager.config.profile(reviewerRole)
	if err != nil {
		return reviewResult{}, err
	}
	return manager.reviewWithProfile(ctx, value, profile)
}

func (manager *Manager) reviewWithProfile(ctx context.Context, value worktree, profile ProfileConfig) (reviewResult, error) {
	arguments, err := reviewArguments(profile)
	if err != nil {
		return reviewResult{}, err
	}
	resultPath := filepath.Join(manager.dataDirectory, "reviews", value.Branch+".json")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		return reviewResult{}, fmt.Errorf("create review directory: %w", err)
	}
	defer os.Remove(resultPath)
	if profile.Adapter == protocol.RuntimeCodex {
		arguments = append(arguments, "--output-last-message", resultPath, "-")
	}
	output, err := runRuntimeCommand(ctx, manager.runtimeExecutable(profile.Adapter), value.Path, arguments, reviewPrompt(value.BaseBranch))
	if err != nil {
		return reviewResult{}, fmt.Errorf("run reviewer: %w: %s", err, boundedText(string(output), protocol.MaxErrorBytes))
	}
	result := string(output)
	if profile.Adapter == protocol.RuntimeCodex {
		body, readErr := os.ReadFile(resultPath)
		if readErr != nil {
			return reviewResult{}, fmt.Errorf("read Codex review result: %w", readErr)
		}
		result = string(body)
	} else if profile.Adapter == protocol.RuntimeClaudeCode {
		result, err = claudeReviewResult(output)
		if err != nil {
			return reviewResult{}, err
		}
	}
	review, err := parseReviewResult(result)
	if err != nil {
		return reviewResult{}, err
	}
	review.profile = profile
	return review, nil
}

func (manager *Manager) repair(ctx context.Context, claim protocol.Claim, repository Repository, value worktree, findings []reviewFinding) error {
	profile, err := manager.executorProfile(claim.Execution.RequiredRuntime)
	if err != nil {
		return err
	}
	before, err := manager.head(ctx, value)
	if err != nil {
		return err
	}
	arguments, err := repairArguments(profile)
	if err != nil {
		return err
	}
	prompt, err := repairPrompt(claim.Session.Target.PublishBranch, findings)
	if err != nil {
		return err
	}
	output, err := runRuntimeCommand(ctx, manager.runtimeExecutable(profile.Adapter), value.Path, arguments, prompt)
	if err != nil {
		return fmt.Errorf("repair review findings: %w: %s", err, boundedText(string(output), protocol.MaxErrorBytes))
	}
	return manager.verifyRepair(ctx, claim, repository, value, before)
}

func (manager *Manager) reviewWork(ctx context.Context, claim protocol.Claim, repository Repository, value worktree) ([]reviewResult, bool, error) {
	reports := make([]reviewResult, 0, maxReviewPasses)
	repaired := false
	for pass := range maxReviewPasses {
		report, err := manager.reviewPass(ctx, value, pass)
		if err != nil {
			return reports, repaired, err
		}
		reports = append(reports, report)
		if report.Verdict == reviewApproved || pass == maxReviewPasses-1 {
			return reports, repaired, nil
		}
		if err := manager.repair(ctx, claim, repository, value, report.Findings); err != nil {
			return reports, true, err
		}
		repaired = true
	}
	return reports, repaired, nil
}

func (manager *Manager) reviewPass(ctx context.Context, value worktree, pass int) (reviewResult, error) {
	if pass == 0 {
		return manager.review(ctx, value)
	}
	profile, err := manager.config.profile(reviewerRole)
	if err != nil {
		return reviewResult{}, err
	}
	profile.ReasoningEffort = "high"
	return manager.reviewWithProfile(ctx, value, profile)
}

func (manager *Manager) verifyRepair(ctx context.Context, claim protocol.Claim, repository Repository, value worktree, before string) error {
	stdout, stderr, err := runGitCommand(ctx, manager.options.GitExecutable, value.Path, 64<<10,
		"status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return commandFailure("inspect repaired worktree", stdout, stderr, err)
	}
	if len(stdout) != 0 {
		return errors.New("review repair must commit every changed and untracked file")
	}
	local, err := manager.head(ctx, value)
	if err != nil {
		return err
	}
	if local == before {
		return errors.New("review repair must create a new commit")
	}
	// Factory owns publication to the immutable branch after the repair commits.
	stdout, stderr, err = runGitCommand(ctx, manager.options.GitExecutable, value.Path, 64<<10,
		"push", "origin", "HEAD:refs/heads/"+claim.Session.Target.PublishBranch)
	if err != nil {
		return commandFailure("push repaired worktree", stdout, stderr, err)
	}
	remote, err := remotePublishCommit(ctx, manager.options.GitExecutable, repository, claim.Session.Target.PublishBranch)
	if err != nil {
		return err
	}
	if local != remote {
		return errors.New("review repair must push the committed HEAD to the Factory publish branch")
	}
	return nil
}

func (manager *Manager) head(ctx context.Context, value worktree) (string, error) {
	stdout, stderr, err := runGitCommand(ctx, manager.options.GitExecutable, value.Path, 64<<10, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", commandFailure("resolve repaired HEAD", stdout, stderr, err)
	}
	return strings.TrimSpace(string(stdout)), nil
}

func repairPrompt(publishBranch string, findings []reviewFinding) (string, error) {
	body, err := json.Marshal(findings)
	if err != nil {
		return "", fmt.Errorf("encode review findings: %w", err)
	}
	return fmt.Sprintf("Fix these independent-review findings. Run affected checks, preserve unrelated changes, commit every change, and update the existing pull request. Factory will publish your committed HEAD to %q; do not create or push another branch.\n\n%s", publishBranch, body), nil
}

func runRuntimeCommand(ctx context.Context, executable, directory string, arguments []string, prompt string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	command.Stdin = strings.NewReader(prompt)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, commandFailure("run coding agent", output, nil, err)
	}
	return output, nil
}

func claudeReviewResult(output []byte) (string, error) {
	type event struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	result := ""
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("decode Claude review response: %w", err)
		}
		events := []event{}
		if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
			if err := json.Unmarshal(value, &events); err != nil {
				return "", fmt.Errorf("decode Claude review response: %w", err)
			}
		} else {
			var item event
			if err := json.Unmarshal(value, &item); err != nil {
				return "", fmt.Errorf("decode Claude review response: %w", err)
			}
			events = append(events, item)
		}
		for _, item := range events {
			if (item.Type == "" || item.Type == "result") && strings.TrimSpace(item.Result) != "" {
				result = item.Result
			}
		}
	}
	if result == "" {
		return "", errors.New("Claude reviewer returned no result")
	}
	return result, nil
}
