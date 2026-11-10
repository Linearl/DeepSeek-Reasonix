// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/composer-narrow-overflow-css.test.ts
//
// tl1508b 修②④：输入卡底部窄态文字溢出清零钉（包 87 复验截图 232643）。
// 嫌疑三类（任务指定）：状态行读数 / 提示语 / 计数徽标。共同缺陷 = nowrap 或
// nowrap span 缺 min-width:0 + ellipsis——flex 里 min-width:auto 的 nowrap 项
// 拒不收缩，窄态只能被 .composer-meta overflow:hidden 中途截断（半字形）。
// jsdom 无布局（scrollWidth===clientWidth===0 恒真），行为断言的等价物 = CSS
// 契约钉（composer-resized-input-css 套件同法）：溢出清零由「这些声明在位」
// 承载，任何一条被回删即红。check:css 三件负责语法/层/theme-token 面。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function matchingBlocks(selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(styles)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

function finalDeclaration(selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const block of matchingBlocks(selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      value = match[1].trim();
    }
  }
  return value;
}

console.log("\ncomposer narrow overflow css (tl1508b 修②④)");

// 状态行读数（同排一行形态下必须可收缩截断，不顶开状态词）。
eq(finalDeclaration(".composer-run-strip__metrics", "min-width"), "0", "run-strip metrics can shrink below content");
eq(finalDeclaration(".composer-run-strip__metrics", "overflow"), "hidden", "run-strip metrics clips instead of spilling");
eq(finalDeclaration(".composer-run-strip__metrics", "text-overflow"), "ellipsis", "run-strip metrics ellipsizes");
eq(finalDeclaration(".composer-run-strip__tally", "min-width"), "0", "run-strip tally can shrink below content");
eq(finalDeclaration(".composer-run-strip__tally", "overflow"), "hidden", "run-strip tally clips instead of spilling");
eq(finalDeclaration(".composer-run-strip__tally", "text-overflow"), "ellipsis", "run-strip tally ellipsizes");

// 提示语①：composer__prompt（输入框下方瞬时提示）。
eq(finalDeclaration(".composer__prompt", "max-width"), "100%", "composer prompt stays inside its box");
eq(finalDeclaration(".composer__prompt", "overflow"), "hidden", "composer prompt clips instead of spilling");
eq(finalDeclaration(".composer__prompt", "text-overflow"), "ellipsis", "composer prompt ellipsizes");

// 提示语②：toolbar-send__hint（只读/不可发送说明）+ 宿主可收缩。
eq(finalDeclaration(".composer-toolbar-send", "flex-shrink"), "1", "send toolbar may shrink so its hint can clamp");
eq(finalDeclaration(".composer-toolbar-send", "min-width"), "0", "send toolbar can shrink below content");
eq(finalDeclaration(".composer-toolbar-send__hint", "min-width"), "0", "send hint can shrink below content");
eq(finalDeclaration(".composer-toolbar-send__hint", "max-width"), "100%", "send hint clamps to the toolbar width (column mode too)");
eq(finalDeclaration(".composer-toolbar-send__hint", "overflow"), "hidden", "send hint clips instead of spilling");
eq(finalDeclaration(".composer-toolbar-send__hint", "text-overflow"), "ellipsis", "send hint ellipsizes");

// 提示语③：composer-effort-applying（fallback 重建在飞提示）。
eq(finalDeclaration(".composer-effort-applying", "min-width"), "0", "effort-applying hint can shrink below content");
eq(finalDeclaration(".composer-effort-applying", "overflow"), "hidden", "effort-applying hint clips instead of spilling");
eq(finalDeclaration(".composer-effort-applying", "text-overflow"), "ellipsis", "effort-applying hint ellipsizes");

// 状态行 detail（tl1508b 修①：不再强制独占第二行，同排一行形态的前提）。
eq(finalDeclaration(".composer-run-strip__detail", "flex"), "0 1 auto", "run-strip detail is an inline-capable flex item, not a forced 100% row");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
