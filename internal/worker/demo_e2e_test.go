package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/josephbolus/agentfactory/internal/controlplane"
	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestDemoIssueIntakeCommitsMarkdownPlan(t *testing.T) {
	root := t.TempDir()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	runTestCommand(t, root, realGit, "init", "--bare", remote)
	runTestCommand(t, root, realGit, "clone", remote, seed)
	runTestCommand(t, seed, realGit, "config", "user.email", "factory@example.test")
	runTestCommand(t, seed, realGit, "config", "user.name", "Factory Test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, seed, realGit, "add", "README.md")
	runTestCommand(t, seed, realGit, "commit", "-m", "Seed")
	runTestCommand(t, seed, realGit, "branch", "-M", "main")
	runTestCommand(t, seed, realGit, "push", "-u", "origin", "main")
	runTestCommand(t, remote, realGit, "symbolic-ref", "HEAD", "refs/heads/main")

	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	issueMarker := filepath.Join(root, "issue-ready")
	writeDemoGitHub(t, filepath.Join(bin, "gh"))
	writeTestExecutable(t, filepath.Join(bin, "git"), `#!/bin/sh
set -eu
if [ "${1:-}" = "fetch" ]; then
	exit 0
fi
if [ "${1:-}" = "ls-remote" ] && [ "${3:-}" = "origin" ]; then
	exec "$FACTORY_TEST_REAL_GIT" ls-remote "$2" "$FACTORY_TEST_REMOTE" "$4"
fi
exec "$FACTORY_TEST_REAL_GIT" "$@"
`)
	writeTestExecutable(t, filepath.Join(bin, "claude"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
	printf '%s\n' '2.1.239'
	exit 0
fi
if [ "${1:-} ${2:-}" = "auth status" ]; then
	printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'
	exit 0
fi
cat >/dev/null
printf '%s\n' \
	'{"type":"system","subtype":"init"}' \
	'{"type":"result","result":"# Plan\n\n- Implement the change.\n\n# Tests\n\n- go test"}'
`)
	writeTestExecutable(t, filepath.Join(bin, "codex"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
	printf '%s\n' 'codex-test'
	exit 0
fi
if [ "${1:-} ${2:-}" = "login status" ]; then
	printf '%s\n' 'authenticated'
	exit 0
fi
if [ "${1:-}" = "app-server" ]; then
	exit 0
fi
sleep 30
`)
	writeTestExecutable(t, filepath.Join(bin, "pi"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
	printf '%s\n' '0.84.3'
	exit 0
fi
printf '%s\n' 'provider model context max-out thinking images' 'deepseek-beta deepseek-v4.1-flash-expires-on-0910 1.0M 131.1K yes yes'
`)
	writeTestExecutable(t, filepath.Join(bin, "curl"), "#!/bin/sh\nexit 0\n")
	writeTestExecutable(t, filepath.Join(bin, "lsof"), "#!/bin/sh\nexit 0\n")
	t.Setenv("FACTORY_TEST_GH_ISSUE", issueMarker)
	t.Setenv("FACTORY_TEST_REAL_GIT", realGit)
	t.Setenv("FACTORY_TEST_REMOTE", remote)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	store, err := controlplane.Open(context.Background(), filepath.Join(root, "factory.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := httptest.NewServer(controlplane.NewHandler(store, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	repository, _, err := store.CreateManagedRepository(context.Background(), protocol.CreateManagedRepositoryRequest{
		RemoteIdentity: "github.com/josephbolus/factory-demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	enableIssueIntake(t, server, repository.ID)

	workerData := t.TempDir()
	options := testOptions(filepath.Join(bin, "codex"))
	options.GitExecutable = filepath.Join(bin, "git")
	options.GitHubExecutable = filepath.Join(bin, "gh")
	options.RuntimeExecutables = map[string]string{
		protocol.RuntimePi: filepath.Join(bin, "pi"), protocol.RuntimeCodex: filepath.Join(bin, "codex"),
		protocol.RuntimeClaudeCode: filepath.Join(bin, "claude"),
	}
	options.HTTPClient = server.Client()
	options.SupervisorCommand = []string{os.Args[0], "-test.run=TestAgentUpdateSupervisorHelper"}
	options.PollInterval = 10 * time.Millisecond
	options.RegistrationInterval = 20 * time.Millisecond
	t.Setenv("FACTORY_TEST_SUPERVISOR_HELPER", "1")
	var workerLogs bytes.Buffer
	manager, err := New(Config{
		Server: server.URL, Name: "demo-e2e", Runtime: protocol.RuntimeCodex,
		Runtimes: []string{protocol.RuntimePi, protocol.RuntimeCodex, protocol.RuntimeClaudeCode},
		Profiles: []ProfileConfig{
			{Name: "planner-opus", Adapter: protocol.RuntimeClaudeCode, Provider: "anthropic", Model: "opus", ReasoningEffort: "medium"},
			{Name: "executor-terra", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-terra", ReasoningEffort: "medium"},
			{Name: "reviewer-sol", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "medium"},
			{Name: "fallback-deepseek", Adapter: protocol.RuntimePi, Provider: "deepseek-beta", Model: "deepseek-v4.1-flash-expires-on-0910", ReasoningEffort: "medium"},
		},
		Roles:         RolesConfig{Planner: "planner-opus", Executor: "executor-terra", Reviewer: "reviewer-sol"},
		MaxConcurrent: 1, DataDirectory: workerData, SourceAccess: []string{"github"},
	}, options, slog.New(slog.NewTextHandler(&workerLogs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Manager shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Manager did not stop")
		}
	}()
	waitForDemoWorker(t, store, manager.ID())

	demoState := filepath.Join(root, "demo-state")
	if err := os.MkdirAll(filepath.Join(demoState, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
	for _, name := range []string{"factory-server.pid", "factory-worker.pid"} {
		if err := os.WriteFile(filepath.Join(demoState, "run", name), pid, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("make", "-C", repositoryRoot(t), "STATE="+demoState, "demo-issue-search")
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("demo-issue-search: %v\n%s", err, output)
	}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.PollGitHubIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := store.RunPage(context.Background(), 10, "")
	if err != nil || len(page.Runs) != 1 {
		t.Fatalf("GitHub intake Runs = %#v, %v", page.Runs, err)
	}
	if len(page.Runs[0].Targets) != 1 || page.Runs[0].Targets[0].SourceTitle != "Fix case-insensitive product search" {
		t.Fatalf("GitHub intake target = %#v", page.Runs[0].Targets)
	}

	planPath, err := waitForMarkdownPlan(workerData)
	if err != nil {
		detail, detailErr := store.Run(context.Background(), page.Runs[0].ID)
		worker, workerErr := store.Worker(context.Background(), manager.ID())
		t.Fatalf("%v\nRun = %#v, %v\nWorker = %#v, %v\nLogs:\n%s", err, detail, detailErr, worker, workerErr, workerLogs.String())
	}
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := "# Plan\n\n- Implement the change.\n\n# Tests\n\n- go test\n"
	if string(plan) != wantPlan {
		t.Fatalf("plan = %q", plan)
	}
	worktreePath := filepath.Dir(filepath.Dir(filepath.Dir(planPath)))
	if err := waitForPlanCommit(realGit, worktreePath); err != nil {
		t.Fatal(err)
	}
}

func writeDemoGitHub(t *testing.T, path string) {
	t.Helper()
	writeTestExecutable(t, path, `#!/bin/sh
set -eu
case "${1:-} ${2:-}" in
	'--version ')
		printf '%s\n' 'gh test'
		;;
	'auth status')
		exit 0
		;;
	'issue create')
		: >"$FACTORY_TEST_GH_ISSUE"
		printf '%s\n' 'https://github.com/josephbolus/factory-demo/issues/42'
		;;
	'issue list')
		if [ -f "$FACTORY_TEST_GH_ISSUE" ]; then
			printf '%s\n' '[{"number":42,"title":"Fix case-insensitive product search","url":"https://github.com/josephbolus/factory-demo/issues/42"}]'
		else
			printf '%s\n' '[]'
		fi
		;;
	'pr list')
		printf '%s\n' '[]'
		;;
	'project item-add')
		printf '%s\n' 'item-id'
		;;
	'repo clone')
		"$FACTORY_TEST_REAL_GIT" clone --no-checkout "$FACTORY_TEST_REMOTE" "$4" >/dev/null
		"$FACTORY_TEST_REAL_GIT" -C "$4" remote set-url origin "https://github.com/$3.git"
		;;
	'api user')
		printf '%s\n' '{"login":"factory-bot"}'
		;;
	'api graphql')
		printf '%s\n' '{"data":{"repository":{"issue":{"projectItems":{"nodes":[{"project":{"title":"Factory"},"fieldValueByName":{"name":"Ready"}}]}}}}}'
		;;
	'api repos/josephbolus/factory-demo')
		printf '%s\n' '{"default_branch":"main"}'
		;;
	'api repos/josephbolus/factory-demo/git/ref/heads/main')
		printf '%s\n' '{"object":{"sha":"demo-commit"}}'
		;;
	'api repos/josephbolus/factory-demo/git/trees/'*)
		printf '%s\n' '{"tree":[]}'
		;;
	'api '*)
		case " $* " in
			*' --slurp '*) printf '%s\n' '[[]]' ;;
			*) printf '%s\n' '{}' ;;
		esac
		;;
esac
`)
}

func enableIssueIntake(t *testing.T, server *httptest.Server, repositoryID string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/v1/repositories/"+repositoryID+"/issue-intake", bytes.NewBufferString(`{"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("enable issue intake = %s: %s", response.Status, body)
	}
}

func waitForDemoWorker(t *testing.T, store *controlplane.Store, workerID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		worker, err := store.Worker(context.Background(), workerID)
		if err == nil && worker.Health == "healthy" && worker.Online {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("demo Worker did not become healthy")
}

func waitForMarkdownPlan(workerData string) (string, error) {
	deadline := time.Now().Add(30 * time.Second)
	pattern := filepath.Join(workerData, "worktrees", "*", ".factory", "plans", "issue-42.md")
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", fmt.Errorf("Markdown plan did not appear at %s", pattern)
}

func waitForPlanCommit(gitExecutable, worktreePath string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := exec.Command(gitExecutable, "-C", worktreePath, "log", "-1", "--format=%s").Output()
		if err == nil && strings.TrimSpace(string(output)) == planCommitMessage {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("Markdown plan commit did not appear")
}
