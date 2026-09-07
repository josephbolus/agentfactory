# Agent Factory Demo

A tiny dependency-free Node.js storefront used to demonstrate Agent Factory's GitHub-issue workflow.

Run `npm test` or `npm start`. It intentionally contains two bugs: case-sensitive product search and cart totals that ignore quantity.

## Live Agent Factory demo: fix the product search

Use three terminals from the Agent Factory checkout.

### 1. Show the bug

```sh
cd demo/factory-demo
git switch main
git pull --ff-only
npm start
```

Open http://127.0.0.1:3000 and type `factory` into Search products. No result
appears even though **Agent Factory T-Shirt** exists: the browser-visible bug is that
search is case-sensitive.

### 2. Start Agent Factory and its GitHub label poller

In two other terminals, at the root Agent Factory checkout:

```sh
make demo-start
```

```sh
make demo-autopoll
```

`demo-autopoll` checks GitHub every 30 seconds. Open the Agent Factory UI at
http://127.0.0.1:7339/.

### 3. Create the GitHub trigger

In a third terminal at the Agent Factory checkout:

```sh
make demo-issue-search
```

That command creates the issue, adds it to the **Agent Factory** GitHub Project,
sets its status to **Ready**, then applies `needs-agent`. Within 30 seconds the
poller creates Agent Factory work. The agent moves the Project item through:

```text
Ready + needs-agent -> In Progress -> Review + needs-human
```

It runs tests and opens a pull request. Agent Factory does not merge or deploy; the
human review boundary remains intact.

Repository workflows live under `.factory/workflows/` as Markdown with YAML
frontmatter. An issue carrying `team:dba` selects `dba/index-review`; create
that routed example from the Factory checkout with:

```sh
make demo-issue-dba
```

### 4. Prove the proposed fix in the browser

After Agent Factory opens its pull request, stop the earlier `npm start` process and
run:

```sh
cd demo/factory-demo
git pull --ff-only
git checkout factory/8c4d9686-3b2-17017a9a-f38
npm start
```

Refresh http://127.0.0.1:3000 and enter `factory` again. **Agent Factory T-Shirt**
now appears. This is the existing Agent Factory-created fix branch from
https://github.com/josephbolus/agentfactory-demo/pull/4. A new demo run will create
its own branch and pull request with different identifiers.

To return to the baseline app afterwards:

```sh
git switch main
```

## Second demo issue

Use `make demo-issue-total` to create the cart-total task. It follows the same
GitHub/Agent Factory flow. This bug currently lives in `calculateTotal` and is best
demonstrated through its regression test rather than the storefront UI.
