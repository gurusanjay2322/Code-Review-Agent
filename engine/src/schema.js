export const SEVERITIES = ['critical', 'high', 'medium', 'low'];
export const CATEGORIES = ['bug', 'security', 'logic', 'performance'];

export const findingsJsonSchema = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          path: { type: 'string' },
          line: { type: 'integer', description: 'New-file line number where the problem is.' },
          endLine: { type: 'integer', description: 'Last line of a multi-line problem; omit for one line.' },
          severity: { type: 'string', enum: SEVERITIES },
          category: { type: 'string', enum: CATEGORIES },
          title: { type: 'string' },
          explanation: { type: 'string' },
          evidence: { type: 'string', description: 'Exact code copied from the cited lines, without line numbers or markers.' },
          suggestion: { type: 'string', description: 'Replacement code for line..endLine only; empty if none.' },
          confidence: { type: 'number', minimum: 0, maximum: 1 },
        },
        required: ['path', 'line', 'severity', 'category', 'title', 'explanation', 'evidence', 'confidence'],
      },
    },
  },
  required: ['findings'],
};

export const verifyJsonSchema = {
  type: 'object',
  properties: {
    results: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'string' },
          supported: { type: 'boolean' },
          reason: { type: 'string' },
        },
        required: ['id', 'supported', 'reason'],
      },
    },
  },
  required: ['results'],
};

const CATEGORY_ALIASES = {
  bugs: 'bug', correctness: 'bug', error: 'bug', crash: 'bug',
  vulnerability: 'security', sec: 'security',
  'logic error': 'logic', 'logic-error': 'logic', logical: 'logic',
  perf: 'performance', efficiency: 'performance',
};

function str(v, max = 4000) {
  return typeof v === 'string' ? v.trim().slice(0, max) : '';
}

function stripLineMarkers(code) {
  // Models sometimes copy the "  42 +| " prefixes from the prompt into evidence.
  return code
    .split('\n')
    .map((l) => l.replace(/^\s*\d+ [+ ]\| ?/, '').replace(/^\s+-\| ?/, ''))
    .join('\n');
}

/**
 * Validates and normalizes raw model output. Returns findings whose path belongs to the chunk;
 * anything malformed is dropped rather than repaired.
 */
export function normalizeFindings(raw, allowedPaths) {
  const list = Array.isArray(raw?.findings) ? raw.findings : Array.isArray(raw) ? raw : [];
  const allowed = new Set(allowedPaths);
  const out = [];
  for (const f of list) {
    if (!f || typeof f !== 'object') continue;
    const path = str(f.path, 500);
    const line = Number.parseInt(f.line, 10);
    if (!allowed.has(path) || !Number.isInteger(line) || line < 1) continue;

    let endLine = Number.parseInt(f.endLine, 10);
    if (!Number.isInteger(endLine) || endLine <= line) endLine = undefined;

    const severity = str(f.severity).toLowerCase();
    let category = str(f.category).toLowerCase();
    category = CATEGORY_ALIASES[category] ?? category;
    if (!SEVERITIES.includes(severity) || !CATEGORIES.includes(category)) continue;

    const title = str(f.title, 200);
    const explanation = str(f.explanation);
    const evidence = stripLineMarkers(str(f.evidence, 2000));
    if (!title || !explanation || !evidence) continue;

    let confidence = Number(f.confidence);
    if (!Number.isFinite(confidence)) confidence = 0;
    confidence = Math.min(1, Math.max(0, confidence));

    const finding = { path, line, severity, category, title, explanation, evidence, confidence };
    if (endLine) finding.endLine = endLine;
    const suggestion = typeof f.suggestion === 'string' ? f.suggestion.replace(/\s+$/, '') : '';
    if (suggestion.trim()) finding.suggestion = suggestion;
    out.push(finding);
  }
  return out;
}

export function normalizeVerifyResults(raw, ids) {
  const known = new Set(ids);
  const list = Array.isArray(raw?.results) ? raw.results : [];
  return list
    .filter((r) => r && known.has(String(r.id)))
    .map((r) => ({ id: String(r.id), supported: r.supported === true, reason: str(r.reason, 500) }));
}

/** Parses model text as JSON, tolerating fenced blocks or leading prose. */
export function parseJsonText(text) {
  if (typeof text !== 'string') throw new Error('model returned no text');
  const trimmed = text.trim();
  try {
    return JSON.parse(trimmed);
  } catch {
    const fenced = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/);
    if (fenced) return JSON.parse(fenced[1]);
    const start = trimmed.indexOf('{');
    const end = trimmed.lastIndexOf('}');
    if (start >= 0 && end > start) return JSON.parse(trimmed.slice(start, end + 1));
    throw new Error('model output is not JSON');
  }
}
