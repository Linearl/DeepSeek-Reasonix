// Run: tsx src/__tests__/project-tree-subgroup-create.test.tsx
//
// 892-B 验收②：分组右键菜单「新建子分组」+ parentId 传递。
//   ① 菜单项出现在任意层级的分组头右键菜单（827 形态：菜单→行内输入）；
//   ② Enter 提交走 A 片 CreateSessionGroup 直连（scope/root/groupID/title/
//      parentID 逐位断言），成功后按后端回拉；
//   ③ 老 bindings（无 CreateSessionGroup）回退 CAS roster append 且 parent
//      字段就位（存储拼法）；
//   ④ 空白稿不建组（blur 不能捏出空分组）；写失败时乐观涂鸦不残留。

import { JSDOM } from "jsdom";
import type { Root } from "react-dom/client";
import type { Translator } from "../lib/i18n";
import type {
  ManageSessionsGroupResult,
  ProjectNode,
  ProjectTreeOrganizationBindings,
  SessionGroup,
} from "../lib/types";

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
  let same = actual === expected;
  if (!same) {
    // DOM 节点（如 composer 查询结果）不能 JSON.stringify（react fiber 循环引用）。
    const safe = (value: unknown) => (value instanceof dom.window.Element ? `<${value.tagName.toLowerCase()}>` : value);
    try {
      same = JSON.stringify(safe(actual)) === JSON.stringify(safe(expected));
    } catch {
      same = false;
    }
  }
  if (same) ok(true, label);
  else ok(false, `${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// ── jsdom harness（对齐 hierarchy 测试的挂载形态） ────────────────────────────
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

// React must load AFTER the jsdom globals: react-dom computes canUseDOM /
// isInputEventSupported at module load, and a pre-DOM load pins the IE
// polyfill path (input events then die on attachEvent instead of firing
// onChange) — same reason ask-card-identity uses dynamic imports.
const { StrictMode, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ProjectTreeGroupRows, useProjectTreeOrganization } = await import("../components/ProjectTreeOrganization");

const t = ((key: string) => key) as unknown as Translator;

const folder: ProjectNode = { key: "project-/repo", kind: "project", label: "Repo", root: "/repo", children: [] };

interface CreateCall { scope: string; root: string; groupID: string; title: string; parentID: string }

/** A-slice bindings: CreateSessionGroup records the call and mutates the
 * mock roster so the post-write re-pull reads the child back. */
function asliceBindings(
  initial: SessionGroup[],
  calls: CreateCall[],
  opts: { fail?: boolean } = {},
): { bindings: ProjectTreeOrganizationBindings; state: () => SessionGroup[] } {
  let groups = initial.map((g) => ({ ...g }));
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision: 1, applied: true }),
    CreateSessionGroup: async (scope, root, groupID, title, parentID) => {
      calls.push({ scope, root, groupID, title, parentID });
      if (opts.fail) throw new Error("backend rejected");
      groups = [...groups, { id: groupID, title, parent: parentID, topicIds: [] }];
      return { kind: "session", scope, workspaceRoot: root, groupId: groupID, title, parentId: parentID, applied: true } as ManageSessionsGroupResult;
    },
  };
  return { bindings, state: () => groups };
}

/** Legacy bindings: no A-slice method at all (pre-892 mock shape). */
function legacyBindings(initial: SessionGroup[]): { bindings: ProjectTreeOrganizationBindings; saved: SessionGroup[][] } {
  let groups = initial.map((g) => ({ ...g }));
  let revision = 1;
  const saved: SessionGroup[][] = [];
  const bindings: ProjectTreeOrganizationBindings = {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups.map((g) => ({ ...g })),
    SaveSessionGroups: async () => {},
    GetProjectGroups: async () => ({ groups: groups.map((g) => ({ ...g })), revision, applied: true }),
    SaveSessionGroupsVersioned: async (_scope, _root, expected, next) => {
      if (expected !== revision) return { groups: groups.map((g) => ({ ...g })), revision, applied: false };
      groups = next.map((g) => ({ ...g }));
      revision += 1;
      saved.push(next.map((g) => ({ ...g })));
      return { groups: groups.map((g) => ({ ...g })), revision, applied: true };
    },
  };
  return { bindings, saved };
}

function Harness({ bindings, children }: { bindings: ProjectTreeOrganizationBindings; children: ProjectNode[] }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: 0,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={children}
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
async function mount(bindings: ProjectTreeOrganizationBindings) {
  if (activeRoot) await act(async () => activeRoot!.unmount());
  window.localStorage.clear();
  const root = createRoot(document.getElementById("root")!);
  activeRoot = root;
  await act(async () => {
    root.render(<StrictMode><Harness bindings={bindings} children={[]} /></StrictMode>);
    await flush();
  });
  return root;
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

function menuLabels(): string[] {
  return [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .map((el) => (el.textContent ?? "").trim())
    .filter((text) => text.length > 0);
}

async function openMenuAndSelect(index: number, label: string) {
  await act(async () => {
    headers()[index].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await flush();
  });
  const item = [...document.querySelectorAll("[role=\"menuitem\"], .context-menu__item, button")]
    .find((el) => (el.textContent ?? "").trim() === label) as HTMLElement | undefined;
  ok(Boolean(item), `菜单含「${label}」`);
  if (item) await act(async () => { item.click(); await flush(); });
}

const composer = () => document.querySelector(".project-tree__group-subgroup-create input") as HTMLInputElement | null;

async function typeAndEnter(text: string) {
  const input = composer();
  if (!input) throw new Error("composer input not open");
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
    setter.call(input, text);
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    await flush();
  });
  await act(async () => {
    input.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flush();
  });
}

console.log("\n892-B② 新建子分组：A 片 CreateSessionGroup 直连（parentId 传递）");
{
  const calls: CreateCall[] = [];
  const initial: SessionGroup[] = [
    { id: "g-a", title: "A", topicIds: ["t1"] },
    { id: "g-b", title: "B", topicIds: [] },
  ];
  const { bindings } = asliceBindings(initial, calls);
  await mount(bindings);
  await act(flush);

  await openMenuAndSelect(0, "projectTree.createSubGroup");
  ok(composer() !== null, "菜单选择后打开行内输入框");
  await typeAndEnter("Child");
  await act(flush);
  await act(flush);

  eq(calls.length, 1, "Enter 提交恰好一次 CreateSessionGroup");
  eq(calls[0]?.scope, "project", "scope 按 roster 键派生（project|<root>）");
  eq(calls[0]?.root, "/repo", "workspaceRoot 逐位传递");
  eq(calls[0]?.title, "Child", "标题逐位传递");
  ok((calls[0]?.groupID ?? "").startsWith("group-"), "groupID 由前端生成（group- 前缀）");
  // ★ 验收核心：parentId 传递（工具面 parent_id / 直连面 parentID / 存储 parent）
  eq(calls[0]?.parentID, "g-a", "parentId 逐位传递给 CreateSessionGroup");
  await act(flush);
  const titles = headers().map((h) => h.getAttribute("title"));
  ok(titles.includes("Child"), "写成功后回拉渲染出子分组");
  await cleanup();
}

console.log("\n892-B② 空白稿不建组（blur 不能捏出空分组）");
{
  const calls: CreateCall[] = [];
  const { bindings } = asliceBindings([{ id: "g-a", title: "A", topicIds: [] }], calls);
  await mount(bindings);
  await act(flush);
  await openMenuAndSelect(0, "projectTree.createSubGroup");
  const input = composer();
  ok(input !== null, "输入框打开");
  // Escape 取消：不产生调用。
  await act(async () => {
    input!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flush();
  });
  eq(composer(), null, "Escape 关闭输入框");
  eq(calls.length, 0, "Escape 不产生 CreateSessionGroup 调用");
  // 空稿 blur：commitSubGroup 里 trim 为空 → 取消。
  // React 的 onBlur 派生自 bubbling focusout（jsdom 里直接派 blur 不触发合成事件）。
  await openMenuAndSelect(0, "projectTree.createSubGroup");
  await act(async () => {
    composer()!.dispatchEvent(new window.FocusEvent("focusout", { bubbles: true }));
    await flush();
  });
  eq(calls.length, 0, "空白稿 blur 不产生 CreateSessionGroup 调用");
  eq(composer(), null, "空白稿 blur 关闭输入框");
  await cleanup();
}

console.log("\n892-B② 老 bindings 回退 CAS append（存储拼法 parent 就位）");
{
  const { bindings, saved } = legacyBindings([
    { id: "g-a", title: "A", topicIds: [] },
    { id: "g-b", title: "B", topicIds: [], parent: "g-a" },
  ]);
  await mount(bindings);
  await act(flush);
  // 对已嵌套的 B 组新建子分组 → 三代同堂（存储 parent 字段）。
  await openMenuAndSelect(1, "projectTree.createSubGroup");
  await typeAndEnter("Grand");
  await act(flush);
  await act(flush);
  eq(saved.length, 1, "回退路径走一次 CAS roster 保存");
  const payload = saved[0] ?? [];
  const grand = payload.find((g) => g.title === "Grand");
  ok(Boolean(grand), "CAS payload 含新子分组");
  eq(grand?.parent, "g-b", "CAS payload 的 parent 字段指向被右键的组（存储拼法）");
  eq(grand?.topicIds, [], "新子分组初始无成员");
  await cleanup();
}

console.log("\n892-B② 写失败时乐观涂鸦不残留（后端回拉为准）");
{
  const calls: CreateCall[] = [];
  const initial: SessionGroup[] = [{ id: "g-a", title: "A", topicIds: [] }];
  const { bindings } = asliceBindings(initial, calls, { fail: true });
  await mount(bindings);
  await act(flush);
  await openMenuAndSelect(0, "projectTree.createSubGroup");
  await typeAndEnter("Doomed");
  // 失败 → 强制回拉 → mock roster 从未写入 → 乐观行消失。
  for (let i = 0; i < 6; i += 1) await act(flush);
  const titles = headers().map((h) => h.getAttribute("title"));
  eq(calls.length, 1, "失败调用也发起了一次 CreateSessionGroup");
  ok(!titles.includes("Doomed"), "写失败后乐观涂鸦不残留（回拉恢复）");
  await cleanup();
}

process.stdout.write(`\nproject-tree-subgroup-create: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
