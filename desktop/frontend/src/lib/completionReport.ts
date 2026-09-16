// Task 112: parse the structured completion report the agent is instructed to
// append after substantial work (config.CompletionReportPolicy). Fields are
// the four Chinese labels from the policy; "无" means the field does not apply.

export type CompletionReportFields = {
  deliverables: string;
  changes: string;
  verification: string;
  remaining: string;
};

export type ParsedCompletionReport = {
  fields: CompletionReportFields;
  /** Assistant body with the trailing report block removed. */
  body: string;
};

const FIELD_PATTERNS: Array<{ key: keyof CompletionReportFields; re: RegExp }> = [
  { key: "deliverables", re: /^(?:[-*]\s*)?(?:\*\*)?(?:交付物|Deliverables?|Outputs?)(?:\*\*)?\s*[:：]/i },
  { key: "changes", re: /^(?:[-*]\s*)?(?:\*\*)?(?:变更|Changes?|What changed)(?:\*\*)?\s*[:：]/i },
  { key: "verification", re: /^(?:[-*]\s*)?(?:\*\*)?(?:验证|Verification|Checks?)(?:\*\*)?\s*[:：]/i },
  { key: "remaining", re: /^(?:[-*]\s*)?(?:\*\*)?(?:未做\s*[/／、]?\s*风险|Remaining\s*[/／]?\s*Risks?|Risks?|Left undone)(?:\*\*)?\s*[:：]/i },
];

const NONE = /^["“']?无["”']?$/;

function normalizeFieldValue(raw: string): string {
  const value = raw.replace(/\s+$/, "").trim();
  if (NONE.test(value)) return "";
  return value;
}

/**
 * parseCompletionReport finds a trailing structured report in an assistant
 * message. It requires at least two known fields so ordinary prose that merely
 * mentions one label is not treated as a report.
 */
export function parseCompletionReport(text: string): ParsedCompletionReport | null {
  if (!text) return null;
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  // Scan from the end: the policy places the report after the last tool result.
  const fieldLineIndexes: Array<{ index: number; key: keyof CompletionReportFields }> = [];
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim();
    if (!line) continue;
    for (const { key, re } of FIELD_PATTERNS) {
      if (re.test(line)) {
        fieldLineIndexes.push({ index: i, key });
        break;
      }
    }
  }
  if (fieldLineIndexes.length < 2) return null;

  // Take the last contiguous-ish block of known fields (allow blank lines and
  // short prose between them, but stop if the gap is large).
  const block: typeof fieldLineIndexes = [];
  for (let i = fieldLineIndexes.length - 1; i >= 0; i -= 1) {
    const entry = fieldLineIndexes[i];
    if (block.length === 0) {
      block.unshift(entry);
      continue;
    }
    const nextIndex = block[0].index;
    const gap = nextIndex - entry.index - 1;
    if (gap > 4) break;
    if (block.some((b) => b.key === entry.key)) break;
    block.unshift(entry);
  }
  if (block.length < 2) return null;

  const fields: CompletionReportFields = { deliverables: "", changes: "", verification: "", remaining: "" };
  for (let i = 0; i < block.length; i += 1) {
    const start = block[i].index;
    const end = i + 1 < block.length ? block[i + 1].index : lines.length;
    const startLine = lines[start];
    const colon = startLine.search(/[:：]/);
    const firstValue = colon >= 0 ? startLine.slice(colon + 1) : "";
    const parts: string[] = [];
    if (firstValue.trim()) parts.push(firstValue.trim());
    for (let j = start + 1; j < end; j += 1) {
      const line = lines[j];
      // Stop at another known field that was not part of the block (shouldn't happen)
      // or at a markdown heading that starts a new section.
      if (/^#{1,6}\s/.test(line.trim())) break;
      if (line.trim()) parts.push(line.trim());
      else if (parts.length > 0) parts.push("");
    }
    fields[block[i].key] = normalizeFieldValue(parts.join("\n"));
  }

  const reportStart = block[0].index;
  // Drop a blank line immediately above the report.
  let bodyEnd = reportStart;
  while (bodyEnd > 0 && lines[bodyEnd - 1].trim() === "") bodyEnd -= 1;
  const body = lines.slice(0, bodyEnd).join("\n").replace(/\s+$/, "");
  if (!body && !fields.deliverables && !fields.changes && !fields.verification && !fields.remaining) return null;
  return { fields, body };
}

export function completionReportHasContent(fields: CompletionReportFields): boolean {
  return Boolean(fields.deliverables || fields.changes || fields.verification || fields.remaining);
}
