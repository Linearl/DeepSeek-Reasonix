// Run: tsx src/__tests__/dag-batch-cleanup-tl1573.test.tsx
//
// tl1573: 会话存储页的「DAG 批量清理」按钮——渲染层验收钉四点：
//  ① 入口按钮以「DAG 批量清理」可见（清理动作在页面上读得出来）；
//  ② 点击触发清理流程：先 plan（只读清单），确认层逐行列出将删文件与体量；
//  ③ 危险操作带确认：执行键在归零门/将删清单俱全时才出现，未确认不 run；
//  ④ run 后结果可见（删除份数+释放体量+失败数），plan 被拒（仍有未迁移）
//     时只给关闭不给删除键。
// tl1573-r2（20261114 用户实测三缺陷返修）追加钉：
//  ⑧⑨ 缺陷①复现钉——同卡迁移段的 nil-slice wire（`sessions: null`，Go nil
//      slice 序列化形态）不得崩渲染（原样即「扫描后页面加载失败」P1）。
//  ⑩⑪⑫ 缺陷③——清理结果改模态面板+表格（标题/时间/体积/状态），旧后端
//      无 rows 时由 plan 清单合成行（nil 家族防线）。
// 后端语义（倒序删/保留最近一份/归零门/互斥）由 desktop/dag_backup_cleanup_test.go
// 覆盖；源码契约（挂载位置/键面）由 dag-backup-cleanup.test.ts 钉住。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { DagBackupCleanupPlanView, DagBackupCleanupRunView, DagMigrationScanView } from "../lib/bridge";
import type { Translator } from "../lib/i18n";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { DagBackupCleanupSection } = await import("../components/DagBackupCleanupSection");
const { LocaleProvider, useT } = await import("../lib/i18n");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

async function flush() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
}

