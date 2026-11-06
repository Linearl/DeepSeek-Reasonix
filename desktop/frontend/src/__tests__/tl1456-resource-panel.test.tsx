// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1456-resource-panel.test.tsx
// tl1456 验收 harness —— 资源管理器（命令面板 → 工具与诊断）：
//  ① 可达链（源码钉）：App.tsx 在「工具与诊断」组追加 cmd-resource-manager、
//     开才挂载 ResourcePanel；三语 keys；bridge 契约+mock。
//  ② 命令面板搜「资源管理器」可达（行为面）：App 同款 item 形态进
//     CommandPalette，查询词过滤命中 + Enter 触发 run。
//  ③ 面板渲染：系统内存条/Go runtime 栅格/进程表（本进程+子进程）/子代理
//     列表；诚实降级（measured/available=false → 「—」/无读数，不用 0 冒充）。
//  ④ 刷新节流：首开即拉一次；interval 挂 [open, refreshMs]（默认 3s），换档
//     重建；手动刷新即时；关闭即全清（零常驻定时器）。
//  ⑤ 代围栏（generation fence）：面板开着时过期回包零写入，只有最新代号
//     能落状态。
//  ⑥ Esc / 点 backdrop 关闭。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { ResourcePanel } from "../components/ResourcePanel";
import type { ResourceSnapshotView } from "../lib/bridge";
import { CommandPalette, type PaletteItem } from "../components/CommandPalette";
import { LocaleProvider } from "../lib/i18n";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

const here = fileURLToPath(new URL(".", import.meta.url));
const frontendRoot = `${here}../..`;
const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");

function installDom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
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

/** setInterval 记录垫片：只登记不真跑，fire() 手动触发单次 tick。 */
function installIntervalShim() {
  type Timer = { id: number; ms: number; cb: () => void; cleared: boolean };
  const timers: Timer[] = [];
  let nextId = 1;
  const w = window as unknown as {
    setInterval: (cb: () => void, ms?: number) => number;
    clearInterval: (id: number) => void;
  };
  w.setInterval = (cb: () => void, ms?: number) => {
    const id = nextId;
    nextId += 1;
    timers.push({ id, ms: ms ?? 0, cb, cleared: false });
    return id;
  };
  w.clearInterval = (id: number) => {
    const t = timers.find((x) => x.id === id);
    if (t) t.cleared = true;
  };
  return {
    timers,
    fire(id: number) {
      const t = timers.find((x) => x.id === id);
      if (t && !t.cleared) t.cb();
    },
  };
}

/**
 * 向 React 受控 input 键入文本。jsdom note（composer-ime 同款）：本 harness
 * 先加载 react-dom 后装 DOM，React 退化到 keyup 驱动的 change 探测——
 * 原生 setter 写值 + input 事件喂捕获面 + trailing keyup 才送达 onChange。
 */
function typeIntoInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
  if (!setter) throw new Error("missing input value setter");
  input.focus(); // focusin 预挂 polyfill（attachEvent 垫片已就位）
  setter.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new KeyboardEvent("keyup", { key: "Process", bubbles: true }));
}

console.log("\ntl1456 resource manager: palette reachability + render honesty + throttle + fence");

