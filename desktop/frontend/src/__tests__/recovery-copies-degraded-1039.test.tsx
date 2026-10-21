// 任务 1039 harness：recovery 副本面板两步（展开会话版本列表 / 点「查看并
// 合并会话副本」）在后端重放被拒时的降级姿态。
//
// 事故形态（20261021 GC开发专员条目）：49MB v3 events 日志重放被拒
// （SessionReplayLimitError），错误一路传到前端，弹窗只剩一句「设置加载
// 失败。重试」——用户与日志都无从判断是哪条链路断了。
//
// 本 harness 钉三点：
//  ① ListRecoveryChains 返回降级行（Degraded=true，计数全零）时，弹窗仍
//     渲染出该会话行，而不是整页失败；
//  ② ListRecoveryCopyGroups reject 时，横幅必须带上后端原因（不能只有
//     「设置加载失败」死话）；
//  ③ 降级行不被推荐为最长链（longestPath 空 ⇒ 前端不标「推荐」）。

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: "en-US" });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;

const { RecoveryCopiesSection } = await import("../components/RecoveryCopiesSection");
const { LocaleProvider } = await import("../lib/i18n");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
}

function installDom() {
  document.getElementById("root")?.remove();
  const root = document.createElement("div");
  root.id = "root";
  document.body.appendChild(root);
  return root;
}

async function flush() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

async function renderSection() {
  const rootEl = installDom();
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(<LocaleProvider><RecoveryCopiesSection /></LocaleProvider>);
  });
  await flush();
  return { root, rootEl };
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button"))
    .find((b) => b.textContent?.trim().includes(text));
}

console.log("\nrecovery copies degraded + failure reason (1039)");

// ① 降级行：ListRecoveryCopyGroups 返回一个会话组，ListRecoveryChains 返回
//    一条 Degraded 行（计数全零、longestPath 空）——弹窗必须渲染出该行，
//    而不是整页失败。
{
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: async () => [{
          mainPath: "/sessions/gc.jsonl",
          mainLabel: "GC开发专员",
          directory: "/sessions",
          mainExists: true,
          mainMessages: 0,
          mainBytes: 49_210_175,
          mainModified: "2026-10-21T00:33:00Z",
          copies: [],
        }],
        ListRecoveryChains: async () => ({
          mainPath: "/sessions/gc.jsonl",
          mainLabel: "GC开发专员",
          chains: [{
            path: "/sessions/gc.jsonl",
            headId: "",
            label: "GC开发专员",
            isMain: true,
            selected: false,
            covered: false,
            messageCount: 0,
            turns: 0,
            lastActivity: "",
            bytes: 49_210_175,
            preview: "",
            degraded: true,
          }],
          longestPath: "",
          longestHead: "",
        }),
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const { root } = await renderSection();
  const openBtn = buttonByText("Review and merge");
  ok(Boolean(openBtn), "① panel renders the open button");
  await act(async () => { openBtn!.click(); });
  await flush();

  const body = document.body.textContent || "";
  ok(body.includes("GC开发专员"), "① degraded conversation row is listed, not an error banner");
  ok(!body.includes("Settings could not be loaded"), "① no full-page load-failed banner for a degraded row");
  ok(body.includes("Recommended") === false, "① degraded row is never recommended as longest");
  await act(async () => root.unmount());
}

// ② 失败原因：ListRecoveryCopyGroups reject——横幅必须带后端原因。
{
  window.go = {
    main: {
      App: {
        ListRecoveryCopyGroups: () => Promise.reject(
          new Error("session history exceeds safe replay limits: encoded_bytes=143498261, limit=143498260"),
        ),
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const { root } = await renderSection();
  const openBtn = buttonByText("Review and merge");
  ok(Boolean(openBtn), "② panel renders the open button");
  await act(async () => { openBtn!.click(); });
  await flush();

  const body = document.body.textContent || "";
  ok(body.includes("Settings could not be loaded"), "② failure banner still shows the generic message");
  ok(body.includes("encoded_bytes=143498261"), "② failure banner surfaces the backend reason (not a dead generic line)");
  await act(async () => root.unmount());
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
