// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1798-motion-tokens.test.ts
// tl1798 — 动效参数统一回归钉（20261124 拍板件）。
//
// 职责（上限+新增即红）：
//   ① token 层单点：动效三档只定义在 styles.css :root（+graphite 主题覆盖），
//      值钉死 120/200/300ms；hover 增强 token（lift/shadow/dim）定义存在；
//   ② token 被引用计数下限：引用被静默清空（= 收拢白做）即红；
//   ③ 散落时长字面量上限：transition 内 0.12-0.18s/0.28s 类字面量不得新增；
//   ④ 菜单延迟单源：MENU_HOVER_OPEN/CLOSE_DELAY_MS 只在 lib/motion.ts 定义
//      （拍板统一 300ms），组件私有 `*_HOVER_*_MS = 数字` 一律红（登记例外除外）。
//
// non-goals：不断言具体选择器（那是 Codemod 交付快照的事），只守结构不变式。

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { MENU_HOVER_CLOSE_DELAY_MS, MENU_HOVER_OPEN_DELAY_MS } from "../lib/motion";

const here = dirname(fileURLToPath(import.meta.url));
const srcRoot = join(here, "..");

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) {
    passed += 1;
    console.log(`  PASS  ${label}`);
  } else {
    failed += 1;
    console.error(`  FAIL  ${label}`);
  }
}

const styles = readFileSync(join(srcRoot, "styles.css"), "utf8");

// ── ① token 层单点 + 拍板值 ──────────────────────────────────────────────
console.log("\ntl1798 ①: token 层定义（:root 单点 + 拍板 120/200/300）");
{
  const defCount = (name: string) => (styles.match(new RegExp(`${name}\\s*:`, "g")) ?? []).length;
  ok(/--dur-fast:\s*120ms/.test(styles) && defCount("--dur-fast") === 2, ":root --dur-fast = 120ms（:root+graphite 两处定义档）");
  ok(/--dur-normal:\s*200ms/.test(styles) && defCount("--dur-normal") === 2, ":root --dur-normal = 200ms");
  ok(/--dur-slow:\s*300ms/.test(styles) && defCount("--dur-slow") === 2, ":root --dur-slow = 300ms");
  ok(/--hover-lift:\s*2px/.test(styles), ":root --hover-lift = 2px");
  ok(/--hover-shadow:/.test(styles), ":root --hover-shadow defined");
  ok(/--hover-dim:\s*8%/.test(styles), ":root --hover-dim = 8%");
  ok(!/--dur-base/.test(styles), "retired name --dur-base fully gone（防两套档名并存）");
  // graphite 主题覆盖段：档名同步（值保留主题个性）
  ok(/--dur-normal:/.test(styles.slice(styles.indexOf("data-theme-style=\"graphite\""))), "graphite override carries --dur-normal (renamed)");
}

// ── ② 引用计数下限（当前基线取整；清空即红） ─────────────────────────────
console.log("\ntl1798 ②: token 引用计数下限");
{
  const count = (re: RegExp) => (styles.match(re) ?? []).length;
  const fastRefs = count(/var\(--dur-fast\)/g);
  const normalRefs = count(/var\(--dur-normal\)/g);
  const slowRefs = count(/var\(--dur-slow\)/g);
  const easeRefs = count(/var\(--ease-out\)/g);
  ok(fastRefs >= 150, `var(--dur-fast) refs = ${fastRefs} >= 150`);
  ok(normalRefs >= 25, `var(--dur-normal) refs = ${normalRefs} >= 25`);
  ok(slowRefs >= 8, `var(--dur-slow) refs = ${slowRefs} >= 8`);
  ok(easeRefs >= 50, `var(--ease-out) refs = ${easeRefs} >= 50`);
  ok(count(/var\(--hover-lift\)/g) >= 2, `var(--hover-lift) refs = ${count(/var\(--hover-lift\)/g)} >= 2`);
  ok(count(/var\(--hover-shadow\)/g) >= 2, `var(--hover-shadow) refs = ${count(/var\(--hover-shadow\)/g)} >= 2`);
}

