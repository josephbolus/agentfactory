# Internal platform adoption

> **Status:** Proposed for review

## 1. Executive summary

Agent Factory becomes the organisation's internal agent platform for many repositories.
It remains a separate service and repository. Project repositories are managed
targets. The cost is a small platform operation: upgrades, Workers, credentials,
and incident ownership.

## 2. Context and scope

The current repository is the Agent Factory prototype. The organisation needs a
canonical codebase, controlled releases, and a staged rollout across its
repositories. This design covers adoption and operation. It does not add product
features.

## 3. System context

```text
Organisation platform repo
        |
        v
Agent Factory server + SQLite <---- authenticated Workers
        |                         |
        +---- managed repositories + Pi, Codex, Claude Code
```

The server owns coordination state. Workers own repository clones, worktrees,
and agent credentials. Managed repositories remain separate Git repositories.

## 4. Proposed design

### How it works

Platform operators maintain one canonical Agent Factory repository and release a
versioned server and Worker binary. The initial deployment uses the existing
control plane and the `factory-demo` repository. Remote Workers remain an
optional TLS-authenticated extension. Each repository retains its own issue,
branch, PR, and CI policy.

### Components and responsibilities

The platform repository owns Agent Factory code, release artifacts, migration review,
and operational documentation. The control plane owns SQLite state and routing.
Workers own provider authentication and local worktrees. Repository owners own
their workflow instructions, agent label, protected branches, and PR rules.

### Decisions

Use a separate platform repository and service. Embedding Agent Factory in an
application monorepo would couple application releases to Worker credentials,
SQLite migrations, and platform incidents. Use approved internal source control
and merge or cherry-pick reviewed changes deliberately. Keep
the existing local-first deployment model.

## 5. Invariants and requirements

### Invariants

- `INV-1`: Only enabled managed repositories may receive Work.
- `INV-2`: A Worker receives only its own credential and never a server
  operator credential.
- `INV-3`: Every production change is reproducible from a tagged platform
  release and reversible by disabling intake or Workers.
- `INV-4`: Repository policy stays in the repository; Agent Factory does not bypass
  branch protection or PR review.

### Requirements

- A staging environment proves one complete issue-to-PR flow before production.
- Production uses separate server state, Worker identities, and credentials.
- Each repository has an owner and documented eligibility rules.

## 6. Interfaces and data

Create an organisation-owned Git repository by importing this history. Deploy one
server database per environment.
Register Workers with environment-specific enrollment tokens. Add repositories
through Agent Factory's managed-repository interface.

No schema migration is required for adoption. A migration is required only when
the platform changes durable control-plane state.

## 7. Failure behavior and lifecycle

If a Worker or provider fails, Work remains queued, failed, or retained under
the existing lifecycle. Disable issue intake to stop new Work. Disable or drain
Workers to stop execution. Retain the database and worktrees until incident
review completes. Restore service by deploying the prior tagged release against
a database backup proven compatible before the upgrade.

## 8. Security, privacy, and operations

Use the authenticated `gh` CLI on the Worker host for GitHub access and retain
provider credentials on each Worker. Back up SQLite before every release and
test restore quarterly. Limit Workers by provider quota, CPU, disk, and
repository trust tier. Do not admit repositories that execute untrusted
contributor code until their Worker isolation policy is accepted.

## 9. Acceptance criteria

- `AC-1`: The organisation-owned repository builds and releases Agent Factory.
- `AC-2`: `factory-demo` completes one labelled issue through an approved PR.
- `AC-3`: Disabling intake prevents new Work without losing existing evidence.
- `AC-4`: A database backup restores into an isolated staging server.
- `AC-5`: The initial deployment admits only `factory-demo` without bypassing repository
  policy.

## 10. Test approach

Prove `AC-1` with the repository check suite and release verification. Prove
`INV-1` and `INV-4` with staging repository routing and protected-branch tests.
Prove `INV-2` through Worker credential inspection and negative API tests. Prove
`INV-3`, `AC-3`, and `AC-4` with an intake-disable drill and restore drill.

## 11. Risks and tradeoffs

- Provider outages can block many repositories. Use separate Worker pools and
  staged enablement.
- A bad platform release can affect all repositories. Require staging, backup,
  and an explicit rollback release.
- Broad repository access increases blast radius. Partition Workers by trust
  tier and repository group.

## 12. Open questions

- When should a second repository join after `factory-demo`? This does not
  block the initial deployment.
- When does the local control plane need a remote Worker? This does not block
  the initial deployment.

## 13. Out of scope

- New provider adapters.
- Changing Agent Factory's data model.
- Migrating application code into the platform repository.