function installDom(): HTMLElement {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

function bodyText(): string {
  return document.body.textContent || "";
}

const PLAN_OK: DagBackupCleanupPlanView = {
  backups: [
    { path: "/sessions/a.jsonl.dagbak-tl1227", bytes: 300, modifiedAt: 1793000000000 },
    { path: "/sessions/b.jsonl.dagbak-tl1227", bytes: 500, modifiedAt: 1793007200000 },
  ],
  deletable: [{ path: "/sessions/a.jsonl.dagbak-tl1227", bytes: 300, modifiedAt: 1793000000000 }],
  keepPath: "/sessions/b.jsonl.dagbak-tl1227",
  keepBytes: 500,
  deleteBytes: 300,
  remainingMigrations: 0,
  // 组件的执行键门是 plan.error === ""（undefined 不算过）——fixture 必须照
  // 后端真实形态带空串。
  error: "",
};

const RUN_OK: DagBackupCleanupRunView = { deleted: 1, deletedBytes: 300, failed: 0, keptPath: "/sessions/b.jsonl.dagbak-tl1227" };

type Spies = {
  planCalls: number;
  runCalls: number;
};

function stubBridge(overrides: { plan?: () => DagBackupCleanupPlanView; run?: () => Promise<DagBackupCleanupRunView> }, spies: Spies) {
  window.go = {
    main: {
      App: {
        PlanDagBackupCleanup: async () => {
          spies.planCalls += 1;
          return overrides.plan ? overrides.plan() : PLAN_OK;
        },
        RunDagBackupCleanup: async () => {
          spies.runCalls += 1;
          if (overrides.run) return await overrides.run();
          return RUN_OK;
        },
      } as unknown as AppBindingsLike,
    },
  } as typeof window.go;
}

type AppBindingsLike = Record<string, unknown>;

/** The section takes a Translator prop; wrap it the way the lab card hosts it. */
function Host() {
  const t = useT();
  return <DagBackupCleanupSection t={t as Translator} busy={false} />;
}

async function mount(spies: Spies, overrides: { plan?: () => DagBackupCleanupPlanView; run?: () => Promise<DagBackupCleanupRunView> } = {}): Promise<Root> {
  stubBridge(overrides, spies);
  const root = createRoot(installDom());
  await act(async () => {
    root.render(<LocaleProvider><Host /></LocaleProvider>);
  });
  await flush();
  return root;
}

console.log("\nDAG batch cleanup button (tl1573)");

// ① 入口可见 + ② 点击触发流程（plan → 逐行确认）。
{
  const spies: Spies = { planCalls: 0, runCalls: 0 };
  const root = await mount(spies);
  const entry = buttonByText("DAG batch cleanup");
  ok(Boolean(entry), "① the entry button reads as DAG batch cleanup");
  ok(Boolean(entry?.disabled === false), "① the entry is clickable");
  await act(async () => { entry?.click(); });
  await flush();
  ok(spies.planCalls === 1, "② the click plans first (one read-only scan)");
  const confirmBox = document.querySelector("[data-dag-backup-cleanup-confirm]");
  ok(Boolean(confirmBox), "② the confirm step renders");
  ok(bodyText().includes("/sessions/a.jsonl.dagbak-tl1227"), "② the confirm lists each deletable file by path");
  ok(bodyText().includes("300 B"), "② the confirm shows per-file size");
  ok(bodyText().includes("keeping the newest"), "② the keep-newest rule stays visible at the confirm step");
  ok(spies.runCalls === 0, "③ nothing is deleted before the explicit confirm click");
  // ③ 确认键出现 → 点击执行 → ④ 结果可见。
  const runButton = buttonByText("Delete 1 old backup(s)");
  ok(Boolean(runButton), "③ the run button names the delete count");
  await act(async () => { runButton?.click(); });
  await flush();
  ok(spies.runCalls === 1, "③ the confirmed click runs the cleanup exactly once");
  const resultBox = document.querySelector("[data-dag-backup-cleanup-result]");
  ok(Boolean(resultBox), "④ the receipt renders after the run");
  ok(bodyText().includes("1") && bodyText().includes("300 B"), "④ the receipt names the deleted count and freed size");
  await act(async () => root.unmount());
}

// ⑤ 归零门拒绝：仍有未迁移时只给关闭，不给删除键。
{
  const spies: Spies = { planCalls: 0, runCalls: 0 };
  const root = await mount(spies, {
    plan: () => ({ ...PLAN_OK, remainingMigrations: 2 }),
  });
  await act(async () => { buttonByText("DAG batch cleanup")?.click(); });
  await flush();
  ok(bodyText().includes("not migrated yet"), "⑤ the blocked reason names the un-migrated sessions");
  ok(!buttonByText("Delete 1 old backup(s)"), "⑤ no delete button is offered while the gate refuses");
  ok(spies.runCalls === 0, "⑤ the gate refusal keeps the run untouched");
  await act(async () => root.unmount());
}

// ⑥ 无可删清单（不足两份）时同样不给删除键。
{
  const spies: Spies = { planCalls: 0, runCalls: 0 };
  const root = await mount(spies, {
    plan: () => ({ ...PLAN_OK, deletable: [] }),
  });
  await act(async () => { buttonByText("DAG batch cleanup")?.click(); });
  await flush();
  ok(bodyText().includes("Nothing to clean up"), "⑥ the nothing-to-clean state is named");
  ok(!buttonByText("Delete 1 old backup(s)"), "⑥ no delete button without a deletable list");
  ok(spies.runCalls === 0, "⑥ the run stays untouched");
  await act(async () => root.unmount());
}

// ⑦ run 失败可见（失败计数进回执，不静默）。
{
  const spies: Spies = { planCalls: 0, runCalls: 0 };
  const root = await mount(spies, {
    run: async () => ({ deleted: 0, deletedBytes: 0, failed: 1 }),
  });
  await act(async () => { buttonByText("DAG batch cleanup")?.click(); });
  await flush();
  await act(async () => { buttonByText("Delete 1 old backup(s)")?.click(); });
  await flush();
  ok(bodyText().includes("1 failed"), "⑦ a failed run names the failure count in the receipt");
  await act(async () => root.unmount());
}

// ⑧ tl1573-r2 缺陷①复现钉：旧后端 wire 送 `sessions: null`（Go nil slice 序列化
// 形态，全库已迁移=零命中时必现）时，迁移卡的确认层必须照常渲染（零待迁移
// 文案），页面不得崩进「设置加载失败」。崩点原样：render 期对 null 调
// .filter（DagMigrationSection 确认层的 pending/activeRows 读数）。
{
  const spies: Spies = { planCalls: 0, runCalls: 0 };
  const { DagMigrationSection } = await import("../components/DagMigrationSection");
  const nullSessionsScan = { sessions: null, scanned: 0, backups: 0, elapsedMs: 0 } as unknown as DagMigrationScanView;
  window.go = {
    main: {
      App: {
        ScanDagMigration: async () => nullSessionsScan,
        RunDagMigration: async () => ({ sessions: null, migrated: 0, active: 0, skipped: 0, failed: 0, elapsedMs: 0 }),
      } as unknown as AppBindingsLike,
    },
  } as typeof window.go;
  const root = createRoot(installDom());
  let crashed: unknown = null;
  const Host2 = () => {
    const t2 = useT();
    return <DagMigrationSection t={t2 as Translator} busy={false} apply={async () => true} />;
  };
  try {
    await act(async () => {
      root.render(<LocaleProvider><Host2 /></LocaleProvider>);
    });
    await flush();
    await act(async () => { buttonByText("Scan")?.click(); });
    await flush();
  } catch (e) {
    crashed = e;
  }
  ok(crashed === null, "⑧ a null-sessions scan must not crash the render (the tl1573-r2 P1 page-load failure)");
  ok(Boolean(document.querySelector("[data-dag-migration-confirm]")), "⑧ the migration confirm step still renders on a null-sessions wire");
  ok(bodyText().includes("Nothing to migrate"), "⑧ the zero-pending state keeps its copy");
  ok(!buttonByText("Migrate now"), "⑧ no migrate button without pending sessions");
  await act(async () => root.unmount());
}

// ⑨ tl1573-r2 缺陷①同族：run 结果 `sessions: null` 时 done 态的 failed 清单读数
// （.some/.filter 链）不得崩——同一个 nil-slice wire 家族。
{
  const { DagMigrationSection } = await import("../components/DagMigrationSection");
  window.go = {
    main: {
      App: {
        ScanDagMigration: async () => ({
          sessions: [{ sessionPath: "/s/x.jsonl", label: "x", logBytes: 10, messages: 1, status: "pending" }],
          scanned: 1, backups: 0, elapsedMs: 0,
        } as unknown as DagMigrationScanView),
        RunDagMigration: async () => ({ sessions: null, migrated: 1, active: 0, skipped: 0, failed: 0, elapsedMs: 5 }),
      } as unknown as AppBindingsLike,
    },
  } as typeof window.go;
  const root = createRoot(installDom());
  let crashed: unknown = null;
  const Host3 = () => {
    const t2 = useT();
    // apply 桩必须真的执行回调——真实 apply（SettingsPanel 的保存包装）会调用
    // fn()；桩吞掉回调会让 run 的内层永不执行、phase 卡在 running。
    return <DagMigrationSection t={t2 as Translator} busy={false} apply={async (fn) => { await fn(); return true; }} />;
  };
  try {
    await act(async () => {
      root.render(<LocaleProvider><Host3 /></LocaleProvider>);
    });
    await flush();
    await act(async () => { buttonByText("Scan")?.click(); });
    await flush();
    await act(async () => { buttonByText("Migrate now")?.click(); });
    await flush();
  } catch (e) {
    crashed = e;
  }
  ok(crashed === null, "⑨ a null-sessions run result must not crash the done-state render");
  ok(Boolean(document.querySelector("[data-dag-migration-result]")), "⑨ the migration receipt renders on a null-sessions wire");
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
