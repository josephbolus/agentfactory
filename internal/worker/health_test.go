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
	"sync"
	"testing"
	"time"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestHealthProbesRunConcurrently(t *testing.T) {
	const probeCount = 5
	started := make(chan struct{}, probeCount)
	release := make(chan struct{})
	done := make(chan struct{})
	var closeOnce sync.Once
	closeRelease := func() { closeOnce.Do(func() { close(release) }) }
	defer closeRelease()

	probes := make([]func(), probeCount)
	for index := range probes {
		probes[index] = func() {
			started <- struct{}{}
			<-release
		}
	}
	go func() {
		runHealthProbes(probes...)
		close(done)
	}()

	for index := 0; index < probeCount; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d of %d health probes started before another probe finished", index, probeCount)
		}
	}
	closeRelease()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent health probes did not finish")
	}
}

func TestRuntimeCapabilityUsesOneDeadlineForVersionAndAuthentication(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "slow-codex")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  sleep 0.5
  echo "codex test"
  exit 0
fi
sleep 3
echo "authenticated"
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	capability := runtimeCapabilityWithin(
		context.Background(), protocol.RuntimeCodex, executable, 1500*time.Millisecond,
	)
	elapsed := time.Since(started)
	if capability.Status != protocol.CapabilityUnauthenticated {
		t.Fatalf("slow auth capability = %#v", capability)
	}
	if elapsed >= 1900*time.Millisecond {
		t.Fatalf("runtime probe used separate command deadlines: elapsed %s", elapsed)
	}
}

func TestRequiredRuntimesRejectUnavailableRole(t *testing.T) {
	capabilities := []protocol.Capability{
		{Kind: protocol.CapabilityKindRuntime, Name: protocol.RuntimeCodex, Status: protocol.CapabilityReady},
		{Kind: protocol.CapabilityKindRuntime, Name: protocol.RuntimeClaudeCode, Status: protocol.CapabilityUnauthenticated},
	}
	if requiredRuntimesReady(capabilities, []string{protocol.RuntimeCodex, protocol.RuntimeClaudeCode}) {
		t.Fatal("requiredRuntimesReady accepted an unavailable planner runtime")
	}
}

func TestPiCapabilityRequiresConfiguredProfile(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "pi")
	writeTestExecutable(t, executable, `#!/bin/sh
if [ "$1" = "--version" ]; then
	printf '%s\n' '0.84.3'
	exit 0
fi
if [ "$1" = "--list-models" ]; then
	printf '%s\n' \
		'provider    model                     context  max-out  thinking  images' \
		'openrouter  moonshotai/kimi-k3        1.0M     131.1K   yes       yes' \
		'xai         grok-4.6                  500K     500K     yes       yes'
	exit 0
fi
exit 1
`)
	profile := ProfileConfig{Adapter: protocol.RuntimePi, Provider: "openrouter", Model: "moonshotai/kimi-k3"}
	capability := runtimeCapabilityWithin(context.Background(), protocol.RuntimePi, executable, time.Second, profile)
	if capability.Status != protocol.CapabilityReady {
		t.Fatalf("Pi capability = %#v", capability)
	}
	profile.Model = "moonshotai/missing"
	capability = runtimeCapabilityWithin(context.Background(), protocol.RuntimePi, executable, time.Second, profile)
	if capability.Status != protocol.CapabilityUnauthenticated || !strings.Contains(capability.Message, "openrouter/moonshotai/missing") {
		t.Fatalf("missing Pi profile capability = %#v", capability)
	}
}

func TestHealthProfilesIncludeFallbacks(t *testing.T) {
	config := Config{
		Profiles: []ProfileConfig{
			{Name: "planner", Adapter: protocol.RuntimeClaudeCode},
			{Name: "fallback", Adapter: protocol.RuntimePi, Provider: "openrouter", Model: "moonshotai/kimi-k3"},
		},
		Roles: RolesConfig{Planner: "planner"},
	}
	profiles := config.healthProfiles()
	if len(profiles) != len(config.Profiles) {
		t.Fatalf("health profiles = %#v", profiles)
	}
}

func TestPiCapabilityRejectsMalformedCatalog(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "pi")
	writeTestExecutable(t, executable, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 0.84.3; else echo 'Warning: models unavailable'; fi\n")
	capability := runtimeCapabilityWithin(context.Background(), protocol.RuntimePi, executable, time.Second)
	if capability.Status != protocol.CapabilityUnauthenticated {
		t.Fatalf("Pi capability = %#v", capability)
	}
}

