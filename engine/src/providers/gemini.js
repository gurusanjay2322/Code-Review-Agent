import { GoogleGenAI, ApiError } from '@google/genai';
import { parseJsonText } from '../schema.js';

const RETRYABLE_STATUS = new Set([408, 429, 500, 502, 503, 504]);

export function createGeminiProvider({ apiKey, model, client }) {
  if (!client && !apiKey) throw new Error('GEMINI_API_KEY is required for the gemini provider');
  const ai = client ?? new GoogleGenAI({ apiKey });

  return {
    name: 'gemini',
    model,
    async generateJSON({ system, prompt, schema, maxOutputTokens }) {
      let response;
      try {
        response = await ai.models.generateContent({
          model,
          contents: prompt,
          config: {
            systemInstruction: system,
            responseMimeType: 'application/json',
            responseJsonSchema: schema,
            temperature: 0.1,
            maxOutputTokens,
          },
        });
      } catch (err) {
        const status = err instanceof ApiError ? err.status : err?.status;
        err.retryable = status ? RETRYABLE_STATUS.has(status) : isNetworkError(err);
        throw err;
      }
      const usage = {
        inputTokens: response.usageMetadata?.promptTokenCount ?? 0,
        outputTokens: response.usageMetadata?.candidatesTokenCount ?? 0,
      };
      const text = response.text;
      if (!text) {
        const reason = response.candidates?.[0]?.finishReason ?? 'unknown';
        const err = new Error(`empty model response (finishReason=${reason})`);
        err.retryable = reason !== 'SAFETY';
        err.usage = usage;
        throw err;
      }
      try {
        return { data: parseJsonText(text), usage };
      } catch (parseErr) {
        parseErr.retryable = true;
        parseErr.usage = usage;
        throw parseErr;
      }
    },
  };
}

function isNetworkError(err) {
  return ['ECONNRESET', 'ETIMEDOUT', 'ENOTFOUND', 'EAI_AGAIN', 'UND_ERR_SOCKET'].includes(err?.code ?? err?.cause?.code);
}
