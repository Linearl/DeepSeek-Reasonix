// Run: tsx src/__tests__/task758-steer-collab-card.test.tsx
//
// 任务758: the same cross-session delivery text enters the transcript through
// two render entities — a user row (UserMessage → im-source card) and a ↪
// steer notice (SteerCard bubble). The entities used to diverge: the card on
// user rows, RAW text (bare contact_id / hop / threadId metadata) on steer
// rows, depending on injection timing and consumption state. After 758 both
// share one cardification decision (lib/collabMessage.ts) and one card.
//
// Pinned here:
//  A. the shared parser (multi-line stamped form / ↪-prefixed copy / BOM /
//     single-line whitespace-collapsed inbox preview / non-collab negatives);
//  B. SteerCard collab cardification (names, id-free visible text, hover ids,
//     plain-steer bubble untouched);
//  C. 723 deferred-placeholder compatibility for collab bodies (collapsed
//     route summary without raw metadata, expand reveals the card).

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";

// TranscriptCards pulls transitive CSS imports; tsx has no asset loader, so
// redirect them to the shared stub the way Vite handles them.
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import {
  collabAsImSource,
  COLLAB_MSG_PREFIX,
} from "../lib/collabMessage";
import { STEER_NOTICE_PREFIX } from "../lib/useController";
import {
  rememberCollabContactNames,
  resetCollabContactNamesForTest,
  setCollabContactDirectory,
} from "../lib/collabContactNames";

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

// 仿真 contact_id：与后端 newContactID() 同构（collab-contact-names 同款）。
const FROM_ID = "sc_aaaa1111bbbb2222cccc3333";
const TO_ID = "sc_dddd4444eeee5555ffff6666";

// 后端 sessionCollabDeliveryText 的 stamping 形态：header (+hop) + 正文 +
// `---` 元数据尾（threadId / 回复方式）。
function stampedText(body: string): string {
  return [
    `${COLLAB_MSG_PREFIX} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1)`,
    "",
    body,
    "",
    "---",
    `会话线程：threadId=msg_01`,
    `回复方式：完成后用 talk_to_session 回信到 contact_id=${FROM_ID}，hop 传 2。`,
  ].join("\n");
}

console.log("\ntask758 steer collab card");

// ── A. 共享判定 collabAsImSource ─────────────────────────────────────────────
{
  const full = stampedText("帮我看下 758 的两张截图");
  const parsed = collabAsImSource(full);
  ok(parsed !== null && parsed.sender === FROM_ID && parsed.chat === TO_ID, "the stamped multi-line form parses (sender/chat extracted)");
  ok(parsed !== null && parsed.text.startsWith("帮我看下 758"), "the body after the header line is the card text");
  ok(parsed !== null && parsed.text.includes("threadId="), "the metadata tail stays in the parsed body (user-row card parity, zero display change)");

  const noticeCopy = `${STEER_NOTICE_PREFIX}${full}`;
  const fromNotice = collabAsImSource(noticeCopy);
  ok(fromNotice !== null && fromNotice.sender === FROM_ID && fromNotice.text === parsed?.text, "the ↪-prefixed notice copy parses identically (hardening)");

  const fromBom = collabAsImSource(`\uFEFF\u200B${full}`);
  ok(fromBom !== null && fromBom.sender === FROM_ID, "BOM/zero-width prefixed text parses identically (hardening)");

  // 收件箱 preview 重建形态：PreviewText 空白折叠 + 120 rune 截断 → 整条一行。
  const collapsedPreview = `${COLLAB_MSG_PREFIX} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1) 正文第一行被折叠进同一行…`;
  const fromPreview = collabAsImSource(collapsedPreview);
  ok(fromPreview !== null && fromPreview.sender === FROM_ID && fromPreview.chat === TO_ID, "the single-line whitespace-collapsed preview form parses");
  ok(fromPreview !== null && fromPreview.text === "正文第一行被折叠进同一行…", "the single-line form keeps the inline remainder as body (hop tail stripped)");
  ok(fromPreview !== null && !fromPreview.text.includes("hop="), "the hop tail never leaks into the card body");

  const plain = "先跑测试再改";
  ok(collabAsImSource(plain) === null, "plain steer text is not collab");
  ok(collabAsImSource(`${STEER_NOTICE_PREFIX}${plain}`) === null, "↪-prefixed plain steer text is not collab");
  ok(collabAsImSource(`${COLLAB_MSG_PREFIX} 来自 contact_id=only-one-side`) === null, "an incomplete header does not cardify");
}

