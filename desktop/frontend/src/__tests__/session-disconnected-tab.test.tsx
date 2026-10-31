// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/session-disconnected-tab.test.tsx
// tl 1266 — 装机重启后会话 runtime 未重站：tab 挂 running 残影但 contact
// directory 查无实体。后端 tabMeta 已按「实体查无」投影 session_missing
// issue 并清掉 running 残影；本套钉前端可见态接线：
//   ① TabBar 按 issue.code==="session_missing" 渲染「已断开」徽章，
//      压过「未加载」，且 hover title 带同一文案；
//   ② types 的 issue code 联合类型收录 session_missing；
//   ③ 三语 locale 都有 tabBar.disconnected；
//   ④ 徽章样式（--err soft-mix）落在 mode-classic.css 的徽章族。

import { readFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, "..");

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

const tabBar = readFileSync(resolve(src, "components/TabBar.tsx"), "utf8");
const types = readFileSync(resolve(src, "lib/types.ts"), "utf8");
const zh = readFileSync(resolve(src, "locales/zh.ts"), "utf8");
const en = readFileSync(resolve(src, "locales/en.ts"), "utf8");
const zhTW = readFileSync(resolve(src, "locales/zh-TW.ts"), "utf8");
const classic = readFileSync(resolve(src, "styles/mode-classic.css"), "utf8");

console.log("\ntl 1266 session-disconnected tab strip wiring");

// ① TabBar badge + hover title + precedence over 未加载
ok(
  tabBar.includes('tab.runtime?.issue?.code === "session_missing"'),
  "TabBar derives sessionDisconnected from the session_missing issue code",
);
ok(
  tabBar.includes('t("tabBar.disconnected")'),
  "TabBar renders the localized disconnected label",
);
ok(
  // Disconnected wins over notLoaded in the same slot: the notLoaded badge
  // must be gated off when the session is missing (one badge per slot).
  /notLoaded && !sessionMissing && compressTier < 3/.test(tabBar.replace(/\r/g, "")),
  "disconnected badge takes precedence over the notLoaded badge in the same slot",
);
ok(
  tabBar.includes("tabbar__mode-badge--session-missing"),
  "TabBar uses the dedicated disconnected badge class",
);

// ② TS contract
ok(
  types.includes('"session_lease_held" | "startup_failed" | "session_missing"'),
  "SessionRuntimeIssue code union includes session_missing",
);

// ③ Locales (three languages, one label)
ok(zh.includes('"tabBar.disconnected": "已断开"'), "zh locale has tabBar.disconnected");
ok(en.includes('"tabBar.disconnected": "Disconnected"'), "en locale has tabBar.disconnected");
ok(zhTW.includes('"tabBar.disconnected": "已斷線"'), "zh-TW locale has tabBar.disconnected");

// ④ Badge style on the --err soft-mix recipe, no new theme token
ok(
  classic.includes(".tabbar__mode-badge--session-missing"),
  "mode-classic.css styles the disconnected badge",
);
ok(
  /tabbar__mode-badge--session-missing[\s\S]*?var\(--err\)/.test(classic),
  "disconnected badge reuses the shared --err token (no new theme token)",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
