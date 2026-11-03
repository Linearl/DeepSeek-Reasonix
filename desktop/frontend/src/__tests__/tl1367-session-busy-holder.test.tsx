// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1367-session-busy-holder.test.tsx
//
// tl1367 验收逐条钉（归档/删除被拒四线的前端面）:
//   ① holder 块解析（lib 行为）：[reasonix-holder k="v" …] 解出 class/pid/
//     name/path/host/acquired/writer；无块文本返回 null；
//   ② 本地化渲染（lib 行为）：五类（self/live/zombie/reused/unknown）按当前
//     locale 出中文可读文案并带身份字段；非 busy 错误剥机器前缀原样透传；
//     无块但命中 sentinel 家族的错误落 unknown 通用文案；
//   ③ trashSession 预检门（controller 挂载行为）：confirmSessionOccupied 返回
//     false 时 DeleteSession 不被调用、不弹 toast；true 时正常删除；lease
//     busy 失败的 toast 走本地化文案（不再裸打英文/机器前缀）；
//   ④ 源码钉：两处菜单首击走 probeOccupiedThen；occupancyDialog.dialog 在场；
//     bridge 接口+mock 声明 ProbeSessionOccupancy；后端探针文件在场；
//   ⑤ 三语 locale：sessionBusy.* 五键 + projectTree.sessionOccupiedConfirm 齐
//     备，占位符集合一致。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/", pretendToBeVisual: true });
(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as unknown as { Node: unknown }).Node = dom.window.Node;
(globalThis as unknown as { Element: unknown }).Element = dom.window.Element;
(globalThis as unknown as { HTMLElement: unknown }).HTMLElement = dom.window.HTMLElement;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else { failed += 1; process.exitCode = 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "../..");
const desktopRoot = resolve(frontendRoot, "..");

const busy = await import("../lib/sessionBusyError");

// 后端 desktop/session_lease_holder.go sessionLeaseHolderBlock 的真实形状
// （值原样引号、Windows 路径不做反斜杠转义）。
const BLOCK_SAMPLE = '[reasonix-holder class="live" pid="27260" name="reasonix-desktop.exe" path="C:\\Program Files\\Reasonix\\versions\\v1\\reasonix-desktop.exe" host="DESKTOP-XYZ" acquired="2026-11-03T04:34:56Z" writer="devbox-27260-a1b2"] this session is in use by another Reasonix window or process (pid 27260) — close that window, then retry';

// ── ① holder 块解析 ─────────────────────────────────────────────────────────
console.log("\ntl1367 ① holder block parsing (lib behaviour)");

{
  const holder = busy.parseSessionBusyHolder(BLOCK_SAMPLE);
  ok(holder !== null, "the block sample parses");
  eq(holder?.cls, "live", "class=live parses into the live holder class");
  eq(holder?.pid, "27260", "holder pid parses");
  eq(holder?.name, "reasonix-desktop.exe", "holder process name parses");
  eq(holder?.path, "C:\\Program Files\\Reasonix\\versions\\v1\\reasonix-desktop.exe", "windows path parses WITHOUT backslash escaping");
  eq(holder?.host, "DESKTOP-XYZ", "hostname parses");
  eq(holder?.acquired, "2026-11-03T04:34:56Z", "acquired stamp parses");
  eq(holder?.writer, "devbox-27260-a1b2", "runtime writer id parses");
  eq(busy.parseSessionBusyHolder("plain refusal text"), null, "text without a block parses to null");
  const reused = busy.parseSessionBusyHolder('[reasonix-holder class="reused" pid="42"] stale record');
  eq(reused?.cls, "reused", "class=reused parses");
  const junk = busy.parseSessionBusyHolder('[reasonix-holder class="mystery" pid="7"]');
  eq(junk?.cls, "unknown", "an unknown class degrades to unknown");
}

// ── ② 本地化渲染 ────────────────────────────────────────────────────────────
console.log("\ntl1367 ② localized rendering (lib behaviour, en mirror)");