// ── ① 可达链源码钉 ──────────────────────────────────────────────────
{
  const appTsx = read("src/App.tsx");
  ok(appTsx.includes('id: "cmd-resource-manager"'), "App registers the palette entry");
  // 追加式注册：落在 toolsCmds（工具与诊断组）块内、helpCmds 之前——
  // 与 tl1462 的注册改动天然可并（不重排既有项）。
  const toolsAt = appTsx.indexOf("const toolsCmds: PaletteItem[]");
  const helpAt = appTsx.indexOf("const helpCmds: PaletteItem[]");
  const entryAt = appTsx.indexOf('id: "cmd-resource-manager"');
  ok(toolsAt >= 0 && helpAt > toolsAt && entryAt > toolsAt && entryAt < helpAt, "the entry sits in the tools & diagnostics group (appended, existing order untouched)");
  ok(appTsx.includes('group: t("palette.group.tools")'), "the entry uses the tools group label");
  ok(appTsx.includes('title: t("palette.cmd.resourceManager")'), "the entry title comes from the locale key (搜索「资源管理器」可达)");
  ok(appTsx.includes("run: () => setResourcePanelOpen(true)"), "running the entry opens the resource panel");
  ok(appTsx.includes("const [resourcePanelOpen, setResourcePanelOpen] = useState(false);"), "panel open state is declared");
  ok(appTsx.includes("{resourcePanelOpen && ("), "the panel mounts only while open (关闭即卸载)");
  ok(appTsx.includes("<ResourcePanel open onClose={() => setResourcePanelOpen(false)} />"), "App mounts ResourcePanel behind the open flag");

  const en = read("src/locales/en.ts");
  const zh = read("src/locales/zh.ts");
  const zhTw = read("src/locales/zh-TW.ts");
  const keys = [
    "palette.cmd.resourceManager",
    "resourcePanel.title",
    "resourcePanel.interval",
    "resourcePanel.intervalOption",
    "resourcePanel.refresh",
    "resourcePanel.close",
    "resourcePanel.loading",
    "resourcePanel.loadFailed",
    "resourcePanel.system.title",
    "resourcePanel.system.used",
    "resourcePanel.system.summary",
    "resourcePanel.system.unavailable",
    "resourcePanel.runtime.title",
    "resourcePanel.runtime.heapAlloc",
    "resourcePanel.runtime.heapSys",
    "resourcePanel.runtime.heapObjects",
    "resourcePanel.runtime.totalSys",
    "resourcePanel.runtime.goroutines",
    "resourcePanel.runtime.gc",
    "resourcePanel.runtime.gcPause",
    "resourcePanel.processes.title",
    "resourcePanel.col.name",
    "resourcePanel.col.pid",
    "resourcePanel.col.memory",
    "resourcePanel.col.uptime",
    "resourcePanel.processes.mainBadge",
    "resourcePanel.processes.noChildren",
    "resourcePanel.subagents.title",
    "resourcePanel.subagents.empty",
  ];
  let localeOk = true;
  for (const key of keys) {
    if (!en.includes(`"${key}"`) || !zh.includes(`"${key}"`) || !zhTw.includes(`"${key}"`)) {
      localeOk = false;
      process.stdout.write(`        missing ${key}\n`);
    }
  }
  ok(localeOk, `three dialects carry all ${keys.length} tl1456 keys`);

  const bridge = read("src/lib/bridge.ts");
  ok(bridge.includes("ResourceSnapshot(): Promise<ResourceSnapshotView>;"), "bridge AppBindings declares ResourceSnapshot");
  ok(bridge.includes("async ResourceSnapshot() {"), "bridge mock implements ResourceSnapshot");
  ok(bridge.includes("available: false"), "bridge mock keeps the dev-shell system strip honest (available=false)");
}

