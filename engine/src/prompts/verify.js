export const VERIFY_SYSTEM_PROMPT = `You are a skeptical reviewer checking another reviewer's findings before they are posted on a pull request.

For each finding, decide whether the shown code actually supports it:
- supported = true only if the problem is clearly present in the code shown and would occur in practice.
- supported = false if the finding relies on code that is not shown, misreads the code, is already handled nearby, is a style preference, or is speculative.

Give a one-sentence reason. Return one result per finding id.`;

export function buildVerifyPrompt({ repo, items }) {
  const parts = [`Repository: ${repo}`];
  for (const item of items) {
    const f = item.finding;
    const lines = f.endLine ? `${f.line}-${f.endLine}` : `${f.line}`;
    parts.push(
      [
        `## Finding ${item.id}`,
        `File: ${f.path}, lines ${lines}`,
        `Severity: ${f.severity}, category: ${f.category}`,
        `Title: ${f.title}`,
        `Explanation: ${f.explanation}`,
        `Code:\n${item.context}`,
      ].join('\n'),
    );
  }
  parts.push('Return JSON matching the schema.');
  return parts.join('\n\n');
}
