package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbolus-owens/factory/internal/protocol"
)

func TestReviewArgumentsPinModel(t *testing.T) {
	arguments, err := reviewArguments(ProfileConfig{Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, " ")
	for _, want := range []string{"exec", "--json", "--model gpt-5.6-sol"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("arguments %q omit %q", joined, want)
		}
	}
	if strings.Contains(joined, "review") || strings.Contains(joined, "--base") {
		t.Fatalf("arguments %q use incompatible native review flags", joined)
	}
}

func TestParseReviewResult(t *testing.T) {
	result, err := parseReviewResult("Verdict: approved\n\nNo findings.")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != reviewApproved || len(result.Findings) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseReviewResultUnwrapsMarkdownFence(t *testing.T) {
	result, err := parseReviewResult("```markdown\nVerdict: approved\n```")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != reviewApproved {
		t.Fatalf("result = %#v", result)
	}
}

func TestParseReviewResultFindings(t *testing.T) {
	result, err := parseReviewResult("Verdict: findings\n\n## Finding: Missing coverage\nAdd a focused regression test.\nLocation: test/store.test.js:12")
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != reviewFindings || len(result.Findings) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if finding := result.Findings[0]; finding.Title != "Missing coverage" || finding.Detail != "Add a focused regression test." || finding.Location != "test/store.test.js:12" {
		t.Fatalf("finding = %#v", finding)
	}
}

func TestParseReviewResultRejectsInvalidVerdict(t *testing.T) {
	if _, err := parseReviewResult("Verdict: maybe"); err == nil {
		t.Fatal("parseReviewResult accepted an invalid verdict")
	}
}

func TestReviewPromptUsesMarkdown(t *testing.T) {
	prompt := reviewPrompt("main")
	for _, want := range []string{"Verdict: approved", "## Finding:", "Markdown only", ".factory/plans/"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt %q omits %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "Return JSON") {
		t.Fatalf("prompt %q requires JSON", prompt)
	}
}

func TestClaudeReviewResultReadsEventArray(t *testing.T) {
	output := []byte(`[
		{"type":"system","subtype":"init"},
		{"type":"assistant","message":{"content":[{"type":"text","text":"ignored"}]}},
		{"type":"result","result":"Verdict: approved"}
	]`)
	result, err := claudeReviewResult(output)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Verdict: approved" {
		t.Fatalf("result = %q", result)
	}
}

func TestClaudeReviewResultReadsEventStream(t *testing.T) {
	output := []byte("{\"type\":\"system\",\"subtype\":\"init\"}\n" +
		"{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ignored\"}]}}\n" +
		"{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Verdict: approved\"}\n")
	result, err := claudeReviewResult(output)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Verdict: approved" {
		t.Fatalf("result = %q", result)
	}
}

func TestReviewCommentBodyReportsCleanReview(t *testing.T) {
	config := Config{Profiles: []ProfileConfig{{Name: "reviewer", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "medium"}}, Roles: RolesConfig{Reviewer: "reviewer"}}
	body := workCommentBody(config, "attempt-1", "Review: completed.", []reviewResult{{Verdict: reviewApproved}}, false)
	for _, want := range []string{reviewCommentMarker("attempt-1"), "No findings.", "APPROVED", "gpt-5.6-sol"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment %q omits %q", body, want)
		}
	}
}

func TestWorkCommentBodyReportsRoles(t *testing.T) {
	config := Config{
		Name: "local-worker",
		Profiles: []ProfileConfig{
			{Name: "planner-opus", Adapter: protocol.RuntimeClaudeCode, Provider: "anthropic", Model: "opus", ReasoningEffort: "medium"},
			{Name: "executor-terra", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-terra", ReasoningEffort: "medium"},
			{Name: "reviewer-sol", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "medium"},
		},
		Roles: RolesConfig{Planner: "planner-opus", Executor: "executor-terra", Reviewer: "reviewer-sol"},
	}
	body := workCommentBody(config, "attempt-1", "Planning: running.", nil, false)
	for _, want := range []string{"local-worker", "planner-opus", "gpt-5.6-terra", "gpt-5.6-sol", "Planning: running."} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment %q omits %q", body, want)
		}
	}
}

