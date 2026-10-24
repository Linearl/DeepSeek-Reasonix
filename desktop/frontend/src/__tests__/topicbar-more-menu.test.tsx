// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/topicbar-more-menu.test.tsx
//
// tl 1119+1120：会话更多菜单（TopicbarMoreMenuContent）一级收敛 + 二级可达 + ▶ 指示。
// 用法：tsx src/__tests__/topicbar-more-menu.test.tsx

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";

// 先建 JSDOM 并改写 navigator.language，再动态 import 依赖 navigator 的 i18n 模块——
// 否则 i18n 内部 `navigator.language` 在 import 阶段已经按运行环境（英文）读取，
// 后续再改 navigator 就不生效（静态 import 的 hoisting 顺序保证不了这一点）。
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: { ...dom.window.navigator, language: "zh" } });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const { TopicbarMoreMenuContent } = await import("../components/TopicbarMoreMenuContent");
const { LocaleProvider, preloadLocale, detectLocale } = await import("../lib/i18n");
// zh 词典是懒加载（preloadLocale 的动态 import），必须在断言中文文案前 await 到位——
// 否则 translate() 走 en 兜底，本测试用中文做结构断言就会全部错位。
await preloadLocale(detectLocale(""));

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

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => {
      await flushTimers();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function untilRendered(label: string) {
  await waitFor(label, () => rootElement.querySelectorAll('[role="menuitem"],[role="menuitemradio"]').length > 0);
}

const calls: string[] = [];
Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
  writeText: async (value: string) => { calls.push(`copy:${value}`); },
} });

function press(target: Element, key: string) {
  target.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
}

function click(target: Element) {
  target.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
}

console.log("\ntopicbar more menu: 1119+1120 collapse contract");
let rootElement = document.getElementById("root")!;
// 每轮重渲染前 root.unmount() 后必须换新的 root 容器节点——react-dom 的 root 一旦
// unmount 就不能再 render（"Cannot update an unmounted root"），这里用一个不断替换
// 的 div 元素模拟"重新挂载"，而不是复用同一个已 unmount 的 root 实例。
let root = createRoot(rootElement);
function resetRoot() {
  root.unmount();
  const fresh = document.createElement("div");
  fresh.id = "root";
  rootElement.replaceWith(fresh);
  rootElement = fresh;
  root = createRoot(rootElement);
}

function openersFixture() {
  return {
    views: [{ id: "code", name: "Code", icon: undefined }],
    preferred: "code",
    workspaceOpenable: true,
    onOpen: () => { calls.push("open"); },
  };
}

async function render(options?: { terminalEnabled?: boolean }) {
  const terminalEnabled = options?.terminalEnabled ?? true;
  await act(async () => {
    root.render(
      <LocaleProvider>
        <TopicbarMoreMenuContent
          sessionHasContent
          getSessionMarkdown={async () => "# Session"}
          exportSession={(format) => { calls.push(`export:${format}`); }}
          openChangedDock={() => { calls.push("changed"); }}
          toggleTerminal={() => { calls.push("terminal"); }}
          terminalEnabled={terminalEnabled}
          openSessionSummary={() => { calls.push("summary"); }}
          tasksOpen={false}
          copied={false}
          initialFocus="first"
          onClose={() => { calls.push("close"); }}
          onCopied={() => { calls.push("copied"); }}
          activeTab={{ id: "one", scope: "local", topicId: "topic-1", sessionPath: "/session/one" }}
          logDirectory="/logs/one"
          surfaceView={{ value: "transcript", setView: (view) => { calls.push(`view:${view}`); } }}
          openers={openersFixture()}
        />
      </LocaleProvider>,
    );
  });
}

await render();
await untilRendered("top-level menu render");

// ---- 一级菜单结构收敛（tl 1120） ----
// 注意：含子菜单的触发项包在 `.topicbar__more-submenu-host` 里（不是菜单 div 的直接子节点），
// 因此这里不能用 `> [role="menuitem"]` 直接子选择器——会漏掉复制和导出/会话视图/打开方式三项。
const topLevelItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-menu .topicbar__menu-item'));
const topLevelLabels = topLevelItems.map((item) => item.textContent?.trim() ?? "");
ok(topLevelLabels.length === 6, `一级菜单收敛为 6 个直接项（实际 ${topLevelLabels.length}: ${topLevelLabels.join(" | ")}）`);
ok(topLevelLabels[0] === "会话摘要", "一级第 1 项 = 会话摘要");
ok(topLevelLabels[1] === "复制和导出", "一级第 2 项 = 复制和导出（原复制会话/导出会话/复制三项合并）");
ok(topLevelLabels[2] === "会话视图", "一级第 3 项 = 会话视图（原转录/轨迹并入）");
ok(topLevelLabels[3] === "改动", "一级第 4 项 = 改动");
ok(topLevelLabels[4] === "终端", "一级第 5 项 = 终端");
ok(topLevelLabels[5] === "打开方式", "一级第 6 项 = 打开方式");
ok(topLevelItems[1]!.getAttribute("aria-haspopup") === "menu", "复制和导出 是子菜单触发项（aria-haspopup=menu）");
ok(topLevelItems[2]!.getAttribute("aria-haspopup") === "menu", "会话视图 是子菜单触发项（aria-haspopup=menu）");
ok(topLevelItems[5]!.getAttribute("aria-haspopup") === "menu", "打开方式 是子菜单触发项（aria-haspopup=menu）");

