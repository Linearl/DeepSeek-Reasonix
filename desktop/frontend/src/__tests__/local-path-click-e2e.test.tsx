// Run: tsx src/__tests__/local-path-click-e2e.test.tsx
//
// Headless end-to-end of the click-to-open chain from issue #7426: chat
// markdown with a plain Windows path is rendered with the real production
// pipeline (remarkLocalPathLinks + urlTransform + RichMarkdownLink), the
// anchor is clicked in a JSDOM document, and the native OpenLocalPath binding
// must receive the decoded local path. A plain http link must instead go to
// the system browser. No UI is involved — the Wails bridge is stubbed via
// window.go.main.App / window.runtime.

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://reasonix.local/" });
const { window } = dom;

// Set globals BEFORE dynamically importing React DOM so the renderer sees a
// real document (ESM imports are hoisted, hence dynamic import below).
(globalThis as Record<string, unknown>).window = window;
(globalThis as Record<string, unknown>).document = window.document;
(globalThis as Record<string, unknown>).HTMLElement = window.HTMLElement;
(globalThis as Record<string, unknown>).MouseEvent = window.MouseEvent;
// React 19 requires this flag for act() to flush renders/pass-through events.
(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

// Bridge spies: OpenLocalPath is what the click must call; BrowserOpenURL is
// what plain http links must call instead.
const opened: string[] = [];
const openedWith: Array<[string, string]> = [];
const browsed: string[] = [];
// Task 1138: when set, the next OpenLocalPath rejects — the chip must surface
// an error toast instead of failing silently.
let openFailure: Error | null = null;
type MockOpeners = {
  openers: Array<{ id: string; name: string; kind: "editor" | "file-manager" }>;
  preferred: string;
};
let openerRaceMode = false;
let openerRaceCalls = 0;
let resolveStaleOpeners: ((value: MockOpeners) => void) | undefined;
(window as unknown as Record<string, unknown>).go = {
  main: {
    App: {
      OpenLocalPath: async (path: string) => {
        opened.push(path);
        if (openFailure) throw openFailure;
      },
      ExternalOpeners: async (): Promise<MockOpeners> => {
        if (openerRaceMode) {
          openerRaceCalls += 1;
          if (openerRaceCalls === 1) {
            return new Promise<MockOpeners>((resolve) => {
              resolveStaleOpeners = resolve;
            });
          }
          return {
            openers: [{ id: "xcode", name: "Xcode", kind: "editor" }],
            preferred: "xcode",
          };
        }
        return {
          openers: [
            { id: "vscode", name: "VS Code", kind: "editor" as const },
            { id: "finder", name: "Finder", kind: "file-manager" as const },
          ],
          preferred: "vscode",
        };
      },
      OpenLocalPathInExternalOpener: async (path: string, id: string) => {
        openedWith.push([path, id]);
      },
      RevealPath: async () => {},
    },
  },
};
(window as unknown as Record<string, unknown>).runtime = {
  BrowserOpenURL: (url: string) => {
    browsed.push(url);
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

const { createElement, StrictMode } = await import("react");
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { default: ReactMarkdown, defaultUrlTransform } = await import("react-markdown");
const { default: remarkGfm } = await import("remark-gfm");
const { remarkLocalPathLinks } = await import("../lib/localPathLinks");
const { localPathFromHref, RichMarkdownLink } = await import("../components/githubLink");
const { ToastProvider } = await import("../lib/toast");

const markdownUrlTransform = (value: string) =>
  localPathFromHref(value) !== null ? value : defaultUrlTransform(value);

const components = { a: RichMarkdownLink };

async function renderClick(
  markdown: string,
  opts: { strictMode?: boolean; wrapToast?: boolean } = {},
): Promise<HTMLAnchorElement[]> {
  const container = window.document.createElement("div");
  window.document.body.appendChild(container);
  const root = createRoot(container);
  const markdownElement = createElement(
    ReactMarkdown,
    { remarkPlugins: [remarkGfm, remarkLocalPathLinks], urlTransform: markdownUrlTransform, components },
    markdown,
  );
  const tree = opts.wrapToast ? createElement(ToastProvider, null, markdownElement) : markdownElement;
  await act(async () => {
    root.render(opts.strictMode ? createElement(StrictMode, null, tree) : tree);
  });
  return Array.from(container.querySelectorAll("a"));
}

console.log("\nheadless click-to-open e2e");

// 1. Issue #7426 scenario: plain Windows path in chat text, CJK dirs.
{
  const anchors = await renderClick("文件在 D:\\Project\\Jhtj\\20250804_000000_001_中停时分析\\05-静态验收.md 已生成");
  ok(anchors.length === 1, "exactly one anchor rendered");
  const href = anchors[0]?.getAttribute("href") ?? "";
  ok(href === "file:///D:/Project/Jhtj/20250804_000000_001_%E4%B8%AD%E5%81%9C%E6%97%B6%E5%88%86%E6%9E%90/05-%E9%9D%99%E6%80%81%E9%AA%8C%E6%94%B6.md",
    `anchor href is the encoded file URL (${href})`);
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened.length === 1, "click invoked OpenLocalPath once");
  ok(opened[0] === "D:/Project/Jhtj/20250804_000000_001_中停时分析/05-静态验收.md",
    `OpenLocalPath received the decoded CJK path (${opened[0]})`);
  ok(browsed.length === 0, "system browser was not involved");
}

// 2. UNC path (markdown folds \\ into \, linkify restores it; the href uses
// forward slashes, which Windows accepts as a UNC form).
{
  const anchors = await renderClick("共享盘 \\\\nas\\share\\docs\\report.md 已生成");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened[1] === "//nas/share/docs/report.md", `UNC path forwarded as slash form (${opened[1]})`);
}

// 3. Canonical authority-form UNC markdown links use the same native path.
{
  const anchors = await renderClick("[共享盘](file://nas/share/docs/report.md)");
  ok(anchors.length === 1, "canonical UNC markdown link renders an anchor");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened[2] === "//nas/share/docs/report.md", `canonical UNC path forwarded correctly (${opened[2]})`);
}

