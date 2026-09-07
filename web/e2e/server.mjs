import {
  chmod,
  mkdir,
  mkdtemp,
  rm,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, join, resolve } from "node:path";
import { spawn } from "node:child_process";

const root = resolve(import.meta.dirname, "../..");
const temporary = await mkdtemp(join(tmpdir(), "factory-ui-e2e-"));
const serverBinary = join(temporary, "factory-server");
const workerBinary = join(temporary, "factory-worker");
const database = join(temporary, "server", "factory.sqlite3");
const workerData = join(temporary, "worker");
const workerConfig = join(temporary, "worker.toml");
const fakeBin = join(temporary, "bin");
const workerID = "11111111-1111-4111-8111-111111111111";

function run(command, args, options = {}) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command, args, {
      cwd: options.cwd ?? root,
      env: options.env ?? process.env,
      stdio: options.stdio ?? "inherit",
    });
    child.once("error", rejectRun);
    child.once("exit", (code, signal) => {
      if (code === 0) resolveRun();
      else rejectRun(new Error(`${command} exited with ${code ?? signal}`));
    });
  });
}

async function createRepository(name) {
  const origin = join(temporary, `${name}-origin.git`);
  const checkout = join(temporary, name);
  await run("git", ["init", "--bare", "--initial-branch=main", origin]);
  await run("git", ["clone", origin, checkout]);
  await run("git", ["config", "user.name", "Agent Factory browser test"], { cwd: checkout });
  await run("git", ["config", "user.email", "factory-browser@example.test"], { cwd: checkout });
  await writeFile(join(checkout, "README.md"), `# ${name}\n`);
  await run("git", ["add", "README.md"], { cwd: checkout });
  await run("git", ["commit", "-m", "test: initialize repository"], { cwd: checkout });
  await run("git", ["push", "--set-upstream", "origin", "main"], { cwd: checkout });
  return checkout;
}

async function createFakeCodex() {
  await mkdir(fakeBin, { recursive: true });
  const executable = join(fakeBin, "codex");
  await writeFile(
    executable,
    `#!/bin/sh
set -eu

if [ "\${1:-}" = "--version" ]; then
  echo "codex-cli 0.0.0-factory-e2e"
  exit 0
fi

if [ "\${1:-}" = "login" ] && [ "\${2:-}" = "status" ]; then
  echo "Logged in for deterministic Agent Factory browser tests"
  exit 0
fi

if [ "\${1:-}" != "exec" ]; then
  echo "unexpected fake Codex arguments: $*" >&2
  exit 2
fi

result_path=
previous=
for argument in "$@"; do
  if [ "$previous" = "--output-last-message" ]; then
    result_path=$argument
    break
  fi
  previous=$argument
done
if [ -z "$result_path" ]; then
  echo "fake Codex did not receive --output-last-message" >&2
  exit 2
fi

prompt=$(cat)
branch=$(git branch --show-current)
printf '%s\\n' '{"type":"progress","message":"Inspected the assigned repository."}'

case "$prompt" in
  *FACTORY_E2E_FAIL*)
    echo "Deterministic fake Codex failure." >&2
    exit 42
    ;;
  *FACTORY_E2E_WAIT*)
    printf '%s\\n' '{"type":"progress","message":"Waiting for operator cancellation."}'
    trap 'exit 143' TERM INT
    while :; do sleep 1; done
    ;;
esac

printf '%s\\n' "Created by the Agent Factory browser proof." > factory-proof.txt
printf '%s\\n' '{"type":"progress","message":"Created deterministic worktree evidence."}'
{
  printf '%s\\n' "Completed by deterministic fake Codex."
  printf 'Branch: %s\\n' "$branch"
  printf 'Worktree: %s\\n' "$PWD"
} > "$result_path"
`,
  );
  await chmod(executable, 0o755);
}

async function createFakeGH() {
  await mkdir(fakeBin, { recursive: true });
  const executable = join(fakeBin, "gh");
  await writeFile(
    executable,
    `#!/bin/sh
set -eu

if [ "\${1:-}" = "--version" ]; then
  echo "gh version 2.94.0 (Agent Factory browser fixture)"
  exit 0
fi

if [ "\${1:-}" = "auth" ] && [ "\${2:-}" = "status" ]; then
  echo "Logged in to github.com for deterministic Agent Factory browser tests"
  exit 0
fi

if [ "\${1:-}" = "issue" ] && [ "\${2:-}" = "list" ]; then
  printf '%s\n' '[{"number":184,"title":"Typed Automation browser fixture","url":"https://github.com/example/automation-fixture/issues/184","state":"OPEN","labels":[{"id":"label-ready","name":"needs-agent","description":"","color":"ffffff"}]}]'
  exit 0
fi

if [ "\${1:-}" = "pr" ] && [ "\${2:-}" = "list" ]; then
  printf '%s\n' '[{"number":185,"title":"Typed pull-request Automation browser fixture","url":"https://github.com/example/automation-fixture/pull/185","state":"OPEN","isDraft":false,"baseRefName":"main","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","labels":[{"id":"label-review","name":"needs-agent","description":"","color":"ffffff"}]}]'
  exit 0
fi

if [ "\${1:-}" = "repo" ] && [ "\${2:-}" = "clone" ]; then
  case "\${3:-}" in
    example/factory-demo)
      origin=${JSON.stringify(join(temporary, "factory-demo-origin.git"))}
      ;;
    example/handbook-demo)
      origin=${JSON.stringify(join(temporary, "handbook-demo-origin.git"))}
      ;;
    example/managed-demo)
      origin=${JSON.stringify(join(temporary, "managed-demo-origin.git"))}
      ;;
    *)
      echo "unexpected fake gh repository: \${3:-}" >&2
      exit 2
      ;;
  esac
  git clone --no-checkout "$origin" "\${4:-}"
  git -C "\${4:-}" remote set-url origin "git@github.com:\${3:-}.git"
  exit 0
fi

if [ "\${1:-}" = "api" ]; then
  case "\${2:-}" in
    repos/*/git/ref/heads/main)
      printf '%s\n' '{"object":{"sha":"browser-test-commit"}}'
      ;;
    repos/*/git/trees/*)
      printf '%s\n' '{"tree":[]}'
      ;;
    repos/*)
      printf '%s\n' '{"default_branch":"main"}'
      ;;
    *)
      echo "unexpected fake gh API path: \${2:-}" >&2
      exit 2
      ;;
  esac
  exit 0
fi

echo "unexpected fake gh arguments: $*" >&2
exit 2
`,
  );
  await chmod(executable, 0o755);
}

