// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/workspace-panel-revive.test.tsx
// 任务 841: 工作区浮层 Icon 无反应——open=true 但不可渲染（liveWidth 瞬态残留
// 小于渲染阈值）时 openWorkspacePanel/openRightDockMode 的早退分支形成死区。
// 钉两层：placement 层证明「残留→不可渲染、清 liveWidth→恢复」闭环；命令层钉
// 「不可渲染时点击必产生状态变化（清 liveWidth），可渲染时幂等早退保持」。
import assert from "node:assert/strict";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useWorkspacePanelCommands } from "../app-runtime/useWorkspacePanelCommands";
import { saveWorkspacePanelOpen, useLayoutStore } from "../store/layout";
import { resolveWorkspacePanelPlacement } from "../lib/workspaceLayout";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);
let commands!: ReturnType<typeof useWorkspacePanelCommands>;
let widthClears = 0;
const closeOverlays = () => {};
const clearLiveWidth = () => { widthClears++; };
const ROOT = "/fixture/revive-841";
function Probe({ visible }: { visible: boolean }) {
  commands = useWorkspacePanelCommands({ workspaceRoot: ROOT, creation: false, visible, closeOverlays, clearLiveWidth,
    availableWidth: 768, clampTreeWidth: (width) => width, setTreeWidth: () => {} });
  return null;
}
const paint = (visible: boolean) => act(async () => root.render(<Probe visible={visible} />));
try {
  // placement 层：841 死区前提与恢复闭环。
  // available = 1440 - 264 - 400 - 8 = 768；resolvedWidth = min(max(280, 560), 768) = 560 ≥ minRender 420，
  // 故 overlay=false；renderWidth 取 liveWidth=320 < 420 → renderable=false（open 却不可渲染）。
  const base = { viewportWidth: 1440, sidebarCollapsed: false, sidebarWidth: 264, chatMinWidth: 400, resizerWidth: 8,
    open: true, maximized: false, preferredWidth: 560, minWidth: 280, minRenderWidth: 420 };
  const stuck = resolveWorkspacePanelPlacement({ ...base, liveWidth: 320 });
  assert.equal(stuck.renderable, false, "liveWidth 残留使 open 面板不可渲染（841 死区前提）");
  const revived = resolveWorkspacePanelPlacement({ ...base, liveWidth: null });
  assert.equal(revived.renderable, true, "清掉 liveWidth 后同参数恢复可渲染（841 恢复闭环）");

  // 命令层：open=true + 不可渲染时 openRightDockMode 必须产生状态变化（清 liveWidth），不得静默早退。
  saveWorkspacePanelOpen(true, ROOT);
  await paint(false);
  const clearsBefore = widthClears;
  await act(async () => commands.openRightDockMode("files"));
  assert.equal(widthClears, clearsBefore + 1, "不可渲染时点击必须清 liveWidth 恢复渲染");
  assert.equal(useLayoutStore.getState().workspacePanelOpen, true, "恢复场景保持 open，不关面板");

  // open=true + 可渲染：幂等早退保持，不产生多余副作用。
  await paint(true);
  const clearsMid = widthClears;
  await act(async () => commands.openRightDockMode("files"));
  assert.equal(widthClears, clearsMid, "可渲染时的重复展开保持幂等早退");
  assert.equal(useLayoutStore.getState().workspacePanelOpen, true, "幂等场景不翻转 open");

  // toggle 链路（浮层 Icon 的实际入口）：不可渲染时点击必产生状态变化。
  await paint(false);
  const clearsToggle = widthClears;
  await act(async () => commands.toggleWorkspacePanel());
  assert.equal(widthClears, clearsToggle + 1, "toggle 在不可渲染时点击清 liveWidth 恢复");
  assert.equal(useLayoutStore.getState().workspacePanelOpen, true, "toggle 恢复场景保持 open");

  // 正常展开/收起循环不回归：可见时 toggle 收起；收起后 Icon 重渲染为展开态（visible=false），再点展开。
  await paint(true);
  await act(async () => commands.toggleWorkspacePanel());
  assert.equal(useLayoutStore.getState().workspacePanelOpen, false, "可见时 toggle 收起（不回归）");
  await paint(false);
  await act(async () => commands.toggleWorkspacePanel());
  assert.equal(useLayoutStore.getState().workspacePanelOpen, true, "收起后 toggle 再展开（不回归）");
  console.log("workspace panel revive (841): unrenderable open click clears liveWidth; idempotent skip and toggle cycle preserved");
} finally {
  await act(async () => root.unmount());
  dom.window.close();
}
