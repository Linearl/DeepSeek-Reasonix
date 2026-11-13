// Run: tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1565-attachment-interactions.test.tsx
// tl1565（P2 跨会话/群聊附件）验收 harness：
//
//   验收 1（G1 渲染统一）：群聊三类引用各一——图片绝对路径附件 / PDF 等非
//     图片附件 / 工作区绝对路径引用——均渲染卡片；图片 lightbox 可导航。
//
//   验收 2（G3 打开能力）：文件卡左键=默认应用打开；文件夹卡左键=打开目录；
//     右键菜单三项（复制内容=文本进剪贴板 / 复制路径 / 打开所在文件夹=资源
//     管理器选中）逐项接线；打开失败（OpenLocalPath 拒绝 / 探针判不可开 /
//     读内容拒绝）有可见 toast，不静默。
//
//   验收 3（G2 守卫）：全仓只剩 attachmentDisplay 一套附件解析正则——
//     ChannelMessageStream 无 attachmentRefsOf 残留；其余源文件零附件解析
//     正则字面量；locale 三语键齐全。
//
// 桥契约（ReadLocalTextFile 门禁族）由 desktop 包 TestReadLocalTextFile* 持有。

import { JSDOM } from "jsdom";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
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
function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  Node: dom.window.Node,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
  MutationObserver: dom.window.MutationObserver,
  InputEvent: dom.window.InputEvent,
  KeyboardEvent: dom.window.KeyboardEvent,
  MouseEvent: dom.window.MouseEvent,
  File: dom.window.File,
  FileReader: dom.window.FileReader,
});
// i18n 的 locale 由 navigator.language 决定（readPref 由桌面配置接管，测试
// 环境恒空）——代理出 zh-CN 让 LocaleProvider 走中文文案（断言中文 toast/菜单）。
const zhNavigator = new Proxy(dom.window.navigator, {
  get(target, prop) {
    if (prop === "language") return "zh-CN";
    const value = Reflect.get(target, prop);
    return typeof value === "function" ? value.bind(target) : value;
  },
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: zhNavigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame?.bind(dom.window) ?? ((cb: FrameRequestCallback) => setTimeout(() => cb(Date.now()), 16) as unknown as number);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame?.bind(dom.window) ?? ((id: number) => clearTimeout(id));

const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
await preloadLocale("zh");
// lazy chunk 预解析：MessageAttachments 经 lazy+Suspense 挂载，显式导入命中
// 模块缓存后 lazy 首帧可用（harness 微任务时序下消空闪抖动）。
await import("../components/MessageAttachments");
const { ChannelMessageStream } = await import("../components/ChannelMessageStream");
const { HUMAN_LOCAL } = await import("../components/ChannelChatPanel");

type CollabChannelMemberView = import("../lib/bridge").CollabChannelMemberView;
type CollabChannelMessageView = import("../lib/bridge").CollabChannelMessageView;

const flush = async (): Promise<void> => {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};
const settle = async (): Promise<void> => {
  for (let round = 0; round < 8; round += 1) await flush();
};

const rootEl = document.getElementById("root")!;
const root = createRoot(rootEl);

const memberDetails: CollabChannelMemberView[] = [
  { contact: HUMAN_LOCAL, type: "human", joinedAt: 1 },
  { contact: "sc_dev", type: "session", joinedAt: 2 },
];

// 桥桩：三类读桥 + 打开家族 + 内容读桥，全部记录调用。
const calls: string[] = [];
const clipboardWrites: string[] = [];
(dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
  main: {
    App: {
      ChannelAttachmentDataURL: async (p: string) => { calls.push(`ChannelAttachmentDataURL:${p}`); return `data:image/png;base64,${encodeURIComponent(p)}`; },
      AttachmentDataURL: async (p: string) => { calls.push(`AttachmentDataURL:${p}`); return `data:image/png;base64,${encodeURIComponent(p)}`; },
      ResolveOpenablePathForTab: async (_tabID: string, path: string) => { calls.push(`Resolve:${path}`); return path; },
      OpenLocalPath: async (path: string) => { calls.push(`OpenLocalPath:${path}`); },
      RevealPath: async (path: string) => { calls.push(`RevealPath:${path}`); },
      ReadLocalTextFile: async (path: string) => { calls.push(`ReadLocalTextFile:${path}`); return "文件正文内容"; },
    },
  },
};
Object.defineProperty(dom.window.navigator, "clipboard", {
  configurable: true,
  value: { writeText: async (text: string) => { clipboardWrites.push(text); return true; } },
});

const paint = async (messages: CollabChannelMessageView[]): Promise<void> => {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ToastProvider>
          <ChannelMessageStream messages={messages} memberDetails={memberDetails} directoryTitle={(id) => (id === "sc_dev" ? "开发会话" : id)} />
        </ToastProvider>
      </LocaleProvider>,
    );
  });
  await settle();
};

