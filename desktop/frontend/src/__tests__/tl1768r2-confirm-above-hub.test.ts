// Run: tsx src/__tests__/tl1768r2-confirm-above-hub.test.ts
//
// tl1768r2: 确认弹窗必须浮在协作中心主面板之上。信件多选删除/清空、通知清空
// 走全局 ConfirmDialog（.reasonix-confirm-backdrop，原 --z-overlay-backdrop 1300），
// 而协作中心面板 .collab-hub-panel 挂 --z-popover(1301)——1300 < 1301 被全屏盖住
// （用户真机实测「点删除无弹窗」）。任务 984 先例（9b0832d841）同根因同修法：
// 面板之上语义的弹层走 --z-popover-elevated。本钉防两路回归：
//   ① 规则钉：.reasonix-confirm-backdrop 必须显式挂 --z-popover-elevated；
//   ② 算术钉：--z-popover-elevated 数值必须 > --z-popover（token 重编号时
//      层级语义仍成立——「confirm 盖过协作中心面板」是唯一不变量）。
// 另钉 .collab-hub-panel 自身仍挂 --z-popover（984 模型的参考面，防顺手改层级
// 破坏「面板为参考面」的既有注释与测试语境）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");

let passed = 0;
let failed = 0;

function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function tokenValue(name: string): number | null {
  const m = styles.match(new RegExp(`--${name}\\s*:\\s*(\\d+)\\s*;`));
  return m ? Number(m[1]) : null;
}

function ruleZValue(selector: string): string | null {
  const idx = styles.indexOf(`${selector} {`);
  if (idx < 0) return null;
  const body = styles.slice(idx, styles.indexOf("}", idx));
  const m = body.match(/z-index:\s*([^;]+);/);
  return m ? m[1].trim() : null;
}

// ① 规则钉：confirm 背衬挂 elevated 档。
const confirmZ = ruleZValue(".reasonix-confirm-backdrop");
ok(confirmZ === "var(--z-popover-elevated)", `.reasonix-confirm-backdrop rides --z-popover-elevated (got ${confirmZ})`);

// ② 算术钉：elevated > popover，confirm 恒在协作中心面板之上。
const elevated = tokenValue("z-popover-elevated");
const popover = tokenValue("z-popover");
ok(elevated !== null && popover !== null, "--z-popover-elevated / --z-popover tokens exist");
ok(elevated !== null && popover !== null && elevated > popover, `--z-popover-elevated(${elevated}) > --z-popover(${popover})`);

// ③ 参考面钉：协作中心主面板仍挂 --z-popover（984 注释模型的参考面）。
const hubZ = ruleZValue(".collab-hub-panel");
ok(hubZ === "var(--z-popover)", `.collab-hub-panel stays on --z-popover (got ${hubZ})`);

// ④ token 钉家族：overlay-backdrop 仍在 popover 之下（984 注释的算术语境不被倒挂）。
const overlayBackdrop = tokenValue("z-overlay-backdrop");
ok(overlayBackdrop !== null && popover !== null && overlayBackdrop < popover, `--z-overlay-backdrop(${overlayBackdrop}) < --z-popover(${popover})`);

process.stdout.write(`\ntl1768r2-confirm-above-hub: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
