import { pathToFileURL } from 'node:url';
import { loadConfig } from './config.js';
import { createProvider } from './providers/index.js';
import { runReview, validateReviewRequest } from './review.js';
import { runVerify, validateVerifyRequest } from './verify.js';

const COMMANDS = {
  review: { validate: validateReviewRequest, run: runReview },
  verify: { validate: validateVerifyRequest, run: runVerify },
};

const stderrLog = {
  warn: (obj, msg) => process.stderr.write(`warn: ${msg} ${JSON.stringify(obj)}\n`),
  error: (obj, msg) => process.stderr.write(`error: ${msg} ${JSON.stringify(obj)}\n`),
};

async function readAll(stream) {
  const chunks = [];
  for await (const c of stream) chunks.push(typeof c === 'string' ? Buffer.from(c) : c);
  return Buffer.concat(chunks).toString('utf8');
}

/**
 * Runs one engine command: request JSON on stdin, response JSON on stdout.
 * Returns the process exit code (0 ok, 2 usage or bad input, 1 failure).
 */
export async function runCli({ argv, stdin, stdout, stderr, provider, config, log = stderrLog }) {
  const command = COMMANDS[argv[0]];
  if (!command) {
    stderr.write(`usage: cli.js <${Object.keys(COMMANDS).join('|')}> < request.json\n`);
    return 2;
  }
  let body;
  try {
    body = JSON.parse(await readAll(stdin));
  } catch (err) {
    stderr.write(`invalid request JSON: ${err.message}\n`);
    return 2;
  }
  const problem = command.validate(body);
  if (problem) {
    stderr.write(`invalid request: ${problem}\n`);
    return 2;
  }
  const result = await command.run(provider, body, {
    concurrency: config.concurrency,
    maxRetries: config.maxRetries,
    maxOutputTokens: config.maxOutputTokens,
    log,
  });
  stdout.write(JSON.stringify(result));
  return 0;
}

async function main() {
  const argv = process.argv.slice(2);
  if (!COMMANDS[argv[0]]) {
    return runCli({ argv, stdin: process.stdin, stdout: process.stdout, stderr: process.stderr });
  }
  const config = loadConfig();
  const provider = createProvider(config);
  return runCli({ argv, stdin: process.stdin, stdout: process.stdout, stderr: process.stderr, provider, config });
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  main().then(
    (code) => {
      process.exitCode = code;
    },
    (err) => {
      process.stderr.write(`${err?.stack ?? err}\n`);
      process.exitCode = 1;
    },
  );
}
