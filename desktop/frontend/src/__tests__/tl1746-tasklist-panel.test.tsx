// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1746-tasklist-panel.test.tsx
// tl1746 第一期验收 harness —— 任务面板（只读直读 tasklist.db）：
//  ① 行为面（jsdom + 真 TasklistPanel + 注入 bindings）：总览四统计卡读数、
//     状态分布行、列表 tab 行渲染、查询失败走错误卡（去实验室设置 + 重试），
//     排除「白屏/空数据假绿」。
//  ② 开关关行为等价（铁律 2）：labFlags 默认 tasklistPanel=false；开关关 =
//     命令项不进 items（搜「任务面板」走 empty 态）；开关开 = 项回到列表，
//     回车触发打开动作。
//  ③ 接线面（源码断言，tl1462 同层）：App.tsx 注册点在 toolsCmds、同一开关
//     同一打开函数、deps 补 tasklistPanelEntryEnabled；实验室 yaml 落可观测
//     性组（unstable + onKeys [tasklistPanel]）；labLightKeys/labPaneRegistry/
//     experimentTiers 三登记齐；三语键齐。
//
// ⚠ harness 约束（tl1462 同款）：jsdom 必须在 import react-dom 之前装好
// （canUseDOM 求值一次）；react/react-dom/组件一律动态 import。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { applyLabFlags, labFlagEnabled } from "../lib/labFlags";
// tl1844：LAB_TIER_COUNTS 改活注册表归并导出（无常量字面可 grep），计数钉
// 换代为常量导入比对（见下方 unstable 钉注）。
import { LAB_TIER_COUNTS } from "../lib/experimentTiers";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { en } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

const dom = installDom();
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const TasklistPanelModule = await import("../components/TasklistPanel");
const { TasklistPanel } = TasklistPanelModule;
type TasklistPanelBindings = import("../components/TasklistPanel").TasklistPanelBindings;
const { CommandPalette } = await import("../components/CommandPalette");
const { LocaleProvider } = await import("../lib/i18n");
type TasklistQueryResult = import("../lib/bridge").TasklistQueryResult;
type TasklistOverview = import("../lib/bridge").TasklistOverview;

type PaletteItem = import("../components/CommandPalette").PaletteItem;

// 与 App.tsx toolsCmds 末尾注册的 cmd-tasklist-panel 同形状（关键词组以 ③
// 的源码断言钉住同步，漂移即红）。
const TASKLIST_KEYWORDS = ["tasklist", "panel", "tasks", "task", "任务", "任务面板", "面板", "看板"];

function tasklistItem(runCalls: { n: number }): PaletteItem {
  return {
    id: "cmd-tasklist-panel",
    group: zh["palette.group.tools"],
    title: zh["palette.cmd.tasklistPanel"],
    icon: null,
    compact: true,
    keywords: TASKLIST_KEYWORDS,
    run: () => { runCalls.n += 1; },
  };
}

async function renderPalette(root: ReturnType<typeof createRoot>, onClose: () => void, items: PaletteItem[]) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <CommandPalette
          open
          onClose={onClose}
          items={items}
          placeholder="Search"
          emptyText="No results"
        />
      </LocaleProvider>,
    );
    await flush();
  });
}

async function typeQuery(query: string) {
  const input = document.querySelector<HTMLInputElement>(".palette__input");
  if (!input) throw new Error("missing palette input");
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    if (!setter) throw new Error("missing value setter");
    setter.call(input, query);
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    await flush();
  });
}

// fixture 总览：与 Go 单测同一组数（total=7 / 待办4 / 完成2 / 取消1）。
const FIXTURE_OVERVIEW: TasklistOverview = {
  path: "D:/fixture/tasklist.db",
  total: 7, pending: 4, done: 2, cancelled: 1,
  byStatus: [
    { key: "done", count: 1 }, { key: "verified", count: 1 }, { key: "cancelled", count: 1 },
    { key: "pending", count: 1 }, { key: "blocked", count: 1 }, { key: "partial", count: 1 }, { key: "watching", count: 1 },
  ],
  byDomain: [{ key: "UI与交互", count: 3 }, { key: "记忆", count: 2 }],
  byPriority: [{ key: "P1", count: 2 }, { key: "P2", count: 2 }, { key: "", count: 1 }],
  openByDomain: [{ key: "记忆", count: 2 }, { key: "UI与交互", count: 1 }],
  openByPriority: [{ key: "P1", count: 1 }, { key: "", count: 1 }],
  generatedAt: "2026-11-22T08:00:00+08:00",
  cached: false,
};

