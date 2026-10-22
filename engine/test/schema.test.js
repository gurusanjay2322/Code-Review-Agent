import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { normalizeFindings, normalizeVerifyResults, parseJsonText } from '../src/schema.js';

const recorded = readFileSync(new URL('./fixtures/gemini-review-response.txt', import.meta.url), 'utf8');

test('parses a recorded fenced model response and normalizes findings', () => {
  const findings = normalizeFindings(parseJsonText(recorded), ['store/user.go']);
  assert.equal(findings.length, 2, 'drops the off-chunk path and the style category');

  const [sql, perf] = findings;
  assert.equal(sql.line, 9);
  assert.equal(sql.endLine, 10);
  assert.equal(sql.evidence, "\tq := fmt.Sprintf(\"SELECT id, name FROM users WHERE name = '%s'\", name)\n\trow := db.QueryRow(q)", 'line markers are stripped');
  assert.ok(sql.suggestion.includes('$1'));

  assert.equal(perf.severity, 'medium', 'severity is lowercased');
  assert.equal(perf.category, 'performance', 'category alias is mapped');
  assert.equal(perf.confidence, 1, 'confidence is clamped');
  assert.equal(perf.endLine, undefined);
  assert.equal(perf.suggestion, undefined);
});

test('drops malformed findings', () => {
  const raw = {
    findings: [
      null,
      { path: 'a.js', line: 0, severity: 'high', category: 'bug', title: 't', explanation: 'e', evidence: 'x', confidence: 1 },
      { path: 'a.js', line: 2, severity: 'urgent', category: 'bug', title: 't', explanation: 'e', evidence: 'x', confidence: 1 },
      { path: 'a.js', line: 2, severity: 'high', category: 'bug', title: '', explanation: 'e', evidence: 'x', confidence: 1 },
      { path: 'a.js', line: '3', severity: 'high', category: 'bug', title: 't', explanation: 'e', evidence: 'x', confidence: 'nope' },
    ],
  };
  const out = normalizeFindings(raw, ['a.js']);
  assert.equal(out.length, 1);
  assert.equal(out[0].line, 3);
  assert.equal(out[0].confidence, 0);
});

test('keeps code lines that merely contain a pipe', () => {
  const [f] = normalizeFindings(
    { findings: [{ path: 'a.sh', line: 1, severity: 'low', category: 'bug', title: 't', explanation: 'e', evidence: 'cat x | grep y', confidence: 0.9 }] },
    ['a.sh'],
  );
  assert.equal(f.evidence, 'cat x | grep y');
});

test('parseJsonText handles plain, prefixed and invalid output', () => {
  assert.deepEqual(parseJsonText('{"findings":[]}'), { findings: [] });
  assert.deepEqual(parseJsonText('Here you go: {"findings":[]} thanks'), { findings: [] });
  assert.throws(() => parseJsonText('no json here'));
});

test('normalizeVerifyResults ignores unknown ids', () => {
  const out = normalizeVerifyResults({ results: [{ id: '0', supported: true, reason: 'ok' }, { id: '9', supported: false }] }, ['0', '1']);
  assert.deepEqual(out, [{ id: '0', supported: true, reason: 'ok' }]);
});
