// tl1429d2 前端钉:回收站同族多桶渲染层合并。
// 现场:一次归档级联把主线桶+十几个 recovery 副本桶搬进 .trash(桶清单逐桶
// 出记录、标题同源),列表上同名占多行。修法=渲染层按 trashFamilyKey 归组:
// 主线记录为主体占一行,副本进计数徽标+展开明细;明细里每个副本可预览、
// 可单独还原(还原哪条=还原哪个桶的单桶语义不破)。
import { registerHooks } from "node:module";
registerHooks({ resolve(specifier, context, nextResolve) { return specifier.endsWith(".svg") ? nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url }) : nextResolve(specifier, context); } });
import assert from "node:assert/strict";
import { managementDom } from "../test-support/managementDom";
import type { SessionMeta } from "../lib/types";
const dom = managementDom();
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
const { TrashPage } = await import("../components/TrashPage");

const familyStem = "20261024-011346.045066400-tl1429d2-family";
const copyHexes = ["29384636d8d4c0b2", "c4e94f82676c9547", "fd8f06a5a1ecf671"];
const trashRecord = (key: string, copy: boolean, deletedAt: number): SessionMeta => ({
  path: `/sessions/.trash/${key}/${key}.jsonl`,
  title: "TL1429d2 族", preview: "TL1429d2 族", topicTitle: "TL1429d2 族",
  turns: 2, createdAt: 1, lastActivityAt: deletedAt, modTime: deletedAt, deletedAt,
  current: false, open: false,
  trashFamilyKey: familyStem,
  trashFamilyCopy: copy,
});
const mainline = () => trashRecord(familyStem, false, 1729000000000);
const copy = (hex: string) => trashRecord(`${familyStem}-recovery-${hex}`, true, 1729000000000 - 1);
const unrelated = (): SessionMeta => ({ path: "/sessions/.trash/unrelated/unrelated.jsonl", title: "无关会话", preview: "无关会话", turns: 1, createdAt: 1, lastActivityAt: 1728000000000, modTime: 1728000000000, deletedAt: 1728000000000, current: false, open: false });
const legacyRecoveryCopy = (): SessionMeta => ({ ...unrelated(), path: "/sessions/.trash/legacy/legacy.jsonl", title: "legacy 母本覆盖副本", recoveryCopy: true });

let sessions: SessionMeta[] = [
  mainline(),
  copy(copyHexes[0]),
  copy(copyHexes[1]),
  copy(copyHexes[2]),
  unrelated(),
  legacyRecoveryCopy(),
];
const restoredPaths: string[] = [];
let listFails = false;
const list = async (): Promise<SessionMeta[]> => { if (listFails) throw new Error("read failure"); return sessions; };
const restore = async (path: string) => { restoredPaths.push(path); sessions = sessions.filter((item) => item.path !== path); };
const purge = async (path: string) => { sessions = sessions.filter((item) => item.path !== path); };
Object.assign(window, { go: { main: { App: { PreviewSession: () => Promise.resolve([]) } } } });

const root = createRoot(document.getElementById("root")!);
const render = (active = true) => <LocaleProvider><ToastProvider><TrashPage active={active} onBack={() => {}} list={list} purge={purge} restore={restore} /></ToastProvider></LocaleProvider>;
await act(async () => root.render(render()));

// 主钉①:默认视图同族合并为一行——主线+3 副本+无关会话=2 行(legacy
// recoveryCopy 记录仍归入折叠的「系统恢复数据」分区,不出现在默认视图),
// 页头计数按族计(Trash · 2)。
const histItems = () => document.querySelectorAll(".hist-item");
assert.equal(histItems().length, 2, "default view renders one row per family, not per bucket");
assert.ok(document.body.textContent?.includes("Trash · 2"), `header count follows family rows (got ${document.querySelector(".management-page__title")?.textContent ?? "no title"})`);
// 主钉②:主线行带副本计数徽标;无关行没有徽标。
const toggles = () => Array.from(document.querySelectorAll<HTMLButtonElement>(".trash-family__toggle"));
assert.equal(toggles().length, 1, "only the family row carries the copy-count badge");
assert.equal(toggles()[0].textContent?.trim(), "+3 copies", "badge counts the family copies");
assert.equal(toggles()[0].getAttribute("aria-expanded"), "false", "copy detail starts collapsed");
// 主钉③:展开明细可见每个副本桶,徽标 recovery-<hex8> 与桶名对得上。
await act(async () => toggles()[0].click());
assert.equal(toggles()[0].getAttribute("aria-expanded"), "true", "toggle marks expanded state");
assert.equal(histItems().length, 5, "expanded detail lists every copy bucket");
const badges = Array.from(document.querySelectorAll(".hist-item__badge--copy")).map((node) => node.textContent?.trim());
assert.equal(badges.length, 3, "each copy child row carries a copy badge");
for (const hex of copyHexes) {
  assert.ok(badges.some((text) => text === `copy recovery-${hex.slice(0, 8)}`), `badge recovery-${hex.slice(0, 8)} maps to its bucket (got ${JSON.stringify(badges)})`);
}
// 主钉④:明细里的副本可单独还原——预览副本行,Restore 只作用于该副本桶。
const childRows = document.querySelectorAll(".trash-family__children .hist-item__main");
await act(async () => (childRows[0] as HTMLButtonElement).click());
assert.equal(document.querySelector(".history-preview__title")?.textContent, "TL1429d2 族", "child row preview works");
await act(async () => (Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((node) => node.textContent?.trim() === "Restore"))!.click());
assert.deepEqual(restoredPaths, [`${familyStem}-recovery-${copyHexes[0]}`].map((key) => `/sessions/.trash/${key}/${key}.jsonl`), "restore from the detail acts on that copy bucket only");
// 主钉⑤:明细保持展开,剩余副本随之减一;再点徽标收起,行数回到合并态且计数更新。
await act(async () => {});
await act(async () => {});
assert.equal(document.querySelectorAll(".trash-family__children .hist-item").length, 2, "remaining copies follow the removed one");
await act(async () => toggles()[0].click());
assert.equal(histItems().length, 2, "collapsing returns the list to the merged view");
assert.equal(toggles()[0].textContent?.trim(), "+2 copies", "badge count follows the removed copy");
assert.ok(sessions.some((item) => item.path.endsWith(`${familyStem}.jsonl`)), "mainline bucket untouched by the child restore");

await act(async () => root.render(render(false)));
assert.equal(document.querySelector(".management-modal-backdrop"), null);
await act(async () => root.unmount());
dom.window.close();
console.log("PASS trash family merge: one row per family, copy-count badge, expandable per-bucket detail with per-copy restore");
