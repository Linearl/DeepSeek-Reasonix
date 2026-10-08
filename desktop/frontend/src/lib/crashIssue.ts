// Task 617 route A: turn a crash/performance payload into a GitHub issue
// skeleton the user can paste into our own repository without reformatting.
// The upstream collection endpoint is down (task 618), so this copy path is the
// zero-dependency fallback: no network, no token, no prerequisites.
//
// The skeleton text is deliberately English-only and self-contained — it gets
// pasted straight into GitHub, so no localized UI strings may leak into it.
// Localized copy lives in the locale files (crash.* / performanceReport.*).

import type { CrashPayload } from "./crash";

export const CRASH_ISSUE_REPO = "Linearl/DeepSeek-Reasonix";
export const CRASH_ISSUE_NEW_URL = `https://github.com/${CRASH_ISSUE_REPO}/issues/new`;

const FENCE = "```";

// A fenced block whose content contains ``` would break the Markdown the user
// pastes; neutralize nested fences before embedding.
function fenced(lang: string, content: string): string {
  const safe = content.replace(/```/g, "``\u200b`");
  return `${FENCE}${lang}\n${safe}\n${FENCE}`;
}

function oneLine(s: string): string {
  return s.replace(/\s+/g, " ").trim();
}

function suggestedTitle(payload: CrashPayload): string {
  const kind = payload.kind || "crash";
  // The message reads better than the bare error class in an issue list
  // ("boom at render" vs "Error"); the class is the fallback.
  const what = oneLine(payload.errorMessage || payload.errorType || "diagnostic report");
  const where = oneLine(payload.topFrame || payload.label || "");
  const head = what.length > 80 ? `${what.slice(0, 80)}…` : what;
  // Task 642: a lab-simulated report must stay identifiable even after the
  // user renames the pasted issue — the mock tag leads the title.
  const tag = payload.testMock ? `[mock][${kind}]` : `[${kind}]`;
  return where ? `${tag} ${head} at ${where}` : `${tag} ${head}`;
}

function suggestedLabels(payload: CrashPayload): string[] {
  // Conservative set: the generic bug tag plus the report kind (crash,
  // exception, performance, feedback, bot). Mock drills (task 642) add explicit
  // test labels so triage can filter them out at a glance.
  const labels = ["bug", payload.kind || "crash"];
  if (payload.testMock) labels.push("mock", "test");
  return labels;
}

export function buildCrashIssueSkeleton(payload: CrashPayload): string {
  const title = suggestedTitle(payload);
  const labels = suggestedLabels(payload).join(", ");
  const environment = [
    `- build: ${payload.buildCommit || "unknown"}${payload.channel ? ` (${payload.channel})` : ""}`,
    `- os/arch: ${osArchLine()}`,
    `- source: ${payload.source || "unknown"}`,
    `- view: ${payload.view || "unknown"}`,
    `- language: ${payload.language || "unknown"}`,
    `- occurred at: ${payload.occurredAt || "unknown"}`,
    ...(payload.testMock ? ["- mock: YES — simulated test event from the lab (task 642), not a real failure"] : []),
  ].join("\n");

  const sections: string[] = [];
  sections.push(`Issue target: ${CRASH_ISSUE_NEW_URL}`);
  sections.push(`Suggested labels: ${labels}`);
  sections.push("---");
  sections.push(`Suggested title: ${title}`);
  sections.push("## Summary");
  sections.push(payload.errorMessage || payload.message || "(no error message captured)");
  if (payload.fingerprintHint) {
    sections.push(`Fingerprint hint: \`${payload.fingerprintHint}\``);
  }
  sections.push("## Environment");
  sections.push(environment);
  if (payload.stack) {
    sections.push("## Stack");
    sections.push(fenced("text", payload.stack));
  }
  if (payload.componentStack) {
    sections.push("## Component stack");
    sections.push(fenced("text", payload.componentStack));
  }
  if (payload.breadcrumbs?.length) {
    sections.push("## Breadcrumbs");
    sections.push(
      payload.breadcrumbs
        .map((crumb) => {
          const cat = crumb.cat ? `[${crumb.cat}] ` : "";
          return `- ${cat}${crumb.msg ?? ""}`.trimEnd();
        })
        .join("\n"),
    );
  }
  sections.push("## Raw diagnostic payload");
  sections.push(fenced("json", safeJson(payload)));
  return sections.join("\n\n") + "\n";
}

function safeJson(payload: CrashPayload): string {
  try {
    return JSON.stringify(payload, null, 2);
  } catch {
    return String(payload);
  }
}

function osArchLine(): string {
  // The payload carries no os/arch of its own (the Go side attaches them at
  // send time); navigator gives us the platform without importing app modules.
  if (typeof navigator === "undefined") return "unknown";
  const ua = navigator.userAgent || "";
  const platform = (navigator as Navigator & { platform?: string }).platform || "";
  return platform || ua || "unknown";
}
