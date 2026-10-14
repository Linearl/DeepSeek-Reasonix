// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task811-collab-fold-unify.test.tsx
//
// 任务811: 跨会话消息自动折叠双症状——同一封投递消息此前按落点实体分裂成
// 三种行为（436 user row 限高 / 705 user row 卡折叠 / 758 steer 卡直渲无折叠），
// 且 866/1553 给 header 加的「，发送于 <时间>」尾巴会污染 758 的解析。
//
// 钉在这里：
//  A. 解析加固（真实三样本原文）：header 的「，发送于 <时间>」不再吞进 chat
//     id，单行 preview 分支剥掉 hop 尾与时间尾后正文不再是时间戳残片；
//  B. steer 卡折叠统一：与 user row 卡同一开关/阈值——开=长消息折叠+可展开，
//     关=全文直渲；723 deferred 展开不再二次折叠；
//  C. reconcile 气泡取全文：preview 气泡经 ReadInboxItem 升级为全文，读失败
//     降级保留 preview。

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

import { collabAsImSource } from "../lib/collabMessage";
import { hydrateQueuedGuidanceBubbles, missingQueuedGuidanceBubbles } from "../lib/inboxSurfaceReconcile";
import { setCollabAutoFold } from "../store/collabDisplay";
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

// ── 真实样本原文（逐字取自 jsonl，只把 contact_id 换成仿真值的版本另建于下方）──
const FROM_ID = "sc_aaaa1111bbbb2222cccc3333";
const TO_ID = "sc_dddd4444eeee5555ffff6666";

// 样本1: 20261012 审计回执（threadId=msg_5f1512238b685c85，旧投递无时间尾）。
// 正文同样按真实样本长度撑过 705 阈值（8 估算行）。
const AUDIT_BODY_LINES = Array.from(
  { length: 10 },
  (_, i) => `② P2 项 ${i + 1}：命名统一采纳，v5 表述软化，残留 grep 清零，notes 修正已 commit。`,
);
const SAMPLE_AUDIT = [
  `${"[跨会话消息]"} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1)`,
  "",
  "【宣发两帖审查回执】20261012（只审不改，产出见下）",
  "结论：未发现超卖、未发现 24 版混入；P1 三处「1s」残留必须改。",
  ...AUDIT_BODY_LINES,
  "",
  "---",
  "会话线程：threadId=msg_5f1512238b685c85",
].join("\n");

// 样本2: 今日转线回执（threadId=msg_b3a2abd01930a490，hop=1 + 发送于尾）。
// 正文长度按真实样本（~1400 字）撑到 705 阈值（8 估算行）之上。
const RECEIPT_BODY_LINES = Array.from(
  { length: 12 },
  (_, i) => `- 第 ${i + 1} 项：800s 剩余清单与低峰批规划已提取，今晚低峰批先行 882+874 双 P1。`,
);
const SAMPLE_RECEIPT = [
  `${"[跨会话消息]"} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1)，发送于 2026-10-14T13:21:00+08:00 Wed`,
  "",
  "【转线完成回执 · fork开发-新7 → 需求与规划线】",
  "五步转线清单已全部执行完毕：",
  ...RECEIPT_BODY_LINES,
  "换线完成，请按新 contact 对接。",
  "",
  "---",
  "会话线程：threadId=msg_b3a2abd01930a490",
].join("\n");

// 样本3: 症状2·换线通知（msg_c1351f06c5c90c08，hop 后缀缺失 + 发送于尾）。
const NOTIFICATION_BODY_LINES = Array.from(
  { length: 10 },
  (_, i) => `- 换线要点 ${i + 1}：contact 引用全部换新，prompt 顶部过期段已替换为 20261014 换线简报。`,
);
const SAMPLE_NOTIFICATION = [
  `${"[跨会话消息]"} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID}，发送于 2026-10-14T13:18:00+08:00 Wed`,
  "",
  "【换线通知 · fork开发主对话 → 新7 · 心跳指向将变更】",
  "fork 开发主对话已完成换线，继任者 = fork开发-新7，后续唤醒/报告均发我。",
  ...NOTIFICATION_BODY_LINES,
].join("\n");

// 症状2 的实际行文本 = PreviewText 单行折叠 + 120 rune 截断形态。120 截断点
// 实测落在 `…T13:18:00+08…`（与 20261014 截图逐字吻合）。
const SAMPLE_NOTIFICATION_PREVIEW =
  `${"[跨会话消息]"} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID}，发送于 2026-10-14T13:18:00+08…`;

console.log("\ntask811 collab fold unify");