// 4. Explicit markdown link to a local file keeps working through the same path.
{
  const anchors = await renderClick("[验收单](file:///D:/docs/acceptance.md)");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened[3] === "D:/docs/acceptance.md", "explicit file:/// markdown link opens locally");

  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 40, clientY: 40 }));
  });
  const contextMenu = window.document.querySelector('[role="menu"]');
  ok(contextMenu !== null, "local path context menu opens on right click");
  const vscodeItem = Array.from(contextMenu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])
    .find((item) => item.textContent?.includes("VS Code"));
  ok(!Array.from(contextMenu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])
    .some((item) => item.textContent?.includes("使用 Finder 打开")), "file manager is not duplicated in the open-with list");
  await act(async () => {
    vscodeItem?.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(JSON.stringify(openedWith) === JSON.stringify([["D:/docs/acceptance.md", "vscode"]]), "context menu opens the path with the selected application");
}

// 5. A browser may emit both contextmenu and right-button auxclick. Only the
// middle button is an open gesture; the right button must leave the menu open
// without also launching the local file.
{
  const anchors = await renderClick("[右键](file:///D:/docs/right-click.md)");
  const openedBefore = opened.length;
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    anchors[0].dispatchEvent(new window.MouseEvent("auxclick", { button: 2, bubbles: true, cancelable: true }));
    await Promise.resolve();
  });
  ok(opened.length === openedBefore, "right-button auxclick does not open a local path");
  await act(async () => {
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape" }));
  });
}

// 6. React StrictMode replays effect setup and cleanup in development. Opener
// discovery after that replay must still populate the context menu.
{
  const anchors = await renderClick("[严格模式](file:///D:/strict.md)", { strictMode: true });
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    await Promise.resolve();
  });
  const menus = window.document.querySelectorAll('[role="menu"]');
  const strictMenu = menus[menus.length - 1];
  ok(strictMenu?.textContent?.includes("VS Code") === true,
    "StrictMode opener discovery populates the local-path menu");
  await act(async () => {
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape" }));
  });
}

