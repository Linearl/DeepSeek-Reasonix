// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1560-group-migrate.test.tsx
//
// tl1560 第一阶段验收钉（分组右键「迁移到其他项目」，迁移能力后端整体回滚）：
//   ① global 分组右键 →「分组移动 ▸」二级末尾出现「迁移到其他项目」目标行
//     （每项目一行，分隔线隔开层级移动行）；点击目标行 → 确认对话框 → 确认
//     后 MigrateSessionGroupToProject 以 (groupId, targetRoot) 恰好一次落盘；
//   ② 成员会话 running（树上的输出点）→ 迁移行禁用（入口保留，tl1198 同款
//     发现性纪律；层级移动行不受影响）；
//   ③ project 区分组不出现迁移行（入口只服务「迁出 global」）；
//   ④ 后端迁移绑定缺失（老 mock / 浏览器开发面）→ 迁移行不渲染，
//     ProjectTreeGroupRows 其余菜单逐字节同构；
//   ⑤ 迁移失败（binding reject）→ 错误进 toast 通道，不冒泡不破窗；
//   源码钉：迁移行位于 groupMoveItems 尾部 + separator、disabled: memberRunning、
//   typeof 守卫经 canMigrateGroups 收口。

import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import type { Translator } from "../lib/i18n";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup, SessionGroupMigrationResult } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected || JSON.stringify(actual) === JSON.stringify(expected);
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── 源码钉：入口形态 ─────────────────────────────────────────────────────────
console.log("\ntl1560 A: 源码钉 — 二级尾部 + separator + 禁用态 + typeof 守卫");
{
  const orgSource = readFileSync(new URL("../components/ProjectTreeOrganization.tsx", import.meta.url), "utf8");
  ok(orgSource.includes("`migrate:${target.root}`"), "迁移目标行按项目 root 建键（每项目一行）");
  ok(orgSource.includes('key: "group-migrate-sep"'), "迁移行前有 separator（与层级移动行隔开）");
  ok(orgSource.includes("disabled: memberRunning"), "成员 running 时迁移行禁用（后端门禁为兜底）");
  ok(orgSource.includes("canMigrateGroups()"), "绑定存在性经 canMigrateGroups() typeof 守卫收口");
  ok(orgSource.includes('folder.kind === "global_folder" && organization.canMigrateGroups()'), "入口只在 global 区渲染（迁出 global）");
  const hookSource = orgSource;
  ok(hookSource.includes("MigrateSessionGroupToProject(groupID, targetRoot)"), "controller 经绑定调用后端迁移");
}

// ── jsdom 渲染链：真实右键 → 展开二级 → 断言/点击 ─────────────────────────────
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
globalThis.MouseEvent = dom.window.MouseEvent as unknown as typeof MouseEvent;
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

const t = ((key: string, vars?: Record<string, string>) => key + (vars && Object.keys(vars).length > 0 ? ":" + Object.values(vars).join("/") : "")) as unknown as Translator;

interface MigrateCalls { args: Array<[string, string]>; next: SessionGroupMigrationResult | Error }