// ── B/C. SteerCard 组件：卡片化 + 723 折叠占位兼容 ──────────────────────────
{
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  // 断言按 zh 文案写：强制 locale（detectLocale 读 navigator.language）。
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { SteerCard } = await import("../components/TranscriptCards");

  const rootEl = document.getElementById("root")!;
  // 每组建全新 root：useCollabContactNames 的快照在挂载时初始化，跨组复用
  // 同一实例会滞留上一组的名单（reset 不通知订阅者）。
  let host = document.createElement("div");
  let root = createRoot(host);
  const freshRoot = () => {
    host = document.createElement("div");
    rootEl.appendChild(host);
    root = createRoot(host);
  };

  const renderSteer = async (text: string, deferred: boolean) => {
    await act(async () => {
      root.render(<LocaleProvider><SteerCard id="s758" text={text} deferred={deferred} /></LocaleProvider>);
    });
  };

  const bodyText = (el: HTMLElement): string => {
    let out = "";
    for (const node of el.querySelectorAll("*")) {
      for (const child of node.childNodes) {
        if (child.nodeType === 3) out += child.textContent ?? "";
      }
    }
    return out;
  };

  const full = stampedText("帮我看下 758 的两张截图\n第二行只有展开才可见");

  // ── B1. 非 deferred + collab：卡片化，header 元数据不再裸露 ──
  resetCollabContactNamesForTest();
  setCollabContactDirectory({ async ListAddressableSessions() { return [{ contactId: FROM_ID, title: "fork-合并线" }, { contactId: TO_ID, title: "fork开发-新6" }]; } });
  act(() => { rememberCollabContactNames([{ contactId: FROM_ID, title: "fork-合并线" }, { contactId: TO_ID, title: "fork开发-新6" }]); });
  await renderSteer(`${STEER_NOTICE_PREFIX}${full}`, false);
  ok(host.querySelector(".im-source-card--steer") !== null, "a collab steer notice renders the im-source card (not the raw bubble)");
  ok(host.querySelector(".steer-line__bubble") === null, "no raw ↪ bubble for a collab body");
  ok((host.textContent ?? "").includes("跨会话消息"), "the card head shows the collab source label");
  ok((host.textContent ?? "").includes("fork-合并线") && (host.textContent ?? "").includes("fork开发-新6"), "both session names render on the card (roster-resolved)");
  const steerCard = host.querySelector(".im-source-card--steer") as HTMLElement;
  // id-free 断言只打 header 亮面（head + meta 脚注）：卡正文与 user row 卡
  // 完全一致 —— `---` 元数据尾仍在正文里（462 钉测同口径，两实体统一 =
  // 不改 user row 的呈现）。
  const chromeText = `${bodyText(steerCard.querySelector(".im-source-card__head") as HTMLElement)}\n${bodyText(steerCard.querySelector(".im-source-card__meta") as HTMLElement)}`;
  ok(!chromeText.includes(FROM_ID) && !chromeText.includes(TO_ID), "raw contact_ids stay out of the card head/route chrome");
  ok(bodyText(steerCard).includes("帮我看下 758"), "the body renders inside the card");
  const hover = steerCard.querySelector(".im-source-card__meta")?.getAttribute("title") ?? "";
  ok(hover.includes(`contact_id=${FROM_ID}`) && hover.includes(`contact_id=${TO_ID}`), "hover keeps the full contact_ids (same contract as the user-row card)");
  ok((host.textContent ?? "").includes("第二行只有展开才可见"), "the non-deferred card is not folded (same as the plain bubble it replaces)");

  // ── B2. 名单为空 → 降级截短 id（非空白） ──
  resetCollabContactNamesForTest();
  setCollabContactDirectory({ async ListAddressableSessions() { return []; } });
  freshRoot();
  await renderSteer(`${STEER_NOTICE_PREFIX}${full}`, false);
  const degraded = host.textContent ?? "";
  ok(degraded.includes("sc_aaaa1111b") && degraded.includes("…"), `an unlisted contact_id degrades to the truncated id on the steer card (${degraded.slice(0, 120).replace(/\n/g, "|")})`);

  // ── B3. 非 deferred + 普通 steer：原气泡零改动 ──
  resetCollabContactNamesForTest();
  freshRoot();
  await renderSteer(`${STEER_NOTICE_PREFIX}先跑测试再改`, false);
  ok(host.querySelector(".steer-line__bubble") !== null && host.querySelector(".im-source-card") === null, "a plain steer keeps the full ↪ bubble (no card)");

  // ── C. deferred + collab：折叠占位语义兼容 723 ──
  resetCollabContactNamesForTest();
  setCollabContactDirectory({ async ListAddressableSessions() { return [{ contactId: FROM_ID, title: "fork-合并线" }, { contactId: TO_ID, title: "fork开发-新6" }]; } });
  act(() => { rememberCollabContactNames([{ contactId: FROM_ID, title: "fork-合并线" }, { contactId: TO_ID, title: "fork开发-新6" }]); });
  freshRoot();
  await renderSteer(`${STEER_NOTICE_PREFIX}${full}`, true);
  let html = host.innerHTML;
  ok((host.querySelector(".steer-line__status")?.textContent ?? "").trim().length > 0, "collapsed placeholder shows the deferred status label");
  ok((host.textContent ?? "").includes("fork-合并线"), "the collapsed summary shows the resolved route, not the raw header");
  ok(!(host.textContent ?? "").includes(FROM_ID), "the collapsed summary carries no bare contact_id");
  ok(!(host.textContent ?? "").includes("第二行"), "the collapsed placeholder hides the body");
  ok(host.querySelector(".im-source-card") === null, "the card only appears after expanding (723 collapsed semantics)");
  ok(html.includes("aria-expanded=\"false\""), "collapsed state is exposed to assistive tech");
  const button = host.querySelector("button.steer-line__bubble--deferred") as HTMLButtonElement | null;
  ok(button !== null, "the placeholder is still the 723 toggle button");
  await act(async () => { button!.click(); });
  html = host.innerHTML;
  ok(html.includes("aria-expanded=\"true\""), "expanded state is exposed to assistive tech");
  ok(host.querySelector(".im-source-card--steer") !== null, "expanding reveals the collab card below the toggle");
  ok((host.textContent ?? "").includes("第二行只有展开才可见"), "the expanded card shows the full body");
  await act(async () => { button!.click(); });
  ok(host.querySelector(".im-source-card") === null, "collapsing again hides the card");
  ok(!(host.textContent ?? "").includes("第二行"), "collapsing again hides the body tail");

  // ── C2. deferred + 普通 steer：723 原行为（首行摘要/展开全文，非卡片） ──
  resetCollabContactNamesForTest();
  freshRoot();
  await renderSteer(`${STEER_NOTICE_PREFIX}看看 723 的截图\n第二行只有展开才可见`, true);
  ok((host.textContent ?? "").includes("看看 723 的截图") && !(host.textContent ?? "").includes("第二行"), "a plain deferred steer keeps the 723 first-line summary");
  const plainButton = host.querySelector("button.steer-line__bubble--deferred") as HTMLButtonElement;
  await act(async () => { plainButton.click(); });
  ok((host.textContent ?? "").includes("第二行") && host.querySelector(".im-source-card") === null, "a plain deferred steer expands to full text, never a card");

  await act(async () => { root.unmount(); });
  host.remove();
  setCollabContactDirectory(null);
  resetCollabContactNamesForTest();
  dom.window.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