const FIXTURE_LIST: TasklistQueryResult = {
  path: "D:/fixture/tasklist.db",
  total: 2,
  limited: false,
  elapsedMs: 1,
  rows: [
    { num: 1748, title: "会话版本分叉改善", status: "pending", priority: "P2", domain: "执行引擎与上下文", createdAt: "2026-11-21", doneAt: "", leftover: "", blockReason: "", groupTag: "" },
    { num: 1742, title: "任务面板一期", status: "in_progress", priority: "P1", domain: "UI与交互", createdAt: "2026-11-20", doneAt: "", leftover: "剩列表 tab", blockReason: "", groupTag: "" },
  ],
};

function fixtureBindings(): TasklistPanelBindings {
  return {
    getOverview: () => Promise.resolve({ ...FIXTURE_OVERVIEW }),
    queryTasks: () => Promise.resolve({ ...FIXTURE_LIST, rows: FIXTURE_LIST.rows.map((r) => ({ ...r })) }),
    settings: () => Promise.resolve({ tasklistDbPath: "" }),
    // tl1775: 面板头部「浏览器打开」——fixture 不真开，标记调用即可。
    openBoard: () => Promise.resolve({ path: "D:/fixture/tasklist.html", stale: false, opened: true, error: "" }),
  };
}

function failingBindings(): TasklistPanelBindings {
  return {
    getOverview: () => Promise.reject(new Error("任务数据库不存在：D:/nope/tasklist.db")),
    queryTasks: () => Promise.reject(new Error("任务数据库不存在：D:/nope/tasklist.db")),
    settings: () => Promise.resolve({ tasklistDbPath: "D:/nope/tasklist.db" }),
    openBoard: () => Promise.resolve({ path: "", stale: false, opened: false, error: "任务数据库不存在：D:/nope/tasklist.db" }),
  };
}

async function renderPanel(root: ReturnType<typeof createRoot>, bindings: TasklistPanelBindings) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TasklistPanel open onClose={() => {}} bindings={bindings} />
      </LocaleProvider>,
    );
    await flush();
    await flush();
  });
}

console.log("\ntl1746 tasklist panel: overview/list behavior + lab gate + wiring");

