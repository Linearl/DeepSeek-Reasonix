// tl1686：右栏常态态 chip 文案精简——『全量记录 1.3M』→『全量1.3M』（窄边栏不折行）。
//
// 钉住三件事：
//  1) 三语 Label 均为精简形态（无「记录/記錄/record」长词），宽度收敛；
//  2) 占位符 {canonical} 仍在（读数语义不动——这是本件的 non-goal）；
//  3) Title（口径说明）保留——「两把尺」的说明不能随文案精简一起丢。
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

const LOCALES = ["zh.ts", "en.ts", "zh-TW.ts"];

function labelOf(locale: string): string {
  const src = read(`../locales/${locale}`);
  const m = src.match(/"context\.historyVolumeLabel":\s*"([^"]*)"/);
  assert.ok(m, `${locale}: 缺 context.historyVolumeLabel`);
  return m[1];
}

function titleOf(locale: string): string {
  const src = read(`../locales/${locale}`);
  const m = src.match(/"context\.historyVolumeTitle":\s*"([^"]*)"/);
  assert.ok(m, `${locale}: 缺 context.historyVolumeTitle`);
  return m[1];
}

test("tl1686: 三语 chip 标签为精简形态且短于旧文案", () => {
  // 旧文案长度：全量记录 {canonical}=16 / 全量記錄 {canonical}=16 / Full record {canonical}=21
  // 新文案：全量{canonical}=13 / 全量{canonical}=13 / Full {canonical}=16
  const maxLen = { "zh.ts": 13, "zh-TW.ts": 13, "en.ts": 16 } as Record<string, number>;
  for (const locale of LOCALES) {
    const label = labelOf(locale);
    assert.ok(
      label.length <= maxLen[locale],
      `${locale}: 标签仍过长「${label}」（${label.length} > ${maxLen[locale]}）`,
    );
    // 精简形态不得再含「记录/記錄/record」长词。
    assert.doesNotMatch(
      label,
      /记录|記錄|record/i,
      `${locale}: 标签未精简「${label}」`,
    );
  }
});

test("tl1686: 占位符 {canonical} 保留（读数语义不动）", () => {
  for (const locale of LOCALES) {
    assert.match(
      labelOf(locale),
      /\{canonical\}/,
      `${locale}: 丢了 {canonical} 占位符——读数语义被改动`,
    );
  }
});

test("tl1686: Title 口径说明保留（两把尺说明不随文案精简丢失）", () => {
  for (const locale of LOCALES) {
    const title = titleOf(locale);
    assert.ok(title.length > 0, `${locale}: Title 被清空`);
    assert.match(title, /canonical/, `${locale}: Title 未保留 canonical 口径说明`);
  }
});

test("tl1686: 三语标签同结构（一个短词 + 直接接占位符，无中间长词）", () => {
  // 三语用词本就不同（zh「全量」/ en「Full」），故比对**结构**而非字面：
  // 标签 = 短词(可含空格) + {canonical}，中间不得夹长词。
  for (const locale of LOCALES) {
    const label = labelOf(locale);
    assert.match(
      label,
      /^[^\s{}]+\s?\{canonical\}$/,
      `${locale}: 标签结构异常「${label}」（应=短词+占位符）`,
    );
  }
});
