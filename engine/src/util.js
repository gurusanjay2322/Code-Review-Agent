/** Runs fn over items with at most `limit` in flight, preserving result order. */
export async function mapLimit(items, limit, fn) {
  const results = new Array(items.length);
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const i = next++;
      results[i] = await fn(items[i], i);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Retries fn while it throws errors marked retryable, with exponential backoff and jitter. */
export async function withRetry(fn, { retries = 3, baseMs = 1000, onRetry } = {}) {
  for (let attempt = 0; ; attempt++) {
    try {
      return await fn(attempt);
    } catch (err) {
      if (!err?.retryable || attempt >= retries) throw err;
      const delay = baseMs * 2 ** attempt + Math.floor(Math.random() * baseMs);
      onRetry?.(err, attempt + 1, delay);
      await sleep(delay);
    }
  }
}
