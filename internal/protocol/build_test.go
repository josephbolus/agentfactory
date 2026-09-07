package protocol

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestStandardBuildPromptUsesRepositoryWorkflow(t *testing.T) {
	if !strings.Contains(StandardBuildProcedurePrompt, ".factory/workflows/implement.md") {
		t.Fatal("standard Build prompt does not load the repository implementation workflow")
	}
	if strings.Contains(StandardBuildProcedurePrompt, "factory update") {
		t.Fatal("standard Build prompt must leave outcome reporting to its Work contract")
	}
}

func TestNormalizeBuildRequestCanonicalizesCallerFingerprintInputs(t *testing.T) {
	request := BuildRequest{
		References:          []string{"https://github.com/Acme/API/issues/00042/", "LINEAR-7"},
		RepositorySpecified: true, Repository: "GITHUB.COM/ACME/API.git",
		RuntimeSpecified: true, Runtime: " CODEX ", Rebuild: true,
	}
	canonical, normalized, fingerprint, err := CanonicalBuildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Repository != "github.com/acme/api" || canonical.Runtime != RuntimeCodex ||
		canonical.References[0] != "https://github.com/acme/api/issues/42" ||
		normalized.References[1].SourceKind != "opaque" || normalized.References[1].SourceKey != "LINEAR-7" {
		t.Fatalf("canonical request = %#v, normalized = %#v", canonical, normalized)
	}
	request.References[0] = canonical.References[0]
	request.Repository = canonical.Repository
	request.Runtime = canonical.Runtime
	_, _, repeated, err := CanonicalBuildRequest(request)
	if err != nil || !bytes.Equal(fingerprint, repeated) {
		t.Fatalf("repeated fingerprint = %x, error %v, want %x", repeated, err, fingerprint)
	}
	request.References = []string{"LINEAR-7", canonical.References[0]}
	_, _, reordered, err := CanonicalBuildRequest(request)
	if err != nil || bytes.Equal(fingerprint, reordered) {
		t.Fatalf("ordered fingerprint = %x, error %v", reordered, err)
	}
}

func TestNormalizeBuildRequestEnforcesReferenceAndBatchLimits(t *testing.T) {
	invalid := []BuildRequest{
		{},
		{References: []string{"opaque-without-repo"}},
		{References: []string{"has space"}, RepositorySpecified: true, Repository: "github.com/acme/api"},
		{References: []string{"https://example.com/acme/api/issues/1"}},
		{References: []string{"https://github.com/acme/api/pulls/1"}},
		{References: []string{"https://github.com/acme/api/issues/0"}},
		{References: []string{" LINEAR-1 "}, RepositorySpecified: true, Repository: "github.com/acme/api"},
		{References: []string{" https://github.com/acme/api/issues/1"}},
		{References: []string{"https://github.com/acme/web/issues/1"}, RepositorySpecified: true, Repository: "github.com/acme/api"},
	}
	for _, request := range invalid {
		if _, err := NormalizeBuildRequest(request); err == nil {
			t.Fatalf("accepted invalid request %#v", request)
		}
	}
	references := make([]string, MaxWorkTargets+1)
	for index := range references {
		references[index] = fmt.Sprintf("ITEM-%d", index)
	}
	if _, err := NormalizeBuildRequest(BuildRequest{
		References: references, RepositorySpecified: true, Repository: "github.com/acme/api",
	}); err == nil {
		t.Fatal("accepted more than 100 references")
	}
}

func TestBuildRequestCanonicalizesExplicitWorkflowSelection(t *testing.T) {
	request := BuildRequest{
		RequestKey: "build-1", References: []string{"https://github.com/acme/api/issues/7"},
		Workflow: " dba/index-review ", WorkflowSpecified: true,
	}
	canonical, normalized, firstFingerprint, err := CanonicalBuildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Workflow != "dba/index-review" || !canonical.WorkflowSpecified ||
		normalized.Workflow != "dba/index-review" || !normalized.WorkflowSpecified {
		t.Fatalf("workflow normalization = %#v, %#v", canonical, normalized)
	}
	request.Workflow = "qa/regression"
	_, _, secondFingerprint, err := CanonicalBuildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstFingerprint, secondFingerprint) {
		t.Fatal("workflow selection did not affect Build fingerprint")
	}
}

func TestBuildRequestRejectsImplicitOrInvalidWorkflowValues(t *testing.T) {
	for _, request := range []BuildRequest{
		{References: []string{"LINEAR-1"}, Workflow: "qa"},
		{References: []string{"LINEAR-1"}, Workflow: "../qa", WorkflowSpecified: true},
		{References: []string{"LINEAR-1"}, Workflow: "QA", WorkflowSpecified: true},
	} {
		if _, _, _, err := CanonicalBuildRequest(request); err == nil {
			t.Fatalf("request %#v was accepted", request)
		}
	}
}