{
  const text = busy.formatSessionBusyError(new Error(BLOCK_SAMPLE));
  ok(text.includes("pid 27260"), "live copy names the holder pid");
  ok(text.includes("reasonix-desktop.exe"), "live copy names the holder process");
  ok(text.toLowerCase().includes("close that window") || text.toLowerCase().includes("close"), "live copy carries the next step");
  ok(!text.startsWith("reasonix_error:"), "no machine prefix leaks into rendered copy");
}
{
  const zombie = busy.formatSessionBusyError(new Error('[reasonix-holder class="zombie" pid="99"] this session was left locked by a crashed Reasonix process (pid 99) — restart Reasonix, then retry'));
  ok(zombie.toLowerCase().includes("restart reasonix"), "zombie copy points at the restart repair");
  const reused = busy.formatSessionBusyError(new Error('[reasonix-holder class="reused" pid="42"] stale lease record'));
  ok(reused.toLowerCase().includes("pid 42"), "reused copy names the stale pid");
  const self = busy.formatSessionBusyError(new Error('[reasonix-holder class="self" pid="12345"] held by another runtime'));
  ok(self.toLowerCase().includes("this reasonix instance"), "self copy points at this instance's own runtime");
}
{
  // 无块但命中 sentinel 家族 → unknown 通用文案（老后端兼容）。en 镜像下
  // localized 文案与 sentinel 同文，故断言落在「走 dict 渲染、无机器前缀、
  // 保留引导」上，而不是子串差异。
  const legacy = busy.formatSessionBusyError(new Error("this session is in use by another Reasonix window or process — close that window (or quit the other Reasonix instance), then retry"));
  ok(busy.isSessionBusyError("this session is in use by another Reasonix window or process"), "the sentinel family is detected without a block");
  ok(legacy.length > 0 && !legacy.startsWith("reasonix_error:") && legacy.includes("then retry"), "a blockless sentinel renders via the localized generic copy");
  ok(busy.isSessionBusyError(new Error("THIS SESSION WAS LEFT LOCKED BY A CRASHED REASONIX PROCESS — RESTART REASONIX")), "sentinel detection is case-insensitive");
}
{
  // 非 busy 错误：剥机器前缀原样透传（不吞真实失败原因）。
  const plain = busy.formatSessionBusyError(new Error("not enough disk space to finish the operation — free some space and retry"));
  eq(plain, "not enough disk space to finish the operation — free some space and retry", "non-busy errors pass through unchanged");
  const marked = busy.formatSessionBusyError(new Error("reasonix_error:topic_archive_forceable: wait for the session to finish"));
  eq(marked, "wait for the session to finish", "forceable prefix is stripped for display");
}

// ── ③ trashSession 预检门（controller 挂载行为） ────────────────────────────
console.log("\ntl1367 ③ trashSession occupancy gate (mounted controller)");

const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { createElement } = await import("react");
const { useState } = await import("react");
const controllerMod = await import("../lib/projectTreeArchive");

type Call = { name: string; args: unknown[] };
function mountSessionHost(goBridge: unknown, confirm: ((sessionPath: string) => Promise<boolean>) | undefined) {
  (dom.window as unknown as { go: unknown }).go = { main: { App: goBridge } };
  const toasts: string[] = [];
  let captured: ((path: string) => Promise<void>) | null = null;
  const refreshRef = { current: async () => {} };
  function Host() {
    const [, force] = useState(0);
    const ctl = controllerMod.useProjectTreeArchiveController({
      treeRef: { current: [] },
      topicLoadSeqRef: { current: {} },
      topicPageStateRef: { current: {} },
      updateTopicPageState: () => {},
      refreshRef,
      optimisticallyRemoveTopic: () => {},
      closeMenu: () => {},
      showToast: (text) => { toasts.push(text); force((n) => n + 1); },
      confirmSessionOccupied: confirm,
    });
    captured = (path: string) => ctl.trashSession(path);
    return null;
  }
  const container = dom.window.document.createElement("div");
  dom.window.document.body.appendChild(container);
  const root = createRoot(container);
  act(() => { root.render(createElement(Host)); });
  return { get handle() { return captured as (path: string) => Promise<void>; }, toasts };
}

