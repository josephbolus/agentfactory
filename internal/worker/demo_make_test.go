package worker

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestDemoRuntimeMatchesExecutor(t *testing.T) {
	root := repositoryRoot(t)
	workerConfig, err := LoadConfig(filepath.Join(root, "examples", "demo-worker.toml"))
	if err != nil {
		t.Fatal(err)
	}
	executor, err := workerConfig.profile(executorRole)
	if err != nil {
		t.Fatal(err)
	}
	var serverConfig struct {
		DefaultBuildRuntime string `toml:"default_build_runtime"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "examples", "demo-server.toml"), &serverConfig); err != nil {
		t.Fatal(err)
	}
	if serverConfig.DefaultBuildRuntime != executor.Adapter {
		t.Fatalf("default build runtime = %q, executor adapter = %q", serverConfig.DefaultBuildRuntime, executor.Adapter)
	}
}

func TestDemoSetupCopiesExampleConfigs(t *testing.T) {
	root := repositoryRoot(t)
	temporary := t.TempDir()
	bin := filepath.Join(temporary, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
set -eu
output=
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-o" ]; then
		shift
		output=$1
		break
	fi
	shift
done
test -n "$output"
mkdir -p "$(dirname "$output")"
: >"$output"
chmod +x "$output"
`)
	state := filepath.Join(temporary, "state")
	command := exec.Command("make", "-C", root, "STATE="+state, "demo-setup")
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("demo-setup: %v\n%s", err, output)
	}
	configs := map[string]string{"demo-server.toml": "config.toml", "demo-worker.toml": "worker.toml"}
	for source, destination := range configs {
		want, err := os.ReadFile(filepath.Join(root, "examples", source))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(state, destination))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s was not copied unchanged", source)
		}
	}
}

func TestDemoIssueRequiresRunningCheckout(t *testing.T) {
	root := repositoryRoot(t)
	temporary := t.TempDir()
	bin := filepath.Join(temporary, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(temporary, "gh-called")
	writeTestExecutable(t, filepath.Join(bin, "gh"), "#!/bin/sh\n: >\"$FACTORY_TEST_GH_CALLED\"\n")
	command := exec.Command("make", "-C", root, "STATE="+filepath.Join(temporary, "state"), "demo-issue", "TITLE=test", "BODY=test")
	command.Env = append(os.Environ(),
		"FACTORY_TEST_GH_CALLED="+called,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "This checkout demo is not running") {
		t.Fatalf("demo-issue = %v\n%s", err, output)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("gh was called: %v", err)
	}
}

func TestDemoIssueTargetsMoveGitHubProjectItem(t *testing.T) {
	tests := []struct {
		name   string
		target string
		title  string
		body   string
	}{
		{
			name: "search", target: "demo-issue-search", title: "Fix case-insensitive product search",
			body: "Typing `factory` does not find `Factory T-Shirt`; search should be case-insensitive.\n\nAcceptance criteria:\n- A lowercase query finds matching products regardless of product-name casing.\n- Add a regression test.\n- `npm test` passes.",
		},
		{
			name: "total", target: "demo-issue-total", title: "Fix cart total with multiple quantities",
			body: "Cart total ignores an item quantity greater than one.\n\nAcceptance criteria:\n- The total is price multiplied by quantity for every item.\n- Add a regression test.\n- `npm test` passes.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newDemoHarness(t)
			output := harness.run(t, test.target)
			if got := readTestFile(t, harness.title); got != test.title {
				t.Fatalf("title = %q", got)
			}
			if got := readTestFile(t, harness.body); got != test.body {
				t.Fatalf("body = %q", got)
			}
			if got := strings.Fields(readTestFile(t, harness.commands)); strings.Join(got, " ") != "issue-create project-item-add project-item-edit issue-edit" {
				t.Fatalf("gh commands = %q", got)
			}
			for _, want := range []string{
				"josephbolus/factory-demo", "item-id", "PVT_kwHOAB2MnM4Bitvg",
				"PVTSSF_lAHOAB2MnM4BitvgzhhktV0", "61e4505c", "needs-agent",
			} {
				if !strings.Contains(readTestFile(t, harness.arguments), want) {
					t.Fatalf("gh arguments omit %q", want)
				}
			}
			if !strings.Contains(output, "Created https://example.test/issues/1 (Project: Ready; label: needs-agent)") {
				t.Fatalf("output = %q", output)
			}
		})
	}
}

