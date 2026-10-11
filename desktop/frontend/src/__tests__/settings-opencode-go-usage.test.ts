// Run: tsx src/__tests__/settings-opencode-go-usage.test.ts
//
// Task 163 — OpenCode Go usage card (source guards + formatting): the
// render-table entry exists (81/123 lost-save lesson), the query gate lives
// in the sidebar card (off = no call, zero regression), the three locales
// ship the same key set, and the countdown/note helpers behave.
//
// 任务 800 — the settings-side usage row is a plain member switch: the
// in-card detail, its single-column layout overrides (tasks 337/346) and the
// in-card query are all retired; the right-dock sidebar card (783, family-
// gated since 799) is the authoritative display. The source guards below
// assert the retirement (negative assertions) and that the sidebar card
// carries the moved wiring whole.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { resetCountdown, usageNoteText } from "../lib/opencodeGoUsage";

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
const panel = readFileSync(join(root, "components/SettingsPanel.tsx"), "utf8").replace(/\n\s*/g, " ");
const card = readFileSync(join(root, "components/SettingsOpenCodeGoUsageCard.tsx"), "utf8").replace(/\n\s*/g, " ");
const sidebar = readFileSync(join(root, "components/OpenCodeGoUsageSidebarCard.tsx"), "utf8").replace(/\n\s*/g, " ");
const styles = readFileSync(join(root, "styles.css"), "utf8");
const bridge = readFileSync(join(root, "lib/bridge.ts"), "utf8");
const en = readFileSync(join(root, "locales/en.ts"), "utf8");
const zh = readFileSync(join(root, "locales/zh.ts"), "utf8");
const zhTW = readFileSync(join(root, "locales/zh-TW.ts"), "utf8");

// ── render table + union + detail wiring ────────────────────────────────────
{
  // 722/724 后 rail 渲染表移交布局数据（源码字面量断言随之腐化）；783 后
  // opencodeGoUsage 以成员身份在用量监测卡——布局数据断言钉它的位置。
  const { LAB_LAYOUT_DEFAULT_DATA } = await import("../lab/labLayoutDefault");
  const obs = LAB_LAYOUT_DEFAULT_DATA.groups.find((g: { key: string }) => g.key === "observability");
  const usage = obs!.entries.find((e: { id: string }) => e.id === "usageMonitoring");
  ok((usage?.members ?? []).some((m: { id: string }) => m.id === "opencodeGoUsage"),
    "render table (layout data) carries the entry as a usageMonitoring member (81/123)");
}

// ── 任务 800：卡内明细与专属布局退役 ──────────────────────────────────────
// settings 卡走 settings-field 默认双列（说明左/开关右），163/337/346 三代
// 卡内专属布局样式与明细行样式全部删净。
ok(!styles.includes(".settings-field--opencode-usage"),
  "the task 346/337 single-column override class is retired from the CSS (任务 800)");
ok(!styles.includes("opencode-go-usage__") && !styles.includes(".opencode-go-usage "),
  "the in-card usage detail styles (rows/percent/reset/note/refresh) are gone (任务 800)");
ok(styles.includes("grid-template-columns: minmax(180px, 260px) minmax(320px, 1fr)"),
  "the shared settings-field grid (copy left / control right) still carries the switch row");

ok(panel.includes('{selected === "usageMonitoring" && (') && !panel.includes('| "opencodeGoUsage"'),
  "任务 783：the standalone pane id is retired — the card lives inside the usageMonitoring merged card (member row, union id dropped)");
ok(panel.includes("Boolean(s.experimentalOpenCodeGoUsage)") && panel.includes("<SettingsOpenCodeGoUsageCard"),
  "the merged card renders the switch component gated by the switch value");
ok(bridge.includes("GetOpenCodeGoUsage(baseUrl: string): Promise<") && bridge.includes("SetExperimentalOpenCodeGoUsage(enabled: boolean): Promise<void>;"),
  "bridge exposes the query and the setter");

// ── 任务 800：设置卡内零查询——明细权威位在侧栏卡（783），family 门在侧栏（799）──
ok(!card.includes("GetOpenCodeGoUsage") && !card.includes("useState") && !card.includes("useEffect"),
  "the settings card keeps no query, no state, no effect — a plain switch row (任务 800)");
ok(!card.includes('data-testid="opencode-go-usage-card"') && !card.includes("data-window"),
  "the in-card usage detail DOM is retired (任务 800)");
ok(sidebar.includes("app.GetOpenCodeGoUsage(OPENCODE_GO_OFFICIAL_BASE)") && sidebar.includes("if (!enabled) {"),
  "the sidebar card owns the query behind the switch gate (163 contract moved whole)");

// ── malformed wire payloads can never crash render (opencodefix) ───────────
// The Go side used to marshal a nil Tiers slice as "tiers": null and render
// threw TypeError: null.find. The remaining settings-family consumer (任务
// 800 起唯一的明细渲染方) must normalize through Array.isArray (covers null
// AND non-array shapes like an object).
ok(sidebar.includes("Array.isArray(usage?.tiers) ? usage.tiers : []") && sidebar.includes("tiers.find((tier) => tier.window === key)"),
  "the sidebar render lookup normalizes null and non-array wire tiers payloads");

