// Run: tsx src/__tests__/settings-preapprove-managed.test.ts
//
// Task 231 — managed-path pre-approval settings surface (source guards)
// → tl1741r2 再换代（用户 20261122 打回裁定「stable 清主选项（常开）、子选项
// 保留配置」：autopilot 卡体回归 rail（无主开关行），卡内
// autopilot-preapprove-subblock 随宿主 pane 回归；preapprove_* 五值 setter/
// bridge 接口/三语九键/CSS 契约语义面零改动）：
//  ① 子块在 autopilot 卡 pane 渲染（无主开关卡的子项面，不占独立 rail 入口）；
//  ② bridge 五值 setter 接口仍在；
//  ③ 三语九键契约与 CSS 形态契约不变。

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
// 任务724/1134:preapprove 子块迁 ExperimentalLabSection、union 迁
// lab/labPaneRegistry(拼接读源,1134 判例)。
const panel = [
  "components/SettingsPanel.tsx",
  "components/ExperimentalLabSection.tsx",
  "lab/labPaneRegistry.ts",
].map((rel) => readFileSync(join(root, rel), "utf8")).join("").replace(/\n\s*/g, " ");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

// ── tl1741r2: the sub-block is back inside the autopilot card pane ─────────
ok(!panel.includes('{ id: "preapproveManagedPaths", group:'),
  "render table has no preapprove rail entry (still folded: member face of the autopilot card, tl1741r2)");
ok(panel.includes("autopilot-preapprove-subblock"), "the pre-approval sub-block renders in the autopilot card pane (tl1741r2)");
ok(panel.includes("app.SetPreapproveManagedPaths("), "the sub-block wires the five-value bridge setter (one apply, no half-applied saves)");

// ── bridge: one five-value write (no half-applied saves) ────────────────────
ok(bridge.includes("SetPreapproveManagedPaths(enabled: boolean, skills: boolean, hooks: boolean, sessionStores: boolean, bashEscape: boolean): Promise<void>;"),
  "bridge interface exposes the five-value setter");

// ── three locales ship the same nine keys ───────────────────────────────────
const keys = [
  "settings.preapproveManagedPaths",
  "settings.preapproveManagedPathsHint",
  "settings.preapproveManagedPaths.on",
  "settings.preapproveManagedPaths.off",
  "settings.preapproveManagedPaths.skills",
  "settings.preapproveManagedPaths.hooks",
  "settings.preapproveManagedPaths.sessionStores",
  "settings.preapproveManagedPaths.bashEscape",
  "settings.preapproveManagedPaths.warning",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}
ok(zh.includes("prompt injection") && zhTW.includes("prompt injection") && en.includes("prompt injection"),
  "the risk line names prompt injection in all three locales");

// ── CSS 形态契约（退役不删——重挂位可复用；任务461-P5 口径不变）──────────
const styles = readFileSync(join(root, "styles.css"), "utf8").replace(/\n\s*/g, " ");
ok(/\.autopilot-preapprove-subblock \.settings-field \{ grid-template-columns: minmax\(180px, 260px\) minmax\(320px, 1fr\);/.test(styles),
  "the pre-approval field keeps the two-column header (intro left, switch right)");
ok(/\.autopilot-preapprove-subblock \.set-seg \{ justify-self: end;/.test(styles),
  "the on/off switch hugs the right edge of its column");
ok(/@container settings-page \(max-width: 680px\) \{ \.autopilot-preapprove-subblock \.settings-field \{ grid-template-columns: minmax\(0, 1fr\);/.test(styles),
  "narrow settings pages still collapse the field to one column");
ok(/\.set-preapprove__row \{[^}]*display: flex;/.test(styles),
  "each category row is a block-level flex line (one category per line)");
for (const [name, text] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  const hooks = text.match(/"settings\.preapproveManagedPaths\.hooks": "([^"]*)"/)?.[1] ?? "";
  const warn = text.match(/"settings\.preapproveManagedPaths\.warning": "([^"]*)"/)?.[1] ?? "";
  ok(hooks.length > 0 && !hooks.includes("settings.json"), `${name} hooks label is simplified (settings.json detail moved out)`);
  ok(warn.includes("settings.json"), `${name} risk line carries the hooks settings.json detail`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
