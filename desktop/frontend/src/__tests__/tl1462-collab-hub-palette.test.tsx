// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1462-collab-hub-palette.test.tsx
// tl1462 验收 harness —— 协作中心接入命令面板（Ctrl+K 搜「协作中心/信件/聊天/
// 群聊」回车直达面板，补键盘入口）：
//  ① 行为面（jsdom + 真 CommandPalette）：注册形状的命令项（compact chip、
//     关键词组）对四个中文词 + 英文词均可搜中（并用不命中词验证 empty 态，
//     排除「单条列表 query 无关也 PASS」的假绿）；回车触发打开动作并关面板。
//  ② 开关关行为等价（铁律 2）：labFlags 默认 collabHub=false；开关关 = 命令
//     项不进 items（与侧栏入口同一 collabHubEntryEnabled 门控），搜索无结果
//     走 empty 态；开关开 = 项回到列表。
//  ③ 接线面（源码断言，task686 ④ 同层）：App.tsx 注册点在 toolsCmds、同一
//     开关同一打开函数（setCollabHubOpen 与侧栏图标 onClick 同源）、deps 补
//     collabHubEntryEnabled、三语键齐且值与侧栏 sidebar.collabHub 一致。
//
// ⚠ harness 约束（本件实测踩坑）：jsdom 必须在 import react-dom 之前装好
// （window/document 挂上 globalThis）。react-dom 的 canUseDOM 与
// isInputEventSupported 在模块初始化时求值一次——若 import 时 window 尚未
// 安装，受控输入 onChange 走 IE polyfill 死路、永不合成（点击不受影响，
// 既有只点击的面板 harness 因此没踩过）。故 react/react-dom/组件一律动态
// import，jsdom 安装先行。
//
// 繁体说明：keywords 只配 EN+简中是既有惯例（cmd-goto-tab 同），繁体命中走
// t() 渲染的 zh-TW 标题（協作中心），见 ③ 的三语断言。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { applyLabFlags, labFlagEnabled } from "../lib/labFlags";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";
import { en } from "../locales/en";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

// jsdom 先行（见文件头 harness 约束），再加载 react 全家。
const dom = installDom();
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { CommandPalette } = await import("../components/CommandPalette");
const { LocaleProvider } = await import("../lib/i18n");

type PaletteItem = import("../components/CommandPalette").PaletteItem;

// 与 App.tsx toolsCmds 末尾注册的 cmd-collab-hub 同形状（关键词组以 ③ 的
// 源码断言钉住同步，漂移即红）。标题取真实 zh 词条——行为面 ride 真文案。
const COLLAB_KEYWORDS = ["collab", "hub", "collaboration", "mail", "letter", "chat", "group", "协作", "协作中心", "信件", "聊天", "群聊"];

function collabHubItem(runCalls: { n: number }): PaletteItem {
  return {
    id: "cmd-collab-hub",
    group: zh["palette.group.tools"],
    title: zh["palette.cmd.collabHub"],
    icon: null,
    compact: true,
    keywords: COLLAB_KEYWORDS,
    run: () => { runCalls.n += 1; },
  };
}

async function renderPalette(root: ReturnType<typeof createRoot>, onClose: () => void, items: PaletteItem[]) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <CommandPalette
          open
          onClose={onClose}
          items={items}
          placeholder="Search"
          emptyText="No results"
        />
      </LocaleProvider>,
    );
    await flush();
  });
}

// typeQuery 走 React 受控输入的原生 setter + input 事件（绕过 React value
// tracker 去重；testing-library 同款手法）。
async function typeQuery(query: string) {
  const input = document.querySelector<HTMLInputElement>(".palette__input");
  if (!input) throw new Error("missing palette input");
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    if (!setter) throw new Error("missing value setter");
    setter.call(input, query);
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    await flush();
  });
}

console.log("\ntl1462 collab hub palette: search hits + enter-to-open + lab gate");

