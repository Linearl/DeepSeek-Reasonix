// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1342-resend-feedback.test.ts
//
// tl1342②/③ FE 面：失败消息「重发」按钮的反馈契约 + 横幅一键全部重发。
// 自动重试（层①）本体在 Go 侧（desktop/turn_write_authority_retry_test.go，
// 五组真 Controller 测试）；本文件钉死前端两层：
//   ②按钮按下必有反馈：sendToTab 结算面（SendTurnOutcome）→ describeResendOutcome
//     三分支（confirmed/degraded/refused+原因）→ toast；成功重发清掉 stale 气泡
//     （resent_failed_turn reducer）；按钮 pending 面钉死在源上。
//   ③横幅多条一键全部重发：runBulkResend 逐条顺序、成功集合可非前缀、单条抛错
//     不中断；横幅按 deliveredIndexes 标已处理（拒绝条目保留在列表）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { initialState, reducer, type SendTurnOutcome } from "../lib/useController";
import { describeResendOutcome, runBulkResend } from "../lib/resendFeedback";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

const STRINGS = {
  sendFailed: "发送失败 — 消息未送达",
  resendSuccess: "重发已受理——消息正在运行",
  resendDegraded: "当前有任务运行中——重发的消息已转入排队/补充指示",
};

console.log("\ntl1342② 重发结算面 → 用户反馈三分支");

const confirmed: SendTurnOutcome = { confirmed: true, degraded: false };
const confirmedFace = describeResendOutcome(confirmed, undefined, STRINGS);
eq(confirmedFace.level, "info", "confirmed resend surfaces an info face");
eq(confirmedFace.text, STRINGS.resendSuccess, "confirmed resend names the success copy");

const degraded: SendTurnOutcome = { confirmed: false, degraded: true };
const degradedFace = describeResendOutcome(degraded, undefined, STRINGS);
eq(degradedFace.level, "info", "degraded resend stays informational");
eq(degradedFace.text, STRINGS.resendDegraded, "degraded resend names the queue destination");

const refused: SendTurnOutcome = { confirmed: false, degraded: false, error: "session write authority stale" };
const refusedFace = describeResendOutcome(refused, undefined, STRINGS);
eq(refusedFace.level, "warn", "refused resend is a warning");
eq(refusedFace.text, `${STRINGS.sendFailed}: session write authority stale`, "refused toast carries the backend reason verbatim");

const thrownFace = describeResendOutcome(undefined, new Error("workspace is still starting"), STRINGS);
eq(thrownFace.level, "warn", "a thrown pre-send failure is a warning");
eq(thrownFace.text, `${STRINGS.sendFailed}: workspace is still starting`, "thrown reason lands in the toast");

const muteFace = describeResendOutcome(refused, undefined, STRINGS);
eq(muteFace.text.includes("session write authority stale"), true, "failure reason always visible (20261101 静默缺陷回归钉)");

console.log("\ntl1342② 成功重发清掉 stale 失败气泡（resent_failed_turn）");

// Three user turns; the middle one failed. Turn numbering = user-item count
// (0-based), the same convention handleEditPrompt/transcriptRows use.
const sentA = reducer(initialState, { type: "user", text: "第一条", seq: 0, submissionId: "s-a" });
const failedB = reducer(sentA, { type: "send_failed", submissionId: "s-b", error: "Send failed: stale" });
const sentB = reducer(failedB, { type: "user", text: "第二条", seq: failedB.seq, submissionId: "s-b" });
const failedB2 = reducer(sentB, { type: "send_failed", submissionId: "s-b", error: "Send failed: stale" });
const sentC = reducer(failedB2, { type: "user", text: "第三条", seq: failedB2.seq, submissionId: "s-c" });
eq(sentC.items.filter((it) => it.kind === "user").length, 3, "fixture: three user bubbles present");
eq(sentC.items.filter((it) => it.kind === "user" && it.failed).length, 1, "fixture: exactly one failed bubble");

const cleaned = reducer(sentC, { type: "resent_failed_turn", turn: 1 });
const usersAfter = cleaned.items.filter((it) => it.kind === "user");
eq(usersAfter.length, 2, "confirmed resend removes the failed bubble");
eq(usersAfter.some((it) => it.kind === "user" && it.failed), false, "no failed face remains after removal");
eq(usersAfter[0].kind === "user" && usersAfter[0].text, "第一条", "the earlier bubble keeps its position");
eq(usersAfter[1].kind === "user" && usersAfter[1].text, "第三条", "the resent bubble keeps the tail");