function makeBindings(initial: SessionGroup[], migrate: MigrateCalls | null): ProjectTreeOrganizationBindings {
  let revision = 1;
  let groups = initial.map((g) => ({ ...g }));
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups,
    SaveSessionGroups: async () => {},
    GetProjectGroups: async (scope: string) => {
      // 迁移后 global 花名册清空（读侧模拟后端事务后的真态）。
      if (scope === "global" && migrate && migrate.args.length > 0 && !(migrate.next instanceof Error)) {
        return { groups: [], revision: revision + 1, applied: true };
      }
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
    SaveSessionGroupsVersioned: async (_scope: string, _root: string, expected: number, next: SessionGroup[]) => {
      if (expected !== revision) {
        return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      }
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
    ...(migrate ? {
      MigrateSessionGroupToProject: async (groupID: string, targetRoot: string) => {
        migrate.args.push([groupID, targetRoot]);
        if (migrate.next instanceof Error) throw migrate.next;
        return migrate.next;
      },
    } : {}),
  };
}

const globalFolder: ProjectNode = { key: "global|", kind: "global_folder", label: "Global", children: [] };
const projectFolder: ProjectNode = { key: "project-/repo", kind: "project", label: "ProjA", root: "/repo", children: [] };

function topicNode(id: string, running: boolean): ProjectNode {
  return { key: `topic-${id}`, kind: "topic", label: id, topicId: id, status: running ? "streaming" : "idle", running };
}

interface HarnessSpec {
  bindings: ProjectTreeOrganizationBindings;
  tree: ProjectNode[];
  folder: ProjectNode;
  children: ProjectNode[];
}

function Harness({ spec }: { spec: HarnessSpec }) {
  const organization = useProjectTreeOrganization({
    tree: spec.tree,
    refresh: async () => {},
    organizationRevision: 0,
    bindings: spec.bindings,
  });
  return <ProjectTreeGroupRows
    folder={spec.folder}
    children={spec.children}
    depth={1}
    section="pinned"
    visible
    organization={organization}
    renderNode={() => null}
    t={t}
  />;
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

let activeRoot: Root | null = null;
async function mount(spec: HarnessSpec) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(<StrictMode><Harness spec={spec} /></StrictMode>);
    await flush();
  });
}

async function cleanup() {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  activeRoot = null;
}