// ── ② 命令面板搜「资源管理器」可达（行为面，App 同款 item 形态）──────
{
  const dom = installDom();
  const calls = { run: 0, close: 0 };
  // 与 App.tsx cmd-resource-manager 同款形态（title/keywords 逐字一致）。
  const resourceEntry: PaletteItem = {
    id: "cmd-resource-manager",
    group: "工具与诊断",
    title: "资源管理器",
    icon: undefined,
    compact: true,
    keywords: ["resource", "manager", "memory", "process", "资源", "管理器", "内存", "进程"],
    run: () => {
      calls.run += 1;
    },
  };
  const decoys: PaletteItem[] = [
    { id: "cmd-usage-stats", group: "工具与诊断", title: "使用统计", compact: true, run: () => {} },
    { id: "cmd-trash", group: "工具与诊断", title: "回收站", compact: true, run: () => {} },
  ];
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <CommandPalette
          open
          onClose={() => {
            calls.close += 1;
          }}
          items={[...decoys, resourceEntry]}
          placeholder="搜索"
          emptyText="无结果"
        />
      </LocaleProvider>,
    );
    await flush();
  });

  const input = document.querySelector<HTMLInputElement>(".palette__input");
  if (!input) throw new Error("missing palette input");
  await act(async () => {
    typeIntoInput(input, "资源管理器");
    await flush();
  });
  const chips = [...document.querySelectorAll<HTMLButtonElement>(".palette__chip")];
  ok(chips.some((c) => c.textContent?.includes("资源管理器")), "querying 资源管理器 surfaces the resource manager chip");
  ok(chips.every((c) => !c.textContent?.includes("回收站")), "non-matching entries are filtered out");
  ok(chips.length === 1, "exactly one chip survives the query");

  input.focus();
  await act(async () => {
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flush();
  });
  ok(calls.run === 1, "Enter runs the resource manager entry");
  ok(calls.close === 1, "Enter closes the palette");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// ── ③ 面板渲染 + 测量诚实性 ─────────────────────────────────────────
{
  const dom = installDom();
  const MB = 1024 * 1024;
  const GB = 1024 * 1024 * 1024;
  const loadCountBox = { n: 0 };
  const frames: ResourceSnapshotView[] = [
    {
      ts: "2026-11-06T10:00:00.000Z",
      uptimeSeconds: 120,
      main: { name: "reasonix.exe", pid: 4117, memoryBytes: 220 * MB, uptimeSeconds: 120, kind: "main", measured: true },
      children: [
        { name: "child-a.exe", pid: 4242, memoryBytes: 512 * 1024, uptimeSeconds: 90, kind: "child", measured: true },
        { name: "child-b.exe", pid: 4243, memoryBytes: 0, uptimeSeconds: 0, kind: "child", measured: false },
      ],
      runtime: {
        heapAllocBytes: 123 * MB,
        heapSysBytes: 256 * MB,
        heapObjects: 1_500_000,
        sysBytes: 400 * MB,
        goroutines: 88,
        numGC: 42,
        lastGCPauseMs: 0.5,
      },
      system: { totalBytes: 8 * GB, availableBytes: 3 * GB, available: true },
      subagents: [
        { ref: "sub-1", name: "调研代理", description: "扫描既有诊断面", origin: "主线开发", startedAt: Date.parse("2026-11-06T09:59:00.000Z") },
        { ref: "sub-2", startedAt: 0 },
      ],
    },
  ];
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ResourcePanel open onClose={() => {}} load={() => Promise.resolve(frames[Math.min(loadCountBox.n++, frames.length - 1)])} />
      </LocaleProvider>,
    );
    await flush();
  });

  ok(document.querySelector('[data-testid="res-panel"]') !== null, "the dialog mounts with the res-panel testid");
  ok(loadCountBox.n === 1, "opening pulls exactly one snapshot immediately (不等第一个 tick)");

  // 系统内存条：8GB 总 / 3GB 可用 → 已用 5GB = 62.5% → round 63%。
  const fill = document.querySelector<HTMLElement>('[data-testid="res-system-fill"]');
  ok(fill !== null && fill.style.width === "63%", `system bar fill sits at 63% (got ${fill?.style.width})`);
  const sysLabel = document.querySelector('[data-testid="res-system-label"]');
  ok(sysLabel?.textContent?.includes("63") === true, "system summary carries the used percent");

  // Go runtime 栅格：堆存活 123MB。
  const runtime = document.querySelector('[data-testid="res-runtime"]');
  ok(runtime?.textContent?.includes("123 MB") === true, "runtime section renders heap alloc in human units");
  ok(runtime?.textContent?.includes("88") === true, "runtime section renders goroutine count");

  // 进程表：本进程行 + 两个子进程行；未测量行诚实显示「—」。
  ok(document.querySelector('[data-testid="res-row-main"]') !== null, "the main process row renders");
  const mainMemory = document.querySelector('[data-testid="res-main-memory"]');
  ok(mainMemory?.textContent === "220 MB", `main process memory renders (got ${mainMemory?.textContent})`);
  const tbody = document.querySelector('[data-testid="res-processes"] tbody');
  const rows = tbody ? [...tbody.querySelectorAll("tr")] : [];
  ok(rows.length === 3, `process table renders main + 2 children (got ${rows.length})`);
  const childMemCells = rows.map((r) => r.querySelectorAll("td")[2]?.textContent ?? "");
  ok(childMemCells.includes("512 KB"), "measured child memory renders in human units");
  ok(childMemCells.includes("—"), "unmeasured child memory renders — (never 0-as-data)");

  // 子代理列表：名字/描述/归属/时长；无名字回退 ref；startedAt=0 → 长度「—」。
  const subRows = [...document.querySelectorAll('[data-testid="res-subagent-row"]')];
  ok(subRows.length === 2, `subagent rows render (got ${subRows.length})`);
  ok(subRows[0]?.textContent?.includes("调研代理") === true && subRows[0]?.textContent?.includes("扫描既有诊断面") === true, "subagent row carries name and description");
  ok(subRows[0]?.textContent?.includes("主线开发") === true, "subagent row carries the owning tab origin");
  ok(subRows[1]?.textContent?.includes("sub-2") === true, "nameless subagent falls back to its ref");
  const elapsedCells = subRows.map((r) => r.querySelector(".res-panel__subagent-elapsed"));
  ok(elapsedCells[0]?.textContent !== "—" && (elapsedCells[0]?.textContent?.length ?? 0) > 0, "subagent elapsed renders a duration label");
  ok(elapsedCells[1]?.textContent === "—", "zero startedAt renders — (not a fake duration)");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// ── ④ 刷新节流：interval 挂 [open, refreshMs]，关闭即清 ─────────────
{
  const dom = installDom();
  const shim = installIntervalShim();
  const loadCount = { n: 0 };
  const mkFrame = (n: number): ResourceSnapshotView => ({
    ts: `2026-11-06T10:00:0${n}.000Z`,
    uptimeSeconds: 0,
    main: { name: "reasonix.exe", pid: 1, memoryBytes: 0, uptimeSeconds: 0, kind: "main", measured: false },
    children: [],
    runtime: { heapAllocBytes: 0, heapSysBytes: 0, heapObjects: 0, sysBytes: 0, goroutines: 0, numGC: 0, lastGCPauseMs: 0 },
    system: { totalBytes: 0, availableBytes: 0, available: false },
    subagents: [],
  });
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ResourcePanel open onClose={() => {}} load={async () => mkFrame(loadCount.n++)} />
      </LocaleProvider>,
    );
    await flush();
  });

  ok(loadCount.n === 1, "first open pulls one snapshot");
  ok(shim.timers.length === 1 && shim.timers[0].ms === 3000, `exactly one interval at the default 3s (got ${shim.timers.length} @ ${shim.timers.map((t) => t.ms).join(",")}ms)`);
  const firstTimer = shim.timers[0].id;

  shim.fire(firstTimer);
  await act(async () => {
    await flush();
  });
  ok(loadCount.n === 2, "a tick pulls one more snapshot");

  const refreshBtn = document.querySelector<HTMLButtonElement>('[data-testid="res-refresh"]');
  await act(async () => {
    refreshBtn?.click();
    await flush();
  });
  ok(loadCount.n === 3, "manual refresh pulls immediately");

  // 换档 2s：旧 interval 清、新 interval 2000ms 建档，无多余拉取。
  const twoSec = document.querySelector<HTMLButtonElement>('[data-testid="res-interval-2000"]');
  await act(async () => {
    twoSec?.click();
    await flush();
  });
  ok(shim.timers.find((t) => t.id === firstTimer)?.cleared === true, "switching the interval clears the old timer");
  ok(shim.timers.some((t) => t.ms === 2000 && !t.cleared) === true, "a fresh 2s interval is installed");
  ok(loadCount.n === 3, "switching the interval does not itself pull");

  // 关闭即卸载 → 所有 interval 全清（零常驻定时器契约）。
  await act(async () => {
    root.unmount();
    await flush();
  });
  const liveTimers = shim.timers.filter((t) => !t.cleared);
  ok(liveTimers.length === 0, `unmount clears every interval (got ${liveTimers.length} live)`);
  shim.fire(firstTimer);
  ok(loadCount.n === 3, "firing a cleared timer pulls nothing");
  dom.window.close();
}