// 行为面共用一个 jsdom 实例；换 items 走 in-place 复渲染（见文件头——canUseDOM
// 正确后受控输入路径恢复正常）。
{
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const runCalls = { n: 0 };
  const closeCalls = { n: 0 };

  // ── ① 四个搜索词 + 英文词搜中；不命中词走 empty；回车触发打开动作 ──
  // 放一个不命中的回收站条目：query 生效时只剩协作中心一枚 chip（若 typing
  // 坏了 query 恒空 → 两枚 chip，断言即红——防「单条列表假绿」）。
  const trash: PaletteItem = { id: "cmd-trash", group: zh["palette.group.tools"], title: zh["palette.cmd.trash"], icon: null, compact: true, run: () => {} };
  await renderPalette(root, () => { closeCalls.n += 1; }, [trash, collabHubItem(runCalls)]);

  for (const query of ["协作中心", "协作", "信件", "聊天", "群聊", "collab", "mail", "chat", "group"]) {
    await typeQuery(query);
    const chips = document.querySelectorAll<HTMLElement>(".palette__chip .palette__chip-label");
    const hit = chips.length === 1 && chips[0]?.textContent === zh["palette.cmd.collabHub"];
    ok(hit, `search "${query}" narrows to exactly the collab hub chip`);
  }

  // 不命中词：filter 全空 → empty 态（query 管道双向生效的反向证据）。
  await typeQuery("zzz-no-match");
  ok(Boolean(document.querySelector(".palette__empty")), "non-matching query lands on the empty state (query pipeline works both ways)");

  // 回车（active 在 query 变化后归 0 → 高亮即过滤后首项）触发 run + 关面板。
  await typeQuery("信件");
  runCalls.n = 0;
  const closeBefore = closeCalls.n;
  await act(async () => {
    document.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await flush();
  });
  ok(runCalls.n === 1, "Enter runs the collab hub open action");
  ok(closeCalls.n === closeBefore + 1, "Enter closes the palette");

  // ── ② 开关关行为等价（铁律 2）────────────────────────────────────
  // labFlags 默认 collabHub=false（802 零痕迹策略；与侧栏入口同一开关语义）。
  applyLabFlags({});
  ok(labFlagEnabled("collabHub") === false, "labFlags defaults collabHub to off");
  applyLabFlags({ collabHub: true });
  ok(labFlagEnabled("collabHub") === true, "applyLabFlags feeds the collabHub switch");
  applyLabFlags({});

  // 开关关 = 命令项不进 items（复现 App.tsx 条件展开的关分支）→ 搜「协作中心」
  // 走 empty 态，面板回到既有 items 的行为。
  const gatedOff = [trash, ...(false ? [collabHubItem({ n: 0 })] : [])];
  await renderPalette(root, () => { closeCalls.n += 1; }, gatedOff);
  await typeQuery("协作中心");
  ok(Boolean(document.querySelector(".palette__empty")), "gate off: searching 协作中心 lands on the empty state (no entry)");
  ok(document.querySelectorAll(".palette__chip").length === 0, "gate off: no collab hub chip renders");

  // 开关开：同一门控表达式产出本项（插条语义 = cmd-goto-tab tl1175 同款）→
  // 搜「协作中心」回到唯一命中。
  const gatedOn = [trash, ...(true ? [collabHubItem({ n: 0 })] : [])];
  await renderPalette(root, () => { closeCalls.n += 1; }, gatedOn);
  await typeQuery("协作中心");
  const onChips = document.querySelectorAll<HTMLElement>(".palette__chip .palette__chip-label");
  ok(onChips.length === 1 && onChips[0]?.textContent === zh["palette.cmd.collabHub"], "gate on: searching 协作中心 surfaces the entry again");

  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── ③ 接线面（源码断言）──────────────────────────────────────────────
{
  const here = fileURLToPath(new URL(".", import.meta.url));
  const frontendRoot = `${here}../..`;
  const read = (rel: string) => readFileSync(`${frontendRoot}/${rel}`, "utf8");
  const appTsx = read("src/App.tsx");

  // 注册点在 toolsCmds：cmd-collab-hub 块存在于 toolsCmds 字面量（锚 =
  // cmd-product-docs 之后、helpCmds 注释之前）。
  const at = appTsx.indexOf('id: "cmd-collab-hub"');
  ok(at > 0, "App.tsx registers cmd-collab-hub");
  const blockEnd = appTsx.indexOf("satisfies PaletteItem", at);
  const block = at > 0 && blockEnd > 0 ? appTsx.slice(at, blockEnd) : "";

  // 同一开关：命令项挂 collabHubEntryEnabled 条件展开（与侧栏图标同一 state，
  // App.tsx 951 行 labFlagEnabled("collabHub") 快照 + onLabFlagsChange 订阅）。
  ok(appTsx.includes("...(collabHubEntryEnabled\n        ? ["), "palette entry rides the collabHubEntryEnabled conditional spread");
  ok(appTsx.includes("{collabHubEntryEnabled && ("), "sidebar icon rides the same collabHubEntryEnabled gate");
  ok(appTsx.includes('const [collabHubEntryEnabled, setCollabHubEntryEnabled] = useState(labFlagEnabled("collabHub"));'), "collabHubEntryEnabled derives from labFlagEnabled(collabHub)");

  // 同一打开函数：palette run 与侧栏 onClick 都调 setCollabHubOpen(true)
  // （CollabHubPanel 模块单例信号），不复制打开逻辑。
  ok(block.includes("run: () => setCollabHubOpen(true)"), "palette run calls setCollabHubOpen(true)");
  ok(appTsx.split("setCollabHubOpen(true)").length >= 3, "sidebar onClick and palette run share setCollabHubOpen(true) (>=2 call sites)");

  // 分组与图标：工具与诊断组（工作流面板等 lab 门控面板项同组），图标与侧栏
  // 入口同为 MessagesSquare。
  ok(block.includes('group: t("palette.group.tools")'), "entry belongs to the tools group");
  ok(block.includes("<MessagesSquare size={15} />"), "entry icon matches the sidebar icon (MessagesSquare)");

  // 搜索词：任务书四词齐 + 英文对应词（繁体不进 keywords 是既有惯例——标题
  // 走 t() 按 locale 出 協作中心，见下面三语断言；参照 cmd-goto-tab）。
  for (const term of COLLAB_KEYWORDS) {
    ok(block.includes(`"${term}"`), `keywords carry "${term}"`);
  }
  ok(!block.includes("協作"), "keywords follow the EN+simplified convention (traditional comes from the locale title)");

  // deps 补 collabHubEntryEnabled（条件展开引用 state，缺 deps = 开关翻转换不来）。
  const depsLine = appTsx.split("\n").find((l) => l.startsWith("  }, [t, paletteSessions") && l.includes("desktopPlatform]);"));
  ok(Boolean(depsLine && depsLine.includes("collabHubEntryEnabled")), "useMemo deps include collabHubEntryEnabled");

  // 三语键齐 + 值与侧栏 sidebar.collabHub 一致（同一面板同一叫法）。
  ok(zh["palette.cmd.collabHub"] === "协作中心" && zh["sidebar.collabHub"] === "协作中心", "zh palette key matches sidebar.collabHub");
  ok(zhTW["palette.cmd.collabHub"] === "協作中心" && zhTW["sidebar.collabHub"] === "協作中心", "zh-TW palette key matches sidebar.collabHub");
  ok(en["palette.cmd.collabHub"] === "Collaboration Hub" && en["sidebar.collabHub"] === "Collaboration Hub", "en palette key matches sidebar.collabHub");

  // 衔接 tl1458：协作中心『通知』tab 已合入，「通知/通知中心」进 cmd-collab-hub
  // 的 keywords（搜通知直达协作中心；进哪个 tab 由面板记忆决定）。
  ok(block.includes('"通知"') && block.includes('"通知中心"') && block.includes('"notification"'),
    "notification keywords land with the tl1458 tab");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