func TestDemoIssuePreservesMarkdownBody(t *testing.T) {
	harness := newDemoHarness(t)
	title := "Test title"
	body := "A `quoted` value.\n\n- first\n- second"
	harness.run(t, "demo-issue", "TITLE="+title, "BODY="+body)
	if got := readTestFile(t, harness.title); got != title {
		t.Fatalf("title = %q", got)
	}
	if got := readTestFile(t, harness.body); got != body {
		t.Fatalf("body = %q", got)
	}
}

type demoHarness struct {
	root      string
	state     string
	bin       string
	title     string
	body      string
	commands  string
	arguments string
}

func newDemoHarness(t *testing.T) demoHarness {
	t.Helper()
	temporary := t.TempDir()
	harness := demoHarness{
		root: repositoryRoot(t), state: filepath.Join(temporary, "state"), bin: filepath.Join(temporary, "bin"),
		title: filepath.Join(temporary, "title"), body: filepath.Join(temporary, "body"),
		commands: filepath.Join(temporary, "commands"), arguments: filepath.Join(temporary, "arguments"),
	}
	if err := os.MkdirAll(filepath.Join(harness.state, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(harness.bin, 0o700); err != nil {
		t.Fatal(err)
	}
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
	for _, name := range []string{"factory-server.pid", "factory-worker.pid"} {
		if err := os.WriteFile(filepath.Join(harness.state, "run", name), pid, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTestExecutable(t, filepath.Join(harness.bin, "curl"), "#!/bin/sh\nexit 0\n")
	writeTestExecutable(t, filepath.Join(harness.bin, "lsof"), "#!/bin/sh\nexit 0\n")
	writeTestExecutable(t, filepath.Join(harness.bin, "gh"), `#!/bin/sh
set -eu
printf '%s\n' "$@" >>"$FACTORY_TEST_GH_ARGUMENTS"
case "$1 $2" in
	'issue create')
		echo issue-create >>"$FACTORY_TEST_GH_COMMANDS"
		shift 2
		while [ "$#" -gt 0 ]; do
			case "$1" in
				--title) shift; printf '%s' "$1" >"$FACTORY_TEST_GH_TITLE" ;;
				--body) shift; printf '%s' "$1" >"$FACTORY_TEST_GH_BODY" ;;
			esac
			shift
		done
		printf '%s\n' 'https://example.test/issues/1'
		;;
	'project item-add')
		echo project-item-add >>"$FACTORY_TEST_GH_COMMANDS"
		printf '%s\n' 'item-id'
		;;
	'project item-edit') echo project-item-edit >>"$FACTORY_TEST_GH_COMMANDS" ;;
	'issue edit') echo issue-edit >>"$FACTORY_TEST_GH_COMMANDS" ;;
esac
`)
	return harness
}

func (h demoHarness) run(t *testing.T, target string, arguments ...string) string {
	t.Helper()
	makeArguments := append([]string{"-C", h.root, "STATE=" + h.state, target}, arguments...)
	command := exec.Command("make", makeArguments...)
	command.Env = append(os.Environ(),
		"FACTORY_TEST_GH_ARGUMENTS="+h.arguments,
		"FACTORY_TEST_GH_BODY="+h.body,
		"FACTORY_TEST_GH_COMMANDS="+h.commands,
		"FACTORY_TEST_GH_TITLE="+h.title,
		"PATH="+h.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", target, err, output)
	}
	return string(output)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
