// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/artifact-card-render.test.tsx
//
// 任务 944：ArtifactCard 渲染 + 打开链路 headless e2e——JSDOM 渲染真实组件，
// 桥接 stub 断言：主按钮直开（OpenLocalPath）、下拉选应用（OpenLocalPathIn
// ExternalOpener）、文件管理器选中态（RevealPath，Windows explorer /select
// 由 Go 侧承担）、md/text 预览分流、html 仅打开、labFlag 关闭态回退。

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";

// ArtifactCard's md 预览懒加载 MarkdownRenderer，传递依赖 katex CSS；tsx 无
// 资源 loader，按 extension-surface.test.tsx 先例重定向到空 stub（Vite 同款语义）。
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://reasonix.local/" });
const { window } = dom;

(globalThis as Record<string, unknown>).window = window;
(globalThis as Record<string, unknown>).document = window.document;
(globalThis as Record<string, unknown>).HTMLElement = window.HTMLElement;
(globalThis as Record<string, unknown>).MouseEvent = window.MouseEvent;
(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

// ── bridge stub：收集每个动作调用 ────────────────────────────────────────
const opened: string[] = [];
const openedWith: Array<[string, string]> = [];
const revealed: string[] = [];
const savedAs: string[] = [];
(window as unknown as Record<string, unknown>).go = {
  main: {
    App: {
      OpenLocalPath: async (path: string) => { opened.push(path); },
      OpenLocalPathInExternalOpener: async (path: string, id: string) => { openedWith.push([path, id]); },
      RevealPath: async (path: string) => { revealed.push(path); },
      SaveLocalPathAs: async (path: string) => { savedAs.push(path); return path; },
      ExternalOpeners: async () => ({
        openers: [
          { id: "vscode", name: "VS Code", kind: "editor" },
          { id: "file-explorer", name: "File Explorer", kind: "file-manager" },
        ],
        preferred: "vscode",
      }),
      ToolResultForTab: async () => null,
    },
  },
};

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const { createElement, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { ArtifactCard } = await import("../components/ArtifactCard");
const { applyLabFlags } = await import("../lib/labFlags");
const { ToastProvider } = await import("../lib/toast");
const { LocaleProvider } = await import("../lib/i18n");
const { RENDER_ARTIFACT_TOOL } = await import("../lib/artifactCard");
type Item = import("../lib/useController").Item;

function toolItem(id: string, args: Record<string, unknown>, output?: Record<string, unknown>): Item {
  return {
    kind: "tool",
    id,
    name: RENDER_ARTIFACT_TOOL,
    args: JSON.stringify(args),
    readOnly: true,
    status: "done",
    output: output ? JSON.stringify(output) : undefined,
  } as Item;
}

async function renderCard(item: Item): Promise<HTMLElement> {
  const container = window.document.createElement("div");
  window.document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(createElement(
      LocaleProvider,
      null,
      createElement(ToastProvider, null, createElement(ArtifactCard, { item: item as never, tabId: undefined })),
    ));
  });
  return container;
}

