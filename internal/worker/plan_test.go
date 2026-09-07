package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestPlanWorkPersistsAndCommitsIssuePlan(t *testing.T) {
	directory := t.TempDir()
	runTestCommand(t, directory, "git", "init")
	runTestCommand(t, directory, "git", "config", "user.email", "factory@example.test")
	runTestCommand(t, directory, "git", "config", "user.name", "Factory")
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, directory, "git", "add", "README.md")
	runTestCommand(t, directory, "git", "commit", "-m", "Seed")
	runtime := filepath.Join(directory, "pi")
	plan := "# Plan\n\n- Implement the change.\n\n# Tests\n\n- go test"
	if err := os.WriteFile(runtime, []byte("#!/bin/sh\nprintf '"+plan+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		config: Config{
			Profiles: []ProfileConfig{{Name: "planner", Adapter: protocol.RuntimePi, Provider: "openrouter", Model: "kimi", ReasoningEffort: "medium"}},
			Roles:    RolesConfig{Planner: "planner"},
		},
		options:       Options{GitExecutable: "git", RuntimeExecutables: map[string]string{protocol.RuntimePi: runtime}},
		dataDirectory: directory,
	}
	claim := protocol.Claim{Session: protocol.ClaimedSession{ID: "work-1", Prompt: "Plan it", Target: protocol.WorkTarget{SourceKind: "github_issue", SourceReference: "https://github.com/acme/api/issues/42"}}}
	path, err := manager.planWork(context.Background(), claim, worktree{Path: directory})
	if err != nil {
		t.Fatal(err)
	}
	if path != ".factory/plans/issue-42.md" {
		t.Fatalf("plan path = %q", path)
	}
	content, err := os.ReadFile(filepath.Join(directory, path))
	if err != nil || string(content) != plan+"\n" {
		t.Fatalf("plan = %q, %v", content, err)
	}
	stdout, _, err := runCommand(context.Background(), "git", directory, 1024, "log", "-1", "--format=%s")
	if err != nil || strings.TrimSpace(string(stdout)) != planCommitMessage {
		t.Fatalf("plan commit = %q, %v", stdout, err)
	}
}

func TestPlanPathUsesWorkIDOutsideGitHub(t *testing.T) {
	path, err := planPath(protocol.Claim{Session: protocol.ClaimedSession{ID: "work-1"}})
	if err != nil || path != ".factory/plans/work-work-1.md" {
		t.Fatalf("plan path = %q, %v", path, err)
	}
}

func TestValidatePlanRejectsBlank(t *testing.T) {
	err := validatePlan([]byte(" \n\t"))
	if err == nil {
		t.Fatal("validatePlan accepted a blank plan")
	}
}

func TestValidatePlanRejectsOversize(t *testing.T) {
	err := validatePlan([]byte(strings.Repeat("x", maxPlanBytes+1)))
	if err == nil {
		t.Fatal("validatePlan accepted an oversized plan")
	}
}

func TestValidatePlanAcceptsMarkdown(t *testing.T) {
	plan := "# Plan\n\n- Implement the change.\n\n# Tests\n\n- go test"
	if err := validatePlan([]byte(plan)); err != nil {
		t.Fatal(err)
	}
}

func TestPlanArgumentsUseClaudeStreamJSON(t *testing.T) {
	arguments, err := planArguments(ProfileConfig{Adapter: protocol.RuntimeClaudeCode, Provider: "anthropic", Model: "opus", ReasoningEffort: "medium"})
	if err != nil || !containsArguments(arguments, []string{"--output-format", "stream-json"}) {
		t.Fatalf("arguments = %#v, %v", arguments, err)
	}
}

func TestClaudePlanResultValidates(t *testing.T) {
	result, err := claudeReviewResult([]byte(`{"result":"# Plan\n\n- Implement the change.\n\n# Tests\n\n- go test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlan([]byte(result)); err != nil {
		t.Fatalf("validatePlan = %v", err)
	}
}