func TestWorkCommentBodyReportsEscalatedReviewer(t *testing.T) {
	config := reviewConfig(protocol.RuntimeCodex, "gpt-5.6-sol")
	report := reviewResult{Verdict: reviewApproved, profile: ProfileConfig{Name: "reviewer", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "high"}}
	body := workCommentBody(config, "attempt-1", "Review: completed.", []reviewResult{report}, true)
	if !strings.Contains(body, "Effective reviewer: `reviewer` (`codex` / `openai` / `gpt-5.6-sol` / `high`)") {
		t.Fatalf("comment %q omits the escalated reviewer", body)
	}
}

func TestCommentReviewFailureKeepsFindings(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "gh.log")
	gh := filepath.Join(directory, "gh")
	body := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = \"api user\" ]; then printf '{\\\"login\\\":\\\"factory-bot\\\"}'; exit 0; fi\n" +
		"if printf '%s ' \"$@\" | grep -q -- '--slurp'; then printf '[[]]'; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" >> " + logPath + "\n"
	if err := os.WriteFile(gh, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{config: reviewConfig(protocol.RuntimeCodex, "gpt-5.6-sol"), options: Options{GitHubExecutable: gh}}
	claim := protocol.Claim{Attempt: protocol.Attempt{ID: "attempt-1"}, Repository: protocol.Repository{RemoteIdentity: "github.com/acme/api"}}
	claim.Session.Target = protocol.WorkTarget{SourceKind: "github_issue", SourceReference: "https://github.com/acme/api/issues/42"}
	reports := []reviewResult{{Verdict: reviewFindings, Findings: []reviewFinding{{Title: "Bug", Detail: "Fix it"}}}}
	if err := manager.commentReviewFailure(context.Background(), claim, worktree{Path: directory}, reports, true, errors.New("repair failed")); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bug", "Fix it", "repair failed", "FAILED"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("gh call = %q; want %q", log, want)
		}
	}
}

func TestRepairArgumentsUseDefaultForLegacyExecutor(t *testing.T) {
	arguments, err := repairArguments(ProfileConfig{Adapter: protocol.RuntimeCodex})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(arguments, " "), "--model") {
		t.Fatalf("legacy repair arguments = %q", arguments)
	}
}