// ── ⑤ 代围栏：过期回包零写入 ────────────────────────────────────────
{
  const dom = installDom();
  const pending: ((v: ResourceSnapshotView) => void)[] = [];
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const frame = (mark: string, usedPercent: number): ResourceSnapshotView => ({
    ts: `2026-11-06T10:00:00.000Z#${mark}`,
    uptimeSeconds: 0,
    main: { name: "reasonix.exe", pid: 1, memoryBytes: 0, uptimeSeconds: 0, kind: "main", measured: false },
    children: [],
    runtime: { heapAllocBytes: 0, heapSysBytes: 0, heapObjects: 0, sysBytes: 0, goroutines: 0, numGC: 0, lastGCPauseMs: 0 },
    system: { totalBytes: 100, availableBytes: 100 - usedPercent, available: true },
    subagents: [],
  });
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ResourcePanel open onClose={() => {}} load={() => new Promise<ResourceSnapshotView>((res) => pending.push(res))} />
      </LocaleProvider>,
    );
    await flush();
  });
  ok(document.querySelector('[data-testid="res-loading"]') !== null, "panel shows the loading state while in flight");
  ok(pending.length === 1, "one in-flight request");

  const refreshBtn = document.querySelector<HTMLButtonElement>('[data-testid="res-refresh"]');
  await act(async () => {
    refreshBtn?.click();
    await flush();
  });
  ok(pending.length === 2, "manual refresh starts a newer request");

  // 过期回包（#1）晚到 → 不落状态，仍在 loading。
  await act(async () => {
    pending[0](frame("stale", 5));
    await flush();
  });
  ok(document.querySelector('[data-testid="res-loading"]') !== null, "a stale response writes nothing (still loading)");

  // 最新代号（#2）回包 → 落状态。
  await act(async () => {
    pending[1](frame("fresh", 77));
    await flush();
  });
  ok(document.querySelector('[data-testid="res-loading"]') === null, "the newest response lands");
  const fill = document.querySelector<HTMLElement>('[data-testid="res-system-fill"]');
  ok(fill !== null && fill.style.width === "77%", `the newest frame drives the bar (got ${fill?.style.width})`);

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

// ── ⑥ Esc 关闭 ─────────────────────────────────────────────────────
{
  const dom = installDom();
  let closes = 0;
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ResourcePanel
          open
          onClose={() => {
            closes += 1;
          }}
          load={async () => ({
            ts: "2026-11-06T10:00:00.000Z",
            uptimeSeconds: 0,
            main: { name: "reasonix.exe", pid: 1, memoryBytes: 0, uptimeSeconds: 0, kind: "main", measured: false },
            children: [],
            runtime: { heapAllocBytes: 0, heapSysBytes: 0, heapObjects: 0, sysBytes: 0, goroutines: 0, numGC: 0, lastGCPauseMs: 0 },
            system: { totalBytes: 0, availableBytes: 0, available: false },
            subagents: [],
          })}
        />
      </LocaleProvider>,
    );
    await flush();
  });
  await act(async () => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flush();
  });
  ok(closes === 1, "Escape closes the panel");

  // 点 backdrop（非面板本体）关闭。
  const backdrop = document.querySelector<HTMLElement>(".res-panel-backdrop");
  await act(async () => {
    backdrop?.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true }));
    await flush();
  });
  ok(closes === 2, "pointer-down on the backdrop closes the panel");

  await act(async () => {
    root.unmount();
  });
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
