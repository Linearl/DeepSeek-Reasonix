// Run: tsx src/__tests__/tl1237-approval-select-trigger-width.test.ts
//
// 任务 tl1237（P2，tl1233 返修）：composer 模式下拉收起态触发器定宽。
// 用户 20261029 05:3x 实测：展开态菜单定宽 ✓，但收起态触发器按钮
// 仍随选中项文案浮动（YOLO 等）比 Autopilot 窄，收起↔展开宽度跳变。
//
// 任务 tl1261（P3）：用户 20261030 实测「等宽了，但是有点过于宽了」——
// 两态同宽值 168px → 148px 回调，定宽机制与开合零跳变约束不变。
//
// 机制：tl1233 只钉了外层容器（min=max=168px）。触发器是
// `:root .settings-select`（width:100%; min-width:0），width:100% 依赖
// 包含块定宽，而外层是 flex-basis:auto 的内容自适应 flex item——百分比
// 宽在主尺寸不定时回落 auto，触发器退回内容宽。min-width 修法会被组件
// 基类 `:root .settings-select { min-width: 0 }` 按 (0,2,0) 特异性压制，
// 故按任务书走外层容器级处理：width 直落触发器 + `:root` 前缀选择器
// （0,3,0 稳压 0,2,0，不依赖懒加载样式的注入顺序）。
//
// 本文件钉四条可证伪契约：
//  ① 定宽在位：scoped 触发器 width === 148px（恒定声明与文案无关，
//     YOLO/Autopilot 同宽，最宽项 Autopilot 不撑开）；
//  ② 选择器形态：必须经外层容器类作用域 + `:root` 前缀（特异性稳压
//     组件基类；漏掉任一即特异性/作用域回归，红）；
//  ③ 三处同值：触发器 width = 外层 min = 外层 max = 148px（展开态菜单
//     宽取触发器实际渲染宽，三处同值才有收起/展开零跳变）；
//  ④ 不外溢：styles.css 不存在裸 `.settings-select` 规则——设置页等其他
//     SettingsSelect 实例零波及。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// Strip comments so declaration parsing never matches prose inside them.
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const settingsSelectCss = readFileSync(resolve(testDir, "../components/SettingsSelect.css"), "utf8")
  .replace(/\/\*[\s\S]*?\*\//g, "");

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

function matchingBlocks(source: string, selector: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(source)) !== null) {
    const selectors = match[1].split(",").map((part) => part.trim());
    if (selectors.includes(selector)) blocks.push(match[2]);
  }
  return blocks;
}

function finalDeclaration(source: string, selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const block of matchingBlocks(source, selector)) {
    const declaration = new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`, "g");
    let match: RegExpExecArray | null;
    while ((match = declaration.exec(block)) !== null) {
      value = match[1].trim();
    }
  }
  return value;
}

console.log("\ntl1237 approval select trigger fixed width css");

const TRIGGER_SELECTOR = ":root .composer-meta__control--approval--select .settings-select";

// ① 定宽在位：收起态触发器恒定 148px（与选中项文案无关，Autopilot 不撑开）。
eq(finalDeclaration(styles, TRIGGER_SELECTOR, "width"), "148px", "收起态触发器（scoped）width 固定 148px——YOLO/Autopilot 同宽零跳变");

// ② 选择器形态：外层容器类作用域 + `:root` 前缀（0,3,0 稳压组件基类 0,2,0）。
ok(matchingBlocks(styles, TRIGGER_SELECTOR).length > 0, "触发器定宽选择器仍为 `:root .composer-meta__control--approval--select .settings-select`（容器级作用域 + :root 特异性前缀）");

// 机制前置件在位：组件基类 width:100%/min-width:0 是本修法存在的原因——
// width:100% 在内容自适应容器里解析不出定宽、min-width 会被此块压制。
const baseBlock = matchingBlocks(settingsSelectCss, ":root .settings-select");
eq(baseBlock.length > 0, true, "SettingsSelect.css 仍有 `:root .settings-select` 基类块");
eq(finalDeclaration(settingsSelectCss, ":root .settings-select", "min-width"), "0", "组件基类 min-width 仍为 0（tl1233/tl1237 选择器特异性依赖成立的前提）");
eq(finalDeclaration(settingsSelectCss, ":root .settings-select", "width"), "100%", "组件基类 width 仍为 100%（百分比解析回落机制的前提）");

// ③ 三处同值：触发器 width = 外层 min = 外层 max = 148px（菜单宽=触发器实际宽）。
eq(finalDeclaration(styles, ".composer-meta__control--approval--select", "min-width"), "148px", "外层容器 min-width 仍 148px（tl1233/tl1261 契约延续）");
eq(finalDeclaration(styles, ".composer-meta__control--approval--select", "max-width"), "148px", "外层容器 max-width 仍 148px（tl1233/tl1261 契约延续）");
eq(finalDeclaration(styles, ".composer-approval-select", "max-width"), "148px", "触发器 max-width 仍 148px（tl1233/tl1261 契约延续）");

// ④ 不外溢：styles.css 无裸 `.settings-select` 规则——其他 SettingsSelect 实例零波及。
eq(matchingBlocks(styles, ".settings-select").length, 0, "styles.css 不存在裸 .settings-select 规则（定宽仅作用于本下拉）");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
