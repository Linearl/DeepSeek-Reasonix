// tl1674 阶段一：pre-repair 拒绝行修复入口 + pre-repair 副本在版本管理区可见。
//
// 钉住两件事（阶段一）：
//  1) 健康闸拒绝项旁有「修复该会话」按钮，走既有修复通道
//     （app.RepairSessionFor —— 与版本管理区「修复」同一入口），不带 diet；
//     修完刷新清单，让用户能直接删。
//  2) pre-repair 备份在版本管理表里**可见**（按宿主会话归属挂计数徽标），
//     但**不**被算进副本链（链式合成是阶段二，候拍板，本件禁做）。
//
// 阶段一红线（阶段二实现不得越界）已随阶段二拍板转正（20261124）履约退役
// ——tl1674b 把它翻转为正向钉：阶段二拼接登记实现面在场。详见该断言处注释。
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

test("tl1674: 拒绝项结构化并带修复入口（复用既有 RepairSessionFor 通道）", () => {
  const src = read("../components/RecoveryCopiesSection.tsx");
  // 拒绝项结构化：带 dir + ownerPath（ownerPath 是修复入口的数据面）。
  assert.match(
    src,
    /kind:\s*"owner-unhealthy"[\s\S]{0,200}ownerPath/,
    "拒绝项未结构化（缺 kind/ownerPath）",
  );
  // 修复动作复用既有通道，不新造修复实现。
  assert.match(src, /RepairSessionFor\(ownerPath, true, true, false\)/, "修复动作未走既有通道");
  assert.match(
    src,
    /data-pre-repair-repair=""/,
    "拒绝行缺「修复该会话」按钮",
  );
  assert.match(
    src,
    /settings\.recoveryCopiesPreRepairRepairAction/,
    "修复按钮缺文案键",
  );
});

test("tl1674: 修复动作不带 diet（瘦身是另一档动作），修完刷新清单", () => {
  const src = read("../components/RecoveryCopiesSection.tsx");
  const call = src.match(/RepairSessionFor\([^)]*\)/);
  assert.ok(call, "未找到 RepairSessionFor 调用");
  assert.match(call[0], /,\s*false\)/, "修复调用应不带 diet（末位为 false）");
  // 修完要刷新改写前备份清单（健康态可能已变）。
  const fn = src.slice(src.indexOf("const repairRefusedOwner"));
  assert.match(fn.slice(0, 2000), /await load\(\)/, "修复后未刷新清单");
});

test("tl1674: pre-repair 副本在版本管理区可见（徽标按 ownerPath 归属）", () => {
  const src = read("../components/RecoveryCopiesSection.tsx");
  assert.match(src, /data-pre-repair-badge=""/, "版本管理表缺 pre-repair 可见徽标");
  assert.match(
    src,
    /preRepairs\.filter\(\(v\) => v\.ownerPath && v\.ownerPath === group\.mainPath\)/,
    "徽标未按宿主会话归属匹配",
  );
  assert.match(src, /settings\.recoveryCopiesPreRepairBadge/, "徽标缺文案键");
});

test("tl1674 阶段二（20261124 拍板转正）：拼接登记实现面在场", () => {
  const src = read("../components/RecoveryCopiesSection.tsx");
  // 本断言原为阶段一红线「不得出现阶段二的拼接/合成链实现」（候拍板期禁
  // 做越界）。阶段二已由用户拍板转正（tl1674 commit_ref 20261124）并由
  // tl1674b 交付（手动拼接入口+链预览+来源标记，纯元信息操作）——红线履
  // 约退役，改为正向钉住阶段二实现面在场（含 1787 续作先行落地的登记徽
  // 标）。注释保留判据史，防后来者误以为红线仍在。
  assert.match(src, /preRepairSplice|拼接点登记|MergePreRepairSplice/i, "阶段二拼接登记实现面缺失（转正交付被回退？）");
  // 徽标不得把 pre-repair 计入 copies 计数（仍只显示真实副本数）。
  const badgeIdx = src.indexOf('data-pre-repair-badge=""');
  const cellIdx = src.lastIndexOf("×{group.copies.length}", badgeIdx);
  assert.ok(cellIdx > 0 && badgeIdx > cellIdx, "徽标与副本计数位置异常");
  assert.match(
    src.slice(cellIdx, badgeIdx),
    /group\.copies\.length/,
    "副本计数未按真实副本数显示",
  );
});

test("tl1674: 三语键齐备（修复动作/回执/徽标）", () => {
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    const src = read(`../locales/${locale}`);
    for (const key of [
      "settings.recoveryCopiesPreRepairRepairAction",
      "settings.recoveryCopiesPreRepairRepairHint",
      "settings.recoveryCopiesPreRepairRepairDone",
      "settings.recoveryCopiesPreRepairRepairNoop",
      "settings.recoveryCopiesPreRepairBadge",
    ]) {
      assert.match(src, new RegExp(`"${key.replace(/\./g, "\\.")}"`), `${locale}: 缺 ${key}`);
    }
  }
});
