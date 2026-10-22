import { VERIFY_SYSTEM_PROMPT, buildVerifyPrompt } from './prompts/verify.js';
import { normalizeVerifyResults, verifyJsonSchema } from './schema.js';
import { addUsage } from './review.js';
import { mapLimit, withRetry } from './util.js';

const BATCH_SIZE = 8;

export function validateVerifyRequest(body) {
  if (!body || typeof body !== 'object') return 'body must be a JSON object';
  if (!Array.isArray(body.items)) return 'items must be an array';
  for (const it of body.items) {
    if (typeof it?.id !== 'string' || !it.finding || typeof it.context !== 'string') {
      return 'each item needs id, finding and context';
    }
  }
  return null;
}

/**
 * Asks the model to confirm each finding against its code. Items the model fails to answer
 * (or batches that error) are returned as supported so verification never silently drops findings
 * because of an outage.
 */
export async function runVerify(provider, body, opts = {}) {
  const { concurrency = 4, maxRetries = 3, maxOutputTokens = 4096, log = console, retryBaseMs } = opts;
  const usage = { inputTokens: 0, outputTokens: 0 };
  const batches = [];
  for (let i = 0; i < body.items.length; i += BATCH_SIZE) batches.push(body.items.slice(i, i + BATCH_SIZE));

  const perBatch = await mapLimit(batches, concurrency, async (items) => {
    const ids = items.map((it) => it.id);
    try {
      const { data, usage: u } = await withRetry(
        () =>
          provider.generateJSON({
            system: VERIFY_SYSTEM_PROMPT,
            prompt: buildVerifyPrompt({ repo: body.repo ?? '', items }),
            schema: verifyJsonSchema,
            maxOutputTokens,
          }),
        { retries: maxRetries, baseMs: retryBaseMs },
      );
      addUsage(usage, u);
      const results = normalizeVerifyResults(data, ids);
      const answered = new Set(results.map((r) => r.id));
      for (const id of ids) {
        if (!answered.has(id)) results.push({ id, supported: true, reason: 'not verified (no answer)' });
      }
      return results;
    } catch (err) {
      addUsage(usage, err.usage);
      log.error?.({ err: err.message }, 'verify batch failed');
      return ids.map((id) => ({ id, supported: true, reason: 'not verified (error)' }));
    }
  });

  return { results: perBatch.flat(), usage };
}
