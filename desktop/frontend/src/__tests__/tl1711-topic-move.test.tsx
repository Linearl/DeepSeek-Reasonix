// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1711-topic-move.test.tsx
//
// tl1711 验收钉（会话右键「移动到项目」，单会话跨分区移动）。验收矩阵见
// docs/reasonix/issues/reports/tl1711-单会话跨项目移动-交付报告-20261120.md §0：
//   ① project 区会话右键 →「移动到项目 ▸」二级出现目标行（目标项目在场、
//     源分区自身不出现）；点击目标行 → 确认弹窗（跨分区数据面非静默动作，
//     tl1560 分组迁移同款纪律）→ 确认后 MoveSessionTopic 恰好一次、参数 =
//     (project, 源root, topicId, 目标root)；成功零 error toast；
//   ② 行 running（树上的输出点）→ 目标行禁用（入口保留，tl1560② 同款
//     发现性纪律；后端活跃门是并发窗口的兜底）；
//   ③ 确认弹窗取消 → 零落盘（会话留原位）；
//   ④ 后端移动绑定缺失（老 binary / 浏览器开发面）→「移动到项目」入口整体
//     不渲染（typeof 守卫，零回退），其余菜单入口保持在场；
//   ⑤ 后端拒绝（活跃门/锁忙/回滚）→ 错误进 toast 通道（error 级），不冒泡
//     不破窗；
//   ⑥ 源码钉：入口接线 + remoteSession 排除 + 绑定契约四参签名 + 三语键
//     齐备且仍被引用（零死键）；
//   ⑦ settle 钉：controller.moveTopicToPartition 成功/失败两路都重拉花名
//     册（GetProjectGroups force）+ 刷新话题页（tl1560 migrateGroupToPartition
//     同款收口）；
//   R 往返无损（A→B→A 注册/标题/createdAt/置顶/转录一致）是磁盘事务面，
//     由 Go 判例钉死：desktop/session_topic_migrate_test.go
//     TestMoveSessionTopicRoundTripLossless（本文件头注释留指针，不在此重钉）。

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: false, media: "", onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
    dispatchEvent: () => false,
  }),
});

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

// React must load AFTER the jsdom globals (canUseDOM pins at module load) —
// same reason the sibling tree tests use dynamic imports.
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTree } = await import("../components/ProjectTree");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");

const SOURCE_ROOT = "/repo";
const TARGET_ROOT = "/other";
const MOVE_TOPIC = "t1";

type Call = { name: string; args: unknown[] };

interface MoveBridgeSpec {
  withMoveBinding: boolean;
  moveRejects: boolean;
  running?: boolean;
}

/** Go bridge: shape-critical overrides + auto no-op fallback so the full
 * ProjectTree mount walks its real startup fan-out. `withMoveBinding=false`
 * 模拟老 binary 的 window.go.main.App（MoveSessionTopic 不存在 → proxy 返回
 * undefined，typeof 守卫据此隐藏入口）——auto fallback 对该方法显式豁免。 */
