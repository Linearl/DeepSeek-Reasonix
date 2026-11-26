// Task 137: resource budgets. The tool payload preview / markdown DOM budget
// helpers were removed with tl1826 (F1-14/A4 A档: production zero-wire since
// 20261110); this suite keeps covering the live resourceBudgets surface.
import {
  HISTORY_BODY_BUDGET_BYTES,
  MARKDOWN_BUDGET_BYTES,
  MARKDOWN_DOM_MAX_ELEMENTS,
  MAX_RESIDENT_SESSIONS,
  TOOL_PREVIEW_MAX_BYTES,
  resourceBudgetSnapshot,
} from "../lib/resourceBudgets";
import { resetSessionDiagnostics, sessionPipelineDiagnostics } from "../lib/sessionDiagnostics";

let passed = 0;
function check(cond: boolean, label: string): void {
  if (!cond) {
    console.error(`  FAIL  ${label}`);
    process.exitCode = 1;
    return;
  }
  passed += 1;
  console.log(`  PASS  ${label}`);
}

// ── constants match the pre-consolidation defaults ──────────────────────────
check(MAX_RESIDENT_SESSIONS === 24, "resident sessions default stays 24");
check(HISTORY_BODY_BUDGET_BYTES === 192 * 1024 * 1024, "history body default stays 192MiB");
check(MARKDOWN_BUDGET_BYTES === 256 * 1024 * 1024, "markdown default stays 256MiB");
const snap = resourceBudgetSnapshot();
check(snap.maxResidentSessions === 24 && snap.historyBodyBudgetBytes === 192 << 20 && snap.markdownBudgetBytes === 256 << 20, "snapshot mirrors the three store ceilings");
check(snap.toolPreviewMaxBytes === TOOL_PREVIEW_MAX_BYTES && snap.markdownDomMaxElements === MARKDOWN_DOM_MAX_ELEMENTS, "snapshot includes preview/DOM budgets");

resetSessionDiagnostics();
check(sessionPipelineDiagnostics().resourceBudget === undefined, "reset clears resource budget diagnostics");

console.log(`\n${passed} passed${process.exitCode ? ", with failures" : ""}`);
if (process.exitCode) process.exit(1);
