import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Readable, Writable } from 'node:stream';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { runCli } from '../src/cli.js';
import { parseJsonText } from '../src/schema.js';

const recorded = readFileSync(new URL('./fixtures/gemini-review-response.txt', import.meta.url), 'utf8');
const cliPath = fileURLToPath(new URL('../src/cli.js', import.meta.url));
const config = { concurrency: 2, maxRetries: 0, maxOutputTokens: 100 };
const quiet = { warn() {}, error() {} };

function sink() {
  let data = '';
  const stream = new Writable({
    write(chunk, _enc, cb) {
      data += chunk;
      cb();
    },
  });
  return { stream, text: () => data };
}

const provider = {
  async generateJSON({ schema }) {
    if (schema.properties.results) {
      return { data: { results: [{ id: '0', supported: false, reason: 'no' }] }, usage: {} };
    }
    return { data: parseJsonText(recorded), usage: { inputTokens: 10, outputTokens: 5 } };
  },
};

async function run(argv, input) {
  const out = sink();
  const err = sink();
  const code = await runCli({ argv, stdin: Readable.from([input]), stdout: out.stream, stderr: err.stream, provider, config, log: quiet });
  return { code, stdout: out.text(), stderr: err.text() };
}

test('review round-trips JSON over stdin and stdout', async () => {
  const req = { repo: 'acme/api', chunks: [{ id: 'c1', files: [{ path: 'store/user.go', content: 'x' }] }] };
  const { code, stdout } = await run(['review'], JSON.stringify(req));
  assert.equal(code, 0);
  const res = JSON.parse(stdout);
  assert.equal(res.findings.length, 2);
  assert.deepEqual(res.usage, { inputTokens: 10, outputTokens: 5 });
});

test('verify round-trips JSON', async () => {
  const req = { repo: 'r', items: [{ id: '0', finding: { path: 'a', line: 1 }, context: 'x' }] };
  const { code, stdout } = await run(['verify'], JSON.stringify(req));
  assert.equal(code, 0);
  assert.equal(JSON.parse(stdout).results[0].supported, false);
});

test('bad input exits 2 without writing stdout', async () => {
  for (const [argv, input] of [[['review'], 'not json'], [['review'], '{"repo":"r"}']]) {
    const { code, stdout, stderr } = await run(argv, input);
    assert.equal(code, 2);
    assert.equal(stdout, '');
    assert.ok(stderr.length > 0);
  }
});

test('the real process rejects unknown commands', () => {
  const res = spawnSync(process.execPath, [cliPath, 'explode'], { input: '{}', encoding: 'utf8' });
  assert.equal(res.status, 2);
  assert.match(res.stderr, /usage/);
});