{
  const calls: Call[] = [];
  const bridge = {
    async DeleteSession(path: string) { calls.push({ name: "DeleteSession", args: [path] }); },
  };
  const host = mountSessionHost(bridge, async () => false);
  await act(async () => { await host.handle("C:\\sessions\\blocked.jsonl"); });
  eq(calls.length, 0, "a declined occupancy confirm does NOT call DeleteSession");
  eq(host.toasts.length, 0, "a declined confirm raises no error toast");
}
{
  const calls: Call[] = [];
  const bridge = {
    async DeleteSession(path: string) { calls.push({ name: "DeleteSession", args: [path] }); },
  };
  const host = mountSessionHost(bridge, async () => true);
  await act(async () => { await host.handle("C:\\sessions\\free.jsonl"); });
  eq(calls.map((c) => c.name), ["DeleteSession"], "an accepted confirm deletes the session");
  eq(host.toasts.length, 0, "a clean delete raises no toast");
}
{
  // lease busy 失败 → toast 走本地化文案（解析 holder 块），不再裸打英文句子。
  const calls: Call[] = [];
  const busyBlock = '[reasonix-holder class="live" pid="777" name="reasonix-desktop.exe" host="H"] this session is in use by another Reasonix window or process — close that window, then retry';
  const bridge = {
    async DeleteSession(_path: string) { throw new Error(busyBlock); },
  };
  const host = mountSessionHost(bridge, undefined);
  await act(async () => { await host.handle("C:\\sessions\\busy.jsonl"); });
  eq(calls.length, 0, "no bridge call beyond the refused delete");
  eq(host.toasts.length, 1, "the refusal still surfaces one toast");
  ok((host.toasts[0] ?? "").includes("777"), "the toast carries the holder pid (localized rendering)");
  ok(!(host.toasts[0] ?? "").includes("[reasonix-holder"), "the raw holder block never reaches the toast");
}

// ── ④ 源码钉：预检接线 ──────────────────────────────────────────────────────
console.log("\ntl1367 ④ wiring source pins");

const treeSource = readFileSync(resolve(frontendRoot, "src/components/ProjectTree.tsx"), "utf8");
const bridgeSource = readFileSync(resolve(frontendRoot, "src/lib/bridge.ts"), "utf8");

ok(treeSource.includes("probeOccupiedThen(sessionPath"), "the row trash menu's first click runs the occupancy preflight");
ok(treeSource.includes("probeOccupiedThen(activeSessionPath"), "the active-topic menu's first click runs the occupancy preflight with the active session path");
ok(treeSource.includes("{occupancyDialog.dialog}"), "the occupancy dialog mounts in the tree");
ok(treeSource.includes("confirmSessionOccupied,"), "the archive controller receives the preflight callback");
ok(treeSource.includes("void trashTopicForceableAny(activeTopicId)"), "the active-topic confirm click goes through the forceable handler (localized failure path)");
ok(bridgeSource.includes("ProbeSessionOccupancy(sessionPath: string): Promise<SessionOccupancyInfo>"), "the bridge interface declares ProbeSessionOccupancy");
ok(bridgeSource.includes("async ProbeSessionOccupancy(_sessionPath: string)"), "the browser-dev mock answers the probe");
ok(readFileSync(resolve(desktopRoot, "session_occupancy_probe.go"), "utf8").includes("func (a *App) ProbeSessionOccupancy"), "the backend probe endpoint exists");

// ── ⑤ 三语 locale ───────────────────────────────────────────────────────────
console.log("\ntl1367 ⑤ locale keys (en/zh/zh-TW)");

const KEYS = [
  "sessionBusy.self",
  "sessionBusy.live",
  "sessionBusy.zombie",
  "sessionBusy.reused",
  "sessionBusy.unknown",
  "projectTree.sessionOccupiedConfirm",
];
function placeholders(line: string): string[] {
  return [...line.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");
}
const enDict = readFileSync(resolve(frontendRoot, "src/locales/en.ts"), "utf8");
const zhDict = readFileSync(resolve(frontendRoot, "src/locales/zh.ts"), "utf8");
const zhTWDict = readFileSync(resolve(frontendRoot, "src/locales/zh-TW.ts"), "utf8");
const enPlaceholders = new Map<string, string>();
for (const key of KEYS) {
  const enLine = enDict.split("\n").find((line) => line.includes(`"${key}"`));
  ok(enLine !== undefined, `en defines ${key}`);
  if (!enLine) continue;
  enPlaceholders.set(key, placeholders(enLine));
  ok(zhDict.includes(`"${key}"`), `zh defines ${key}`);
  ok(zhTWDict.includes(`"${key}"`), `zh-TW defines ${key}`);
  const zhLine = zhDict.split("\n").find((line) => line.includes(`"${key}"`));
  const zhTWLine = zhTWDict.split("\n").find((line) => line.includes(`"${key}"`));
  if (zhLine) eq(placeholders(zhLine), enPlaceholders.get(key), `zh ${key} placeholders match en`);
  if (zhTWLine) eq(placeholders(zhTWLine), enPlaceholders.get(key), `zh-TW ${key} placeholders match en`);
}

console.log(`\ntl1367: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