func TestClaudeCapabilityRequiresLoggedInStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{name: "logged in", status: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`, want: protocol.CapabilityReady},
		{name: "logged out", status: `{"loggedIn":false}`, want: protocol.CapabilityUnauthenticated},
		{name: "malformed", status: `{`, want: protocol.CapabilityUnauthenticated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "claude")
			writeTestExecutable(t, executable, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 2.1.239; else printf '%s' '"+test.status+"'; fi\n")
			capability := runtimeCapabilityWithin(context.Background(), protocol.RuntimeClaudeCode, executable, time.Second)
			if capability.Status != test.want {
				t.Fatalf("Claude capability = %#v", capability)
			}
		})
	}
}

func TestLocalClaudeProbeRejectsTerminalError(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "claude")
	writeTestExecutable(t, executable, `#!/bin/sh
printf '%s\n' '[{"type":"result","result":"unknown model","is_error":true}]'
`)
	profile := ProfileConfig{
		Adapter:         protocol.RuntimeClaudeCode,
		Provider:        "anthropic",
		Model:           "missing-model",
		ReasoningEffort: "medium",
	}
	err := probeClaudeModel(context.Background(), executable, t.TempDir(), profile)
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("Claude probe error = %v", err)
	}
}

func TestLocalRuntimeProfiles(t *testing.T) {
	if os.Getenv("FACTORY_TEST_LOCAL_RUNTIMES") != "1" {
		t.Skip("set FACTORY_TEST_LOCAL_RUNTIMES=1 to check host credentials")
	}
	root := repositoryRoot(t)
	configs := []string{"demo-worker.toml", "demo-worker-ant.toml"}
	type profileKey struct {
		adapter  string
		provider string
		model    string
		effort   string
	}
	checked := make(map[profileKey]struct{})
	for _, name := range configs {
		config, err := LoadConfig(filepath.Join(root, "examples", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range config.Profiles {
			key := profileKey{profile.Adapter, profile.Provider, profile.Model, profile.ReasoningEffort}
			if _, found := checked[key]; found {
				continue
			}
			checked[key] = struct{}{}
			checkLocalProfile(t, root, profile)
		}
	}
}

func checkLocalProfile(t *testing.T, root string, profile ProfileConfig) {
	t.Helper()
	executable, err := exec.LookPath(defaultRuntimeExecutable(profile.Adapter))
	if err != nil {
		t.Fatal(err)
	}
	capability := runtimeCapabilityWithin(context.Background(), profile.Adapter, executable, healthCheckTimeout, profile)
	if capability.Status != protocol.CapabilityReady {
		t.Fatalf("profile %q capability = %#v", profile.Name, capability)
	}
	if profile.Adapter != protocol.RuntimeClaudeCode {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := probeClaudeModel(ctx, executable, root, profile); err != nil {
		t.Fatal(err)
	}
}

func probeClaudeModel(ctx context.Context, executable, root string, profile ProfileConfig) error {
	arguments, err := structuredOutputArguments(profile)
	if err != nil {
		return err
	}
	arguments = append(arguments, "Reply with OK only.")
	stdout, stderr, err := runCommand(ctx, executable, root, maxSupervisorErrorBytes, arguments...)
	if err != nil {
		return commandFailure("probe Claude model "+profile.Model, stdout, stderr, err)
	}
	return checkClaudeOutput(stdout)
}

func checkClaudeOutput(output []byte) error {
	type event struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	found := false
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode Claude model response: %w", err)
		}
		events := []event{}
		if bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
			if err := json.Unmarshal(value, &events); err != nil {
				return fmt.Errorf("decode Claude model response: %w", err)
			}
		} else {
			var item event
			if err := json.Unmarshal(value, &item); err != nil {
				return fmt.Errorf("decode Claude model response: %w", err)
			}
			events = append(events, item)
		}
		for _, item := range events {
			if item.Type != "result" {
				continue
			}
			found = true
			if item.IsError {
				return errors.New(firstNonEmpty(strings.TrimSpace(item.Result), "Claude model probe failed"))
			}
		}
	}
	if !found {
		return errors.New("Claude model probe returned no terminal result")
	}
	return nil
}