// ── ① 行为面：面板渲染（fixture 注入）───────────────────────────────
{
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  await renderPanel(root, fixtureBindings());
  const cards = document.querySelector("[data-testid='tl-cards']");
  ok(Boolean(cards), "overview renders the stat-card block");
  const cardText = cards?.textContent ?? "";
  ok(cardText.includes("7") && cardText.includes("4") && cardText.includes("2") && cardText.includes("1"),
    "stat cards carry the fixture numbers (total/pending/done/cancelled)");
  // 面板 useT 在 harness 环境（无语言偏好）回落 en——标签断言按 en 字典。
  ok(cardText.includes(en["tasklist.overview.total"]) && cardText.includes(en["tasklist.overview.pending"]),
    "stat cards carry locale labels");
  ok(Boolean(document.querySelector("[data-testid='tl-status-done']")), "status distribution renders per-status rows");
  const statusRowText = document.querySelector("[data-testid='tl-status-done']")?.textContent ?? "";
  ok(statusRowText.includes(en["tasklist.status.done"]), "status rows carry locale labels");

  // tl1746 返修：分布条必须「轨道+填充+右侧计数」三元素齐全——填充是块级
  // div（span 版 width 不生效，零宽不渲染=本次返修根因）且宽度按 max 归一。
  const fillInfo = (rowSelector: string) => {
    const fill = document.querySelector(`${rowSelector} .res-panel__sysbar-fill`);
    return { tag: fill?.tagName ?? "", width: fill instanceof HTMLElement ? fill.style.width : "" };
  };
  const doneFill = fillInfo("[data-testid='tl-status-done']");
  ok(doneFill.tag === "DIV" && doneFill.width === "100%",
    "status fill is a block div at full width when it is the max (fixture: all counts=1)");
  // fixture byStatus 缺 in_progress 桶 → count=0 → 0%（零计数零填充）；
  // 其余 7 行满条（都是 max=1）。逐行核对绑定面。
  const statusWidths = Object.fromEntries(["pending", "in_progress", "partial", "watching", "blocked", "done", "verified", "cancelled"]
    .map((key) => [key, fillInfo(`[data-testid='tl-status-${key}']`).width]));
  ok(statusWidths["in_progress"] === "0%" &&
    ["pending", "partial", "watching", "blocked", "done", "verified", "cancelled"].every((k) => statusWidths[k] === "100%"),
    "all 8 status rows render a fill bound to the max-normalized width (zero count = 0%)");

  // 比例口径：openByDomain fixture = 记忆 2 / UI与交互 1（max=2）→ 100% / 50%。
  const domainWidths = Array.from(document.querySelectorAll(".tlpanel__bar-row")).map((row) => ({
    label: row.querySelector(".tlpanel__bar-label")?.textContent ?? "",
    fill: row.querySelector(".res-panel__sysbar-fill"),
  })).filter((r) => r.label === "记忆" || r.label === "UI与交互");
  const jiYi = domainWidths.find((r) => r.label === "记忆");
  const uiHx = domainWidths.find((r) => r.label === "UI与交互");
  ok(Boolean(jiYi?.fill) && jiYi?.fill instanceof HTMLElement && jiYi.fill.style.width === "100%",
    "domain fill: max-count row (记忆=2) fills the full track");
  ok(Boolean(uiHx?.fill) && uiHx?.fill instanceof HTMLElement && uiHx.fill.style.width === "50%",
    "domain fill: half-of-max row (UI与交互=1) renders at 50%");

  // 切列表 tab → fixture 行渲染（标题 + 状态中文标签 + prio_mark 口径列）。
  await act(async () => {
    (document.querySelector("[data-testid='tl-tab-list']") as HTMLButtonElement)?.click();
    await flush();
    await flush();
  });
  const table = document.querySelector("[data-testid='tl-table']");
  ok(Boolean(table), "list tab renders the table");
  const rowText = table?.textContent ?? "";
  ok(rowText.includes("1748") && rowText.includes("会话版本分叉改善"), "list rows carry fixture task numbers and titles");
  ok(rowText.includes(en["tasklist.status.pending"]) && rowText.includes(en["tasklist.status.in_progress"]),
    "status column renders the locale labels (dict lookup works)");
  ok(rowText.includes("Leftover: 剩列表 tab"), "leftover note renders under the title (tl.py --note 落点可见)");

  await act(async () => { root.unmount(); });
  await flush();

  // ── 错误态：查询 reject → 面板内错误卡（不白屏）+ 去设置 + 重试 ──
  const root2 = createRoot(rootEl);
  await renderPanel(root2, failingBindings());
  const errCard = document.querySelector("[data-testid='tl-error']");
  ok(Boolean(errCard), "query failure renders the in-panel error card (no white screen)");
  ok((errCard?.textContent ?? "").includes("任务数据库不存在"), "error card carries the friendly reason");
  ok(Boolean(document.querySelector("[data-testid='tl-error-go-settings']")), "error card offers the go-to-lab-settings button");

  await act(async () => { root2.unmount(); });
  dom.window.close();
}

