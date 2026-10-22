function int(env, name, def) {
  const v = Number.parseInt(env[name] ?? '', 10);
  return Number.isFinite(v) ? v : def;
}

const DEFAULT_MODELS = {
  gemini: 'gemini-3.6-flash',
  cursor: 'composer-2.5',
};

export function loadConfig(env = process.env) {
  const provider = (env.LLM_PROVIDER || 'gemini').toLowerCase();
  return {
    port: int(env, 'ENGINE_PORT', 8090),
    provider,
    model: env.LLM_MODEL || DEFAULT_MODELS[provider] || '',
    geminiApiKey: env.GEMINI_API_KEY || '',
    cursorApiKey: (env.CURSOR_API_KEY || '').trim(),
    concurrency: Math.max(1, int(env, 'ENGINE_CONCURRENCY', 4)),
    maxRetries: Math.max(0, int(env, 'ENGINE_MAX_RETRIES', 3)),
    maxOutputTokens: int(env, 'ENGINE_MAX_OUTPUT_TOKENS', 8192),
  };
}
