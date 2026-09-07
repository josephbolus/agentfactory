package protocol

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseWorkflowCatalogEntry(t *testing.T) {
	content := []byte(`---
id: dba/index-review
title: DBA index review
description: Review schema and query changes.
github_issue:
  labels_all:
    - team:dba
    - change:index
---

Review the issue and repository schema.
`)
	workflow, err := ParseRepositoryWorkflow(".factory/workflows/dba/index-review.md", "abc123", content)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.ID != "dba/index-review" || workflow.Title != "DBA index review" ||
		workflow.Path != ".factory/workflows/dba/index-review.md" ||
		len(workflow.LabelsAll) != 2 || workflow.Instructions == "" || workflow.Digest == "" {
		t.Fatalf("workflow = %#v", workflow)
	}

	if got := workflow.LabelsAll[0]; got != "team:dba" {
		t.Fatalf("first label = %q", got)
	}
}

func TestParseRepositoryWorkflowRejectsInvalidCatalogEntries(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "missing frontmatter", path: ".factory/workflows/qa.md", body: "instructions"},
		{name: "unsafe id", path: ".factory/workflows/qa.md", body: "---\nid: ../qa\ntitle: QA\n---\ncheck"},
		{name: "unknown field", path: ".factory/workflows/qa.md", body: "---\nid: qa\ntitle: QA\nunknown: value\n---\ncheck"},
		{name: "unknown GitHub issue field", path: ".factory/workflows/qa.md", body: "---\nid: qa\ntitle: QA\ngithub_issue:\n  owner: qa\n---\ncheck"},
		{name: "empty instructions", path: ".factory/workflows/qa.md", body: "---\nid: qa\ntitle: QA\n---\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseRepositoryWorkflow(test.path, "abc123", []byte(test.body)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseRepositoryWorkflowRejectsExplicitEmptyLabels(t *testing.T) {
	content := []byte("---\nid: qa\ntitle: QA\ngithub_issue:\n  labels_all: []\n---\ncheck")
	if _, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", content); err == nil {
		t.Fatal("expected explicit empty labels to be rejected")
	}
}

func TestParseRepositoryWorkflowAllowsManualOnlyWithoutLabels(t *testing.T) {
	content := []byte("---\nid: qa\ntitle: QA\n---\ncheck")
	workflow, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", content)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.LabelsAll != nil {
		t.Fatalf("manual-only labels = %#v", workflow.LabelsAll)
	}
}

func TestRepositoryWorkflowParserLabelBoundaries(t *testing.T) {
	for _, count := range []int{1, MaxWorkflowLabels} {
		t.Run(fmt.Sprintf("%d labels accepted", count), func(t *testing.T) {
			workflow, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", workflowWithLabels(count))
			if err != nil {
				t.Fatal(err)
			}
			if len(workflow.LabelsAll) != count {
				t.Fatalf("labels = %#v", workflow.LabelsAll)
			}
		})
	}
	t.Run("too many labels rejected", func(t *testing.T) {
		if _, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", workflowWithLabels(MaxWorkflowLabels+1)); err == nil {
			t.Fatal("expected too many labels to be rejected")
		}
	})
	t.Run("case insensitive duplicate rejected", func(t *testing.T) {
		content := []byte("---\nid: qa\ntitle: QA\ngithub_issue:\n  labels_all: [Team:DBA, team:dba]\n---\ncheck")
		if _, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", content); err == nil {
			t.Fatal("expected duplicate labels to be rejected")
		}
	})
}

func TestRepositoryWorkflowParserFileSizeBoundaries(t *testing.T) {
	base := []byte("---\nid: qa\ntitle: QA\n---\n")
	content := append(append([]byte(nil), base...), bytes.Repeat([]byte("x"), MaxRepositoryWorkflowBytes-len(base))...)
	if _, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", content); err != nil {
		t.Fatalf("maximum file rejected: %v", err)
	}
	content = append(content, 'x')
	if _, err := ParseRepositoryWorkflow(".factory/workflows/qa.md", "abc123", content); err == nil {
		t.Fatal("expected oversized file to be rejected")
	}
}

