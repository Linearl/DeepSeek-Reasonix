// Run: tsx src/__tests__/unfiled-inputs.test.tsx
// 任务 1023 阶段 0：未入档输入前端整合——就地时间锚定、已处理过滤、
// 去重铁律（transcript 已可见的永不再显示）的前端半边断言。

import { JSDOM } from "jsdom";
import { strict as assert } from "node:assert";
import { readFile } from "node:fs/promises";
import { countUnhandled, readHandledTurns, markHandledTurns, unfiledSessionKeyOf, withUnfiledNoticeItems } from "../lib/unfiledInputs";
import type { UnfiledInputView } from "../lib/types";
import type { Item } from "../lib/useController";

// jsdom provides localStorage（已处理标记的持久化半边用它验证）
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" });
(globalThis as unknown as { localStorage: Storage }).localStorage = dom.window.localStorage;

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const TITLE = "未入档输入（已从检查点恢复）";

function entry(turn: number, time: number): UnfiledInputView {
  return { turn, msgIndex: turn * 10, time, prompt: `输入 ${turn} 的原文`, anchorIndex: turn * 10 };
}

function userItem(id: string, createdAt?: number): Item {
  return { kind: "user", id, text: "已入档消息", ...(createdAt !== undefined ? { createdAt } : {}) };
}

console.log("\nunfiled inputs: in-place time anchoring");
{
  const items: Item[] = [userItem("u1", 1000), { kind: "assistant", id: "a1", text: "回复", reasoning: "", streaming: false, createdAt: 3000 }];
  const out = withUnfiledNoticeItems(items, [entry(1, 2000)], new Set(), TITLE);
  ok(out.length === 3, "notice inserted between the anchored neighbours");
  ok(out[1].kind === "notice" && out[1].id === "unfiled:1", "notice sits before the first item created after the input time");
  ok((out[1] as { level?: string }).level === "warn", "warn level keeps the card outside the work-process fold");
}
{
  // 无时间戳可锚定 → 兜底追加到末尾，绝不静默丢弃。
  const items: Item[] = [userItem("u1"), { kind: "assistant", id: "a1", text: "回复", reasoning: "", streaming: false }];
  const out = withUnfiledNoticeItems(items, [entry(2, 0)], new Set(), TITLE);
  ok(out.length === 3 && out[2].kind === "notice", "unanchorable entry still renders at the end");
}

console.log("\nunfiled inputs: handled filtering (去重铁律 front half)");
{
  const items: Item[] = [userItem("u1", 1000)];
  const handled = new Set([1]);
  const out = withUnfiledNoticeItems(items, [entry(1, 500)], handled, TITLE);
  ok(out.length === 1 && out[0].kind === "user", "handled entries never render again in the transcript");
  ok(countUnhandled([entry(1, 500), entry(2, 600)], handled) === 1, "banner count drops once handled");
}

console.log("\nunfiled inputs: handled persistence (localStorage only)");
{
  const key = `test-session-${Date.now()}`;
  markHandledTurns(key, [7, 3]);
  const turns = Array.from(readHandledTurns(key));
  assert.deepEqual(turns, [3, 7]);
  ok(true, "handled turns persist sorted per session key");
}

console.log("\nunfiled inputs: batch mark-all-handled (task 1045)");
{
  // 41 条逐条点是用户 2026-10-21 实证痛点；批量入口把全部未处理 turn 一次交
  // markHandledTurns（turns 数组形参，localStorage 一次写入）。
  const key = `batch-session-${Date.now()}`;
  const entries: UnfiledInputView[] = [1, 2, 3, 4, 5].map((turn) => entry(turn, 1000 + turn));
  markHandledTurns(key, entries.map((e) => e.turn));
  const handled = readHandledTurns(key);
  ok(countUnhandled(entries, handled) === 0, "batch mark-all-handled clears every pending entry");
  ok(handled.size === 5, "batch write covers all five turns in one localStorage write");
  // 与单条 markHandledTurns 合流无冲突：先批量再单条补写同一 turn 不重复、不报错。
  markHandledTurns(key, [3]);
  ok(readHandledTurns(key).size === 5, "re-marking a batched turn is a no-op (idempotent merge)");
}

console.log("\nunfiled inputs: fold contract source of truth (task 1045)");
{
  // 致命面修复的核心契约：组件文件与 CSS 折叠阈值必须同源（都在 FOLDED_LINES=6
  // 行）。这里读两份源文件做交叉核对——改任一侧忘改另一侧即在此处炸红。
  const banner = await readFile(new URL("../components/UnfiledInputsBanner.tsx", import.meta.url), "utf8");
  const styles = await readFile(new URL("../styles.css", import.meta.url), "utf8");
  ok(banner.includes('const [expanded, setExpanded] = useState(false)') && banner.includes("unfiled-card__body--open\" : \"\""), "body starts collapsed; --open class only after an explicit expand click");
  ok(styles.includes(".unfiled-card__body") && styles.includes("max-height: 9.6em"), "CSS fold cap (max-height 9.6em ≈ 6 lines) present for the collapsed body");
  ok(styles.includes(".unfiled-card__body--open") && styles.includes("max-height: 64em"), "expanded body keeps a hard cap (64em) so a 50KB prompt can never squat the transcript");
  ok(styles.includes(".unfiled-card__head") && styles.includes("position: sticky"), "head row (title + actions) is sticky so buttons never sink below the fold");
}