function makeGoBridge(spec: MoveBridgeSpec) {
  const calls: Call[] = [];
  const base: Record<string, unknown> = {
    async GetProjectTreeSnapshot() {
      return {
        revision: 1,
        projects: [
          {
            key: `project-${SOURCE_ROOT}`, kind: "project", label: "Proj A", root: SOURCE_ROOT,
            children: [{ key: `topic:${SOURCE_ROOT}:${MOVE_TOPIC}`, kind: "topic", label: "Moved session", root: SOURCE_ROOT, topicId: MOVE_TOPIC, running: spec.running === true }],
          },
          {
            key: `project-${TARGET_ROOT}`, kind: "project", label: "Proj B", root: TARGET_ROOT,
            children: [],
          },
        ],
        catalog: { state: "ready", revision: 0, indexed: 0, total: 0, repairPending: 0, repairActive: 0, repairDeferred: 0, repairBlocked: 0 },
      };
    },
    async ListProjectTopics(req: { workspaceRoot: string }) {
      return {
        items: req.workspaceRoot === SOURCE_ROOT
          ? [{ key: `topic:${SOURCE_ROOT}:${MOVE_TOPIC}`, kind: "topic", label: "Moved session", root: SOURCE_ROOT, topicId: MOVE_TOPIC, running: spec.running === true }]
          : [],
        revision: 1, nextCursor: "", complete: true,
      };
    },
    async ExternalOpeners() { return { openers: [], preferred: null }; },
    async GetProjectGroups() { return { groups: [], revision: 1, applied: true }; },
    async GetProjectTreeRuntimeSnapshot() {
      // running 必须经运行态投影合入树行（静态快照/话题页的 running 会被
      // withoutRuntimeState 剥掉）——merge 按 (scope, workspaceRoot, topicId) 对位。
      return {
        revision: spec.running ? 1 : 0,
        topics: spec.running ? [{
          scope: "project",
          workspaceRoot: SOURCE_ROOT,
          node: { key: `topic:${SOURCE_ROOT}:${MOVE_TOPIC}`, kind: "topic", label: "Moved session", root: SOURCE_ROOT, topicId: MOVE_TOPIC, running: true, status: "streaming" },
        }] : [],
      };
    },
    async ManageSessionsList() { return []; },
    async RemoteConnectionStatuses() { return {}; },
    async Platform() { return "windows"; },
    // MoveSessionTopic 按 spec 条件挂载：withMoveBinding=false 时该键不在
    // base 里（`prop in target` 先于 auto fallback 命中，unconditional 定义
    // 会让 typeof 守卫永远为真——④ 首跑实测钉）。
    ...(spec.withMoveBinding ? {
      async MoveSessionTopic(scope: string, sourceRoot: string, topicID: string, targetRoot: string) {
        calls.push({ name: "MoveSessionTopic", args: [scope, sourceRoot, topicID, targetRoot] });
        if (spec.moveRejects) throw new Error("this session is still working — wait for it to finish before moving it");
        return { topicId: topicID, targetWorkspaceRoot: targetRoot, movedSessions: 1 };
      },
    } : {}),
    async ReportFrontendLog() {},
  };
  return {
    calls,
    bridge: new Proxy(base, {
      get(target, prop: string) {
        if (prop in target) return (target as Record<string, unknown>)[prop];
        // 老 binary 形态：MoveSessionTopic 显式缺席（auto no-op fallback 会把
        // 缺席方法补成函数，typeof 守卫就永远为真——④ 首跑实测钉）。
        if (prop === "MoveSessionTopic") return undefined;
        return async () => undefined;
      },
    }),
  };
}

async function flush() { await new Promise((resolve) => setTimeout(resolve, 0)); }
async function settle(rounds = 6) { for (let i = 0; i < rounds; i += 1) await act(flush); }

let activeRoot: { unmount(): Promise<void> | void } | null = null;
let activeCalls: Call[] = [];