async function click(el: Element) {
  await act(async () => {
    el.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

console.log("\nartifact card render + open chain");

// ── 1. md 卡：头部 + 预览切换 + 主按钮直开 ───────────────────────────────
{
  const item = toolItem(
    "t1",
    { path: "D:\\out\\weekly.md", title: "周报" },
    { ok: true, path: "D:\\out\\weekly.md", title: "周报", kind: "md", size: 2048, preview: "# 本周进展\n- 完成 944", previewTruncated: false },
  );
  const container = await renderCard(item);
  ok(container.querySelector(".artifact-card") !== null, "md 卡渲染 artifact-card 根节点");
  ok(container.querySelector(".artifact-card__title")?.textContent === "周报", "头部显示 title");
  ok(container.querySelector(".artifact-card__size")?.textContent === "2.0 KB", "头部显示文件大小");
  ok(container.querySelector(".artifact-card__path")?.textContent === "D:\\out\\weekly.md", "头部显示路径");
  ok(container.querySelector(".artifact-card__preview-toggle") !== null, "md 卡带预览切换按钮");
  // 预览默认折叠，点开才渲染内容。
  ok(container.querySelector(".artifact-card__preview-body") === null, "预览默认折叠");
  await click(container.querySelector(".artifact-card__preview-toggle")!);
  ok(container.querySelector(".artifact-card__preview-body") !== null, "点击后预览展开");
  // 主按钮直开。
  await click(container.querySelector(".artifact-card__open")!);
  ok(opened.length === 1 && opened[0] === "D:\\out\\weekly.md", "主按钮调用 OpenLocalPath 直开");
}

// ── 2. text 卡：等宽预览（CodeViewer 容器存在）────────────────────────────
{
  const item = toolItem(
    "t2",
    { path: "D:\\out\\dump.txt" },
    { ok: true, path: "D:\\out\\dump.txt", kind: "text", size: 100, preview: "line1\nline2" },
  );
  const container = await renderCard(item);
  ok(container.querySelector(".artifact-card")?.getAttribute("data-artifact-kind") === "text", "text 卡 data-artifact-kind=text");
  await click(container.querySelector(".artifact-card__preview-toggle")!);
  ok(container.querySelector(".artifact-card__preview-body") !== null, "text 卡预览展开（CodeViewer 等宽渲染）");
}

// ── 3. html 卡：仅打开、无预览切换 ───────────────────────────────────────
{
  const item = toolItem(
    "t3",
    { path: "D:\\out\\page.html" },
    { ok: true, path: "D:\\out\\page.html", kind: "html", size: 500 },
  );
  const container = await renderCard(item);
  ok(container.querySelector(".artifact-card")?.getAttribute("data-artifact-kind") === "html", "html 卡分流正确");
  ok(container.querySelector(".artifact-card__preview-toggle") === null, "html 卡无内嵌预览（首期仅打开）");
  await click(container.querySelector(".artifact-card__open")!);
  ok(opened[opened.length - 1] === "D:\\out\\page.html", "html 卡主按钮走 OpenLocalPath（浏览器打开）");
}

// ── 4. 下拉菜单：应用目录 + 文件管理器选中态 + 复制 + 另存 ────────────────
{
  openedWith.length = 0;
  revealed.length = 0;
  const item = toolItem(
    "t4",
    { path: "D:\\out\\r.md" },
    { ok: true, path: "D:\\out\\r.md", kind: "md", size: 10, preview: "x" },
  );
  const container = await renderCard(item);
  await click(container.querySelector(".artifact-card__menu-btn")!);
  const menu = window.document.querySelector(".context-menu, [role='menu']");
  ok(menu !== null, "箭头按钮展开下拉菜单");
  const items = Array.from(window.document.querySelectorAll<HTMLElement>(".context-menu__item, [role='menuitem']"));
  const labels = items.map((el) => el.textContent ?? "");
  ok(labels.some((label) => label.includes("VS Code")), "菜单列出应用目录（VS Code）");
  ok(labels.some((label) => label.includes("在文件管理器中显示") || label.includes("Reveal")), "菜单含文件管理器显示项");
  ok(!labels.some((label) => label.includes("File Explorer")), "应用目录排除 file-manager 项（避免与 Reveal 重复）");
  // 选应用打开。
  const vscodeItem = items.find((el) => (el.textContent ?? "").includes("VS Code"));
  if (vscodeItem) {
    await click(vscodeItem);
    ok(openedWith.length === 1 && openedWith[0][0] === "D:\\out\\r.md" && openedWith[0][1] === "vscode", "选应用 → OpenLocalPathInExternalOpener(vscode)");
  } else {
    ok(false, "选应用菜单项存在");
  }
  // Reveal。
  await click(container.querySelector(".artifact-card__menu-btn")!);
  const items2 = Array.from(window.document.querySelectorAll<HTMLElement>(".context-menu__item, [role='menuitem']"));
  const revealItem = items2.find((el) => (el.textContent ?? "").includes("在文件管理器中显示") || (el.textContent ?? "").includes("Reveal"));
  if (revealItem) {
    await click(revealItem);
    ok(revealed.length === 1 && revealed[0] === "D:\\out\\r.md", "文件管理器显示 → RevealPath（explorer /select 选中态由 Go 侧承担）");
  } else {
    ok(false, "Reveal 菜单项存在");
  }
  // 另存为。
  await click(container.querySelector(".artifact-card__menu-btn")!);
  const items3 = Array.from(window.document.querySelectorAll<HTMLElement>(".context-menu__item, [role='menuitem']"));
  const saveItem = items3.find((el) => (el.textContent ?? "").includes("另存为") || (el.textContent ?? "").includes("Save as"));
  if (saveItem) {
    await click(saveItem);
    ok(savedAs.length === 1 && savedAs[0] === "D:\\out\\r.md", "另存为 → SaveLocalPathAs");
  } else {
    ok(false, "另存为菜单项存在");
  }
}

// ── 5. labFlag 关闭态：卡片组件本身不感知门控（门控在行渲染层），此处验证
//    flag 默认关且可翻——门控接线由 useTranscriptRowRenderer 的条件承担。────
{
  const { labFlagEnabled } = await import("../lib/labFlags");
  ok(labFlagEnabled("renderArtifact") === false, "renderArtifact flag 默认关（铁律 2）");
  applyLabFlags({ renderArtifact: true });
  ok(labFlagEnabled("renderArtifact") === true, "applyLabFlags 可翻开");
  applyLabFlags({ renderArtifact: false });
  ok(labFlagEnabled("renderArtifact") === false, "可关闭");
}

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
