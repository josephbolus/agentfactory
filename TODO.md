# Repository workflow routing correction

Status: delivered to PR #14; awaiting review and merge. Owner: `/root`.

## Scope and contracts

- [x] Canonicalize `github.com/owner/repository` to `owner/repository` for every GitHub workflow API/comment path; reject malformed and non-GitHub identities. Files: `internal/controlplane/repository_workflows.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: exact REST paths and malformed-identity tests pass.
- [x] Freeze the issue-intake catalog revision through admission and clear diagnostics only after admission succeeds. Files: `internal/controlplane/github_intake.go`, `internal/controlplane/build.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: source rotation test proves one refresh and the selected commit/body.
- [x] Freeze repository-specific scheduled workflow snapshots at claim and reuse them on retry. Files: `internal/controlplane/task_scheduler.go`, `internal/controlplane/tasks.go`, `internal/protocol/tasks.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: transient retry test preserves distinct repositories' original content.
- [x] Reject truncated GitHub trees as unavailable. Files: `internal/controlplane/repository_workflows.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: truncated response test fails closed.
- [x] Reset an unavailable UI workflow to `implement`, or block save with a clear message when no compatible workflow exists. Files: `web/src/Tasks.tsx`, `web/src/Tasks.test.tsx`. Acceptance: both UI behavior tests pass.
- [x] Preserve strict YAML declaration presence and enforce workflow catalog/parser boundaries. Files: `internal/protocol/repository_workflows.go`, `internal/protocol/repository_workflows_test.go`. Acceptance: labels 1/10/11, case-insensitive duplicates, file/content, 100/101-file, catalog-size, duplicate-ID, and unknown-field cases pass; explicit `labels_all: []` is rejected.
- [x] Refresh repository workflow catalogs for first default Build and rebuild, while replaying before network access. Files: `internal/controlplane/build.go`, `internal/controlplane/build_test.go`, `internal/controlplane/test_helpers_test.go`. Acceptance: first-build, replay-offline, and rebuild-after-change tests pass.
- [x] Keep repository-specific workflow instructions out of Run summaries while retaining them in Run detail. Files: `internal/controlplane/tasks.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: the regression test failed before the scrub and passed after it.
- [x] Durably claim scheduled occurrences before resolving workflow snapshots. Files: `internal/controlplane/task_scheduler.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: missing policy records a visible retry, and repaired policy admits the same occurrence.
- [x] Enforce GitHub tree count and byte limits before fetching workflow blobs. Files: `internal/controlplane/repository_workflows.go`, `internal/controlplane/repository_workflows_test.go`. Acceptance: excessive file count, file size, and catalog size cause zero blob requests.
- [x] Exercise dynamic managed-repository acquisition in Chromium. Files: `web/e2e/server.mjs`, `web/e2e/control-plane.spec.ts`. Acceptance: a repository absent from the worker configuration clones with a canonical GitHub origin and completes its three-stage Run.

## Verification evidence

- [x] Failing-first regression tests recorded for slug injection, intake refresh count, scheduled snapshot, truncated tree, empty labels, default Build, and UI fallback/blocking behavior.
- [x] Live GitHub path proof: `gh api repos/josephbolus/agentfactory --jq '.full_name + " " + .default_branch'` returned `josephbolus/agentfactory main` with host-keychain access (`require_escalated`); credentials were not exposed.
- [x] Focused Go tests and `make test` complete on the final tree.
- [x] Go race (`286` worker tests), `go vet`, vulnerability, and static analysis checks complete.
- [x] UI lint, typecheck, unit tests (`47`), and production build complete.
- [x] Fresh Chromium suite: 10 tests passed on 2026-08-31, including dynamic managed-repository acquisition.
- [x] Release artifact reproducibility and completeness checks complete; `git diff --check` passes.
- [x] Generated `web/dist` updated with the UI source change.

## Delivery and handoff

- [x] Commit only task files on `feat/repository-workflow-routing`.
- [x] Verify `git ls-remote origin main` before publication.
- [x] Push correction commit and update PR #14; do not merge.
- [x] Preserve and report demo PR #40 ordering; do not modify its worktree unless required by this correction.
- [x] Record CI status: neither PR has configured checks. Independent re-review was requested; the reviewer exhausted its quota after the final correction.
- [x] Confirm parent worktree remains unchanged except for its pre-existing `README.md` and `examples/demo-worker-council.toml` state.
