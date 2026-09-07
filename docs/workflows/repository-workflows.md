# Repository workflows

Repository workflows are versioned Markdown policy in
`.factory/workflows/**/*.md`. Agent Factory refreshes the catalog from the
repository default branch and freezes the selected workflow in each Work
snapshot.

## Frontmatter

Use YAML frontmatter for every workflow. `implement.md` may omit it only as a
legacy fallback; new repositories should include it.

```markdown
---
id: dba/index-review
title: Review database indexes
description: Inspect index coverage and query plans before proposing a change.
github_issue:
  labels_all:
    - team:dba
---

Reproduce the query problem, make the smallest safe change, run focused checks,
and open one tested pull request for human review.
```

`id`, `title`, and `description` identify the workflow. `labels_all` is
optional. When present, every listed label must be on the live GitHub issue;
matching is case-insensitive. Do not use issue title, body, or comments for
routing.

No matching rule selects `implement`. More than one matching rule blocks intake
and records a diagnostic.

## Common workflows

```markdown
---
id: implement
title: Implement a ready ticket
description: Deliver an approved issue as a tested pull request.
---

Implement the smallest cohesive change. Run relevant checks, review the diff,
push a branch, open a linked pull request, and move the Project item to Review.
Never merge or deploy.
```

```markdown
---
id: triage
title: Triage a new ticket
description: Turn an incoming issue into an implementation-ready specification.
github_issue:
  labels_all:
    - factory:ready-for-spec
---

Clarify scope, acceptance criteria, risks, and verification. Route the issue to
human approval. Do not change code or open a pull request.
```

```markdown
---
id: bug-finder
title: Find a verified defect
description: Find one defensible defect and route it for implementation.
github_issue:
  labels_all:
    - factory:bug-finder
---

Prove one non-duplicate defect, open its issue, and route it to the
implementation workflow. Do not change code or open a pull request.
```

Triage and bug-finder are routing-only. Workflows that implement a change,
including DBA and integration workflows, publish tested code PRs.

## GitHub issue loop

```text
Ready + needs-agent
        |
        v
catalog refresh -> label match or implement fallback
        |
        v
In Progress -> tested pull request -> Review + needs-human
```

The worker rechecks the live issue before changing it. If catalog refresh,
routing, verification, publishing, or CI is blocked, it comments with the
exact blocker. A human owns merge and deployment.

Factory allows up to two repair attempts across three independent review
passes. It preserves unresolved findings for a human.

For issue work, Factory also commits `.factory/plans/issue-<number>.md` on the
work branch as an audit artifact. Reviews preserve that generated plan.

## Demo

From the Agent Factory checkout:

```sh
make demo-start
make demo-issue-search
```

`demo-issue-search` creates a GitHub issue, adds it to Project 1 as `Ready`,
and applies `needs-agent`. Inspect the selected workflow and Work at
`http://127.0.0.1:7339/`, then inspect the linked GitHub issue and pull request.
Stop the isolated demo with `make demo-stop`.
