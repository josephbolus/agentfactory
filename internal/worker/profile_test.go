package worker

import (
	"reflect"
	"testing"

	"github.com/josephbolus/agentfactory/internal/protocol"
)

func TestRuntimeArgumentsUseProfileEffort(t *testing.T) {
	tests := []struct {
		runtime string
		want    []string
	}{
		{protocol.RuntimeCodex, []string{"exec", "--json", "--color", "never", "--model", "model", "-c", "model_reasoning_effort=medium"}},
		{protocol.RuntimeClaudeCode, []string{"--print", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions", "--model", "model", "--effort", "medium"}},
		{protocol.RuntimePi, []string{"--print", "--no-session", "--provider", "provider", "--model", "model", "--thinking", "medium"}},
	}
	for _, test := range tests {
		arguments, err := runtimeArguments(test.runtime, "provider", "model", "medium")
		if err != nil || !reflect.DeepEqual(arguments, test.want) {
			t.Fatalf("%s arguments = %#v, %v", test.runtime, arguments, err)
		}
	}
}

func TestConfiguredProfilesValidateAndBuild(t *testing.T) {
	profiles := []ProfileConfig{
		{Name: "planner", Adapter: protocol.RuntimeClaudeCode, Provider: "anthropic", Model: "opus", ReasoningEffort: "medium"},
		{Name: "executor", Adapter: protocol.RuntimeCodex, Provider: "openai", Model: "gpt-5.6-terra", ReasoningEffort: "medium"},
		{Name: "fallback", Adapter: protocol.RuntimePi, Provider: "openrouter", Model: "moonshotai/kimi-k3", ReasoningEffort: "medium"},
	}
	config := Config{
		Server:        "http://127.0.0.1:7339",
		Name:          "worker",
		Runtime:       protocol.RuntimeCodex,
		Runtimes:      []string{protocol.RuntimeClaudeCode, protocol.RuntimeCodex, protocol.RuntimePi},
		MaxConcurrent: 1,
		DataDirectory: t.TempDir(),
		Profiles:      profiles,
		Roles:         RolesConfig{Planner: "planner", Executor: "executor"},
	}
	if err := validateConfig(config); err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		arguments, err := runtimeArguments(profile.Adapter, profile.Provider, profile.Model, profile.ReasoningEffort)
		if err != nil || !containsArguments(arguments, []string{"--model", profile.Model}) {
			t.Fatalf("%s arguments = %#v, %v", profile.Name, arguments, err)
		}
	}
}

func TestRuntimeArgumentsRejectInvalidEffort(t *testing.T) {
	if _, err := runtimeArguments(protocol.RuntimePi, "provider", "model", "ultra"); err == nil {
		t.Fatal("runtimeArguments accepted unsupported effort")
	}
}

func TestReadOnlyArgumentsRestrictTools(t *testing.T) {
	tests := []struct {
		adapter string
		want    []string
	}{
		{protocol.RuntimeCodex, []string{"--sandbox", "read-only"}},
		{protocol.RuntimeClaudeCode, []string{"--permission-mode", "plan"}},
		{protocol.RuntimePi, []string{"--tools", "read,grep,find,ls"}},
	}
	for _, test := range tests {
		arguments, err := readOnlyArguments(ProfileConfig{Adapter: test.adapter, Provider: "provider", Model: "model", ReasoningEffort: "medium"})
		if err != nil || !containsArguments(arguments, test.want) {
			t.Fatalf("%s arguments = %#v, %v", test.adapter, arguments, err)
		}
	}
}

func containsArguments(arguments, want []string) bool {
	for index := 0; index <= len(arguments)-len(want); index++ {
		if reflect.DeepEqual(arguments[index:index+len(want)], want) {
			return true
		}
	}
	return false
}
