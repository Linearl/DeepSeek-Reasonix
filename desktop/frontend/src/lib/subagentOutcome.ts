export type SubagentOutcomeFields = {
  subagentRef?: string;
  subagentStatus?: string;
  subagentErrorCode?: string;
  subagentRetryable?: boolean;
};

export function parseSubagentOutcomeText(text?: string): SubagentOutcomeFields {
  if (!text) return {};
  const head = text.slice(0, 1024);
  const ref = head.match(/^Subagent reference(?: \(failed\))?: ([^\n]+)/m)?.[1]?.trim();
  const match = head.match(/^Subagent outcome: status=([^\s]+) retryable=(true|false)(?: error_code=([^\s]+))?/m);
  if (!ref || !match) return {};
  return {
    subagentRef: ref,
    subagentStatus: match[1],
    subagentRetryable: match[2] === "true",
    subagentErrorCode: match[3],
  };
}