console.log("\nunfiled inputs: large-window viewer (tl 1127, 方案 A)");
{
  // tl 1127 契约：41 条实例中内联展开列表被 .main overflow:hidden 裁掉且无滚动。
  // 「查看」必须开大窗口（portal 出 body），列表在窗口内独立滚动、滚动链隔离；
  // 尺寸受视口约束；Esc/遮罩可关；关闭后焦点还原；全部处理后不残留空白窗口。
  // JSDOM 无布局引擎，尺寸/滚动属运行时属性——按项目惯例用源码文本契约断言
  // CSS 关键声明与组件关键行为（同上方 fold contract 的交叉核对模式）。
  const banner = await readFile(new URL("../components/UnfiledInputsBanner.tsx", import.meta.url), "utf8");
  const styles = await readFile(new URL("../styles.css", import.meta.url), "utf8");
  const viewerCss = await readFile(new URL("../components/UnfiledInputsViewer.css", import.meta.url), "utf8");
  const listCss = viewerCss.slice(viewerCss.indexOf(".unfiled-viewer__list"));
  const dialogCss = viewerCss.slice(viewerCss.indexOf(".unfiled-viewer {"), viewerCss.indexOf(".unfiled-viewer__head"));

  // 验收 1：列表独立滚动 + flex 高度链（min-height:0，否则 flex 子项不收缩滚不动）
  ok(/\.unfiled-viewer__list\s*{[^}]*overflow-y:\s*auto/.test(listCss), "viewer list scrolls independently (overflow-y: auto)");
  ok(/\.unfiled-viewer__list\s*{[^}]*min-height:\s*0/.test(listCss), "flex chain min-height: 0 so the list can actually shrink and scroll to the last entry");
  // 验收 2：视口约束 + 关闭入口（.modal 的 width:100% 继承，max-width 封顶）
  ok(/max-width:\s*min\(960px,\s*90vw\)/.test(dialogCss), "dialog width capped at min(960px, 90vw)");
  ok(/height:\s*min\(640px,\s*85vh\)/.test(dialogCss), "dialog height capped at min(640px, 85vh) ≤ 85vh");
  ok(banner.includes("<ModalCloseButton") && banner.includes("t.close"), "X close button present in the viewer head");
  // 验收 4：滚动链隔离——列表滚到底不带动底层 transcript
  ok(/overscroll-behavior:\s*contain/.test(listCss), "scroll chain isolated (overscroll-behavior: contain) — the transcript underneath never scrolls along");
  ok(/\.unfiled-viewer-backdrop\s*{[^}]*z-index:\s*var\(--z-modal\)/.test(viewerCss), "backdrop rides the --z-modal token (z-index token contract)");
  // 预算面：viewer 样式走动态 import 独立 chunk（SessionEventsPanel.css 先例），不占 app-shell 棘轮
  ok(banner.includes('void import("./UnfiledInputsViewer.css")'), "viewer CSS rides a dynamic-import chunk — no app-shell budget cost");
  // 验收 1（续）：portal 到 body，不受 .main overflow:hidden 裁剪
  ok(banner.includes("createPortal(") && banner.includes("document.body"), "viewer portals to document.body — immune to .main overflow: hidden clipping");
  // 验收 7：键盘可达——焦点进窗口、Esc 关、还原焦点、Tab 循环
  ok(banner.includes('event.key === "Escape"'), "Esc closes the viewer");
  ok(banner.includes("restoreFocusRef") && banner.includes("restoreFocusRef.current.focus()"), "focus returns to the trigger button after close");
  ok(banner.includes("closeButtonRef.current?.focus()"), "focus enters the window on open (close button first)");
  ok(banner.includes('aria-modal="true"') && banner.includes('role="dialog"'), "viewer is an aria-modal dialog");
  ok(banner.includes('event.key !== "Tab"') && banner.includes("last.focus()"), "Tab cycles inside the dialog (focus trap)");
  // 验收 3：条目卡片复用，操作行为与横幅面一致
  ok(/<div\s+className="unfiled-viewer__list">[\s\S]*?<UnfiledEntryCard/.test(banner), "per-entry cards render inside the viewer list with the same UnfiledEntryCard actions");
  // 验收 5：空态不残留——全部处理后横幅与大窗口一起卸载
  ok(/if \(unhandledCount === 0\) return null;/.test(banner), "banner (and its viewer child) unmount once every entry is handled");
  // 验收 2（续）：遮罩点击关闭（项目规范 role="presentation" + target 判定）
  ok(banner.includes('role="presentation"') && banner.includes("event.target === event.currentTarget"), "backdrop click closes the viewer");
}

