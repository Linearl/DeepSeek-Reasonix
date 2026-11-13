// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/svg-stub-register.mjs --import ./scripts/css-stub-register.mjs src/__tests__/tl1568-md-image-width.test.tsx
//
// tl1568（P2 desktop）：markdown 图片渲染无宽度约束——超宽截图被截断（缩放+可放大）。
//
// 20261112 用户实测：转录区 markdown 图片按原始像素宽渲染、超出容器右半被
// .transcript 的 overflow-x:hidden 裁掉。现态核查（本件开工复核）：约束规则
// 早已存在（MarkdownImage.css），但只覆盖 `.md > img` / `.md p > img` 两种落位
// ——a>img / li>img 等结构性落位是盲区；且缩放后无看原图路径（验收②）。
//
// 可证伪断言：
//  Part A（源码钉，仿 tl1121 手法——JSDOM 无布局引擎）：
//   ① .md img 后代全形态约束规则在位（display:block 防竖排 + max-width:100%
//      + height:auto 等比缩放 + object-fit:contain）；
//   ② 旧两落位选择器退场（泛化超集替代，不留双份真相源）；
//   ③ zoomable 标记类 + cursor:zoom-in 在位（点击放大可达性提示）；
//   ④ MarkdownImage 接 viewer context（点击/Enter/Space 开图、无 viewer 零交互）；
//   ⑤ Transcript 根 provide viewer opener + 渲染 ImageViewer（tl1289 载体复用）；
//   ⑥ 负钉：WorkspacePanel 只 provide tabId 不 provide viewer——工作区预览面
//      markdown 图零行为扩散（任务书验收③的三 src 形态渲染链零触碰）。
//  Part B（jsdom 行为钉）：MarkdownImage 单元级行为——有 viewer context 时
//   点击/键盘开图回调收到 Go 解析后的 url+filename；无 context 时无任何
//   交互属性挂 img。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const mdCss = readFileSync(resolve(here, "../components/MarkdownImage.css"), "utf8");
const imageTsx = readFileSync(resolve(here, "../components/MarkdownImage.tsx"), "utf8");
const contextTsx = readFileSync(resolve(here, "../components/MarkdownImageContext.tsx"), "utf8");
const transcriptTsx = readFileSync(resolve(here, "../components/Transcript.tsx"), "utf8");
const workspacePanelTsx = readFileSync(resolve(here, "../components/WorkspacePanel.tsx"), "utf8");

console.log("\ntl1568 markdown 图片宽度约束 + 点击放大");

