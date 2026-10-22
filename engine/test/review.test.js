import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runReview } from '../src/review.js';
import { runVerify } from '../src/verify.js';
import { buildServer } from '../src/server.js';
import { parseJsonText } from '../src/schema.js';

const recorded = readFileSync(new URL('./fixtures/gemini-review-response.txt', import.meta.url), 'utf8');
const quiet = { warn() {}, error() {} };

function recordedProvider(text, { failTimes = 0, retryable = true } = {}) {
  let calls = 0;
  return {
    name: 'recorded',
    model: 'test',
    get calls() {
      return calls;
    },
    async generateJSON() {
      calls++;
      if (calls <= failTimes) {
        const err = new Error('503 overloaded');
        err.retryable = retryable;
        throw err;
      }
      return { data: parseJsonText(text), usage: { inputTokens: 100, outputTokens: 20 } };
    },
  };
}

const chunk = (id, path = 'store/user.go', tokens = 50) => ({ id, tokens, files: [{ path, language: 'go', content: '    9 +| code\n' }] });

test('reviews chunks from a recorded response', async () => {
  const provider = recordedProvider(recorded);
  const res = await runReview(provider, { repo: 'acme/api', prTitle: 't', chunks: [chunk('c1')] }, { log: quiet });
  assert.equal(res.findings.length, 2);
  assert.deepEqual(res.usage, { inputTokens: 100, outputTokens: 20 });
  assert.deepEqual(res.errors, []);
});

test('retries retryable errors then succeeds', async () => {
  const provider = recordedProvider(recorded, { failTimes: 2 });
  const res = await runReview(provider, { repo: 'r', chunks: [chunk('c1')] }, { maxRetries: 3, retryBaseMs: 1, log: quiet });
  assert.equal(provider.calls, 3);
  assert.equal(res.findings.length, 2);
});

test('reports a failed chunk without failing the others', async () => {
  let call = 0;
  const provider = {
    async generateJSON() {
      call++;
      if (call === 1) {
        const err = new Error('bad request');
        err.retryable = false;
        throw err;
      }
      return { data: parseJsonText(recorded), usage: {} };
    },
  };
  const res = await runReview(provider, { repo: 'r', chunks: [chunk('c1'), chunk('c2')] }, { concurrency: 1, log: quiet });
  assert.equal(res.errors.length, 1);
  assert.equal(res.errors[0].chunkId, 'c1');
  assert.equal(res.findings.length, 2);
});

test('skips chunks beyond the per-PR token cap', async () => {
  const provider = recordedProvider(recorded);
  const body = { repo: 'r', maxTotalTokens: 120, chunks: [chunk('c1', undefined, 100), chunk('c2', undefined, 100)] };
  const res = await runReview(provider, body, { concurrency: 1, log: quiet });
  assert.deepEqual(res.skippedChunks, ['c2']);
  assert.equal(provider.calls, 1);
});

test('verify keeps findings when the model fails', async () => {
  const provider = {
    async generateJSON() {
      const err = new Error('down');
      err.retryable = false;
      throw err;
    },
  };
  const items = [{ id: '0', finding: { path: 'a', line: 1 }, context: 'x' }];
  const res = await runVerify(provider, { repo: 'r', items }, { log: quiet });
  assert.deepEqual(res.results.map((r) => r.supported), [true]);
});

test('verify drops what the model rejects', async () => {
  const provider = recordedProvider(JSON.stringify({ results: [{ id: '0', supported: false, reason: 'handled above' }] }));
  const items = [
    { id: '0', finding: { path: 'a', line: 1 }, context: 'x' },
    { id: '1', finding: { path: 'a', line: 2 }, context: 'y' },
  ];
  const res = await runVerify(provider, { repo: 'r', items }, { log: quiet });
  assert.deepEqual(
    res.results.map((r) => [r.id, r.supported]),
    [['0', false], ['1', true]],
  );
});

test('HTTP API validates input and returns findings', async () => {
  const app = buildServer({ provider: recordedProvider(recorded), config: { concurrency: 2, maxRetries: 0 }, logger: false });
  const bad = await app.inject({ method: 'POST', url: '/review', payload: { repo: 'r', chunks: [{ id: 'c' }] } });
  assert.equal(bad.statusCode, 400);

  const ok = await app.inject({ method: 'POST', url: '/review', payload: { repo: 'r', chunks: [chunk('c1')] } });
  assert.equal(ok.statusCode, 200);
  assert.equal(ok.json().findings.length, 2);

  const health = await app.inject({ method: 'GET', url: '/healthz' });
  assert.equal(health.json().ok, true);
  await app.close();
});
