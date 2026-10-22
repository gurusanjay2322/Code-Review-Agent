import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createCursorProvider } from '../src/providers/cursor.js';
import { findingsJsonSchema, normalizeFindings } from '../src/schema.js';
import { loadConfig } from '../src/config.js';

const recorded = readFileSync(new URL('./fixtures/gemini-review-response.txt', import.meta.url), 'utf8');

function stubPrompt(result) {
  const calls = [];
  const fn = async (message, options) => {
    calls.push({ message, options });
    return typeof result === 'function' ? result() : result;
  };
  fn.calls = calls;
  return fn;
}

const usage = { inputTokens: 1200, outputTokens: 300, cacheReadTokens: 0, cacheWriteTokens: 0, totalTokens: 1500 };

test('runs a tool-less local agent and parses its JSON answer', async () => {
  const prompt = stubPrompt({ id: 'run-1', status: 'finished', result: recorded, usage });
  const provider = createCursorProvider({ apiKey: 'key', model: 'composer-2.5', prompt, workdir: '/tmp/empty' });

  const { data, usage: u } = await provider.generateJSON({ system: 'SYSTEM', prompt: 'CODE', schema: findingsJsonSchema });
  assert.equal(normalizeFindings(data, ['store/user.go']).length, 2);
  assert.deepEqual(u, { inputTokens: 1200, outputTokens: 300 });

  const [{ message, options }] = prompt.calls;
  assert.ok(message.startsWith('SYSTEM') && message.endsWith('CODE'));
  assert.ok(message.includes('"findings"'), 'schema is embedded in the prompt');
  assert.deepEqual(options.tools, []);
  assert.deepEqual(options.model, { id: 'composer-2.5' });
  assert.deepEqual(options.local, { cwd: '/tmp/empty', settingSources: [] });
  assert.equal(options.apiKey, 'key');
});

test('a failed run is retryable and carries usage', async () => {
  const prompt = stubPrompt({ id: 'run-2', status: 'error', error: { message: 'stalled' }, usage });
  const provider = createCursorProvider({ apiKey: 'key', model: 'm', prompt, workdir: '.' });
  await assert.rejects(provider.generateJSON({ system: '', prompt: '', schema: {} }), (err) => {
    assert.equal(err.retryable, true);
    assert.equal(err.usage.inputTokens, 1200);
    assert.match(err.message, /run-2 error: stalled/);
    return true;
  });
});

test('a cancelled run is not retried', async () => {
  const prompt = stubPrompt({ id: 'run-3', status: 'cancelled' });
  const provider = createCursorProvider({ apiKey: 'key', model: 'm', prompt, workdir: '.' });
  await assert.rejects(provider.generateJSON({ system: '', prompt: '', schema: {} }), (err) => err.retryable === false);
});

test('non-JSON answers are retryable', async () => {
  const prompt = stubPrompt({ id: 'run-4', status: 'finished', result: 'I found no issues.' });
  const provider = createCursorProvider({ apiKey: 'key', model: 'm', prompt, workdir: '.' });
  await assert.rejects(provider.generateJSON({ system: '', prompt: '', schema: {} }), (err) => err.retryable === true);
});

test('requires an API key and defaults the model per provider', () => {
  assert.throws(() => createCursorProvider({ apiKey: '', model: 'm' }), /CURSOR_API_KEY/);
  assert.equal(loadConfig({ LLM_PROVIDER: 'cursor', CURSOR_API_KEY: ' k ' }).model, 'composer-2.5');
  assert.equal(loadConfig({ LLM_PROVIDER: 'cursor', CURSOR_API_KEY: ' k ' }).cursorApiKey, 'k');
  assert.equal(loadConfig({}).model, 'gemini-3.6-flash');
  assert.equal(loadConfig({ LLM_PROVIDER: 'cursor', LLM_MODEL: 'auto' }).model, 'auto');
});