const GLOBAL_ATT = "C:\\w\\.reasonix\\attachments";
// ══ 验收 1（G1）：三类引用各一，卡片齐渲染 ═══════════════════════════════
{
  await paint([
    { id: "a1", sender: "sc_dev", body: `图 @${GLOBAL_ATT}\\shot.png`, at: 1, fanout: [] },
    { id: "a2", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\report.pdf 见附件`, at: 2, fanout: [] },
    { id: "a3", sender: "sc_dev", body: `源码 @D:\\proj\\src\\main.ts 看一下`, at: 3, fanout: [] },
  ]);
  eq(document.querySelectorAll(".msg-attachment--image").length, 1, "the image attachment ref renders an image card");
  const thumbs = document.querySelectorAll(".msg-attachment__icon--image img");
  eq(thumbs.length, 1, "the image card carries an inline thumbnail (ChannelAttachmentDataURL)");
  eq(document.querySelectorAll(".msg-attachment--file").length, 2, "the pdf attachment ref and the workspace path render file cards");
  const streamText = document.getElementById("root")!.textContent ?? "";
  ok(streamText.includes("report.pdf"), "the pdf card carries its name");
  ok(streamText.includes("main.ts"), "the workspace path card carries its name");
  ok(bridgeCallStartedWith("ChannelAttachmentDataURL:"), "the image preview reads through the channel bridge");
}

function bridgeCallStartedWith(prefix: string): boolean {
  return calls.some((c) => c.startsWith(prefix));
}

// ══ 验收 1（G1）：图片 lightbox 可导航（tl1289 通道零回退）═══════════════
{
  await paint([
    { id: "b1", sender: "sc_dev", body: `两张图 @${GLOBAL_ATT}\\one.png @${GLOBAL_ATT}\\two.png`, at: 1, fanout: [] },
  ]);
  await settle();
  const cards = Array.from(document.querySelectorAll(".msg-attachment--image")) as HTMLElement[];
  eq(cards.length, 2, "two image cards render");
  await act(async () => { cards[0].click(); });
  await settle();
  const viewer = document.querySelector(".image-viewer") ?? document.querySelector("[class*='image-viewer']");
  ok(viewer, "clicking an image card opens the lightbox");
  const counterBefore = document.querySelector(".image-viewer__counter")?.textContent ?? "";
  ok(counterBefore.includes("1"), `the lightbox starts at the first image (${JSON.stringify(counterBefore)})`);
  const next = document.querySelector(".image-viewer__nav--next") as HTMLButtonElement | null;
  ok(next, "the lightbox has a next control");
  if (next) {
    await act(async () => { next.click(); });
    await settle();
    const counterAfter = document.querySelector(".image-viewer__counter")?.textContent ?? "";
    ok(counterAfter !== counterBefore && counterAfter.includes("2"), `the lightbox navigates to the second image (${JSON.stringify(counterAfter)})`);
  }
  await act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  await settle();
  await paint([]);
  document.querySelector(".image-viewer__close")?.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await settle();
}

// ══ 验收 2（G3）：文件卡左键=默认应用打开（接线+探针）═══════════════════
{
  calls.length = 0;
  await paint([
    { id: "c1", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\report.pdf`, at: 1, fanout: [] },
    // 文件夹引用形态=正斜杠结尾（isDisplayReference 的 isDir 判据）；尾部反
    // 斜杠形态会被 @-token 的「转义空格」语法吞进 token，不是合法 folder 引用。
    { id: "c2", sender: "sc_dev", body: `目录 @D:/proj/docs/ 见文档`, at: 2, fanout: [] },
  ]);
  const cards = Array.from(document.querySelectorAll(".msg-attachment--file, .msg-attachment--folder")) as HTMLElement[];
  eq(cards.length, 2, "a file card and a folder card render");
  eq(document.querySelectorAll(".msg-attachment--folder").length, 1, "the trailing-slash ref renders a folder card");
  await act(async () => { cards[0].click(); });
  await settle();
  ok(calls.some((c) => c.startsWith("OpenLocalPath:") && c.endsWith("report.pdf")), `left-click opens the file via OpenLocalPath (${JSON.stringify(calls)})`);
  await act(async () => { cards[1].click(); });
  await settle();
  ok(calls.some((c) => c.startsWith("OpenLocalPath:") && /docs\/?$/.test(c)), "left-click on the folder card opens the directory");
  // 键盘可达性：Enter 与左键同义。
  calls.length = 0;
  await act(async () => {
    cards[0].dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
  });
  await settle();
  ok(calls.some((c) => c.startsWith("OpenLocalPath:")), "Enter on the file card opens it too (键盘同权)");
}