// 7. Overlapping opener discovery keeps the latest response.
{
  openerRaceMode = true;
  const anchors = await renderClick("[竞态](file:///D:/race.md)");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 50, clientY: 50 }));
  });
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 50, clientY: 50 }));
    await Promise.resolve();
  });
  const raceMenu = window.document.querySelector('[role="menu"]');
  ok(openerRaceCalls === 2 && raceMenu?.textContent?.includes("Xcode") === true,
    "latest opener discovery populates the local-path menu");
  await act(async () => {
    resolveStaleOpeners?.({
      openers: [{ id: "stale", name: "Stale Editor", kind: "editor" }],
      preferred: "stale",
    });
    await Promise.resolve();
  });
  ok(raceMenu?.textContent?.includes("Xcode") === true && !raceMenu?.textContent?.includes("Stale Editor"),
    "stale opener discovery cannot replace the latest result");
}

// 8. Plain http link must NOT hit OpenLocalPath. A right-button auxclick must
// also leave it to the context-menu gesture instead of opening the browser.
{
  const anchors = await renderClick("见 https://example.com/page 文档");
  const browsedBefore = browsed.length;
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("auxclick", { button: 2, bubbles: true, cancelable: true }));
  });
  ok(browsed.length === browsedBefore, "right-button auxclick does not open an http link");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(browsed.length === 1 && browsed[0] === "https://example.com/page", "http link went to the system browser");
  ok(opened.length === 4, "OpenLocalPath was not called for the http link");
}

// 9. Non-path text renders no anchors and no clicks.
{
  const anchors = await renderClick("版本 1.2.3 已发布");
  ok(anchors.length === 0, "no anchors for plain text");
}

console.log("\ntask 1138 — spaced-path chips, failure toast, inline-code precision");

// 10. A space-bearing absolute path renders exactly one chip and a click
// forwards the FULL spaced path to the native opener.
{
  const anchors = await renderClick("安装 C:\\Program Files\\app.exe 完成");
  ok(anchors.length === 1, "spaced drive path renders exactly one chip");
  ok(anchors[0].getAttribute("data-path-kind") === "file", "file chip carries data-path-kind=file");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened[opened.length - 1] === "C:/Program Files/app.exe",
    `click forwarded the full spaced path (${opened[opened.length - 1]})`);
}

// 11. A trailing separator marks the chip as a directory.
{
  const anchors = await renderClick("[输出目录](file:///D:/docs/)");
  ok(anchors.length === 1 && anchors[0].getAttribute("data-path-kind") === "directory",
    "trailing separator renders a directory chip");
}

// 12. A failed open (missing path) surfaces a visible error toast — never a
// silent catch.
{
  openFailure = new Error("open failed: no such file");
  const anchors = await renderClick("打开 file:///D:/missing/报告.md 查看", { wrapToast: true });
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
    await Promise.resolve();
  });
  const toast = window.document.querySelector(".toast--error");
  ok(toast !== null && (toast.textContent ?? "").includes("no such file"),
    "failed open shows an error toast with the reason");
  openFailure = null;
}

// 13. Task 1138 follow-up: a code span whose ENTIRE value is one absolute
// path chipifies (LLM answers overwhelmingly print paths in backticks; DSH
// renders such spans as chips). Non-path and mixed-content spans stay inert
// (task 1138 acceptance 4, precision first).
{
  const anchors = await renderClick("配置在 `C:\\Windows\\System32\\cmd.exe` 里");
  ok(anchors.length === 1, "whole-span absolute path inside backticks renders a chip");
  await act(async () => {
    anchors[0].dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  ok(opened[opened.length - 1] === "C:/Windows/System32/cmd.exe",
    `code-span chip click forwards the decoded path (${opened[opened.length - 1]})`);
  ok(window.document.querySelectorAll("code").length === 0, "the path span no longer renders as raw code");
}
{
  const anchors = await renderClick("先跑 `npm run build`，再改 `src/lib/foo.ts`，输出见 `path=D:\\x\\y.md` 记录");
  ok(anchors.length === 0, "non-path / relative / mixed-content spans render no chips");
  ok(window.document.querySelectorAll("code").length === 3, "all three code spans still render as code");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
