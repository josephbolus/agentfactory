---
id: dba/index-review
title: Review database indexes
description: Inspect index coverage and query plans before proposing a change.
github_issue:
  labels_all:
    - team:dba
---

# Review database indexes

Use the authenticated `gh` CLI to re-read the live issue. Find its item in the
**Factory** GitHub Project. Confirm it is **Ready** and has `needs-agent`, then
move it to **In Progress** before investigating.

Inspect the issue and the database schema. Reproduce the reported behavior,
check index coverage and representative query plans, and make the smallest
safe change. Add a focused regression test or explain why one is not practical.
Run the relevant checks and open one tested pull request for human review. Then
move the Project item to **Review**, remove `needs-agent`, and add `needs-human`.

If the request is unsafe, contradictory, or blocked by missing repository
evidence, comment with the exact blocker. Then move the Project item to
**Review**, remove `needs-agent`, and add `needs-human`. Do not invent schema,
queries, or product requirements.