console.log("\nunfiled inputs: tl1372 A2 dual bucket (pending recovery, 实验开关默认关)");
{
  // A2 双桶：class 缺省/单桶 = 「未进入对话」；"pending_recovery" = 「待恢复」
  // （CAS 拒档，原文在 recovery 副本中）。横幅总数不因 class 改变——任一桶
  // 非空即挂横幅，文案按桶分。
  const entries: UnfiledInputView[] = [
    entry(1, 1000),
    { ...entry(2, 2000), class: "pending_recovery" as const },
  ];
  ok(countUnhandled(entries, new Set()) === 2, "both buckets count toward the banner presence");
  ok(countUnhandled(entries, new Set([2])) === 1, "handled keys work per turn across buckets");
  // 源码契约：横幅按桶分文案（entryBucket 集中判定 + 三语 recovery 文案）。
  const banner = await readFile(new URL("../components/UnfiledInputsBanner.tsx", import.meta.url), "utf8");
  ok(banner.includes("function entryBucket"), "bucket decision is centralized in entryBucket");
  ok(banner.includes('entry.class === "pending_recovery"'), "only the pending_recovery class opts into recovery copy");
  ok(banner.includes("unfiledHeadlineText"), "headline composes the never-filed alarm and the recovery line");
  ok((banner.match(/recoveryBanner:/g) || []).length === 3 && (banner.match(/recoveryTag:/g) || []).length === 3, "recovery copy present in all three locales");
  // 默认单桶契约：desktop 桥的 class 字段 omitempty，开关关时 wire 无新字段、
  // 前端呈现与历史逐字节一致。
  const appGo = await readFile(new URL("../../../unfiled_inputs_app.go", import.meta.url), "utf8");
  ok(appGo.includes(`json:"class,omitempty"`), "class field is omitempty on the wire — flag-off payloads unchanged");
  ok(appGo.includes('in.Class == "pending_recovery"'), "desktop bridge forwards only the pending_recovery bucket");
}

console.log("\nunfiled inputs: tl1372 dismiss key normalization (recovery fork follows the parent)");
{
  // 判则②：recovery fork 换 sessionPath 后 dismiss 必须跟随——键归一到同根
  // 父会话路径（与后端 agent.RecoveryFilenameRootID 同构的剥层判定）。
  const parent = "C:/sessions/20261102-021909.744449100-mimo-token-plan.jsonl";
  const fork = "C:/sessions/20261102-021909.744449100-mimo-token-plan-recovery-9318b1f6a1b2c3d4.jsonl";
  ok(unfiledSessionKeyOf(fork) === parent, "a recovery fork maps onto the parent session path");
  ok(unfiledSessionKeyOf(parent) === parent, "a plain session keeps its own path (存量键零迁移)");
  const nestedFork = "C:/sessions/20261102-021909.744449100-mimo-token-plan-recovery-0123456789abcdef-recovery-fedcba9876543210.jsonl";
  ok(unfiledSessionKeyOf(nestedFork) === parent, "nested recovery forks peel back to the same root");
  ok(unfiledSessionKeyOf("") === "" && unfiledSessionKeyOf("  ") === "", "empty stays empty (tab-id fallback upstream)");
  ok(unfiledSessionKeyOf("C:/sessions/foo-recovery-short.jsonl") === "C:/sessions/foo-recovery-short.jsonl", "a suffix that is not 16 hex is not a recovery marker");
  ok(unfiledSessionKeyOf("C:/sessions/foo-recovery-0123456789ABCDEF.jsonl") === "C:/sessions/foo.jsonl", "uppercase hex matches the backend peel");
  ok(unfiledSessionKeyOf("C:/sessions/foo-recovery-0123456789abcde.jsonl") === "C:/sessions/foo-recovery-0123456789abcde.jsonl", "15 hex is not a recovery marker");
  ok(unfiledSessionKeyOf("tab-1") === "tab-1", "non-path fallbacks pass through unchanged");

  // 行为面：fork 前标记 → fork 后仍生效；不同会话不串。
  const keyParent = unfiledSessionKeyOf(parent);
  markHandledTurns(keyParent, [7]);
  ok(readHandledTurns(unfiledSessionKeyOf(fork)).has(7), "a mark made before the fork still applies after the fork");
  markHandledTurns(unfiledSessionKeyOf(nestedFork), [9]);
  ok(readHandledTurns(keyParent).has(9), "a mark made on a nested fork lands on the shared parent key");
  const otherKey = unfiledSessionKeyOf("C:/sessions/another-session-recovery-0123456789abcdef.jsonl");
  ok(otherKey === "C:/sessions/another-session.jsonl" && !readHandledTurns(otherKey).has(7), "a different session topic never sees the parent's handled set");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
