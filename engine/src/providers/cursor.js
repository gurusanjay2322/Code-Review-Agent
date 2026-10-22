import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Agent, CursorSdkError } from '@cursor/sdk';
import { parseJsonText } from '../schema.js';

/**
 * Runs each request as a one-shot local Cursor agent with no tools, so the model can only answer
 * from the code in the prompt. The agent's cwd is an empty scratch directory: nothing on the
 * engine host is exposed and no project rules are loaded.
 */
export function createCursorProvider({ apiKey, model, prompt = Agent.prompt.bind(Agent), workdir }) {
  if (!apiKey) throw new Error('CURSOR_API_KEY is required for the cursor provider');
  const cwd = workdir ?? mkdtempSync(join(tmpdir(), 'review-engine-'));

  return {
    name: 'cursor',
    model,
    async generateJSON({ system, prompt: userPrompt, schema }) {
      const message = [
        system,
        'Respond with a single JSON object and nothing else: no prose, no markdown fences.',
        `The JSON must match this JSON Schema:\n${JSON.stringify(schema)}`,
        '---',
        userPrompt,
      ].join('\n\n');

      let result;
      try {
        result = await prompt(message, {
          apiKey,
          model: { id: model },
          local: { cwd, settingSources: [] },
          tools: [],
          name: 'pr-review-engine',
        });
      } catch (err) {
        err.retryable = err instanceof CursorSdkError ? err.isRetryable : true;
        throw err;
      }

      const usage = {
        inputTokens: result.usage?.inputTokens ?? 0,
        outputTokens: result.usage?.outputTokens ?? 0,
      };
      if (result.status !== 'finished') {
        const err = new Error(`cursor run ${result.id} ${result.status}: ${result.error?.message ?? 'no details'}`);
        err.retryable = result.status === 'error';
        err.usage = usage;
        throw err;
      }
      try {
        return { data: parseJsonText(result.result), usage };
      } catch (parseErr) {
        parseErr.retryable = true;
        parseErr.usage = usage;
        throw parseErr;
      }
    },
  };
}
