// Run: tsx src/__tests__/tl1233-approval-select-width.test.ts
//
// 任务 tl1233（P3）：权限档位下拉框固定宽——Autopilot 最宽导致与
// 询问/自动/Yolo 切换时下拉宽度跳变（用户 20261028 截图实证）。
//
// 机制：外层容器 `.composer-meta__control--approval--select` 是
// `flex: 0 0 auto` 的收缩项，min 116 / max 168 之间随选中项内容宽浮动。
// 修复（用户确认的固定宽方案）：min-width 抬到与 max-width 同值 168px
// （按最宽项 Autopilot 预留），切换零跳变。
//
// 本文件钉两条可证伪契约：
//  ① 宽度恒定：min-width === max-width === 168px（回退成浮动宽即红）；
//  ② 链路在位：Composer 里审批档位下拉仍挂 `composer-approval-select`
//     类（组件改类名后 CSS 契约落空即红）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const composerSource = readFileSync(resolve(testDir, "../components/Composer.tsx"), "utf8");

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

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

console.log("\ntl1233 approval select fixed width css");

// ① 固定宽契约：min = max = 168px（最宽项 Autopilot 预留），切换零跳变。
eq(finalDeclaration(".composer-meta__control--approval--select", "min-width"), "168px", "approval select 容器 min-width 固定 168px");
eq(finalDeclaration(".composer-meta__control--approval--select", "max-width"), "168px", "approval select 容器 max-width 固定 168px（min=max=恒定宽）");
eq(finalDeclaration(".composer-meta__control--approval--select", "flex"), "0 0 auto", "approval select 容器不被父级拉伸/压缩");
// 内层触发器与外层上限一致（168 上限语义不变，仅由 min 钉死）。
eq(finalDeclaration(".composer-approval-select", "max-width"), "168px", "内层触发器 max-width 仍 168px（与外层一致）");

// ② 链路在位：审批档位下拉（任务 940 两态的 select 形态）仍挂 composer-approval-select 类。
ok(composerSource.includes("composer-approval-select"), "Composer 审批下拉仍挂 composer-approval-select 类");
ok(/composer-approval-select--\$\{[^}]*\}/.test(composerSource), "Composer 审批下拉仍按档位挂模式修饰类");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
