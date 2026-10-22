import Fastify from 'fastify';
import { pathToFileURL } from 'node:url';
import { loadConfig } from './config.js';
import { createProvider } from './providers/index.js';
import { runReview, validateReviewRequest } from './review.js';
import { runVerify, validateVerifyRequest } from './verify.js';

export function buildServer({ provider, config, logger = true }) {
  const app = Fastify({ logger, bodyLimit: 25 * 1024 * 1024 });
  const opts = () => ({
    concurrency: config.concurrency,
    maxRetries: config.maxRetries,
    maxOutputTokens: config.maxOutputTokens,
    retryBaseMs: config.retryBaseMs,
    log: app.log,
  });

  app.get('/healthz', async () => ({ ok: true, provider: provider.name, model: provider.model }));

  app.post('/review', async (req, reply) => {
    const problem = validateReviewRequest(req.body);
    if (problem) return reply.code(400).send({ error: problem });
    const started = Date.now();
    const result = await runReview(provider, req.body, opts());
    req.log.info(
      { repo: req.body.repo, chunks: req.body.chunks.length, findings: result.findings.length, errors: result.errors.length, usage: result.usage, ms: Date.now() - started },
      'review complete',
    );
    return result;
  });

  app.post('/verify', async (req, reply) => {
    const problem = validateVerifyRequest(req.body);
    if (problem) return reply.code(400).send({ error: problem });
    return runVerify(provider, req.body, opts());
  });

  return app;
}

async function main() {
  const config = loadConfig();
  const provider = createProvider(config);
  const app = buildServer({ provider, config });
  const shutdown = async () => {
    await app.close();
    process.exit(0);
  };
  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
  await app.listen({ port: config.port, host: '0.0.0.0' });
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