// ── ② 开关关行为等价（铁律 2）───────────────────────────────────────
{
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const runCalls = { n: 0 };
  const closeCalls = { n: 0 };
  const trash: PaletteItem = { id: "cmd-trash", group: zh["palette.group.tools"], title: zh["palette.cmd.trash"], icon: null, compact: true, run: () => {} };

  applyLabFlags({});
  ok(labFlagEnabled("tasklistPanel") === false, "labFlags defaults tasklistPanel to off");
  applyLabFlags({ tasklistPanel: true });
  ok(labFlagEnabled("tasklistPanel") === true, "applyLabFlags feeds the tasklistPanel switch");
  applyLabFlags({});

  // 开关关 = 命令项不进 items（复现 App.tsx 条件展开的关分支）→ 搜「任务面板」走 empty 态。
  // 放回收站条目占位：query 生效时最多剩一枚 chip（typing 坏了 query 恒空 → 两枚，断言即红）。
  const gatedOff = [trash, ...(false ? [tasklistItem(runCalls)] : [])];
  await renderPalette(root, () => { closeCalls.n += 1; }, gatedOff);
  await typeQuery("任务面板");
  ok(Boolean(document.querySelector(".palette__empty")), "gate off: searching 任务面板 lands on the empty state (no entry)");
  ok(document.querySelectorAll(".palette__chip").length === 0, "gate off: no tasklist chip renders");

  // 开关开：同一门控表达式产出本项 → 搜「任务」命中；回车触发打开 + 关面板。
  const gatedOn = [trash, ...(true ? [tasklistItem(runCalls)] : [])];
  await renderPalette(root, () => { closeCalls.n += 1; }, gatedOn);
  await typeQuery("任务");
  const chips = document.querySelectorAll<HTMLElement>(".palette__chip .palette__chip-label");
  ok(chips.length === 1 && chips[0]?.textContent === zh["palette.cmd.tasklistPanel"], "gate on: searching 任务 surfaces exactly the tasklist entry");
  runCalls.n = 0;
  const closeBefore = closeCalls.n;
  await act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flush();
  });
  ok(runCalls.n === 1, "Enter runs the tasklist open action");
  ok(closeCalls.n === closeBefore + 1, "Enter closes the palette");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── ③ 接线面（源码断言）──────────────────────────────────────────────
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const frontendRoot = `${here}../..`;
  const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");
  const appTsx = read("src/App.tsx");
  const yaml = read("src/lab/lab-layout.yaml");

  // 注册点在 toolsCmds：cmd-tasklist-panel 块存在。
  const at = appTsx.indexOf('id: "cmd-tasklist-panel"');
  ok(at > 0, "App.tsx registers cmd-tasklist-panel");
  const blockEnd = appTsx.indexOf("satisfies PaletteItem", at);
  const block = at > 0 && blockEnd > 0 ? appTsx.slice(at, blockEnd) : "";

  // 同一开关：命令项挂 tasklistPanelEntryEnabled 条件展开（与实验室卡同一开关语义）。
  ok(appTsx.includes("...(tasklistPanelEntryEnabled\n        ? ["), "palette entry rides the tasklistPanelEntryEnabled conditional spread");
  ok(appTsx.includes('const [tasklistPanelEntryEnabled, setTasklistPanelEntryEnabled] = useState(labFlagEnabled("tasklistPanel"));'), "tasklistPanelEntryEnabled derives from labFlagEnabled(tasklistPanel)");

  // 同一打开函数：palette run 与挂载点同源 setTasklistPanelOpen。
  ok(block.includes("run: () => setTasklistPanelOpen(true)"), "palette run calls setTasklistPanelOpen(true)");
  ok(appTsx.includes("{tasklistPanelOpen && (") && appTsx.includes("<TasklistPanel open onClose={() => setTasklistPanelOpen(false)} />"), "panel mounts from the same tasklistPanelOpen state");

  // 分组与搜索词：工具与诊断组；中文词 + 英文词齐（繁体走 t() 标题，惯例同 cmd-goto-tab）。
  ok(block.includes('group: t("palette.group.tools")'), "entry belongs to the tools group (工具与诊断)");
  for (const term of TASKLIST_KEYWORDS) {
    ok(block.includes(`"${term}"`), `keywords carry "${term}"`);
  }
  ok(!block.includes("任務"), "keywords follow the EN+simplified convention (traditional comes from the locale title)");

  // deps 补 tasklistPanelEntryEnabled（条件展开引用 state）。
  const depsLine = appTsx.split("\n").find((l) => l.startsWith("  }, [t, paletteSessions") && l.includes("desktopPlatform]);"));
  ok(Boolean(depsLine && depsLine.includes("tasklistPanelEntryEnabled")), "useMemo deps include tasklistPanelEntryEnabled");

  // boot 快照应用：applyLabFlags 映射 tasklistPanel 行存在。
  ok(appTsx.includes("tasklistPanel: settings.experimentalTasklistPanel ?? false"), "boot snapshot maps experimentalTasklistPanel into labFlags");

  // 实验室 yaml：可观测性组内 tasklistPanel 卡，unstable + onKeys [tasklistPanel]。
  const obsAt = yaml.indexOf("- key: observability");
  ok(obsAt > 0, "yaml has the observability group");
  const entryAt = yaml.indexOf("- id: tasklistPanel", obsAt);
  ok(entryAt > 0, "yaml registers the tasklistPanel entry");
  const nextGroupAt = yaml.indexOf("- key: dev-debug", entryAt);
  const entryBlock = entryAt > 0 && nextGroupAt > entryAt ? yaml.slice(entryAt, nextGroupAt) : "";
  ok(entryBlock.includes("tier: unstable"), "tasklistPanel card is the unstable tier");
  ok(entryBlock.includes("onKeys: [tasklistPanel]"), "tasklistPanel card light key is tasklistPanel");

  // 三登记齐：灯键谓词 / pane 白名单 / 档位注册表。
  const lightKeys = read("src/lab/labLightKeys.ts");
  ok(lightKeys.includes("tasklistPanel: (s: SettingsView) => Boolean(s.experimentalTasklistPanel)"), "labLightKeys registers the tasklistPanel predicate");
  const paneRegistry = read("src/lab/labPaneRegistry.ts");
  ok(paneRegistry.includes('"tasklistPanel"'), "LAB_PANE_IDS whitelists tasklistPanel");
  const tiers = read("src/lib/experimentTiers.ts");
  ok(tiers.includes('tasklistPanel: "unstable"'), "experimentTiers registers tasklistPanel as unstable");
  // tl1844 换代：原两钉=常量字面 unstable=7 + 史注文本——tl1844 起
  // LAB_TIER_COUNTS 由活注册表归并导出（无常量字面可 grep）、史注由 git 与
  // task562/task1005 测试头注承载 → 改「常量 ≡ 注册表 unstable 实数」自洽
  // 断言。本件语义（tasklistPanel 在册 unstable，上行 id 钉）不变，计数步进
  // 与史注追加不再需要跟本文件。
  const liveUnstable = (tiers.match(/: "unstable",/g) ?? []).length;
  ok(LAB_TIER_COUNTS.unstable === liveUnstable, `LAB_TIER_COUNTS.unstable matches the live register (${liveUnstable})`);

  // Go 侧同档登记（对账另一半）：render.go tasklistPanel 条目 unstable + 家族子键。
  const goRender = read("../../internal/config/render.go");
  ok(goRender.includes('{"tasklistPanel", LabTierUnstable, []string{"experimental_tasklist_panel", "tasklist_db_path"}}'), "Go labFeatureTiers carries the tasklistPanel entry with the family key");

  // 三语键齐 + 关键读数（palette 标题三语；面板状态词锚点）。
  ok(zh["palette.cmd.tasklistPanel"] === "任务面板", "zh palette key");
  ok(zhTW["palette.cmd.tasklistPanel"] === "任務面板", "zh-TW palette key");
  ok(en["palette.cmd.tasklistPanel"] === "Tasklist Panel", "en palette key");
  ok(zh["tasklist.status.done"] === "已完成" && zhTW["tasklist.status.done"] === "已完成" && en["tasklist.status.done"] === "Done", "status dict keys land in all three locales");
  ok(Boolean(zh["tasklist.error.goSettings"]) && Boolean(en["tasklist.error.goSettings"]), "error-card guidance keys exist");

  // tl1746 返修：分布条渐变同款网页版（tl.py .dtrack>div 渐变式），scoped 在
  // tlpanel 不牵连 ResourcePanel 实心条；轨道/填充必须块级元素契约。
  const stylesCss = read("src/styles.css");
  ok(stylesCss.includes(".tlpanel__bar .res-panel__sysbar-fill") &&
    /(^|[^\w-])\.tlpanel__bar \.res-panel__sysbar-fill \{[^}]*linear-gradient\(90deg,\s*var\(--accent\),\s*var\(--ok\)\)/.test(stylesCss),
    "tlpanel distribution fill carries the web-version accent→ok gradient (scoped to tlpanel)");
  const panelTsx = read("src/components/TasklistPanel.tsx");
  ok(panelTsx.includes('<div className="res-panel__sysbar tlpanel__bar">') &&
    panelTsx.includes('<div className="res-panel__sysbar-fill"') &&
    !panelTsx.includes('<span className="res-panel__sysbar'),
    "distribution track and fill are block divs (ResourcePanel same-shape contract; inline span = tl1746 root cause)");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