// ---- ▶ 展开引导指示（tl 1119）+ 方向随弹出翻转（tl 1148） ----
// tl 1148：子菜单固定左弹（CSS right: calc(100% + 4px)），指示箭头关闭态 ▷
// （lucide-chevron-right，惯例=有子菜单）、打开态翻 ◁（lucide-chevron-left，
// 指向子菜单实际出现的方向）。lucide 图标渲染为 svg 且带 `lucide-chevron-*`
// class，可直接按 class 断言图标身份。
const submenuTriggers = topLevelItems.filter((item) => item.getAttribute("aria-haspopup") === "menu");
ok(submenuTriggers.length === 3, "一级菜单含 3 个子菜单触发项（复制和导出/会话视图/打开方式）");
for (const [index, trigger] of submenuTriggers.entries()) {
  const indicator = trigger.querySelector(".topicbar__more-submenu-indicator");
  ok(indicator !== null, `第 ${index + 1} 个子菜单触发项带 .topicbar__more-submenu-indicator ▶ 指示`);
  ok(indicator?.classList.contains("lucide-chevron-right") === true,
    `tl1148 关闭态：第 ${index + 1} 个指示箭头朝右（▷，表示有子菜单）`);
  ok(indicator?.classList.contains("lucide-chevron-left") === false,
    `tl1148 关闭态：第 ${index + 1} 个指示箭头未翻转（非 ◁）`);
}

// ---- 二级可达零丢失（tl 1120）：复制和导出 ▸ ----
const copyExportTrigger = topLevelItems[1]!;
await act(async () => { copyExportTrigger.click(); });
await waitFor("copyExport submenu", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="复制和导出"]') !== null);
// tl1148 打开态：子菜单左弹，触发项箭头翻 ◁ 指向实际出现方向。
const openedCopyExportIndicator = rootElement.querySelector('.topicbar__more-menu .topicbar__menu-item[aria-expanded="true"] .topicbar__more-submenu-indicator');
ok(openedCopyExportIndicator?.classList.contains("lucide-chevron-left") === true,
  "tl1148 打开态：复制和导出 触发项箭头翻 ◁（指向左弹的子菜单）");
ok(openedCopyExportIndicator?.classList.contains("lucide-chevron-right") === false,
  "tl1148 打开态：复制和导出 触发项箭头不再是 ▷");
const copyExportItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-submenu [role="menuitem"]'));
const copyExportLabels = copyExportItems.map((item) => item.textContent?.trim() ?? "");
ok(copyExportItems.length === 8, `复制和导出 ▸ 二级覆盖 8 项（实际 ${copyExportItems.length}: ${copyExportLabels.join(" | ")}）`);
ok(copyExportLabels.includes("复制会话"), "复制会话 保留在二级");
ok(copyExportLabels.includes("导出 Markdown") && copyExportLabels.includes("导出 JSON")
  && copyExportLabels.includes("导出 PDF") && copyExportLabels.includes("导出图片"), "导出 MD/JSON/PDF/图片 四项保留在二级");
ok(copyExportLabels.includes("会话路径") && copyExportLabels.includes("日志路径") && copyExportLabels.includes("会话 ID"), "路径/日志路径/会话ID 保留在二级");

// 二级每一项都必须可达（enabled）或显式 disabled——不可达 = 丢失。
const unreachable = copyExportItems.filter((item) => item.disabled);
ok(unreachable.length === 0, "复制和导出 ▸ 无不可达（disabled）二级项");

// 触发一次真实导出回调；点击后整菜单被 onClose 关闭（closeAndRun 语义，与 tl 1100 的
// 导出二级项行为一致：先关菜单再执行），用一次完整重渲染验证关闭后仍可再次进入。
await act(async () => { click(copyExportItems[1]!); });
ok(calls.includes("export:markdown"), "二级点击「导出 Markdown」触发 exportSession('markdown') 回调");
resetRoot();
await render();
await untilRendered("copyExport reopen render");
const reopenedTopItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-menu .topicbar__menu-item'));
await act(async () => { reopenedTopItems[1]!.click(); });
await waitFor("copyExport submenu reopened", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="复制和导出"]') !== null);
const reopenedItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-submenu [role="menuitem"]'));
await act(async () => { click(reopenedItems[0]!); });
await waitFor("copySession callback", () => calls.includes("copy:# Session") || calls.includes("copied"));
ok(calls.includes("copy:# Session") || calls.includes("copied"), "二级首项「复制会话」触发 getSessionMarkdown 复制回调");