const noopHealthy = reducer(sentC, { type: "resent_failed_turn", turn: 0 });
eq(noopHealthy, sentC, "a turn that is not failed is never removed");

const noopOutOfRange = reducer(sentC, { type: "resent_failed_turn", turn: 9 });
eq(noopOutOfRange, sentC, "an out-of-range turn is a no-op");

console.log("\ntl1342③ 横幅一键全部重发（runBulkResend）");

const allOk = await runBulkResend(["a", "b"], async () => true);
eq(allOk.ok, 2, "bulk resend counts every delivered entry");
eq(allOk.failed, 0, "bulk resend reports zero refusals when all land");
eq(JSON.stringify(allOk.deliveredIndexes), "[0,1]", "delivered indexes cover the full batch in order");

const middleFail = await runBulkResend(["a", "b", "c"], async (prompt) => prompt !== "b");
eq(middleFail.ok, 2, "a mid-batch refusal does not stop the run");
eq(middleFail.failed, 1, "the refused entry is counted");
eq(JSON.stringify(middleFail.deliveredIndexes), "[0,2]", "successes are NOT assumed to be a prefix — indexes are exact");

const throwsCount = await runBulkResend(["a", "b"], async (prompt) => {
  if (prompt === "a") throw new Error("bridge unavailable");
  return true;
});
eq(throwsCount.ok, 1, "a throwing resend counts as refused and the loop continues");
eq(throwsCount.failed, 1, "the throw is surfaced as a refusal count");
eq(JSON.stringify(throwsCount.deliveredIndexes), "[1]", "the surviving entry is delivered");

const sequential: number[] = [];
await runBulkResend(["a", "b", "c"], async (prompt) => {
  sequential.push(prompt);
  return true;
});
eq(sequential.join(","), "a,b,c", "bulk resend runs strictly sequentially (one session's turn gate)");

console.log("\ntl1342②/③ 接线钉死（源断言）");

const here = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(here, "../App.tsx"), "utf8");
const messageSource = readFileSync(resolve(here, "../components/Message.tsx"), "utf8");
const bannerSource = readFileSync(resolve(here, "../components/UnfiledInputsBanner.tsx"), "utf8");
const controllerSource = readFileSync(resolve(here, "../lib/useController.ts"), "utf8");

eq(appSource.includes("onResendPrompt={handleResendFailedPrompt}"), true, "the failed-resend button no longer rides the edit handler");
eq(!appSource.includes("onResendPrompt={handleEditPrompt}"), true, "edit-rewind flow is out of the failed-resend path");
eq(appSource.includes("showToast(t(\"composer.workspaceStarting\"), \"warn\")") && appSource.includes("handleResendFailedPrompt"), true, "guard refusals toast instead of returning silently");
eq(appSource.includes("onResend={handleBannerResend}"), true, "the banner resend rides the outcome-aware channel");
eq(appSource.includes("if (outcome?.confirmed) return true;") && appSource.includes("describeResendOutcome"), true, "banner marks handled only on a confirmed receipt; refusals toast the reason");
eq(messageSource.includes("disabled={!canResend || resending}") && messageSource.includes('resending ? t("msg.resending") : t("msg.resend")'), true, "the resend button has a real pending face (disabled + 重发中…)");
eq(messageSource.includes("const canResend = failed && turn !== undefined && onResend !== undefined;"), true, "the resend gate no longer inherits editDisabled (a failed turn never has a checkpoint — the born-disabled defect)");
eq(controllerSource.includes("resendFailedToTab") && controllerSource.includes('type: "resent_failed_turn", turn: failedTurn'), true, "resend settlement dispatches the bubble cleanup on confirmation");
eq(bannerSource.includes("runBulkResend") && bannerSource.includes("deliveredIndexes.map((index) => turns[index])"), true, "the banner marks handled by exact delivered indexes, never by head-slice");
eq(bannerSource.includes("if (delivered) onMarkHandled(entry.turn, t.resent)"), true, "a refused per-entry resend never marks the entry handled");
eq(bannerSource.includes("resendAllPending ? t.resendAllPending : t.resendAll"), true, "resend-all has a pending face");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
