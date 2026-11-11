// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1547-ui-caliber.test.ts
//
// tl1547 (tl1377 P2-c)：UI 评审口径不一致族修复。本文件钉死三面的验收契约：
//
//   A（显示名贯通，验收①/A3/A2）：
//     - 连接 displayName 登记注册表后，modelRefDisplayLabel 与设置页
//       providerDisplayLabel 同名（同一 displayName > 内置默认 > name 阶梯）；
//     - 未登记的 provider 回退内置默认标签，绝不回退完整 ref 防行宽跳变（A2
//       语义的 FE 侧：显示名解析永不做「短档缺失→长档兜底」）；
//     - ModelInfo.displayName（模型档显示名，A3）在 ref 显示形式中消费。
//   B（相对时间词表合一，验收②/B3；B2 日期约定）：
//     - topicActivityLabel 与 formatRelativeAgeLabel 同输入同输出（一个词表，
//       同屏侧栏行与工具卡同词）；compact 无「前」字形态退役；
//     - formatCalendarDate 三语逐字节一致（YYYY-MM-DD 连字符约定，1012 同源）。
//   C（索引占位 i18n，验收⑤）：
//     - history.indexing 三语键齐备 + historyIndexedTitle 拼接契约。
//
// 验收③（侧栏 vs 历史面板同名）= session-title-contract.test.ts 的 C1 链序
// 钉；验收④（复用目录恢复标记）= desktop/latest_session_workspace_test.go
// 的 Go 钉（sessionTitle 在 Go 侧拼好，FE 原样渲染）——不在此重复。
//
// 三语（en/zh/zh-TW）同跑：en 直灌字典、zh/zh-TW 动态 import（tl1299 判例）。

import type { Translator } from "../lib/i18n";
import { en } from "../locales/en";

import { formatRelativeAgeLabel } from "../lib/durationFormat";
import { formatCalendarDate } from "../lib/messageTimestamp";
import {
  modelRefDisplayLabel,
  providerConnectionLabel,
  registerModelRefLabels,
} from "../lib/modelRefLabel";
import { topicActivityDateLabel, topicActivityLabel } from "../lib/projectTreeTopic";
import { historyIndexedTitle } from "../lib/session";

let passed = 0;
let failed = 0;
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
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function stubTranslator(dict: Record<string, string>): Translator {
  return ((key: keyof typeof en, vars?: Record<string, string | number>) => {
    let out: string = dict[key as string] ?? String(key);
    if (vars) {
      for (const [k, v] of Object.entries(vars)) out = out.replaceAll(`{${k}}`, String(v));
    }
    return out;
  }) as Translator;
}

const tEn = stubTranslator(en as unknown as Record<string, string>);

console.log("\ntl1547 UI caliber: A — connection display names reach ref-holding surfaces");

// 验收①：设置 displayName 后，ref 显示形式与设置页同一阶梯（displayName >
// 内置默认 > name）。注册表由 ModelSwitcher 的目录快照喂（生产链路）。
{
  registerModelRefLabels([
    { provider: "deepseek", providerLabel: "我的DeepSeek", ref: "deepseek/deepseek-v4-flash", displayName: "DeepSeek V4 Flash" },
    { provider: "opencode-go-0c9f", ref: "opencode-go-0c9f/glm-5" },
  ]);
  eq(
    modelRefDisplayLabel("deepseek/deepseek-v4-flash"),
    "我的DeepSeek/DeepSeek V4 Flash",
    "A1: renamed connection + per-model display name both resolve in the ref display form",
  );
  eq(
    providerConnectionLabel("deepseek"),
    "我的DeepSeek",
    "A1: the registry label is the same string the settings panel renders",
  );
  // 未登记连接：内置默认标签兜底（vendor 可读名），绝不回退完整 ref（A2）。
  eq(
    providerConnectionLabel("minimax"),
    "MiniMax",
    "A2: unknown providers fall back to the built-in vendor label, never a raw id",
  );
  eq(
    modelRefDisplayLabel("minimax/MiniMax-M3.1"),
    "MiniMax/MiniMax-M3.1",
    "A2: ref display keeps provider/model shape without widening to a longer raw id",
  );
  eq(
    providerConnectionLabel("opencode-go-0c9f"),
    "opencode-go-0c9f",
    "A2: an unrenamed custom connection reads as its own name (no invented label)",
  );
  // A3 关闭 displayName 时回退裸模型 id；provider 段仍走内置默认标签阶梯。
  registerModelRefLabels([{ provider: "glm", ref: "glm/glm-5" }]);
  eq(
    modelRefDisplayLabel("glm/glm-5"),
    "GLM/glm-5",
    "A3: without a model displayName the bare model id stands; the provider segment keeps its built-in label",
  );
  eq(modelRefDisplayLabel(""), "", "A1: empty ref renders empty");
  eq(modelRefDisplayLabel("  "), "", "A1: whitespace ref renders empty");
  eq(
    modelRefDisplayLabel("mimo-v2.6-flash"),
    "mimo-v2.6-flash",
    "A1: legacy single-segment ids pass through untouched",
  );
}