// ══ 验收 2（G3）：右键菜单三项接线 ══════════════════════════════════════
{
  calls.length = 0;
  clipboardWrites.length = 0;
  await paint([
    { id: "d1", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\report.pdf`, at: 1, fanout: [] },
  ]);
  const card = document.querySelector(".msg-attachment--file") as HTMLElement;
  await act(async () => {
    card.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 30 }));
  });
  await flush();
  const menu = document.querySelector(".context-menu");
  ok(menu, "right-click opens the attachment context menu");
  const labels = Array.from(document.querySelectorAll(".context-menu [role='menuitem']")).map((b) => b.textContent ?? "");
  ok(labels.some((l) => l.includes("复制内容")), `menu has copy-contents (${JSON.stringify(labels)})`);
  ok(labels.some((l) => l.includes("复制路径")), "menu has copy-path");
  ok(labels.some((l) => l.includes("在文件管理器中显示")), "menu has reveal-in-file-manager");

  const itemByLabel = (needle: string) => {
    const el = Array.from(document.querySelectorAll(".context-menu [role='menuitem']")).find((b) => (b.textContent ?? "").includes(needle)) as HTMLElement | undefined;
    return el;
  };
  // 复制内容：ReadLocalTextFile → 剪贴板收到文本正文。
  await act(async () => { itemByLabel("复制内容")?.click(); });
  await settle();
  ok(calls.some((c) => c.startsWith("ReadLocalTextFile:")), "copy-contents engages the text read bridge");
  eq(clipboardWrites[0], "文件正文内容", "copy-contents puts the file text on the clipboard");

  // 复制路径：剪贴板收到引用路径。
  await act(async () => {
    card.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 30 }));
  });
  await flush();
  await act(async () => { itemByLabel("复制路径")?.click(); });
  await settle();
  eq(clipboardWrites[1], `${GLOBAL_ATT}\\report.pdf`, "copy-path puts the attachment path on the clipboard");

  // 打开所在文件夹：RevealPath（资源管理器选中）。
  calls.length = 0;
  await act(async () => {
    card.dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 30, clientY: 30 }));
  });
  await flush();
  await act(async () => { itemByLabel("在文件管理器中显示")?.click(); });
  await settle();
  ok(calls.some((c) => c.startsWith("RevealPath:")), "reveal engages RevealPath (资源管理器选中)");
}

// ══ 验收 2（G3）：失败路径可见提示，不静默 ══════════════════════════════
{
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
    main: {
      App: {
        ResolveOpenablePathForTab: async (_tabID: string, path: string) => path, // 探针放行——拒绝由动作桥本身给出
        OpenLocalPath: async () => { throw new Error("open refused"); },
        RevealPath: async () => { throw new Error("reveal refused"); },
        ReadLocalTextFile: async () => { throw new Error('file "x.pdf" is not text content'); },
        ChannelAttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
        AttachmentDataURL: async () => "data:image/png;base64,iVBORw0KGgo=",
      },
    },
  };
  const toastText = async (): Promise<string> => {
    await settle();
    return Array.from(document.querySelectorAll(".toast__text")).map((el) => el.textContent ?? "").join("|");
  };
  await paint([{ id: "e0", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\probe.pdf`, at: 1, fanout: [] }]);
  // 探针拒绝（返回空串）→ 左键打开=「路径当前无法打开」诚实提示。
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = async () => "";
  await paint([{ id: "e1", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\probe.pdf`, at: 2, fanout: [] }]);
  const probeCard = document.querySelector(".msg-attachment--file") as HTMLElement;
  await act(async () => { probeCard.click(); });
  let text = await toastText();
  ok(text.includes("该路径当前无法打开"), `probe-rejected open shows an honest toast (${JSON.stringify(text)})`);
  // 动作桥拒绝（OpenLocalPath 报错）→ toast 附原因。
  (dom.window as unknown as { go: { main: { App: Record<string, unknown> } } }).go.main.App.ResolveOpenablePathForTab = async (_tabID: string, path: string) => path;
  await paint([{ id: "e2", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\report.pdf`, at: 3, fanout: [] }]);
  await act(async () => { (document.querySelector(".msg-attachment--file") as HTMLElement).click(); });
  text = await toastText();
  ok(text.includes("无法使用") && text.includes("open refused"), `open refusal surfaces in a toast (${JSON.stringify(text)})`);
  // 复制内容读桥拒绝（二进制）→ toast 附原因。
  await paint([{ id: "e3", sender: "sc_dev", body: `报告 @${GLOBAL_ATT}\\report.pdf`, at: 4, fanout: [] }]);
  await act(async () => {
    (document.querySelector(".msg-attachment--file") as HTMLElement).dispatchEvent(new dom.window.MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 20, clientY: 20 }));
  });
  await flush();
  await act(async () => {
    const el = Array.from(document.querySelectorAll(".context-menu [role='menuitem']")).find((b) => (b.textContent ?? "").includes("复制内容")) as HTMLElement | undefined;
    el?.click();
  });
  text = await toastText();
  ok(text.includes("无法使用") && text.includes("not text"), `copy-contents refusal surfaces in a toast (${JSON.stringify(text)})`);
  root.unmount();
}

