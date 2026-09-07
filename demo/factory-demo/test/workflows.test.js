const assert = require('node:assert/strict');
const { readFile } = require('node:fs/promises');
const { join } = require('node:path');
const test = require('node:test');

const workflowRoot = process.env.WORKFLOW_ROOT || join(__dirname, '..');
const workflows = [
  {
    name: 'implement.md',
    id: 'implement',
    title: 'Implement a ready ticket',
    description: 'Implement an approved issue and hand a tested pull request to a human.',
    labels: [],
  },
  {
    name: 'triage.md',
    id: 'triage',
    title: 'Triage and refine a new ticket',
    description: 'Turn an incoming issue into an implementation-ready specification.',
    labels: ['factory:ready-for-spec'],
  },
  {
    name: 'bug-finder.md',
    id: 'bug-finder',
    title: 'Find real bugs in the code',
    description: 'Find one defensible defect and route it for implementation.',
    labels: ['factory:bug-finder'],
  },
  {
    name: 'dba/index-review.md',
    id: 'dba/index-review',
    title: 'Review database indexes',
    description: 'Inspect index coverage and query plans before proposing a change.',
    labels: ['team:dba'],
  },
];

for (const { name, id, title, description, labels } of workflows) {
  test(`${name} declares workflow metadata`, async () => {
    const path = join(workflowRoot, '.factory', 'workflows', name);
    const content = await readFile(path, 'utf8');
    const end = content.indexOf('\n---\n', 4);

    assert.ok(content.startsWith('---\n'), 'frontmatter must start the file');
    assert.ok(end > 4, 'frontmatter must have a closing delimiter');

    const expected = [
      `id: ${id}`,
      `title: ${title}`,
      `description: ${description}`,
    ];
    if (labels.length) {
      expected.push('github_issue:', '  labels_all:');
      expected.push(...labels.map((label) => `    - ${label}`));
    }

    const metadata = content.slice(4, end).split('\n');
    assert.deepEqual(metadata, expected);
  });
}

test('DBA workflow routes blocked work to human review', async () => {
  const path = join(workflowRoot, '.factory', 'workflows', 'dba', 'index-review.md');
  const content = await readFile(path, 'utf8');

  for (const expected of [
    'move it to **In Progress**',
    'move the Project item to **Review**',
    'remove `needs-agent`',
    'add `needs-human`',
  ]) {
    assert.match(content, new RegExp(expected.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
});
