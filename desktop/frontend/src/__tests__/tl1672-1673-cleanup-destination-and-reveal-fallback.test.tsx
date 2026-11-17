// tl1672 + tl1673：pre-repair 清理去向/拒绝分流 + 决策模式按钮降级。
//
// 两件都属「界面零告知」家族：
//  - tl1672：清理成功 70 份进了系统回收站，界面只写「进回收站」没说清是
//    系统回收站（可还原）；健康闸拒绝与真失败在 UI 上同形（都进 errors
//    的 banner--error），用户读成「删除报错了」。
//  - tl1673：decision-patterns.md 是注入用聚合文件，首次决策固化轮才生成，
//    此前永远不存在——「打开位置」按钮此前是死提示（无出路）。
//
// 本钉钉住三件事：
//  1) 回收站去向文案含「系统回收站 + 可还原」；
//  2) reason 令牌分流：owner-unhealthy/gone 进 refusals（警示），其余进
//     errors（错误）；
//  3) 按钮在文件缺失但父目录存在时降级为「打开所在文件夹」，且点击走
//     revealFallback（定位目录）而非死 no-op。
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

test("tl1672: 清理完成回执写明系统回收站且可还原（三语）", () => {
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = read(`../locales/${locale}`);
    const done = src.match(/"settings\.recoveryCopiesPreRepairCleanDone":\s*"([^"]*)"/);
    assert.ok(done, `${locale}: CleanDone 键缺失`);
    assert.match(
      done[1],
      /回收站|回收桶|recycle bin/i,
      `${locale}: 完成回执未写明回收站去向`,
    );
    assert.match(
      done[1],
      /还原|restorable|還原/i,
      `${locale}: 完成回执未写明可还原`,
    );
  }
});

test("tl1672: 健康闸拒绝与真失败分流——拒绝进 refusals(警示)，其余进 errors(错误)", () => {
  const src = read("../components/RecoveryCopiesSection.tsx");
  assert.match(src, /const \[refusals, setRefusals\]/, "缺少 refusals 状态");
  assert.match(
    src,
    /failure\.reason === "owner-unhealthy"/,
    "未按 owner-unhealthy 令牌分流健康闸拒绝",
  );
  assert.match(
    src,
    /failure\.reason === "gone"/,
    "未按 gone 令牌分流「已不在原处」",
  );
  // 拒绝走警示块、真失败走错误块——两者不得再同形。
  assert.match(src, /banner--warning/, "拒绝清单未用警示样式");
  assert.match(src, /banner--error/, "真失败清单应有错误样式");
  const warnIdx = src.indexOf("banner--warning");
  const errIdx = src.indexOf("banner--error");
  assert.ok(warnIdx > 0 && errIdx > 0, "警示/错误块缺失");
  assert.notEqual(warnIdx, errIdx, "警示与错误块同形（未分离）");
});

test("tl1673: 决策模式按钮在文件缺失时降级为打开所在文件夹并走目录回落", () => {
  const hook = read("../lib/revealLocation.ts");
  assert.match(hook, /useRevealLocationWithParentFallback/, "缺父目录回落钩子");
  assert.match(hook, /parentDirOf/, "缺父目录解析");
  assert.match(hook, /fileMissing/, "缺 fileMissing 判据");

  const comp = read("../components/ExperimentalLabSection.tsx");
  assert.match(comp, /useRevealLocationWithParentFallback/, "决策模式未接回落钩子");
  assert.match(
    comp,
    /settings\.revealLocationOpenFolder/,
    "决策模式按钮未降级为「打开所在文件夹」",
  );
  assert.match(comp, /decisionReveal\.revealFallback\(\)/, "按钮点击未走目录回落");
  assert.match(
    comp,
    /settings\.decisionPatternsNotGeneratedYet/,
    "未附生成时机说明",
  );
});

test("tl1673: 三语键齐备（打开所在文件夹 + 生成时机说明）", () => {
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = read(`../locales/${locale}`);
    assert.match(src, /"settings\.revealLocationOpenFolder"/, `${locale}: 缺 OpenFolder 键`);
    assert.match(
      src,
      /"settings\.decisionPatternsNotGeneratedYet"/,
      `${locale}: 缺 NotGeneratedYet 键`,
    );
  }
});
