# Repository workflow routing

> **Status:** Implemented

Factory treats repository workflows as versioned Markdown policy stored beside
the code. The control plane catalogs `.factory/workflows/**/*.md` from each
managed repository's default branch, exposes safe metadata for authoring, and
freezes the selected policy at admission time.

## File contract

Every workflow except the legacy `implement.md` fallback starts with YAML
frontmatter:

```markdown
---
id: qa/browser
title: Browser verification
description: Verify the visible behavior with a real browser.
github_issue:
  labels_all: [team:qa, needs-browser]
---

Run the browser checks and attach the relevant evidence to the pull request.
```

IDs are lowercase slash-separated segments. Titles and descriptions are
bounded. `labels_all` is optional, normalized to lowercase, and requires every
listed label for a match. The body must be non-empty Markdown and is bounded by
the catalog limits. Unknown frontmatter fields, duplicate IDs, duplicate
labels, unsafe paths, oversized files, and oversized catalogs invalidate the
catalog.

An existing `.factory/workflows/implement.md` without frontmatter remains
valid as the `implement` fallback. A repository with no implementation file
receives the built-in `standard-build` instructions as a synthetic fallback.

## Catalog and admission

The server reads the GitHub default branch through `gh`, records its commit SHA,
parses all workflow blobs, and stores metadata plus instructions in the
repository catalog. The metadata API omits instructions. A refresh can be
requested from the repository detail page or the HTTP API.

`factory build --workflow ID` refreshes each target catalog before a new
admission and requires the ID on every target. A saved Task validates the same
condition when it is created or edited. Fleet Procedures and schedules reuse
the saved Task selection. The built-in standard Build uses `implement` unless
an explicit ID is supplied.

Issue intake uses only the live issue labels to select a workflow. It never
treats title, body, comments, or linked content as routing instructions.
Exactly one matching `labels_all` rule is required. No match selects
`implement`; multiple matches or a failed catalog refresh block the issue,
persist a diagnostic, and update one marked Issue comment.

## Frozen evidence

Each admitted Run Task snapshot and repository Work stores:

- workflow ID, title, description, path, default-branch commit SHA, and digest;
- the exact Markdown instructions used for the prompt;
- the rendered prompt with trusted workflow policy separated from untrusted
  work-item context.

The same snapshot is retained by retries, scheduled pending occurrences, fleet
admissions, and Work replacement. A later commit can change future admissions
but cannot mutate existing Work.

## Interfaces

| Surface | Contract |
| --- | --- |
| Catalog | `GET /api/v1/repositories/{repository_id}/workflows` |
| Refresh | `POST /api/v1/repositories/{repository_id}/workflows/refresh` |
| Task options | `POST /api/v1/repository-workflows/options` |
| Build | `factory build --workflow ID REFERENCE...` |

Diagnostics are returned with the catalog metadata and identify blocked issue
number, URL, stable code, message, and candidate IDs. Catalog responses never
return the Markdown body; Run detail is the deliberate evidence boundary for
the frozen instructions used by a Work.

## Verification

```sh
GOCACHE=/tmp/factory-gocache go test ./internal/protocol ./internal/controlplane ./internal/factorycli
cd web && npm run typecheck && npm test && npm run build
```

The focused Go tests cover parser limits, fallback behavior, ambiguity,
case-insensitive labels, catalog persistence and diagnostics, explicit Build
refresh, intake comments, Task, Procedure, schedule, replacement snapshots,
and the HTTP API. The UI tests cover workflow option selection and saving.
