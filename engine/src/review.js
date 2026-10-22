import { REVIEW_SYSTEM_PROMPT, buildReviewPrompt } from './prompts/review.js';
import { findingsJsonSchema, normalizeFindings } from './schema.js';
import { mapLimit, withRetry } from './util.js';

export function validateReviewRequest(body) {
  if (!body || typeof body !== 'object') return 'body must be a JSON object';
  if (typeof body.repo !== 'string' || !body.repo) return 'repo is required';
  if (!Array.isArray(body.chunks)) return 'chunks must be an array';
  for (const c of body.chunks) {
    if (!c || typeof c.id !== 'string' || !Array.isArray(c.files) || c.files.length === 0) {
      return 'each chunk needs an id and a non-empty files array';
    }
    for (const f of c.files) {
      if (typeof f?.path !== 'string' || typeof f?.content !== 'string') return 'each file needs path and content';
    }
  }
  return null;
}

/**
 * Reviews every chunk with bounded concurrency. A failed chunk is reported in `errors`
 * instead of failing the whole review; chunks beyond maxTotalTokens are reported in `skippedChunks`.
 */
export async function runReview(provider, body, opts = {}) {
  const { concurrency = 4, maxRetries = 3, maxOutputTokens = 8192, log = console, retryBaseMs } = opts;
  const maxTotal = Number(body.maxTotalTokens) || 0;
  const usage = { inputTokens: 0, outputTokens: 0 };
  const errors = [];
  const skippedChunks = [];
  let reserved = 0;

  const perChunk = await mapLimit(body.chunks, concurrency, async (chunk) => {
    const estimate = Number(chunk.tokens) || 0;
    if (maxTotal && reserved + estimate > maxTotal) {
      skippedChunks.push(chunk.id);
      return [];
    }
    reserved += estimate;

    const prompt = buildReviewPrompt({ ...body, chunk });
    try {
      const { data, usage: u } = await withRetry(
        () => provider.generateJSON({ system: REVIEW_SYSTEM_PROMPT, prompt, schema: findingsJsonSchema, maxOutputTokens }),
        {
          retries: maxRetries,
          baseMs: retryBaseMs,
          onRetry: (err, attempt, delay) => {
            addUsage(usage, err.usage);
            log.warn?.({ chunk: chunk.id, attempt, delay, err: err.message }, 'retrying chunk');
          },
        },
      );
      addUsage(usage, u);
      reserved += (u?.inputTokens ?? 0) - estimate;
      return normalizeFindings(data, chunk.files.map((f) => f.path));
    } catch (err) {
      addUsage(usage, err.usage);
      log.error?.({ chunk: chunk.id, err: err.message }, 'chunk review failed');
      errors.push({ chunkId: chunk.id, error: err.message });
      return [];
    }
  });

  return { findings: perChunk.flat(), usage, errors, skippedChunks };
}

export function addUsage(total, u) {
  if (!u) return;
  total.inputTokens += u.inputTokens ?? 0;
  total.outputTokens += u.outputTokens ?? 0;
}