console.log("\ntl1547 UI caliber: B — one relative-time vocabulary (1299 contract)");

// 验收②：同输入下侧栏行标签与工具卡标签逐字节相同（同一个函数）。
{
  const bands: Array<[number, string]> = [
    [0, "just now"],
    [59, "59s ago"],
    [12 * 60, "12m ago"],
    [2 * 3600 + 7 * 60, "2h 7m ago"],
    [3 * 86400, "3d ago"],
    [400 * 86400, "400d ago"],
  ];
  const now = Date.now();
  for (const [seconds, expected] of bands) {
    const viaSidebar = topicActivityLabel(now - seconds * 1000, tEn);
    eq(viaSidebar, expected, `B1/B3: sidebar row label at ${seconds}s reads the 1299 band (${expected})`);
    eq(
      viaSidebar,
      formatRelativeAgeLabel(seconds, tEn),
      `B1: sidebar and tool card agree byte-for-byte at ${seconds}s`,
    );
  }
  eq(topicActivityLabel(0, tEn), "", "B1: zero/unknown activity renders empty");
  eq(topicActivityLabel(-5, tEn), "", "B1: negative activity renders empty");
}

// B2：绝对日期约定统一——手工连字符，三语逐字节一致。
{
  const ms = new Date(2026, 10, 4, 21, 30).getTime(); // local 2026-11-04
  eq(topicActivityDateLabel(ms), "2026-11-04", "B2: sidebar exact date uses the hyphen convention");
  eq(formatCalendarDate(ms), "2026-11-04", "B2: shared calendar date splices YYYY-MM-DD");
  eq(formatCalendarDate(0), "", "B2: zero timestamp renders empty");
  eq(formatCalendarDate(Number.NaN), "", "B2: NaN timestamp renders empty");
  // 本地时区钉：任一时区下拼出的都是本地日历日的 YYYY-MM-DD。
  const local = new Date(2026, 0, 9);
  eq(formatCalendarDate(local.getTime()), "2026-01-09", "B2: local calendar day splices with zero-padding");
}

console.log("\ntl1547 UI caliber: C — indexing placeholder is localized (three locales)");

// 验收⑤：history.indexing 三语键齐备；拼接契约 = 状态 + " — " + stamp。
{
  eq(historyIndexedTitle("20261031 22:04", "Indexing history"), "Indexing history — 20261031 22:04", "C4: en placeholder carries the stamp");
  eq(historyIndexedTitle(undefined, "Indexing history"), "Indexing history", "C4: missing stamp keeps the status alone");
  const { zh: zhDict } = await import("../locales/zh");
  const { zhTW: zhTWDict } = await import("../locales/zh-TW");
  eq(zhDict["history.indexing"], "正在整理历史", "C4: zh placeholder key present");
  eq(zhTWDict["history.indexing"], "正在整理歷史", "C4: zh-TW placeholder key present");
  eq(en["history.indexing"], "Indexing history", "C4: en placeholder key present");
  eq(
    historyIndexedTitle("20261031 22:04", zhDict["history.indexing"]),
    "正在整理历史 — 20261031 22:04",
    "C4: zh rendering composes status + stamp",
  );
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