// ══ 验收 3（G2 守卫）：全仓只剩 attachmentDisplay 一套附件解析正则 ═══════
{
  const here = dirname(fileURLToPath(import.meta.url));
  const srcRoot = resolve(here, "..");
  const whitelist = [resolve(srcRoot, "lib", "attachmentDisplay.ts")];
  const offenders: string[] = [];
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        if (entry === "__tests__") continue;
        walk(full);
        continue;
      }
      if (!/\.tsx?$/.test(entry)) continue;
      if (whitelist.includes(full)) continue;
      const raw = readFileSync(full, "utf8");
      // 剥注释再查——守卫针对「实现回潮」，注释里的历史叙述不算。
      const source = raw
        .replace(/\/\*[\s\S]*?\*\//g, " ")
        .replace(/^\s*\/\/[^\n]*/gm, " ");
      // 旧实现的标志性标识符：任何源文件不得再出现。
      if (source.includes("attachmentRefsOf")) offenders.push(`${full}: attachmentRefsOf`);
      // 附件解析正则字面量（.reasonix/attachments 出现在正则位置）：唯一实现
      // 在 lib/attachmentDisplay.ts，别处出现=第二真相源回潮。
      if (/\/[^/\n]*\.reasonix\\\/attachments/.test(source) || /new RegExp\([^)]*attachments/.test(source)) {
        offenders.push(`${full}: attachment-path regex literal`);
      }
    }
  };
  walk(srcRoot);
  eq(offenders.length, 0, `no second attachment-parsing regex outside lib/attachmentDisplay.ts (${JSON.stringify(offenders)})`);
  const streamSrc = readFileSync(resolve(srcRoot, "components", "ChannelMessageStream.tsx"), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/^\s*\/\/[^\n]*/gm, " ");
  ok(!streamSrc.includes("attachmentRefsOf"), "ChannelMessageStream carries no attachmentRefsOf residue (剥注释)");
  ok(!/\.(?:png|jpe?g|gif|webp)\$/iu.test(streamSrc), "ChannelMessageStream carries no image-extension whitelist regex");
  // 桥声明钉：复制内容读桥的手写契约（wails generate 禁用）。
  const bridgeSrc = readFileSync(resolve(srcRoot, "lib", "bridge.ts"), "utf8");
  ok(bridgeSrc.includes("ReadLocalTextFile(path: string): Promise<string>"), "bridge declares ReadLocalTextFile (手写契约)");
}

// ══ 验收 3（G2）：locale 三语键齐全 ═════════════════════════════════════
{
  const here = dirname(fileURLToPath(import.meta.url));
  const needed = ["msg.copyContents", "msg.copyPath", "msg.attachmentPathUnavailable"];
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const source = readFileSync(resolve(here, "..", "locales", locale), "utf8");
    for (const key of needed) {
      ok(source.includes(`"${key}":`), `${locale} declares ${key}`);
    }
  }
}

process.stdout.write(`\ntl1565-attachment-interactions: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
process.exit(0);
