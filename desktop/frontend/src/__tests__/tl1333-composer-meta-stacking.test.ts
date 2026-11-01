// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1333-composer-meta-stacking.test.ts
//
// tl1333 回归钉（CSS 契约层）：用户定高 composer 在窄宽下工具栏 flex-wrap 换行时，
// textarea（.composer__content 定位层）会绘制在非定位的 .composer-meta 之上，透明
// 输入层盖住首行图标（elementFromPoint 命中 textarea、cursor:text，点击失效）。
// 修复 = .composer-meta 自身提为定位层并以 --z-local-raised 压回绘制顺序之上。
// 本钉锁两条声明与所引用的 z-index token 必须同时存在——防止未来合并像 tl1191
// 那样把规则静默丢进单一布局文件（真实浏览器层断言见 bench/composer-toolbar-wrap-hit.mjs）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");

let passed = 0;
let failed = 0;

function check(ok: boolean, label: string, detail = "") {
  if (ok) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}${detail ? `: ${detail}` : ""}\n`);
    failed += 1;
  }
}

// 抽出选择器含 .composer-meta 的顶层声明块（含修复注释的那一块）。
function composerMetaBlocks(): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(".composer-meta")) blocks.push(match[2]);
  }
  return blocks;
}

const metaBlocks = composerMetaBlocks();
const stackingBlock = metaBlocks.find(
  (body) => body.includes("position: relative") && body.includes("z-index: var(--z-local-raised)"),
);

check(metaBlocks.length > 0, "styles.css declares a .composer-meta rule");
check(
  stackingBlock !== undefined,
  ".composer-meta stacks above the positioned composer input layer (tl1333)",
  "expect position: relative + z-index: var(--z-local-raised) on .composer-meta",
);
check(
  /--z-local-raised:\s*\d+/.test(styles),
  "the referenced --z-local-raised token is defined",
);
// token 合规由 scripts/check-z-index-tokens.mjs 门禁统一执行，此处不重复。

process.stdout.write(`\ntl1333-composer-meta-stacking: ${passed} passed, ${failed} failed\n`);
process.exit(failed === 0 ? 0 : 1);