async function createFakeSSH() {
  await mkdir(fakeBin, { recursive: true });
  const executable = join(fakeBin, "ssh");
  await writeFile(
    executable,
    `#!/bin/sh
set -eu

case "$*" in
  *"git-upload-pack 'example/factory-demo.git'"*)
    exec git-upload-pack ${JSON.stringify(join(temporary, "factory-demo-origin.git"))}
    ;;
  *"git-receive-pack 'example/factory-demo.git'"*)
    exec git-receive-pack ${JSON.stringify(join(temporary, "factory-demo-origin.git"))}
    ;;
  *"git-upload-pack 'example/handbook-demo.git'"*)
    exec git-upload-pack ${JSON.stringify(join(temporary, "handbook-demo-origin.git"))}
    ;;
  *"git-receive-pack 'example/handbook-demo.git'"*)
    exec git-receive-pack ${JSON.stringify(join(temporary, "handbook-demo-origin.git"))}
    ;;
  *"git-upload-pack 'example/managed-demo.git'"*)
    exec git-upload-pack ${JSON.stringify(join(temporary, "managed-demo-origin.git"))}
    ;;
  *"git-receive-pack 'example/managed-demo.git'"*)
    exec git-receive-pack ${JSON.stringify(join(temporary, "managed-demo-origin.git"))}
    ;;
esac

echo "unexpected fake ssh arguments: $*" >&2
exit 2
`,
  );
  await chmod(executable, 0o755);
}

await Promise.all([
  run("go", ["build", "-o", serverBinary, "./cmd/factory-server"]),
  run("go", ["build", "-o", workerBinary, "./cmd/factory-worker"]),
  createFakeCodex(),
  createFakeGH(),
  createFakeSSH(),
]);
const [factoryRepository, handbookRepository] = await Promise.all([
  createRepository("factory-demo"),
  createRepository("handbook-demo"),
  createRepository("managed-demo"),
]);

await mkdir(workerData, { recursive: true });
await writeFile(join(workerData, "worker-id"), `${workerID}\n`, { mode: 0o600 });
await writeFile(
  workerConfig,
  `server = "http://127.0.0.1:17437"
name = "Real local worker"
max_concurrent = 1
data_directory = ${JSON.stringify(workerData)}

[repositories.factory-demo]
path = ${JSON.stringify(factoryRepository)}

[repositories.handbook-demo]
path = ${JSON.stringify(handbookRepository)}
`,
);

const server = spawn(
  serverBinary,
  ["-listen", "127.0.0.1:17437", "-database", database],
  {
    cwd: root,
    env: {
      ...process.env,
      HOME: temporary,
      FACTORY_DATA_HOME: temporary,
      PATH: `${fakeBin}${delimiter}${process.env.PATH ?? ""}`,
    },
    stdio: "inherit",
  },
);

async function waitForServer() {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch("http://127.0.0.1:17437/healthz");
      if (response.ok) return;
    } catch {
      // The server may still be starting.
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error("Factory browser test server did not become healthy");
}

async function createManagedRepository(remoteIdentity) {
  const response = await fetch("http://127.0.0.1:17437/api/v1/repositories", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ remote_identity: remoteIdentity }),
  });
  if (!response.ok) throw new Error(`Could not provision ${remoteIdentity}: ${response.status}`);
}

await waitForServer();
await Promise.all([
  createManagedRepository("github.com/example/factory-demo"),
  createManagedRepository("github.com/example/handbook-demo"),
  createManagedRepository("github.com/example/managed-demo"),
]);

const worker = spawn(
  workerBinary,
  ["--config", workerConfig],
  {
    cwd: root,
    env: {
      ...process.env,
      HOME: temporary,
      FACTORY_DATA_HOME: temporary,
      PATH: `${fakeBin}${delimiter}${process.env.PATH ?? ""}`,
    },
    stdio: "inherit",
  },
);

let stopping = false;
async function stopChild(child, signal) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  await new Promise((resolveStop) => {
    child.once("exit", resolveStop);
    if (!child.kill(signal)) resolveStop();
  });
}

async function stop(signal = "SIGTERM", exitCode = 0) {
  if (stopping) return;
  stopping = true;
  await stopChild(worker, signal);
  await stopChild(server, signal);
  await rm(temporary, { recursive: true, force: true });
  process.exit(exitCode);
}

process.on("SIGINT", () => void stop("SIGINT"));
process.on("SIGTERM", () => void stop("SIGTERM"));
server.once("exit", (code) => {
  if (!stopping) void stop("SIGTERM", code ?? 1);
});
worker.once("exit", (code) => {
  if (!stopping) void stop("SIGTERM", code ?? 1);
});
