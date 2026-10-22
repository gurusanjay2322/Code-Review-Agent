import { createCursorProvider } from './cursor.js';
import { createGeminiProvider } from './gemini.js';

/**
 * A provider implements:
 *   generateJSON({ system, prompt, schema, maxOutputTokens })
 *     -> Promise<{ data: object, usage: { inputTokens, outputTokens } }>
 * and throws errors with `retryable: boolean` set.
 */
export function createProvider(config) {
  switch (config.provider) {
    case 'gemini':
      return createGeminiProvider({ apiKey: config.geminiApiKey, model: config.model });
    case 'cursor':
      return createCursorProvider({ apiKey: config.cursorApiKey, model: config.model });
    default:
      throw new Error(`unsupported LLM_PROVIDER "${config.provider}"`);
  }
}