func TestRepositoryWorkflowCatalogFileAndByteBoundaries(t *testing.T) {
	for _, count := range []int{MaxRepositoryWorkflowFiles, MaxRepositoryWorkflowFiles + 1} {
		t.Run(fmt.Sprintf("%d files", count), func(t *testing.T) {
			_, err := BuildRepositoryWorkflowCatalog("repo-1", "github.com/acme/api", "abc123", workflowFiles(count, 0))
			if count == MaxRepositoryWorkflowFiles && err != nil {
				t.Fatalf("maximum file count rejected: %v", err)
			}
			if count == MaxRepositoryWorkflowFiles+1 && err == nil {
				t.Fatal("expected oversized file count to be rejected")
			}
		})
	}
	for _, total := range []int{MaxRepositoryWorkflowCatalog, MaxRepositoryWorkflowCatalog + 1} {
		t.Run(fmt.Sprintf("%d bytes", total), func(t *testing.T) {
			_, err := BuildRepositoryWorkflowCatalog("repo-1", "github.com/acme/api", "abc123", workflowFiles(50, total))
			if total == MaxRepositoryWorkflowCatalog && err != nil {
				t.Fatalf("maximum catalog size rejected: %v", err)
			}
			if total == MaxRepositoryWorkflowCatalog+1 && err == nil {
				t.Fatal("expected oversized catalog to be rejected")
			}
		})
	}
}

func TestRepositoryWorkflowCatalogRejectsDuplicateIDs(t *testing.T) {
	files := map[string][]byte{
		".factory/workflows/one.md": []byte("---\nid: shared\ntitle: One\n---\ncheck"),
		".factory/workflows/two.md": []byte("---\nid: shared\ntitle: Two\n---\ncheck"),
	}
	if _, err := BuildRepositoryWorkflowCatalog("repo-1", "github.com/acme/api", "abc123", files); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate workflow IDs error = %v", err)
	}
}

func workflowWithLabels(count int) []byte {
	labels := make([]string, count)
	for index := range labels {
		labels[index] = fmt.Sprintf("team:%d", index)
	}
	return []byte(fmt.Sprintf("---\nid: qa\ntitle: QA\ngithub_issue:\n  labels_all: [%s]\n---\ncheck", strings.Join(labels, ", ")))
}

func workflowFiles(count, total int) map[string][]byte {
	files := make(map[string][]byte, count)
	for index := 0; index < count; index++ {
		path := fmt.Sprintf(".factory/workflows/workflow-%d.md", index)
		body := []byte(fmt.Sprintf("---\nid: workflow-%d\ntitle: Workflow %d\n---\ncheck", index, index))
		if total != 0 {
			target := total / count
			if index < total%count {
				target++
			}
			body = append(body, bytes.Repeat([]byte("x"), target-len(body))...)
		}
		files[path] = body
	}
	return files
}

func TestRepositoryWorkflowCatalogMatchesLabels(t *testing.T) {
	catalog, err := BuildRepositoryWorkflowCatalog("repo-1", "github.com/acme/api", "abc123", map[string][]byte{
		".factory/workflows/implement.md": []byte("implement"),
		".factory/workflows/qa.md":        []byte("---\nid: qa\ntitle: QA\ngithub_issue:\n  labels_all: [team:qa]\n---\ncheck"),
		".factory/workflows/dba.md":       []byte("---\nid: dba\ntitle: DBA\ngithub_issue:\n  labels_all: [team:dba]\n---\ncheck"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if workflow, err := catalog.MatchIssueLabels([]string{"TEAM:QA", "needs-agent"}); err != nil || workflow.ID != "qa" {
		t.Fatalf("QA match = %#v, %v", workflow, err)
	}
	if workflow, err := catalog.MatchIssueLabels([]string{"needs-agent"}); err != nil || workflow.ID != "implement" {
		t.Fatalf("fallback match = %#v, %v", workflow, err)
	}
}

func TestRepositoryWorkflowCatalogRejectsAmbiguousMatches(t *testing.T) {
	catalog, err := BuildRepositoryWorkflowCatalog("repo-1", "github.com/acme/api", "abc123", map[string][]byte{
		".factory/workflows/a.md": []byte("---\nid: a\ntitle: A\ngithub_issue:\n  labels_all: [team:qa]\n---\na"),
		".factory/workflows/b.md": []byte("---\nid: b\ntitle: B\ngithub_issue:\n  labels_all: [team:qa]\n---\nb"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.MatchIssueLabels([]string{"team:qa"}); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ambiguity error = %v", err)
	}
}