// ── crash isolation: the pane's usage row sits behind an ErrorBoundary ─────
// A failure beyond the known shapes must not kill the settings subtree
// (A-line evidence: post-crash the settings child tree stayed unrecoverable).
ok(panel.includes("<ErrorBoundary>") && panel.includes("<SettingsOpenCodeGoUsageCard")
  && /<ErrorBoundary>[\s\S]*<SettingsOpenCodeGoUsageCard[\s\S]*<\/ErrorBoundary>/.test(panel),
  "the usage row renders behind an ErrorBoundary in the settings pane");
ok(panel.includes('import { ErrorBoundary } from "./ErrorBoundary"'),
  "SettingsPanel imports the shared ErrorBoundary");

// ── formatting helpers ──────────────────────────────────────────────────────
const now = Date.parse("2026-09-25T12:00:00Z");
eqCountdown("", "", "empty iso stays empty");
eqCountdown("not-a-date", "", "invalid iso stays empty");
eqCountdown("2026-09-25T11:00:00Z", "", "past reset shows nothing");
eqCountdown("2026-09-25T12:42:00Z", "42m", "under an hour renders minutes");
eqCountdown("2026-09-25T15:12:00Z", "3h 12m", "under a day renders hours+minutes");
eqCountdown("2026-09-27T16:00:00Z", "2d 4h", "over a day renders days+hours");

const t = (key: string) => key;
assert.equal(usageNoteText("", t), "", "empty note renders nothing");
assert.equal(usageNoteText("no-subscription", t), "settings.opencodeGoUsage.note.noSubscription", "403 maps to the entitlement note");
assert.equal(usageNoteText("auth-failed", t), "settings.opencodeGoUsage.note.authFailed", "401 maps to the auth note");
assert.equal(usageNoteText("no-key", t), "settings.opencodeGoUsage.note.noKey", "missing key maps to its own note");
ok(true, "note mapping covers 403/401/no-key distinctly");

// ── three locales ship the same key set ─────────────────────────────────────
const keys = [
  "settings.opencodeGoUsage",
  "settings.opencodeGoUsageHint",
  "settings.opencodeGoUsage.on",
  "settings.opencodeGoUsage.off",
  "settings.opencodeGoUsage.window.rolling",
  "settings.opencodeGoUsage.window.weekly",
  "settings.opencodeGoUsage.window.monthly",
  "settings.opencodeGoUsage.resetsIn",
  "settings.opencodeGoUsage.refresh",
  "settings.opencodeGoUsage.note.noKey",
  "settings.opencodeGoUsage.note.noSubscription",
  "settings.opencodeGoUsage.note.authFailed",
  "settings.opencodeGoUsage.note.unsupported",
  "settings.opencodeGoUsage.note.failed",
  "settings.opencodeGoUsage.note.empty",
];
for (const key of keys) {
  ok(en.includes(`"${key}"`), `en ships ${key}`);
  ok(zh.includes(`"${key}"`), `zh ships ${key}`);
  ok(zhTW.includes(`"${key}"`), `zh-TW ships ${key}`);
}

// ── the no-key note carries setup guidance (task 337 acceptance ③) ─────────
// The card must tell users where to put the key: Settings → Model services →
// OpenCode Go (the configured connection they already saw), plus the env var.
const noKeyLine = (source: string) => (source.match(/"settings\.opencodeGoUsage\.note\.noKey":\s*"([^"]+)"/) ?? [])[1] ?? "";
ok(/设置\s*→\s*模型服务\s*→\s*OpenCode Go/.test(noKeyLine(zh)) && noKeyLine(zh).includes("OPENCODE_GO_API_KEY"),
  "zh no-key note points at Settings → Model services → OpenCode Go and the env var");
ok(/設定\s*→\s*模型服務\s*→\s*OpenCode Go/.test(noKeyLine(zhTW)) && noKeyLine(zhTW).includes("OPENCODE_GO_API_KEY"),
  "zh-TW no-key note carries the same guidance");
ok(/Settings → Model services → OpenCode Go/.test(noKeyLine(en)) && noKeyLine(en).includes("OPENCODE_GO_API_KEY"),
  "en no-key note carries the same guidance");

// ── 任务 800 layout: plain member row + spread intro slot ──────────────────
// The single-column override (task 337/346) is retired: the Go row renders
// like every other member row of a merged card (copy left / switch right),
// the merged-card head spreads its intro across the right intro slot, and
// the GLM row's duplicated hint (left tooltip + right paragraph) is deduped.
ok(!card.includes("settings-field--opencode-usage") && !card.includes("settings-field--wrapped"),
  "the Go row uses the default settings-field grid (no layout override class, 任务 800)");
const segAt = card.indexOf("<SettingsOptions");
ok(segAt >= 0 && card.indexOf("<SettingsField") >= 0 && card.indexOf("<SettingsField") < segAt,
  "the row renders its copy (SettingsField) around the switch segment");
ok(panel.includes('className="settings-field__hint">{t("settings.usageMonitoringHint")}'),
  "the merged-card head spreads its intro across the right intro slot (任务 800)");
ok(!panel.includes('className="settings-field__hint-line">{t("settings.glmPlanUsageHint")}'),
  "the GLM row no longer renders its hint twice (任务 800 dedupe)");

function eqCountdown(iso: string, expected: string, label: string) {
  const actual = resetCountdown(iso, now);
  if (actual === expected) ok(true, `countdown: ${label}`);
  else ok(false, `countdown: ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

assert.ok(passed >= 70, `expected at least 70 checks, got ${passed}`);
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