// ── A. 解析加固 ────────────────────────────────────────────────────────────
{
  const audit = collabAsImSource(SAMPLE_AUDIT);
  ok(audit !== null && audit.sender === FROM_ID && audit.chat === TO_ID, "样本1(审计回执) sender/chat 解析干净");
  ok(audit !== null && audit.text.startsWith("【宣发两帖审查回执】"), "样本1 正文从回执标题开始");

  const receipt = collabAsImSource(SAMPLE_RECEIPT);
  ok(receipt !== null && receipt.chat === TO_ID, "样本2(转线回执) chat 不吞「，发送于」");
  ok(receipt !== null && !receipt.chat.includes("发送于"), "样本2 chat 不含发送于尾巴");
  ok(receipt !== null && receipt.text.startsWith("【转线完成回执"), "样本2 正文从回执标题开始（时间尾留在 header）");
  ok(receipt !== null && !receipt.text.includes("2026-10-14T13:21:00"), "样本2 正文不含时间戳残片");

  const notification = collabAsImSource(SAMPLE_NOTIFICATION);
  ok(notification !== null && notification.chat === TO_ID, "样本3(换线通知, hop=0) chat 不吞「，发送于」");
  ok(notification !== null && notification.text.startsWith("【换线通知"), "样本3 正文从通知标题开始");

  // 症状2 的单行 preview 形态：修复前 body = `2026-10-14T13:18:00+08…`（时间戳
  // 残片），meta chat 污染成 `…，发送于`——即截图「三行无入口」的成因。
  const preview = collabAsImSource(SAMPLE_NOTIFICATION_PREVIEW);
  ok(preview !== null && preview.chat === TO_ID, "症状2 单行 preview: chat 解析干净");
  ok(preview !== null && !preview.text.includes("2026-10-14"), "症状2 单行 preview: body 不再是时间戳残片");
  ok(preview !== null && preview.text === "", "症状2 单行 preview: 时间尾剥净后正文为空（全文由 C 的气泡升级提供）");

  // 回归: 758 的旧形态不受影响。
  const collapsed = `${"[跨会话消息]"} 来自 contact_id=${FROM_ID} → 发至 contact_id=${TO_ID} (hop=1) 正文第一行被折叠进同一行…`;
  const fromPreview = collabAsImSource(collapsed);
  ok(fromPreview !== null && fromPreview.text === "正文第一行被折叠进同一行…", "758 回归: 单行 preview 正文保留（无发送于尾时不误剥）");
  const plain = collabAsImSource("先跑测试再改");
  ok(plain === null, "758 回归: 普通 steer 仍不卡片化");
  ok(collabAsImSource(`${"[跨会话消息]"} 来自 contact_id=only-one-side`) === null, "758 回归: 残缺 header 仍不卡片化");
}

// ── B/C 需要的公共装置 ─────────────────────────────────────────────────────
async function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "zh-CN" });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const bodyText = (el: Element | null): string => {
  if (!el) return "";
  let out = "";
  for (const node of el.querySelectorAll("*")) {
    for (const child of node.childNodes) {
      if (child.nodeType === 3) out += child.textContent ?? "";
    }
  }
  return out;
};

