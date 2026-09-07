package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

const (
	planCommitMessage = "Add Factory work plan"
	maxPlanBytes      = 64 << 10
)

func (manager *Manager) planWork(ctx context.Context, claim protocol.Claim, value worktree) (string, error) {
	if len(manager.config.Profiles) == 0 || manager.config.Roles.Planner == "" {
		return "", nil
	}
	profile, err := manager.config.profile(plannerRole)
	if err != nil {
		return "", err
	}
	arguments, err := planArguments(profile)
	if err != nil {
		return "", err
	}
	resultPath := filepath.Join(manager.dataDirectory, "plans", claim.Session.ID+".md")
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o700); err != nil {
		return "", fmt.Errorf("create planner result directory: %w", err)
	}
	if profile.Adapter == protocol.RuntimeCodex {
		arguments = append(arguments, "--output-last-message", resultPath, "-")
	}
	output, err := runRuntimeCommand(ctx, manager.runtimeExecutable(profile.Adapter), value.Path, arguments, planPrompt(claim))
	if err != nil {
		return "", fmt.Errorf("run planner: %w", err)
	}
	plan := output
	if profile.Adapter == protocol.RuntimeCodex {
		plan, err = os.ReadFile(resultPath)
		if err != nil {
			return "", fmt.Errorf("read planner result: %w", err)
		}
	} else if profile.Adapter == protocol.RuntimeClaudeCode {
		text, decodeErr := claudeReviewResult(output)
		if decodeErr != nil {
			return "", decodeErr
		}
		plan = []byte(text)
	}
	if err := validatePlan(plan); err != nil {
		return "", err
	}
	path, err := planPath(claim)
	if err != nil {
		return "", err
	}
	absolute := filepath.Join(value.Path, path)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return "", fmt.Errorf("create plan directory: %w", err)
	}
	if err := os.WriteFile(absolute, append(plan, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write plan artifact: %w", err)
	}
	stdout, stderr, err := runCommand(ctx, manager.options.GitExecutable, value.Path, 64<<10, "add", "--", path)
	if err != nil {
		return "", commandFailure("stage plan artifact", stdout, stderr, err)
	}
	stdout, stderr, err = runCommand(ctx, manager.options.GitExecutable, value.Path, 64<<10, "commit", "-m", planCommitMessage)
	if err != nil {
		return "", commandFailure("commit plan artifact", stdout, stderr, err)
	}
	return path, nil
}

func planPath(claim protocol.Claim) (string, error) {
	if claim.Session.Target.SourceKind == "github_issue" {
		number, err := issueNumber(claim.Session.Target.SourceReference)
		if err != nil {
			return "", err
		}
		return filepath.ToSlash(filepath.Join(".factory", "plans", fmt.Sprintf("issue-%d.md", number))), nil
	}
	return filepath.ToSlash(filepath.Join(".factory", "plans", "work-"+claim.Session.ID+".md")), nil
}

func planPrompt(claim protocol.Claim) string {
	return "Plan this Factory Work before implementation. Return Markdown with a concise summary, implementation steps, and tests.\n\n" + claim.Session.Prompt
}

func planArguments(profile ProfileConfig) ([]string, error) {
	return readOnlyArguments(profile)
}

func validatePlan(value []byte) error {
	if len(value) > maxPlanBytes {
		return errors.New("planner result exceeds 64 KiB")
	}
	if strings.TrimSpace(string(value)) == "" {
		return errors.New("planner result is blank")
	}
	return nil
}