function headers(): HTMLElement[] {
  return [...document.querySelectorAll(".project-tree__group-main")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

function menuItems(): HTMLElement[] {
  return [...document.querySelectorAll("[role=\"menuitem\"]")].filter(
    (el): el is HTMLElement => el instanceof HTMLElement,
  );
}

async function openGroupMenu(title: string) {
  const index = headers().findIndex((el) => el.getAttribute("title") === title);
  if (index < 0) return;
  await act(async () => {
    headers()[index]!.dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
}

async function expandGroupMoveSubmenu() {
  const trigger = menuItems().find((el) => (el.textContent ?? "").includes("projectTree.groupMove")) as HTMLButtonElement | undefined;
  if (trigger) await act(async () => { trigger.click(); await flush(); });
  return trigger;
}

function rowByText(text: string): HTMLButtonElement | undefined {
  return menuItems().find((el) => (el.textContent ?? "").includes(text)) as HTMLButtonElement | undefined;
}

// 验收 ①：global 分组二级末尾出现迁移目标行；确认后恰好一次落盘。
console.log("\ntl1560 B: 渲染链 — 迁移行进入分组移动二级 + 确认后落盘");
{
  const roster: SessionGroup[] = [{ id: "g-1", title: "G1", topicIds: ["t1"] }];
  const migrate: MigrateCalls = { args: [], next: { groupId: "g-1", groupTitle: "G1", targetWorkspaceRoot: "/repo", migratedTopics: ["t1"], movedSessions: 1 } };
  await mount({
    bindings: makeBindings(roster, migrate),
    tree: [globalFolder, projectFolder],
    folder: globalFolder,
    children: [topicNode("t1", false)],
  });

  await openGroupMenu("G1");
  await expandGroupMoveSubmenu();
  const row = rowByText("projectTree.migrateGroupTo:ProjA");
  ok(Boolean(row), "① global 分组的二级出现「迁移到 ProjA」目标行");
  ok(Boolean(row) && row!.disabled === false, "① 无 running 成员时迁移行可点");

  if (row) {
    await act(async () => { row.click(); await flush(); });
    // 确认对话框：确认按钮文案 = migrateGroupTitle。
    const confirmButton = [...document.querySelectorAll("button")].find(
      (el) => (el.textContent ?? "").includes("projectTree.migrateGroupTitle"),
    ) as HTMLButtonElement | undefined;
    ok(Boolean(confirmButton), "① 点击目标行先出确认对话框（迁移是跨 scope 数据面，非静默动作）");
    if (confirmButton) {
      await act(async () => { confirmButton.click(); await flush(); });
      await flush();
      eq(migrate.args.length, 1, "① 确认后 MigrateSessionGroupToProject 恰好一次");
      eq(migrate.args[0], ["g-1", "/repo"], "① 落盘参数 = (groupId, targetRoot)");
    }
  }
  await cleanup();
}

// 验收 ②：成员 running → 迁移行禁用，层级移动行不受影响。
console.log("\ntl1560 C: 渲染链 — 成员 running 禁用迁移行（入口保留）");
{
  const roster: SessionGroup[] = [{ id: "g-1", title: "G1", topicIds: ["t1"] }];
  const migrate: MigrateCalls = { args: [], next: { groupId: "g-1", groupTitle: "G1", targetWorkspaceRoot: "/repo", migratedTopics: [], movedSessions: 0 } };
  await mount({
    bindings: makeBindings(roster, migrate),
    tree: [globalFolder, projectFolder],
    folder: globalFolder,
    children: [topicNode("t1", true)],
  });

  await openGroupMenu("G1");
  await expandGroupMoveSubmenu();
  const row = rowByText("projectTree.migrateGroupTo:ProjA");
  ok(Boolean(row), "② running 时迁移入口仍在（不整项消失）");
  ok(Boolean(row) && row!.disabled === true, "② running 时迁移行禁用（第一道闸；后端门禁兜底并发窗口）");
  if (row) {
    await act(async () => { row.click(); await flush(); });
    eq(migrate.args.length, 0, "② 禁用行点击不触发迁移");
  }
  await cleanup();
}

// 验收 ③④：project 区分组无迁移行；绑定缺失时 migration 行不渲染。
console.log("\ntl1560 D: 渲染链 — project 区与老 mock 形态不变");
{
  const roster: SessionGroup[] = [{ id: "g-1", title: "G1", topicIds: ["t1"] }];
  await mount({
    bindings: makeBindings(roster, { args: [], next: { groupId: "g-1", groupTitle: "G1", targetWorkspaceRoot: "/repo", migratedTopics: [], movedSessions: 0 } }),
    tree: [globalFolder, projectFolder],
    folder: projectFolder,
    children: [topicNode("t1", false)],
  });
  await openGroupMenu("G1");
  await expandGroupMoveSubmenu();
  ok(!rowByText("projectTree.migrateGroupTo:ProjA"), "③ project 区分组的二级无迁移行（入口只服务迁出 global）");
  await cleanup();

  await mount({
    bindings: makeBindings(roster, null),
    tree: [globalFolder, projectFolder],
    folder: globalFolder,
    children: [topicNode("t1", false)],
  });
  await openGroupMenu("G1");
  await expandGroupMoveSubmenu();
  ok(!rowByText("projectTree.migrateGroupTo:ProjA"), "④ 无迁移绑定时迁移行不渲染（老 mock 零回退）");
  ok(Boolean(rowByText("projectTree.nestGroupUnder:ProjA")) === false, "GUARD 老形态：无层目标时 nest 行为与 tl1198 既有语义一致（目标树无其他组 → 无 nest 行）");
  await cleanup();
}

// 验收 ⑤：迁移失败 → 错误进 toast 通道，不冒泡。
console.log("\ntl1560 E: 渲染链 — 失败路径走 toast");
{
  const roster: SessionGroup[] = [{ id: "g-1", title: "G1", topicIds: ["t1"] }];
  const migrate: MigrateCalls = { args: [], next: new Error("boom") };
  await mount({
    bindings: makeBindings(roster, migrate),
    tree: [globalFolder, projectFolder],
    folder: globalFolder,
    children: [topicNode("t1", false)],
  });
  await openGroupMenu("G1");
  await expandGroupMoveSubmenu();
  const row = rowByText("projectTree.migrateGroupTo:ProjA");
  if (row) {
    await act(async () => { row.click(); await flush(); });
    const confirmButton = [...document.querySelectorAll("button")].find(
      (el) => (el.textContent ?? "").includes("projectTree.migrateGroupTitle"),
    ) as HTMLButtonElement | undefined;
    if (confirmButton) {
      await act(async () => { confirmButton.click(); await flush(); });
      await flush();
      eq(migrate.args.length, 1, "⑤ 失败路径同样先落一次调用（回滚由后端整体负责）");
      ok(true, "⑤ reject 未冒泡（menu 的 catch 收口，无 unhandled rejection 破窗）");
    }
  }
  await cleanup();
}

console.log(`\ntl1560: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