async function mountTree(spec: MoveBridgeSpec) {
  if (activeRoot) await act(async () => { void activeRoot!.unmount(); await flush(); });
  window.localStorage.clear();
  const { calls, bridge } = makeGoBridge(spec);
  activeCalls = calls;
  (dom.window as unknown as { go: unknown }).go = { main: { App: bridge } };
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(
      <StrictMode>
        <LocaleProvider>
          <ToastProvider>
            {/* timeFilter 是显示过滤的必传口径：缺省会把无活动时间的 shell 行全滤掉 */}
            <ProjectTree timeFilter="all" onTimeFilterChange={() => {}} />
          </ToastProvider>
        </LocaleProvider>
      </StrictMode>,
    );
    await flush();
  });
  await settle(4);
  // 快照行是壳：topic 行在项目文件夹展开后才渲染（折叠态只画 pinned shell）。
  const folder = document.querySelector(".project-tree__folder-main");
  if (folder instanceof HTMLElement) {
    await act(async () => { folder.click(); await flush(); });
    await settle(4);
  }
  return [...document.querySelectorAll(".project-tree__topic")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

async function cleanup() {
  if (activeRoot) await act(async () => { void activeRoot!.unmount(); await flush(); });
  activeRoot = null;
}

const menuItems = () => [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
  (el): el is HTMLElement => el instanceof HTMLElement,
);
function findByText(items: HTMLElement[], needles: string[]): HTMLElement | undefined {
  return items.find((item) => {
    const text = item.textContent ?? "";
    return needles.some((needle) => text.includes(needle));
  });
}
const dialog = () => document.querySelector(".reasonix-confirm-dialog") as HTMLElement | null;
const dialogMessage = () => document.querySelector(".reasonix-confirm-dialog__message")?.textContent ?? "";
const dialogButtons = () => [...document.querySelectorAll(".reasonix-confirm-dialog .modal__actions button")] as HTMLElement[];
const errorToasts = () => [...document.querySelectorAll(".toast--error")] as HTMLElement[];

const MOVE_ENTRY_NEEDLES = ["移动到项目", "Move to project"];
const TARGET_ROW_NEEDLES = ["移动到「Proj B」", "Move to \"Proj B\""];
const SOURCE_ROW_NEEDLES = ["移动到「Proj A」", "Move to \"Proj A\""];
const GLOBAL_ROW_NEEDLES = ["移动到 global 工作区", "Move to the global workspace"];

/** Opens the topic context menu, expands the「移动到项目」submenu, and returns
 * the target row (undefined when the entry or the row is absent). */
async function openTopicMoveMenu(row: HTMLElement) {
  await act(async () => {
    row.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const entry = findByText(menuItems(), MOVE_ENTRY_NEEDLES);
  if (!entry) return undefined;
  await act(async () => { entry.click(); await flush(); });
  return findByText(menuItems(), TARGET_ROW_NEEDLES);
}

// ── ① 确认后恰好一次落盘（参数全对 + 零 error toast） ─────────────────────────
console.log("\ntl1711① confirm dialog routes one MoveSessionTopic call with the exact partition args");
{
  const rows = await mountTree({ withMoveBinding: true, moveRejects: false });
  ok(rows.length > 0, "整树挂载：topic 行渲染");
  const target = rows.length > 0 ? await openTopicMoveMenu(rows[0]!) : undefined;
  ok(Boolean(target), "① 「移动到项目 ▸」二级出现目标行「移动到「Proj B」」");
  ok(!findByText(menuItems(), SOURCE_ROW_NEEDLES), "① 源分区自身（Proj A）不出现在目标清单（自移动无语义）");
  ok(target === undefined || target!.disabled === false, "① idle 行的目标行可点");
  if (target) {
    await act(async () => { target!.click(); await flush(); });
    ok(dialog() !== null, "① 点击目标行先出确认弹窗（跨分区数据面，非静默动作）");
    ok(dialogMessage().includes("Moved session") || dialogMessage().includes("Moved session".length === 0), "① 弹窗消息携带会话名");
    ok(dialogMessage().includes("Proj B"), "① 弹窗消息携带目标分区名");
    const confirm = dialogButtons().find((b) => !b.classList.contains("btn--danger")) ?? null;
    // tone 非 danger（移动可回滚不是破坏性操作）→ 确认按钮 = 非 danger 的主按钮。
    const primary = dialogButtons().find((b) => {
      const text = b.textContent ?? "";
      return text.includes("移动到项目") || text.includes("Move to project");
    }) ?? confirm;
    ok(primary !== null, "① 确认按钮在场");
    if (primary) {
      await act(async () => { primary!.click(); await flush(); });
      await settle(6);
    }
    eq(activeCalls.length, 1, "① 确认后 MoveSessionTopic 恰好一次");
    eq(activeCalls[0]?.name, "MoveSessionTopic", "① 落盘走 MoveSessionTopic 绑定");
    eq(activeCalls[0]?.args, ["project", SOURCE_ROOT, MOVE_TOPIC, TARGET_ROOT], "① 落盘参数 = (project, /repo, t1, /other)");
    eq(errorToasts().length, 0, "① 成功路径零 error toast");
    eq(dialog(), null, "① 确认后对话框关闭");
  }
  await cleanup();
}

// ── ② running 行禁用（入口保留） ──────────────────────────────────────────────
console.log("\ntl1711② running row disables the target rows (entry stays, backend gate is the fallback)");
{
  const rows = await mountTree({ withMoveBinding: true, moveRejects: false, running: true });
  const target = rows.length > 0 ? await openTopicMoveMenu(rows[0]!) : undefined;
  ok(Boolean(target), "② running 时移动入口仍在（不整项消失）");
  ok(target === undefined || target!.disabled === true, "② running 时目标行禁用（第一道闸）");
  if (target) {
    await act(async () => { target!.click(); await flush(); });
    eq(activeCalls.length, 0, "② 禁用行点击不触发移动");
    eq(dialog(), null, "② 禁用行点击不出确认弹窗");
  }
  await cleanup();
}

// ── ③ 取消分支：零落盘（会话留原位） ──────────────────────────────────────────
console.log("\ntl1711③ cancel branch: zero MoveSessionTopic, session stays");
{
  const rows = await mountTree({ withMoveBinding: true, moveRejects: false });
  const target = rows.length > 0 ? await openTopicMoveMenu(rows[0]!) : undefined;
  if (target) {
    await act(async () => { target!.click(); await flush(); });
    const cancel = dialogButtons().find((b) => {
      const text = b.textContent ?? "";
      return text.includes("取消") || text.includes("Cancel");
    }) ?? null;
    ok(cancel !== null, "③ 取消按钮在场");
    if (cancel) {
      await act(async () => { cancel!.click(); await flush(); });
      await settle(4);
    }
    eq(activeCalls.length, 0, "③ 取消后零 MoveSessionTopic（会话留在原位）");
    eq(dialog(), null, "③ 取消后对话框关闭");
  }
  await cleanup();
}

// ── ④ 无绑定：入口整体不渲染（其余菜单保持同构） ──────────────────────────────
console.log("\ntl1711④ without the move binding the entry disappears (older binary stays byte-identical)");
{
  const rows = await mountTree({ withMoveBinding: false, moveRejects: false });
  ok(rows.length > 0, "④ 整树挂载：topic 行渲染");
  if (rows.length > 0) {
    await act(async () => {
      rows[0]!.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
      await flush();
    });
    ok(!findByText(menuItems(), MOVE_ENTRY_NEEDLES), "④ 无绑定 →「移动到项目」入口不渲染（typeof 守卫，零回退）");
    ok(Boolean(findByText(menuItems(), ["移动到分组", "Move to group", "重命名", "Rename"])), "④ 其余菜单入口保持在场（分组/重命名菜单同构）");
  }
  await cleanup();
}

// ── ⑤ 失败路径：后端拒绝 → error toast，不冒泡 ────────────────────────────────
console.log("\ntl1711⑤ backend refusal lands in the error toast channel, no unhandled break");
{
  const rows = await mountTree({ withMoveBinding: true, moveRejects: true });
  const target = rows.length > 0 ? await openTopicMoveMenu(rows[0]!) : undefined;
  if (target) {
    await act(async () => { target!.click(); await flush(); });
    const primary = dialogButtons().find((b) => {
      const text = b.textContent ?? "";
      return text.includes("移动到项目") || text.includes("Move to project");
    }) ?? null;
    if (primary) {
      await act(async () => { primary!.click(); await flush(); });
      await settle(6);
    }
    eq(activeCalls.length, 1, "⑤ 失败路径同样先落一次调用（回滚由后端整体负责）");
    ok(errorToasts().length > 0, "⑤ 拒绝进 error toast 通道（可见出口）");
    eq(dialog(), null, "⑤ 对话框已关闭");
  }
  await cleanup();
}

// ── ⑦ settle 钉：controller 两路 settle 都重拉花名册 + 刷新话题页 ─────────────
console.log("\ntl1711⑦ controller settle: both outcomes re-pull rosters and refresh topic pages");
{
  const { useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");
  type Translator = Parameters<typeof useProjectTreeOrganization>[0] extends never ? never : import("../lib/i18n").Translator;
  const t = ((key: string) => key) as unknown as Translator;
  let groupPulls = 0;
  let refreshes = 0;
  const bindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => [],
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => { groupPulls += 1; return { groups: [], revision: 1, applied: true }; },
    MoveSessionTopic: async (scope: string, sourceRoot: string, topicID: string, targetRoot: string) => {
      return { topicId: topicID, targetWorkspaceRoot: targetRoot, movedSessions: 1 };
    },
  };
  const globalFolder = { key: "global|", kind: "global_folder" as const, label: "Global", children: [] };
  const projectFolder = { key: "project-/repo", kind: "project" as const, label: "Proj A", root: SOURCE_ROOT, children: [] };
  const otherProjectFolder = { key: "project-/other", kind: "project" as const, label: "Proj B", root: TARGET_ROOT, children: [] };

  // controller 抓柄挂载：断言与 moveTopicToPartition 调用都发生在渲染外——
  // 组件体内调 move（内部 setState）会把渲染循环喂成死循环（首跑实测钉）。
  let organization: ReturnType<typeof useProjectTreeOrganization> | null = null;
  function Harness() {
    const controller = useProjectTreeOrganization({
      tree: [globalFolder, projectFolder, otherProjectFolder],
      refresh: async () => { refreshes += 1; },
      organizationRevision: 0,
      bindings: bindings as unknown as import("../lib/types").ProjectTreeOrganizationBindings,
    });
    organization = controller;
    return null;
  }
  let settled: string | null = null;
  const root = createRoot(document.getElementById("root")!);
  await act(async () => {
    root.render(<StrictMode><Harness /></StrictMode>);
    await flush();
  });
  await settle(2);
  const controller = organization;
  ok(controller !== null, "⑦ hook 挂载抓到 controller");
  if (controller) {
    eq(controller.canMigrateTopics(), true, "⑦ 有绑定 → canMigrateTopics true（typeof 守卫在场）");
    const targets = controller.migrationTargets({ scope: "project", root: SOURCE_ROOT });
    eq(targets.map((n) => n.root ?? "global"), ["global", TARGET_ROOT], "⑦ 目标清单 = global 区 + 其他项目（源剔除）");
    await act(async () => {
      void controller.moveTopicToPartition("project", SOURCE_ROOT, MOVE_TOPIC, TARGET_ROOT).then(
        () => { settled = "ok"; },
        () => { settled = "rejected"; },
      );
      await flush();
    });
    await settle(4);
  }
  await act(async () => { void root.unmount(); await flush(); });
  ok(settled === "ok", "⑦ moveTopicToPartition 成功返回（binding 回执穿透）");
  ok(groupPulls >= 2, `⑦ settle 重拉花名册（GetProjectGroups pulls=${groupPulls}，含源/目标两区与强制重拉）`);
  ok(refreshes >= 1, `⑦ settle 刷新话题页（refreshes=${refreshes}）`);
}

// ── ⑥ 源码钉：接线 + 契约签名 + remoteSession 排除 + 三语零死键 ───────────────
console.log("\ntl1711⑥ source pins: wiring, contract signature, remote exclusion, three-locale live keys");
{
  const here = dirname(fileURLToPath(import.meta.url));
  const srcRoot = resolve(here, "..");
  const treeSource = readFileSync(join(srcRoot, "components/ProjectTree.tsx"), "utf8");
  const typesSource = readFileSync(join(srcRoot, "lib/sessionCatalogTypes.ts"), "utf8");

  ok(treeSource.includes('key: "move-to-project"'), "⑥ 「移动到项目」submenu 挂在会话菜单");
  ok(treeSource.includes('t("projectTree.moveTopicToProject")'), "⑥ 入口文案走三语键");
  ok(treeSource.includes('t("projectTree.moveTopicToGlobal")') && treeSource.includes('t("projectTree.moveTopicTo", { project: target.label })'), "⑥ 目标行双文案键（global 独立键 + 项目参数键）");
  ok(treeSource.includes("disabled: node.running === true"), "⑥ running 行禁用目标行（入口保留）");
  ok(treeSource.includes("organization.moveTopicToPartition(topicMoveSource.scope, topicMoveSource.root, topicId, target.root ?? \"\")"), "⑥ 叶子路由进 controller（源分区按行解析、global 目标 root 空）");
  ok(treeSource.includes("organization.canMigrateTopics()"), "⑥ 绑定存在性经 canMigrateTopics() typeof 守卫收口");
  ok(treeSource.includes("!node.remoteSession && organization.canMigrateTopics()"), "⑥ 远端会话排除（无本地盘面）");
  ok(treeSource.includes("const topicMoveDialog = useConfirmDialog();") && treeSource.includes("{topicMoveDialog.dialog}"), "⑥ 确认弹窗持有独立 useConfirmDialog 实例并挂在组件树内");
  ok(treeSource.includes("target.root ?? \"global\""), "⑥ 目标行键含 global 兜底（互迁第三向）");
  ok(typesSource.includes("MoveSessionTopic?(scope: string, sourceRoot: string, topicID: string, targetWorkspaceRoot: string): Promise<SessionTopicMigrationResult>"), "⑥ 绑定契约四参签名（sessionCatalogTypes.ts）");

  // 三语键齐备且仍被源码引用（零死键：迁移不产生孤儿键）。
  const zh = (await import("../locales/zh")).zh as unknown as Record<string, string>;
  const en = (await import("../locales/en")).en as unknown as Record<string, string>;
  const zhTw = (await import("../locales/zh-TW")).zhTW as unknown as Record<string, string>;
  const liveKeys = [
    "projectTree.moveTopicToProject",
    "projectTree.moveTopicTo",
    "projectTree.moveTopicToGlobal",
    "projectTree.moveTopicConfirmTitle",
    "projectTree.moveTopicConfirm",
    "projectTree.topicMoved",
    "projectTree.topicMoveFailed",
  ] as const;
  for (const key of liveKeys) {
    ok(typeof zh[key] === "string" && zh[key].length > 0, `zh has ${key}`);
    ok(typeof en[key] === "string" && en[key].length > 0, `en has ${key}`);
    ok(typeof zhTw[key] === "string" && zhTw[key].length > 0, `zh-TW has ${key}`);
    ok(treeSource.includes(`"${key}"`), `${key} 仍被 ProjectTree 引用（未成死键）`);
  }
}

process.stdout.write(`\ntl1711-topic-move: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
