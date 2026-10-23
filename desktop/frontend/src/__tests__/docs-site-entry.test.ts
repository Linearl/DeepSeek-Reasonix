// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/docs-site-entry.test.ts
// 任务 1102 验收（命令面板「产品文档」→ 静态站跳转）：
//  ① URL 形状：docsSite.DOCS_SITE_BASE_URL 是 https 开头、以 / 结尾的绝对地址，
//     且当前为 Pages 启用前的占位（指向仓库 docs-site 目录）——回填改常量后
//     这里同步改断言，防止「以为回填了其实没有」；
//  ② 打开动作：openProductDocs() 走 Wails 的 BrowserOpenURL（系统默认浏览器，
//     不在 webview 内导航），参数恰为常量本身（mock 断言调用参数）；
//  ③ 非阻塞：openProductDocs 同步返回（无 Promise、无 await 面），满足
//     「跳转调用不阻塞」要求；
//  ④ 快捷键与命令面板接线锚点：SHORTCUT_DEFINITIONS 注册了 help.docs.open
//     （F1）；App.tsx 里命令面板存在「产品文档」项并调用 openProductDocs。

import assert from "node:assert/strict";

import { DOCS_SITE_BASE_URL, openProductDocs } from "../lib/docsSite";
import { SHORTCUT_DEFINITIONS } from "../lib/keyboardShortcuts";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntask 1102 product-docs entry");

// ① URL 形状 + 占位语义。
ok(DOCS_SITE_BASE_URL.startsWith("https://"), "docs base URL is https");
ok(DOCS_SITE_BASE_URL.endsWith("/"), "docs base URL ends with a slash");
ok(
  DOCS_SITE_BASE_URL.includes("docs-site"),
  "docs base URL still points at the docs-site placeholder (update this assert when the Pages URL is backfilled)",
);

// ② 打开动作：window.runtime.BrowserOpenURL 恰好收到常量本身。
{
  const g = globalThis as { window?: unknown };
  const calls: string[] = [];
  const fakeWindow = {
    runtime: { BrowserOpenURL: (url: string) => { calls.push(url); } },
  };
  g.window = fakeWindow;
  try {
    openProductDocs();
    ok(calls.length === 1, "openProductDocs opens the system browser exactly once");
    ok(calls[0] === DOCS_SITE_BASE_URL, "openProductDocs passes the docs base URL verbatim");
    ok(
      typeof (fakeWindow as { open?: unknown }).open === "undefined",
      "openProductDocs does not navigate the webview (no window.open fallback used when runtime exists)",
    );
  } finally {
    delete g.window;
  }
}

// ②′ 无 runtime 时的浏览器开发桩：退回 window.open（bridge 既有语义）。
{
  const g = globalThis as { window?: unknown };
  const calls: string[] = [];
  g.window = { open: (url: string) => { calls.push(url); } };
  try {
    openProductDocs();
    ok(calls.length === 1 && calls[0] === DOCS_SITE_BASE_URL, "browser dev mock falls back to window.open with the same URL");
  } finally {
    delete g.window;
  }
}

// ③ 非阻塞：返回 void（不是 Promise），不存在 await 面。
ok(openProductDocs.length === 0, "openProductDocs takes no arguments");
ok(openProductDocs() === undefined, "openProductDocs returns void synchronously (fire-and-forget)");

// ④ 快捷键注册锚点：help.docs.open（F1）存在于 SHORTCUT_DEFINITIONS。
{
  const def = SHORTCUT_DEFINITIONS.find((d) => d.action === "help.docs.open");
  ok(def !== undefined, "help.docs.open is registered in SHORTCUT_DEFINITIONS");
  ok(def?.section === "help", "help.docs.open lives in the help section");
  const f1 = def?.defaults;
  ok(
    Boolean(f1 && f1.darwin.key === "F1" && f1.windows.key === "F1" && f1.linux.key === "F1"),
    "help.docs.open defaults to F1 on every platform",
  );
  ok(
    Boolean(f1 && !f1.darwin.ctrl && !f1.darwin.meta && !f1.darwin.alt && !f1.darwin.shift),
    "help.docs.open is a bare F1 (no modifier)",
  );
  // F1 不与其他已注册动作冲突（源码级断言：除 help.docs.open 外无第二处 F1）。
  const f1Owners = SHORTCUT_DEFINITIONS.filter((d) =>
    Object.values(d.defaults).some((combo) => combo.key.toUpperCase() === "F1"),
  );
  ok(f1Owners.length === 1 && f1Owners[0].action === "help.docs.open", "F1 is claimed by exactly one shortcut definition");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