// ── Part A①②：显示尺寸约束泛化 ──
{
  ok(/\.md img \{[^}]*display: block;/s.test(mdCss), "① .md img display:block（防竖排，tl1121 契约本质）");
  ok(/\.md img \{[^}]*max-width: 100%;/s.test(mdCss), "① .md img max-width:100%（容器约束，超宽等比缩放）");
  ok(/\.md img \{[^}]*height: auto;/s.test(mdCss), "① .md img height:auto（等比缩放不变形）");
  ok(/\.md img \{[^}]*object-fit: contain;/s.test(mdCss), "① .md img object-fit:contain（缩放不裁切内容）");
  ok(!/\.md > img,/.test(mdCss) && !/\.md p > img \{/.test(mdCss), "② 旧两落位选择器退场（.md img 超集替代，零双份规则）");
  ok(/cursor: zoom-in/.test(mdCss) && /\.md-image-zoomable \{/.test(mdCss), "③ zoomable 标记类 + cursor:zoom-in 在位");
}

// ── Part A④：MarkdownImage 接 viewer ──
{
  ok(imageTsx.includes("MarkdownImageViewerContext"), "④ MarkdownImage 读 viewer context");
  ok(/const zoomable = openViewer !== null;/.test(imageTsx), "④ 无 viewer 宿主零交互分支（zoomable 判空）");
  ok(/onClick=\{zoomable \? \(\) => openViewer\(/.test(imageTsx), "④ 点击开图（有 viewer 才挂 onClick）");
  ok(/event\.key === "Enter" \|\| event\.key === " "/.test(imageTsx), "④ Enter/Space 键盘可达（MessageAttachments tl1289 同款）");
  ok(imageTsx.includes("draggable={false}"), "④ img draggable=false（不与选择拖拽抢手势）");
  ok(imageTsx.includes('md-image-zoomable'), "④ 有 viewer 时挂 zoomable 标记类");
  ok(!/cursor: zoom-in/.test(imageTsx), "④ 光标提示归 CSS 不内联（样式单点）");
}

// ── Part A⑤：Transcript 根 provide + ImageViewer 载体 ──
{
  ok(transcriptTsx.includes("MarkdownImageViewerContext.Provider"), "⑤ Transcript 根 provide viewer opener");
  ok(/lazy\(\(\) => import\("\.\/ImageViewer"\)/.test(transcriptTsx), "⑤ ImageViewer 按需加载（转录壳不吞 viewer/标注编辑器体积）");
  ok(/<ImageViewer\s/.test(transcriptTsx) && transcriptTsx.includes("openMarkdownImageViewer"), "⑤ viewer 状态挂转录根 + opener 下发（tl1289 MessageAttachments 同形态）");
  ok(/closeMarkdownImageViewer/.test(transcriptTsx), "⑤ 关闭回调在位");
}

// ── Part A⑥：负钉——工作区预览面零扩散 ──
{
  ok(workspacePanelTsx.includes("MarkdownImageTabContext.Provider"), "⑥ WorkspacePanel 仍 provide tabId（渲染链不回退）");
  ok(!workspacePanelTsx.includes("MarkdownImageViewerContext"), "⑥ WorkspacePanel 不 provide viewer（工作区预览面 markdown 图保持纯展示零行为变化）");
}

// context 默认值：不 provide 时 opener 为 null（任何宿主挂上本模块即安全）。
ok(contextTsx.includes("createContext<((url: string, name?: string) => void) | null>(null)"), "⑥ viewer context 默认 null（未 provide 宿主安全退化）");

// ── Part B：jsdom 行为钉 ──
{
  const { JSDOM } = await import("jsdom");
  const React = (await import("react")).default;
  const { act } = await import("react");
  const { createRoot } = await import("react-dom/client");

  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div><div class="chat-pane"></div></body></html>', {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({ matches: false, media: "", onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, dispatchEvent: () => false }),
  });

  const flush = async () => {
    for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  };

  (window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        ResolveMarkdownImageForTab: async (_tabId: string, source: string) =>
          source.startsWith("data:")
            ? { url: source }
            : { url: "/__reasonix_workspace_media/token-1568/shot.png", filename: "shot.png" },
      },
    },
  };

  const { MarkdownImage } = await import("../components/MarkdownImage");
  const { MarkdownImageViewerContext } = await import("../components/MarkdownImageContext");

  const host = document.createElement("div");
  document.getElementById("root")!.appendChild(host);
  const root = createRoot(host);

  const img = (): HTMLImageElement => host.querySelector("img")!;
  let opened: { url: string; name?: string } | null = null;
  const opener = (url: string, name?: string) => { opened = { url, name }; };

  // 有 viewer：点击开图，回调收 Go 解析后的同源 URL + filename
  await act(async () => {
    root.render(
      React.createElement(MarkdownImageViewerContext.Provider, { value: opener },
        React.createElement(MarkdownImage, { src: "tmp/shot.png", alt: "截图" })),
    );
    await flush();
  });
  ok(Boolean(img()), "B 渲染出 img");
  ok(img().getAttribute("src") === "/__reasonix_workspace_media/token-1568/shot.png", "B src=Go 解析后的同源 URL（验收③ workspace 形态渲染不回退）");
  ok((img().getAttribute("class") ?? "").includes("md-image-zoomable"), "B 有 viewer 时挂 zoomable 类");
  ok(img().getAttribute("tabindex") === "0", "B 有 viewer 时键盘可达 tabIndex");
  ok(img().getAttribute("draggable") === "false", "B draggable=false");
  await act(async () => {
    img().click();
    await new Promise(r => setTimeout(r, 0));
  });
  ok(opened !== null && opened.url === "/__reasonix_workspace_media/token-1568/shot.png" && opened.name === "shot.png",
    "B 点击开图：回调收 resolved url + filename");
  opened = null;
  await act(async () => {
    img().dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await new Promise(r => setTimeout(r, 0));
  });
  ok(opened !== null, "B Enter 键开图");

  // 无 viewer：零交互属性（工作区预览等宿主形态）
  const host2 = document.createElement("div");
  document.getElementById("root")!.appendChild(host2);
  const root2 = createRoot(host2);
  await act(async () => {
    root2.render(React.createElement(MarkdownImage, { src: "tmp/shot.png", alt: "截图" }));
    await flush();
  });
  const img2 = host2.querySelector("img")!;
  ok(Boolean(img2) && img2.getAttribute("src") === "/__reasonix_workspace_media/token-1568/shot.png", "B 无 viewer 宿主渲染不回退");
  ok(img2.getAttribute("class") === null, "B 无 viewer 不挂 zoomable 类");
  ok(img2.getAttribute("tabindex") === null && img2.getAttribute("onclick") === null, "B 无 viewer 零交互属性");

  await act(async () => { root.unmount(); root2.unmount(); });
  host.remove(); host2.remove();
  dom.window.close();
}

process.stdout.write(`\ntl1568 md-image width+viewer: ${passed} passed, ${failed} failed\n`);
process.exit(failed > 0 ? 1 : 0);