func TestRepairPromptUsesFactoryBranch(t *testing.T) {
	prompt, err := repairPrompt("factory/work-42", []reviewFinding{{Title: "Bug", Detail: "Fix it"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "factory/work-42") {
		t.Fatalf("prompt %q omits the Factory publish branch", prompt)
	}
}

func TestExecutionStageIsTerminal(t *testing.T) {
	for _, test := range []struct {
		state string
		want  string
	}{
		{state: "failed", want: "Implementation: failed.\n\nReview: skipped.\n\nFinal verdict: **FAILED**"},
		{state: "succeeded", want: "Implementation: completed.\n\nReview: skipped.\n\nFinal verdict: **SUCCEEDED**"},
	} {
		if got := executionStage("Planning: completed.", test.state, "supervisor failed"); !strings.Contains(got, test.want) || !strings.Contains(got, "supervisor failed") {
			t.Fatalf("execution stage = %q; want %q", got, test.want)
		}
	}
}

func TestExecutionStageKeepsSkippedPlanAndCancellation(t *testing.T) {
	stage := executionStage("Planning: skipped.", "cancelled", "runtime stopped")
	for _, want := range []string{"Planning: skipped.", "Implementation: cancelled.", "Final verdict: **CANCELLED**", "runtime stopped"} {
		if !strings.Contains(stage, want) {
			t.Fatalf("execution stage = %q; want %q", stage, want)
		}
	}
}

func TestReviewProgressKeepsSkippedPlan(t *testing.T) {
	stage := reviewProgressStage("Planning: skipped.")
	if !strings.Contains(stage, "Planning: skipped.\n\nImplementation: completed.\n\nReview: running.") {
		t.Fatalf("review progress = %q", stage)
	}
}

func TestIssueNumber(t *testing.T) {
	value, err := issueNumber("https://github.com/acme/api/issues/42")
	if err != nil || value != 42 {
		t.Fatalf("issue number = %d, %v", value, err)
	}
}

func TestCommentReviewCreatesIssueComment(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "gh.log")
	gh := filepath.Join(directory, "gh")
	body := "#!/bin/sh\n" +
		"if [ \"$1 $2\" = \"api user\" ]; then printf '{\\\"login\\\":\\\"factory-bot\\\"}'; exit 0; fi\n" +
		"if printf '%s ' \"$@\" | grep -q -- '--slurp'; then printf '[[]]'; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" >> " + logPath + "\n"
	if err := os.WriteFile(gh, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{config: reviewConfig(protocol.RuntimeCodex, "gpt-5.6-sol"), options: Options{GitHubExecutable: gh}}
	claim := protocol.Claim{Attempt: protocol.Attempt{ID: "attempt-1"}, Repository: protocol.Repository{RemoteIdentity: "github.com/acme/api"}}
	claim.Session.Target = protocol.WorkTarget{SourceKind: "github_issue", SourceReference: "https://github.com/acme/api/issues/42"}
	if err := manager.commentReview(context.Background(), claim, worktree{Path: directory}, []reviewResult{{Verdict: reviewApproved}}, false); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "POST\nrepos/acme/api/issues/42/comments") || !strings.Contains(string(log), "APPROVED") {
		t.Fatalf("gh call = %q", log)
	}
}

func TestReviewWorkRepairsAndReviewsAgain(t *testing.T) {
	directory := t.TempDir()
	scratch := t.TempDir()
	remote := filepath.Join(scratch, "remote.git")
	runTestCommand(t, directory, "git", "init")
	runTestCommand(t, directory, "git", "config", "user.email", "factory@example.test")
	runTestCommand(t, directory, "git", "config", "user.name", "Factory")
	if err := os.WriteFile(filepath.Join(directory, "seed"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, directory, "git", "add", "seed")
	runTestCommand(t, directory, "git", "commit", "-m", "Seed")
	runTestCommand(t, directory, "git", "init", "--bare", remote)
	runTestCommand(t, directory, "git", "remote", "add", "origin", remote)
	runTestCommand(t, directory, "git", "push", "origin", "HEAD:factory/work")
	statePath := filepath.Join(scratch, "review-count")
	logPath := filepath.Join(scratch, "runtime.log")
	runtime := filepath.Join(scratch, "codex")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> " + logPath + "\n" +
		"result=\n" +
		"for arg in \"$@\"; do if [ \"$previous\" = \"--output-last-message\" ]; then result=$arg; fi; previous=$arg; done\n" +
		"if [ -z \"$result\" ]; then git commit --allow-empty -m Repair >/dev/null; exit 0; fi\n" +
		"count=0; if [ -f " + statePath + " ]; then count=$(cat " + statePath + "); fi\n" +
		"count=$((count + 1)); printf '%s' \"$count\" > " + statePath + "\n" +
		"if [ \"$count\" -lt 3 ]; then printf 'Verdict: findings\\n\\n## Finding: Bug\\nFix it\\n' > \"$result\"; else printf 'Verdict: approved\\n' > \"$result\"; fi\n"
	if err := os.WriteFile(runtime, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		config:        reviewConfig(protocol.RuntimeCodex, "gpt-5.6-sol"),
		options:       Options{GitExecutable: "git", RuntimeExecutables: map[string]string{protocol.RuntimeCodex: runtime}},
		dataDirectory: scratch,
	}
	claim := protocol.Claim{Execution: protocol.SessionExecution{RequiredRuntime: protocol.RuntimeCodex}, Session: protocol.ClaimedSession{Target: protocol.WorkTarget{PublishBranch: "factory/work"}}}
	reports, repaired, err := manager.reviewWork(context.Background(), claim, Repository{Path: directory}, worktree{Path: directory, BaseBranch: "main", Branch: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if !repaired || len(reports) != 3 || reports[2].Verdict != reviewApproved {
		t.Fatalf("reports = %#v, repaired = %t", reports, repaired)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "--output-last-message") != 3 || !strings.Contains(string(log), "--model\ngpt-5.6-sol") {
		t.Fatalf("runtime log = %q", log)
	}
}

func reviewConfig(adapter, model string) Config {
	return Config{
		Profiles: []ProfileConfig{
			{Name: "executor", Adapter: adapter, Provider: adapter, Model: model, ReasoningEffort: "medium"},
			{Name: "reviewer", Adapter: adapter, Provider: adapter, Model: model, ReasoningEffort: "medium"},
		},
		Roles: RolesConfig{Executor: "executor", Reviewer: "reviewer"},
	}
}