async function main() {
  const dom = await installDom();
  const { LocaleProvider, preloadLocale } = await import("../lib/i18n");
  await preloadLocale("zh");
  const { SteerCard } = await import("../components/TranscriptCards");

  const rootEl = document.getElementById("root")!;
  let host = document.createElement("div");
  let root = createRoot(host);
  // 与 task758 同款：每组建全新 root（不 unmount 旧的——在途 act 与 unmount
  // 冲突会炸 "Cannot update an unmounted root"）。
  const freshRoot = () => {
    host = document.createElement("div");
    rootEl.appendChild(host);
    root = createRoot(host);
  };
  // zustand store 切换会通知已挂载的卡片，必须包进 act。
  const setFold = async (enabled: boolean | undefined) => {
    await act(async () => { setCollabAutoFold(enabled); });
  };
  const renderSteer = async (text: string, deferred: boolean) => {
    await act(async () => {
      const tree: React.ReactElement = (
        <LocaleProvider>
          <SteerCard id="s811" text={text} deferred={deferred} />
        </LocaleProvider>
      );
      root.render(tree);
    });
  };

  resetCollabContactNamesForTest();
  setCollabContactDirectory({ async ListAddressableSessions() { return []; } });

  // ── B. steer 卡折叠统一（两个真实长样本 + 开关两态）────────────────────
  await setFold(true);

  // 样本2 转线回执：开关开 → 折叠成摘要条，正文不可见但可展开。
  await renderSteer(`↪ ${SAMPLE_RECEIPT}`, false);
  ok(host.querySelector(".im-source-card--steer") !== null, "B1 转线回执渲染为 steer 卡");
  ok(host.querySelector(".im-source-card__text--folded") !== null, "B1 开关开: 转线回执折叠为摘要条");
  ok(!bodyText(host.querySelector(".im-source-card__text--folded")).includes("五步转线清单"), "B1 折叠态正文不可见");
  ok((host.textContent ?? "").includes("msg_b3a2abd01930a490") === false, "B1 折叠态 threadId 尾注不裸露");
  const expandBtn = host.querySelector<HTMLButtonElement>(".im-source-card__fold-toggle");
  ok(expandBtn !== null && expandBtn.getAttribute("aria-expanded") === "false", "B1 展开按钮存在且初始收起");
  await act(async () => { expandBtn?.click(); });
  ok((host.textContent ?? "").includes("五步转线清单"), "B1 展开后全文可见");
  ok(host.querySelector(".im-source-card__fold-toggle")?.getAttribute("aria-expanded") === "true", "B1 展开后有收起按钮");
  await act(async () => { host.querySelector<HTMLButtonElement>(".im-source-card__fold-toggle")?.click(); });
  ok(!(host.textContent ?? "").includes("五步转线清单"), "B1 再次收起隐藏正文");

  // 样本1 审计回执（旧投递，无时间尾）：同样折叠。
  freshRoot();
  await renderSteer(`↪ ${SAMPLE_AUDIT}`, false);
  ok(host.querySelector(".im-source-card__text--folded") !== null, "B2 审计回执（旧 header 形态）同样折叠");

  // 症状2 的 preview 气泡升级为全文后（C 的产物）也走同一折叠门。
  freshRoot();
  await renderSteer(`↪ ${SAMPLE_NOTIFICATION}`, false);
  ok(host.querySelector(".im-source-card__text--folded") !== null, "B3 换线通知全文同样折叠");

  // 开关关 = 现状全量展示（705 铁律 2）。
  await setFold(undefined);
  freshRoot();
  await renderSteer(`↪ ${SAMPLE_RECEIPT}`, false);
  ok(host.querySelector(".im-source-card__text--folded") === null, "B4 开关关: 不折叠");
  ok((host.textContent ?? "").includes("五步转线清单"), "B4 开关关: 全文直渲");
  ok(host.querySelectorAll(".im-source-card__fold-toggle").length === 0, "B4 开关关: 无折叠控件");

  // 723 deferred 占位：展开后的卡片不再二次折叠（用户已显式点开）。
  await setFold(true);
  freshRoot();
  await renderSteer(`↪ ${SAMPLE_RECEIPT}`, true);
  ok(host.querySelector(".im-source-card--steer") === null, "B5 deferred 占位初始收起（723 语义不变）");
  const deferredBtn = host.querySelector<HTMLButtonElement>("button.steer-line__bubble--deferred");
  ok(deferredBtn !== null, "B5 占位仍是 723 切换按钮");
  await act(async () => { deferredBtn?.click(); });
  ok(host.querySelector(".im-source-card--steer") !== null, "B5 展开揭示卡片");
  ok((host.textContent ?? "").includes("五步转线清单"), "B5 展开即全文（不再二次折叠）");

  // ── C. reconcile 气泡取全文 ────────────────────────────────────────────
  const snapshot = {
    items: [
      { id: "q811", state: "queued", intent: "steer", preview: SAMPLE_NOTIFICATION_PREVIEW },
      { id: "q-dead", state: "queued", intent: "steer", preview: SAMPLE_NOTIFICATION_PREVIEW },
    ],
  };
  const previewBubbles = missingQueuedGuidanceBubbles(snapshot, []);
  ok(previewBubbles.length === 2 && previewBubbles[0].text.includes("2026-10-14T13:18:00+08"), "C1 preview 气泡携带120-rune 截断文本（症状2 输入形态）");

  const hydrated = await hydrateQueuedGuidanceBubbles(previewBubbles, async (id) =>
    id === "q811" ? { displayText: SAMPLE_NOTIFICATION } : undefined,
  );
  ok(hydrated[0].text.includes("【换线通知") && hydrated[0].text.includes("心跳指向将变更"), "C2 读到全文 → 气泡升级为全文");
  ok(collabAsImSource(hydrated[0].text)?.text.startsWith("【换线通知") === true, "C2 升级后的气泡卡片化出完整正文");
  ok(!hydrated[0].text.includes("2026-10-14T13:18:00+08…"), "C2 升级后不含截断时间残片");
  ok(hydrated[1].text === previewBubbles[1].text, "C3 读失败/无全文 → 降级保留 preview 气泡（不吞消息）");

  const throwing = await hydrateQueuedGuidanceBubbles(previewBubbles, async () => { throw new Error("gone"); });
  ok(throwing[1].text === previewBubbles[1].text, "C4 ReadInboxItem 抛错同样降级");

  await act(async () => { root.unmount(); });
  host.remove();
  setCollabContactDirectory(null);
  resetCollabContactNamesForTest();
  await setFold(undefined);
  dom.window.close();
}

void main().then(() => {
  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
});
