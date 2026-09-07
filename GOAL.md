# Goal: slim Agent Factory into a label-driven software factory

Paste after `/goal` (see compact prompt at the bottom). Defaults below are
**Recommended** until keep/drop answers override them.

Repo: `/home/jbolus/factory-main` (`github.com/josephbolus/agentfactory`).
Do not start from Machinist, Rust `FACTORY`, or `0cee079`. Those are spec
and prompt sources only.

## Objective

Refactor this Go tree until the kernel is:

```text
GitHub issue + labels
    -> poll / admit once per visit
    -> match one repository workflow
    -> freeze markdown + SHA
    -> one sandboxed agent Work
    -> PR + needs-human  OR  needs-human question
    -> human merges. Factory never does.
```

Plan, execute, and review are **separate label visits**, not a pipeline
engine and not a foreman prompt. Sequence lives on the ticket.

## Target loop

```text
factory:ready-for-spec     -> .factory/workflows/triage.md  -> human
factory:ready-to-implement -> .factory/workflows/implement.md -> PR
factory:ready-for-review   -> .factory/workflows/review.md  -> needs-human
human merges
```

Exact label names stay in repository workflow frontmatter (`labels_all`).
Intake still requires an explicit agent-request label (today: `needs-agent`).
One continuous visit to a matching label set runs once. Leaving the set
rearms. Returning creates new Work.

Factory owns: poll, trust, routing, durable claim, freeze, concurrency,
timeout, worktree, supervision, cancel, history, recovery, never-merge.
The workflow owns: reading the issue, editing code or the ticket, `git`/`gh`,
opening a PR, writing evidence.

## Keep (Recommended)

| Feature | Why |
|---|---|
| GitHub issue intake (`needs-agent` poll) | This is the factory door |
| `.factory/workflows/**/*.md` + `labels_all` | Repo-owned policy, not Factory-owned SDLC |
| Frozen workflow snapshot (id, path, SHA, digest, body) | Replay and audit |
| Re-check live issue before the worker acts | Stale poll must not start work |
| `factory-server` + `factory-worker` + SQLite | Durable local control plane |
| Loopback HTTP API, CLI reads API only | Process boundary already correct |
| Worker worktrees, leases, heartbeats, retries | Execution kernel |
| Cleanup fail-closed; retain dirty/failed trees | Safety |
| `needs-human` / never merge | Shipping boundary |
| Codex + Claude Code runtimes | Real agents |
| Worker ↛ controlplane import boundary | Keep the `just boundary` test |
| Tests that pin intake, routing, freeze, isolation | Do not drop coverage to make files smaller |

## Drop (Recommended)

| Feature | Why |
|---|---|
| Cloud Run fake + `cloud-run-*` synthetic workers | Speculative, not used |
| Pipelines (1–20 stages sharing a worktree) | Encodes SDLC in Factory; one Work becomes a foreman |
| Saved Tasks / Procedures as the primary UX | GitHub is the queue |
| `factory run PROCEDURE --repos all` fleet | Wrong control plane |
| Execution-profile Cloud backends | Same as Cloud Run |
| `agent_update` semantic status protocol | Trusts the agent as source of truth for stage |
| GitHub Project status as a hard requirement | Labels are enough; Project is visualization |
| Definition/Automation leftovers in docs | Already superseded; delete, don't migrate |

## Keep-but-demote (Recommended)

| Feature | New role |
|---|---|
| `factory build ISSUE...` | Keep for 1 issue (maybe a small batch). Not a 100-target fleet. |
| Cron / scheduled workflows | Optional second source, same as Rust `trigger.bug-finder`. Not the main loop. |
| Embedded React UI | Slim to Work, intake, workers, run detail. No pipeline/procedure authoring. |
| Remote VM TLS workers | Freeze. Do not expand. Local worker is the v1 path. |
| Pi runtime | Drop unless you override. Two runtimes is enough. |
| `needs-input` + checkpoint resume | Drop unless you override. Relabel and re-admit is the factory resume. |
| `.factory/plans/issue-N.md` | Optional workflow instruction, not a kernel type. |
| Built-in `standard-build` / `implement` fallback | Keep as the zero-match fallback only. |

## Architecture after the cut

Four concepts, same as Rust Factory:

```text
Source    GitHub issues (gh CLI)
Trigger   label set on an open issue
Workflow  one markdown file in the repo
Worker    runtime, worktree, timeout, concurrency
```

Data model:

```text
Issue visit -> Work (one workflow, one repo, one frozen prompt)
            -> Execution -> Attempt (lease, process, events)
```

One Work has exactly one workflow, not N pipeline stages.
Run may still group a manual batch, but intake of one issue creates one Work.

Delete or stop compiling:

- `internal/controlplane/fake_cloud.go`
- `internal/controlplane/pipelines.go` and `pipelines_http.go`
- `internal/controlplane/procedures.go`
- `internal/controlplane/execution_profiles.go` (if only cloud)
- Task-as-Procedure authoring in `tasks.go` / UI `Tasks.tsx`

Split, do not rewrite blindly:

- `internal/controlplane/store.go` (~2473)
- `internal/controlplane/tasks.go` (~2134)

New/keep packages by concern: intake, catalog, admission, work, worker,
store. Worker must still not import controlplane.

## Refactor slices (do in order)

Each slice must leave `just` quality gates green. Do not mix “delete Cloud
Run” with “change intake semantics” in one change.

1. **Lock keep/drop** from the operator answers into this file.
2. **Rewrite `ARCHITECTURE.md`** to the four-concept model. Mark dropped
   types as removed, not “future”.
3. **Delete Cloud Run / fake provider / synthetic workers** and their tests.
4. **Collapse Pipeline** so a Work has one frozen workflow prompt. Migrate
   the built-in single-stage pipeline to that. Remove stage tables from the
   hot path.
5. **Demote Tasks/Procedures/fleet.** GitHub intake is the default door.
   `factory build` admits issue URLs. Saved prompt-in-SQLite is gone or
   hidden.
6. **Intake semantics.** One visit per matching label set. Rearm on leave.
   Ambiguous `labels_all` still blocks. Zero match → `implement`.
7. **Default workflows** in docs/examples: triage, implement, review.
   Review agent never merges.
8. **Slim UI and CLI** to match. Dead routes and commands go away.
9. **Split god files** along the new package boundaries.
10. **Docs purge.** Remove superseded design records from the operator path
    (`docs/README.md` should describe what exists).

## Invariants (do not violate)

1. Ticket content is untrusted. Workflow markdown from the default branch
   is trusted policy.
2. Routing uses labels only, never title/body/comments.
3. Factory never merges or enables auto-merge.
4. Claims are durable. Replay cannot double-start Work.
5. Live GitHub is re-read before the agent process starts.
6. Loopback-only operator API.
7. Worker does not import `internal/controlplane`.
8. Cleanup fails closed.
9. Unix-only is acceptable. Do not add Windows.

## Quality gates (every slice)

```sh
just format-check
just vet
just boundary
just staticcheck
go test ./...
```

If the slice touches UI:

```sh
just ui-check
```

Do not “fix” gates by deleting tests that still encode a **kept** behavior.
Deleting tests for a **dropped** behavior is required.

## Done when

Evidence a verifier can reproduce:

- [ ] `ARCHITECTURE.md` describes label → one workflow → one Work → human.
      No Pipeline, Procedure fleet, or Cloud Run as current behavior.
- [ ] `rg -n 'Cloud Run|fake_cloud|Pipeline|procedures' --glob '!docs/**'`
      on `cmd/` and `internal/` is empty, or only refers to removal.
- [ ] GitHub intake tests prove: `needs-agent` poll, `labels_all` match,
      freeze SHA, ambiguous block, implement fallback, live re-check.
- [ ] A Work row stores exactly one workflow snapshot, not a stage list.
- [ ] CLI can admit one GitHub issue and show Work without pipeline flags.
- [ ] `just boundary`, `go test ./...`, `just vet` pass.
- [ ] Docs/examples show triage → implement → review via labels, never merge.

## What not to do

- Do not port Rust `daemon.rs` / `storage.rs`.
- Do not import Machinist’s “opaque script owns the SDLC”.
- Do not add a DAG, approval engine, or generic workflow graph.
- Do not make Factory the ticket tracker.
- Do not expand Cloud, fleet, or multi-tenant features.
- Do not rebrand or rename the module until the kernel is small.

## Spec sources (read-only)

- This repo: `docs/workflows/repository-workflows.md`,
  `docs/repository-workflow-routing/design.md`,
  `internal/controlplane/github_intake.go`
- Rust model: `~/src/tries/FACTORY/docs/design.md` and
  `~/src/tries/FACTORY/.factory/config.toml`
- Phase prompt text only: `~/src/tries/factory/.factory/phases/*.md`
- Not a base: `/tmp/machinist-main`

---

## Compact `/goal` paste

```
/goal Refactor /home/jbolus/factory-main per GOAL.md into a label-driven software factory: GitHub issue labels admit one frozen repository workflow as one Work; plan/execute/review are separate label visits; never merge. Delete Cloud Run, Pipelines-as-SDLC, Procedure fleet, and Task-as-queue. Keep intake, catalog, freeze, workers, leases, worktrees, Codex+Claude. Each slice must pass just format-check vet boundary staticcheck and go test ./.... Done when ARCHITECTURE.md matches that kernel, Cloud Run/Pipeline/Procedure are gone from cmd+internal, intake+routing+freeze tests pass, and a Work stores one workflow not a stage list.
```