// ---- 二级可达零丢失（tl 1120）：会话视图 ▸（含转录/轨迹） ----
// 上一段已 closeAndRun 整菜单关闭，需重渲染取最新一级项引用。
resetRoot();
await render();
await untilRendered("views reopen render");
const topLevelItems2 = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-menu .topicbar__menu-item'));
const viewsTrigger = topLevelItems2[2]!;
await act(async () => { viewsTrigger.click(); });
await waitFor("views submenu", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="会话视图"]') !== null);
// tl1148 打开态：会话视图 触发项箭头同样翻 ◁（三处逐一验证）。
const openedViewsIndicator = rootElement.querySelector('.topicbar__more-menu .topicbar__menu-item[aria-expanded="true"] .topicbar__more-submenu-indicator');
ok(openedViewsIndicator?.classList.contains("lucide-chevron-left") === true,
  "tl1148 打开态：会话视图 触发项箭头翻 ◁");
const viewItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-submenu [role="menuitemradio"]'));
const viewLabels = viewItems.map((item) => item.textContent?.trim() ?? "");
ok(viewItems.length === 2 && viewLabels[0] === "转录" && viewLabels[1] === "轨迹", `会话视图 ▸ 二级含 转录/轨迹（实际 ${viewLabels.join(" | ")}）`);
await act(async () => { click(viewItems[1]!); });
ok(calls.includes("view:trajectory"), "二级点击「轨迹」触发 setView('trajectory') 回调");

// ---- 二级可达零丢失（tl 1100 保留能力）：打开方式 ▸ ----
// 会话视图点击同样 closeAndRun 关闭了菜单，重渲染取最新一级项引用。
resetRoot();
await render();
await untilRendered("openers reopen render");
const topLevelItems3 = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-menu .topicbar__menu-item'));
const openersTrigger = topLevelItems3[5]!;
await act(async () => { openersTrigger.click(); });
await waitFor("openers submenu", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="打开方式"]') !== null);
// tl1148 打开态：打开方式 触发项箭头同样翻 ◁（三处逐一验证）。
const openedOpenersIndicator = rootElement.querySelector('.topicbar__more-menu .topicbar__menu-item[aria-expanded="true"] .topicbar__more-submenu-indicator');
ok(openedOpenersIndicator?.classList.contains("lucide-chevron-left") === true,
  "tl1148 打开态：打开方式 触发项箭头翻 ◁");
const openerItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-submenu [role="menuitemradio"]'));
ok(openerItems.length === 1 && openerItems[0]!.textContent?.trim() === "Code", "打开方式 ▸ 二级含发现的外部应用列表");

// ---- 键盘：Escape 逐层关闭、ArrowRight 展开、ArrowLeft 收起 ----
await act(async () => { press(openerItems[0]!, "Escape"); });
await waitFor("openers submenu close", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="打开方式"]') === null);
ok(document.activeElement === openersTrigger, "Escape 关闭子菜单并把焦点还给触发项");
// tl1148：子菜单收起后箭头恢复关闭态 ▷（方向与「有子菜单」惯例一致）。
const reclosedOpenersIndicator = openersTrigger.querySelector(".topicbar__more-submenu-indicator");
ok(reclosedOpenersIndicator?.classList.contains("lucide-chevron-right") === true,
  "tl1148 收起后：打开方式 触发项箭头恢复 ▷");
ok(reclosedOpenersIndicator?.classList.contains("lucide-chevron-left") === false,
  "tl1148 收起后：打开方式 触发项箭头不再是 ◁");
await act(async () => { press(openersTrigger, "ArrowRight"); });
await waitFor("openers submenu reopen via ArrowRight", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="打开方式"]') !== null);
const reopenedOpenerItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-submenu [role="menuitemradio"]'));
// ArrowLeft/Escape 的收起逻辑在 SubmenuShell 的 onKeyDown 里是同一个 if 分支
// （`ArrowLeft || Escape`），这里用 ArrowLeft 打开后的二级项触发一次，验证该分支生效。
await act(async () => { press(reopenedOpenerItems[0]!, "ArrowLeft"); });
await waitFor("openers submenu close via ArrowLeft", () => rootElement.querySelector('.topicbar__more-menu [role="menu"][aria-label="打开方式"]') === null);
ok(true, "ArrowRight 展开 / ArrowLeft 收起 子菜单");

// ---- terminalEnabled=false 场景下一级结构仍保持收敛 ----
resetRoot();
await render({ terminalEnabled: false });
await untilRendered("disabled-terminal top-level menu render");
const disabledTerminalItems = Array.from(rootElement.querySelectorAll<HTMLButtonElement>('.topicbar__more-menu .topicbar__menu-item'));
ok(disabledTerminalItems.length === 6, "terminalEnabled=false 时一级仍为 6 项（终端项 disabled 但占位保留）");
await act(async () => { root.unmount(); });

if (failed > 0) {
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  process.exit(1);
}
console.log(`\n${passed} passed, ${failed} failed`);
