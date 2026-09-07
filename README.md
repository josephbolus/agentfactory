# Agent Factory

**Run repeatable software work through AI coding agents across repositories and machines.**

[![Developer preview](https://img.shields.io/badge/status-developer%20preview-5b7cfa.svg)](#project-status)

[Quick start](#quick-start) ·
[Documentation](docs/README.md) ·
[Architecture](ARCHITECTURE.md)

Agent Factory is a *local-first* control plane for fully autonomous coding agents. Define
software work once, run it across one or many Git repositories, and see every
agent, worktree, result, failure, and retry in one place.

Agent Factory is confidential and proprietary Owens OnLine software for internal
use by Owens OnLine employees only. It is designed for builders who have outgrown a collection of terminal windows
but still want agents to run on infrastructure and credentials they control.

```text
Define work  ->  Dispatch repositories  ->  Run agents  ->  Inspect outcomes
                       |                       |
                       +-- Git worktrees       +-- Pi, Codex, Claude Code
                       +-- local or VM Workers +-- bounded concurrency
```

## What Agent Factory gives you

- **Repeatable software work.** Save a prompt, repository scope, runtime,
  schedule, and execution settings as one Task.
- **One operational view.** Follow active Runs, repository Sessions, Attempts,
  agent events, results, failures, and retained worktrees from the browser.
- **A worker fleet you control.** Run Pi, Codex, or Claude Code on a laptop,
  workstation, or remote VM without exposing the operator API publicly.
- **Git-native isolation.** Every Attempt runs in its own worktree. Clean work
  is reclaimed while unpublished or failed work remains inspectable.
- **Durable coordination.** SQLite state, leases, heartbeats, retries,
  cancellation, schedules, and bounded APIs survive process restarts.

## Quick start

Requirements:

- Go 1.25.13 or newer on the 1.25 release line, or Go 1.26.6 or newer
- Git, `curl`, and `just`
- An authenticated Pi, Codex, or Claude Code CLI on the Worker host
- GitHub CLI when using managed GitHub repositories

```sh
# Obtain Agent Factory from approved internal source control.
cd factory
just build
mkdir -p ~/.factory
cp examples/worker.toml ~/.factory/worker.toml
just run
```

Open [http://127.0.0.1:7337](http://127.0.0.1:7337). Edit
`~/.factory/worker.toml` to enable the agent runtimes installed on your machine.
Use `~/.factory/bin/factory status` to read current Runs or
`~/.factory/bin/factory workers` to check the Worker pool from the terminal.
Use `~/.factory/bin/factory build ISSUE...` to admit up to 100 GitHub issues or
repository-scoped opaque references as independent Work in one Run.
Pass `--workflow ID` to select a repository workflow explicitly; without it,
`standard-build` uses the repository's `implement` workflow.
The [local guide](docs/local.md) covers authentication, repository setup, and
the complete first Run.

Node.js is only required when changing the browser UI. Normal builds use the
committed embedded assets.

## Agent Factory demo

The [Agent Factory demo](demo/factory-demo/README.md) runs a local control plane
and Worker for the internal demo repository. It creates an issue, adds it to the
demo project as `Ready`, and labels it `needs-agent` for intake.

Requirements: authenticated `gh`, authenticated Codex and Claude Code CLIs,
`curl`, and `lsof`.

The demo also includes an optional Pi profile using OpenRouter's
`moonshotai/kimi-k3`. Check the local Pi catalog and run a minimal request
through each configured Claude model:

```sh
make test-local-runtimes
```

In one terminal, start the demo:

```sh
make demo-start
```

Open [http://127.0.0.1:7339](http://127.0.0.1:7339). In another terminal,
create demo Work:

```sh
make demo-issue-search
# or
make demo-issue-total
```

Inspect the Work in the local UI and the marked Agent Factory comment on its GitHub
Issue. Stop it with:

```sh
make demo-stop
```

`make demo-issue-*` refuses to create an Issue unless this checkout's server
and Worker are running and healthy. `go test ./...` checks this guard and the
mocked GitHub Project workflow without creating GitHub state.

Older Agent Factory releases used repository-local `.factory/config.toml` files to
define source commands, label triggers, schedules, and Worker settings. The Go
control plane retired that model. Server configuration now owns intake timing,
Worker configuration owns runtime capacity, and the control plane owns GitHub
intake. Repository `.factory/workflows/*.md` files remain versioned with the
repository and are loaded from the checked-out commit by the built-in
`standard-build` Procedure.

Each workflow is Markdown with YAML frontmatter. The `id`, `title`, and
optional `github_issue.labels_all` fields are catalog metadata; the Markdown
body is trusted repository policy. For example:

```markdown
---
id: dba/index-review
title: Review database indexes
description: Check index health and query plans.
github_issue:
  labels_all: [team:dba]
---

Run the repository's index and query-plan checks. Open one tested pull request.
```

Factory refreshes this catalog from the default branch, routes GitHub issues by
case-insensitive label matches, and records the selected workflow commit and
digest in every Run and Work snapshot. Ambiguous or unavailable routes remain
blocked with a diagnostic; issue text never chooses a workflow.

See [repository workflows](docs/workflows/repository-workflows.md) for
frontmatter, role, routing, and demo examples.

## How it works

The Go control plane owns Tasks, Runs, schedules, durable state, and admission.
Workers poll for eligible Sessions, prepare isolated Git worktrees, supervise
the selected coding-agent runtime, and report bounded events and results.

```text
Browser
   |
   | loopback HTTP + JSON
   v
Agent Factory control plane
  SQLite, scheduler, Run admission, embedded UI
   ^
   | authenticated polling, leases, events, completion
   |
Agent Factory Workers
  repository cache, isolated worktrees, agent slots
   |
   +-- Pi
   +-- Codex
   `-- Claude Code
```

The operator surface stays on loopback. Remote Workers use a separate,
TLS-authenticated endpoint. The control plane stores coordination metadata,
while Workers retain Git contents, runtime credentials, and worktrees. Read the
[architecture](ARCHITECTURE.md) and [security policy](SECURITY.md) before
running untrusted code.

## Project status

Agent Factory is in **developer preview**. Compatibility-breaking changes are
expected while the product model settles.

Implemented today:

- Go control-plane API and embedded React UI
- durable Tasks, Runs, Sessions, Attempts, leases, events, and cancellation
- transactional multi-item `factory build` admission with durable replay keys
- manual and scheduled work across one or many repositories
- Pi, Codex, and Claude Code Worker capabilities
- managed repository catalog, Worker caches, and isolated Git worktrees
- repository workflow catalogs, label routing, frozen workflow snapshots, and
  routing diagnostics
- table, list, and Kanban Run views with repository-level detail
- local Workers and authenticated remote VM Workers

Active product work is moving Agent Factory toward an agent-directed queue for work
items and repository fleets. Existing coding agents own engineering judgment;
Agent Factory owns repeatable procedures, Worker capacity, durable state, and one
view of the work. See the
[target design](docs/software-factory/design.md) and
[product vision](docs/software-factory/vision.md).

## Development

```sh
just test
just vet
just ui-check
```

Browser-facing changes are proven against a real Go server with:

```sh
just test-browser
```

Run the documented checks before opening an internal pull request. Internal pull
requests from Owens OnLine employees are welcome. Proposed and historical designs
are indexed in [docs](docs/README.md).

## Access

Use approved internal source control. See [LICENSE](LICENSE) for the proprietary
and confidential use notice.
