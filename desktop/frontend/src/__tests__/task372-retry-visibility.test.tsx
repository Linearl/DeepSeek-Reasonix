// Task 372 acceptance (provider stream-error auto-retry visibility — pure
// visibility layer, the task-243 A4 admission semantics untouched):
//  ① in-window strip copy carries the budget counts: "auto-retried N
//     (N/limit) times, recovering…" (zh/en/zh-TW);
//  ② the running face stays on during retries (retrying handler sets
//     running/turnActive — asserted on the source) and the project-tree
//     unread blue dot stays off because a non-empty topic status short-
//     circuits the unread branch;
//  ③ three end states: recovering (budget frame), terminated ("gave up
//     after limit retries" + manual-continue action wired to onPrompt), and
//     recovered (turn_done clears state.retry → strip vanishes);
//  ④ retrybudget admission semantics unchanged — the Go suite
//     (retrybudget_test.go 5 tests + task372 Go tests) runs alongside.
//
// Run: npx tsx src/__tests__/task372-retry-visibility.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { zh } from "../locales/zh";
import { en } from "../locales/en";
import { zhTW } from "../locales/zh-TW";
import { recoveryStatusText, type RecoveryRetry } from "../lib/recoveryStatus";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const controller = read("../lib/useController.ts");
const renderer = read("../components/useTranscriptRowRenderer.tsx");
const cards = read("../components/TranscriptCards.tsx");

// Minimal translator over the real locale tables (no rendering needed).
const dicts = { zh, en, zhTW } as const;
type Lang = keyof typeof dicts;
function makeT(lang: Lang) {
  return (key: string, vars?: Record<string, string | number>) => {
    const dict = dicts[lang] as Record<string, string>;
    let text = dict[key] ?? key;
    if (vars) for (const [k, v] of Object.entries(vars)) text = text.replaceAll(`{${k}}`, String(v));
    return text;
  };
}

console.log("\ntask 372 retry visibility");

// ① in-window copy with budget counts, three languages.
{
  const retry: RecoveryRetry = { attempt: 3, max: 8, recovery: { budget_used: 3, budget_limit: 8 } };
  const zhText = recoveryStatusText(makeT("zh"), retry, Date.now());
  ok(zhText.includes("已自动重试 3/8 次") && zhText.includes("恢复中"), `zh in-window copy: ${zhText}`);
  const enText = recoveryStatusText(makeT("en"), retry, Date.now());
  ok(enText.includes("auto-retried 3/8 times"), `en in-window copy: ${enText}`);
  const twText = recoveryStatusText(makeT("zhTW"), retry, Date.now());
  ok(twText.includes("已自動重試 3/8 次"), `zh-TW in-window copy: ${twText}`);
}

// ③a terminated end state names the give-up with N and the limit.
{
  const retry: RecoveryRetry = { attempt: 8, max: 8, recovery: { budget_used: 8, budget_limit: 8, budget_exhausted: true } };
  const text = recoveryStatusText(makeT("zh"), retry, Date.now());
  ok(text.includes("已终止") && text.includes("8 次后放弃"), `zh terminated copy: ${text}`);
  const enText = recoveryStatusText(makeT("en"), retry, Date.now());
  ok(enText.includes("terminated") && enText.includes("after 8 retries"), `en terminated copy: ${enText}`);
}

// ③a' legacy frames (no budget fields) keep the pre-372 copy — zero drift.
{
  const legacy: RecoveryRetry = { attempt: 2, max: 3, recovery: {} };
  const text = recoveryStatusText(makeT("zh"), legacy, Date.now());
  ok(text === zh["status.retrying"].replace("{attempt}", "2").replace("{max}", "3"), `legacy copy unchanged: ${text}`);
}

// ② running face: the retrying handler keeps running/turnActive/cancellable.
ok(/e\.kind === "retrying"/.test(controller) && /running: true/.test(controller) && /turnActive: true/.test(controller), "retrying handler pins running + turnActive (runtime face stays on)");
{
  const idx = controller.indexOf('e.kind === "retrying"');
  const block = controller.slice(idx, idx + 700);
  ok(block.includes("running: true") && block.includes("turnActive: true"), "running/turnActive assignment lives inside the retrying branch");
}
// ② blue dot: unread short-circuits while a topic status is non-empty, and
//    the dot enum does not list error — both source guards already exist.
{
  const topic = read("../lib/projectTreeTopic.ts");
  ok(topic.includes('if (topicStatus(node) !== "") return false'), "unread blue dot stays off whenever a status is present (retrying included)");
  ok(/thinking\s*\|\s*"streaming"|thinking.*streaming.*waiting_confirmation/s.test(topic), "status-dot enum covers the retrying face (thinking/streaming)");
}

// ③b recovered end state: turn_done clears state.retry → strip vanishes.
ok(/streamInterruptNoticeShown: undefined,/.test(controller) && /retry: undefined,/.test(controller), "turn_done resets state.retry (recovered turn drops the strip)");

// ③b' terminated strip becomes the error notice with the manual-continue action.
ok(controller.includes('t("notice.retryExhausted", { limit: exhaustedLimit })'), "exhausted turn_done renders the terminated notice copy");
ok(controller.includes('action: "manual_continue"'), "terminated notice carries the manual-continue action");
ok(controller.includes("detail: e.detail ?? e.err"), "raw provider error preserved in detail");
ok(controller.includes('"consolidate_recovery" | "manual_continue"'), "notice action union admits manual_continue");

// ③c manual-continue button: label + dispatch into the composer prompt.
ok(renderer.includes('row.item.action === "manual_continue"') && renderer.includes('onPrompt(t("notice.retryExhaustedContinuePrompt"))'), "renderer dispatches manual_continue into onPrompt");
ok(cards.includes('item.action === "manual_continue" ? t("notice.manualContinue")'), "notice card renders the manual-continue label");

// Locale keys across the three languages.
for (const [name, dict] of [["zh", zh], ["en", en], ["zh-TW", zhTW]] as const) {
  const keys = ["status.retryingBudget", "status.retryExhausted", "notice.retryExhausted", "notice.retryExhaustedContinuePrompt", "notice.manualContinue"];
  const missing = keys.filter((k) => !(k in dict));
  ok(missing.length === 0, `locale ${name} carries all 5 task-372 keys`);
}

// ④ scope guard: the admission surface itself has no task-372 edits — the
//    budget file only gained read-only observers (Allow body untouched).
{
  const budget = read("../../../../internal/agent/retrybudget.go");
  const allowIdx = budget.indexOf("func (b *RetryBudget) Allow(");
  const allowEnd = budget.indexOf("\n}", allowIdx);
  const allowBody = budget.slice(allowIdx, allowEnd);
  ok(!allowBody.includes("372") && allowBody.includes("return true, kind, \"admitted\""), "Allow body carries no task-372 edits (admission semantics untouched)");
  ok(budget.includes("func (b *RetryBudget) Count(") && budget.includes("func (b *RetryBudget) Limit("), "read-only Count/Limit observers exist");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