// ── ③ 散落 transition 时长字面量上限（遗留登记，新增即红）─────────────────
console.log("\ntl1798 ③: transition 时长字面量上限（遗留登记，新增即红）");
{
  // 基线口径：styles.css 全文件 transition 声明中的拍板六档及其相邻散值字面量
  // （0.1s-0.16s/0.18s/0.2s/0.28s）。20261125 收拢后遗留 75 处 = other/装饰性
  // hover + 非 hover 关联的布局/进出场过渡；上限=75，任何新增必须走 token。
  const literalRe = /transition[^;{}]*?\b0\.(1[0-6]|18|2|28)s?\b/g;
  const n = (styles.match(literalRe) ?? []).length;
  ok(n <= 75, `transition literal durations = ${n} <= 75 (tl1798 基线遗留；新增字面量必须走 token)`);
}

// ── ④ 菜单延迟单源 ────────────────────────────────────────────────────────
console.log("\ntl1798 ④: 菜单 hover 延迟单源（拍板统一 300ms）");
{
  ok(MENU_HOVER_OPEN_DELAY_MS === 300, "motion.MENU_HOVER_OPEN_DELAY_MS === 300");
  ok(MENU_HOVER_CLOSE_DELAY_MS === 300, "motion.MENU_HOVER_CLOSE_DELAY_MS === 300");
  ok(MENU_HOVER_OPEN_DELAY_MS === MENU_HOVER_CLOSE_DELAY_MS, "open/close 同值（拍板统一 300ms）");

  // 全 src 扫描：禁止组件私有 `*_HOVER_*_MS = 数字` / `*_HOVER_*_MS = 350` 类定义回潮。
  // 登记例外（非菜单域，各有独立语义，tl1798 核对表披露）：
  //   SIDEBAR_HOVER_EXPAND_DELAY_MS / SIDEBAR_HOVER_COLLAPSE_DELAY_MS（侧栏导航，非菜单）
  //   HOVER_CARD_DELAY_MS（TabBar 悬停卡 tl1797，散点遗留待拍板）
  //   HOVER_PREFETCH_DEBOUNCE_MS（TabBar hover 预取防抖，非弹层延迟）
  const allowedNames = new Set([
    "SIDEBAR_HOVER_EXPAND_DELAY_MS",
    "SIDEBAR_HOVER_COLLAPSE_DELAY_MS",
    "HOVER_CARD_DELAY_MS",
    "HOVER_PREFETCH_DEBOUNCE_MS",
    "MENU_HOVER_OPEN_DELAY_MS",
    "MENU_HOVER_CLOSE_DELAY_MS",
  ]);
  const offenders: string[] = [];
  const { readdirSync, statSync } = await import("node:fs");
  function walk(d: string) {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      const p = join(d, e.name);
      if (e.isDirectory()) {
        if (e.name === "__tests__" || e.name === "node_modules") continue;
        walk(p);
      } else if (/\.(tsx|ts)$/.test(e.name)) {
        const rel = p.slice(srcRoot.length + 1).split("\\").join("/");
        if (rel === "lib/motion.ts") continue; // 唯一定义点
        const text = readFileSync(p, "utf8");
        for (const m of text.matchAll(/(?:export\s+)?(?:const|let)\s+([A-Za-z0-9_]*HOVER[A-Za-z0-9_]*_(?:MS|DELAY_MS))\s*=\s*(?!MENU_HOVER)(\d+)/g)) {
          if (!allowedNames.has(m[1])) offenders.push(`${rel}: ${m[1]} = ${m[2]}`);
        }
      }
    }
  }
  walk(srcRoot);
  ok(offenders.length === 0, `no private *_HOVER_*_MS constants outside lib/motion.ts${offenders.length ? " -> " + offenders.join("; ") : ""}`);
}

console.log(`\ntl1798-motion-tokens: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
