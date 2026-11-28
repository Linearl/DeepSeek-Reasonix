// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1855r2-acp-row-lab-style.test.tsx
//
// tl1855r2 acceptance pins — ACP 常驻监听行布局样式返修（包121 装机验收发现）：
// tl1855 件②把该行挪进「本地服务」页时用了裸 checkbox（settings-toggle，
// 全仓无 CSS、开关在左/说明在右），与全应用设置行形态脱节。返修 = 换回
// 实验室统一样式：
// ① 行解剖同实验室行（tl1855 前资源治理卡成员行 5 原形态基准）：
//    SettingsField（label+hint 左 / 控件右）+ SettingsOptions set-seg
//    分段开/关（与 orphanHandling 等行同样式类/同结构）；
// ② tl1855 功能交付零回退：TierBadge 随节标题、hint 三态（err/运行地址/
//    待重启）、重启横幅（RestartDesktop）、键 experimental_acp_listen 经
//    SetExperimentalACPListen、乐观显示态——只动布局，语义零变化。
// non-goals（承 tl1855 原范围）：不触碰 ACP 功能/键/重启语义；本页
// localserver.enable 行的 legacy checkbox 形态不在本件范围。

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import type { AppBindings } from "../lib/bridge";
import type { SettingsView } from "../lib/settingsViewTypes";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntl1855r2 ACP row lab-style");

// ── source-level pins（①同结构 + ②功能元素在源） ──
const localServerSource = readFileSync(new URL("../components/LocalServerPage.tsx", import.meta.url), "utf8");
const labSource = readFileSync(new URL("../components/ExperimentalLabSection.tsx", import.meta.url), "utf8");

// ① 控件同样式类：与实验室行逐字同款（layout="field" + set-seg 分段）。
const LAB_SEG_CONTROL = '<SettingsOptions layout="field" className="set-seg">';
ok(
  labSource.includes(LAB_SEG_CONTROL),
  "baseline sanity: lab rows still carry the set-seg segmented control (orphanHandling et al.)",
);
ok(
  localServerSource.includes(LAB_SEG_CONTROL),
  "① ACP row control uses the lab's exact segmented classes (settings-options--field + set-seg)",
);
// ① 同结构：SettingsField 承载行（label/hint 左、控件右），hint 走三态 acpHint。
ok(
  localServerSource.includes('<SettingsField testId="acp-listen-toggle" label={t("settings.acpListen")} hint={acpHint}>'),
  "① ACP row is a SettingsField row (copy left / control right anatomy) with the three-state hint",
);
// ① 旧形态退场：裸 checkbox 翻转行不复存在（toggleAcpListen 已换显式目标值）。
ok(
  localServerSource.includes("toggleAcpListen") === false && localServerSource.includes("checked={acpOn}") === false,
  "① the legacy raw-checkbox toggle row is gone (no toggleAcpListen / checked={acpOn} binding)",
);
// ② TierBadge 随节标题（task562 专属断言同源，双保险钉）。
ok(
  localServerSource.includes("EXPERIMENT_FEATURE_TIERS.acpListen"),
  "② TierBadge (EXPERIMENT_FEATURE_TIERS.acpListen) still rides the section header (tl1855 delivery intact)",
);
// ② 重启横幅 + RestartDesktop。
ok(
  localServerSource.includes("settings-restart-banner") && localServerSource.includes("app.RestartDesktop()"),
  "② the restart-required banner with the RestartDesktop action survives (tl1855 delivery intact)",
);
// ② 键/setter：SetExperimentalACPListen 与乐观显示态。
ok(
  localServerSource.includes("app.SetExperimentalACPListen(") && localServerSource.includes("setAcpOnLocal("),
  "② SetExperimentalACPListen + optimistic local display state survive (semantics unchanged)",
);
// ② hint 三态键引用全保留。
ok(
  localServerSource.includes("settings.acpListen.notRunning")
    && localServerSource.includes("settings.acpListen.running")
    && localServerSource.includes("settings.acpListen.awaitRestart")
    && localServerSource.includes("settings.acpListenHint"),
  "② the three-state hint keys (err / running+addr / awaitRestart / default) all survive",
);

// ── render-level pins（①同结构 DOM 断言 + ②横幅/徽章/hint 实渲染） ──
const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;

const setCalls: boolean[] = [];
const restartCalls: number[] = [];
window.go = { main: { App: {
  async ServePoolStatus() {
    return { enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "t", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5"] };
  },
  async ACPListenStatus() {
    return { running: true, addr: "127.0.0.1:18790", err: "" };
  },
  async SetExperimentalACPListen(next: boolean) {
    setCalls.push(next);
  },
  async RestartDesktop() {
    restartCalls.push(1);
  },
} as Partial<AppBindings> as AppBindings } };

const [{ createRoot }, { LocalServerPage }, { LocaleProvider }, { act }] = await Promise.all([
  import("react-dom/client"),
  import("../components/LocalServerPage"),
  import("../lib/i18n"),
  import("react"),
]);

const el = document.createElement("div");
document.body.appendChild(el);
const root = createRoot(el);
// s 快照注入 experimentalACPListen=true（SettingsPanel 注入路径；可空但导航
// 过滤保证实际可达时已加载——tl1855 原注释语义）。
await act(async () => {
  root.render(
    <LocaleProvider>
      <LocalServerPage s={{ experimentalACPListen: true } as unknown as SettingsView} />
    </LocaleProvider>,
  );
  await new Promise((resolve) => setTimeout(resolve, 20));
});
await act(async () => {
  for (let i = 0; i < 6; i++) {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
});

const section = () => document.querySelector('[data-testid="acp-listen-section"]');
const row = () => document.querySelector('[data-testid="acp-listen-toggle"]');

// ① 行解剖：copy（label+hint 左）在前、control（分段控件右）在后（DOM 序=网格列序）。
const copy = row()?.querySelector(".settings-field__copy");
const control = row()?.querySelector(".settings-field__control");
ok(row() !== null && copy !== null && control !== null, "① ACP row renders the SettingsField anatomy (copy + control slots)");
ok(
  copy !== null && control !== null
    && (copy.compareDocumentPosition(control) & dom.window.Node.DOCUMENT_POSITION_FOLLOWING) !== 0,
  "① copy (label/hint) precedes control in DOM order = toggle sits on the RIGHT of the row",
);
// ① 控件形态：settings-options--field set-seg 分段、两枚 set-seg__btn、选中态随快照。
const seg = section()?.querySelector(".settings-field__control .settings-options.settings-options--field.set-seg");
const segButtons = seg ? Array.from(seg.querySelectorAll(":scope > button.set-seg__btn")) : [];
ok(seg !== null, "① the control renders the same set-seg segmented classes as lab rows (component-level same-class pin)");
ok(segButtons.length === 2, "① the segmented control carries exactly two buttons (关/开)");
ok(
  segButtons[1]?.classList.contains("set-seg__btn--on") && !segButtons[0]?.classList.contains("set-seg__btn--on"),
  "① selected state mirrors the snapshot (experimentalACPListen=true → 开 lit, 关 dark)",
);
// ① 旧形态退场：ACP 节内零裸 checkbox。
ok(section()?.querySelector("input[type=checkbox]") === null, "① no raw checkbox inside the ACP section anymore");
// ② TierBadge 随节标题实渲染。
ok(section()?.querySelector(".settings-section__header [data-lab-tier]") !== null, "② the tier badge renders in the section header");
// ② hint 三态之「监听中：addr」实渲染（值随 ACPListenStatus mock）。
ok(
  (row()?.querySelector(".settings-field__hint")?.textContent ?? "").includes("127.0.0.1:18790"),
  "② the running-state hint renders with the live listen addr",
);

// ② 点「关」→ setter 落显式 false、横幅出现、横幅按钮走 RestartDesktop。
await act(async () => {
  (segButtons[0] as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 20));
});
ok(setCalls.length === 1 && setCalls[0] === false, "② clicking 关 calls SetExperimentalACPListen(false) — same key/setter as the lab row");
const banner = section()?.querySelector(".settings-restart-banner[role=status]");
ok(banner !== null, "② the restart-required banner appears after a flip (tl1855 delivery intact)");
const bannerAction = banner?.querySelector<HTMLButtonElement>(".settings-restart-banner__action");
await act(async () => {
  bannerAction?.click();
  await new Promise((resolve) => setTimeout(resolve, 20));
});
ok(restartCalls.length === 1, "② the banner action calls RestartDesktop");

// ② 乐观显示态：翻转后关钮点亮（快照 s=true ⊕ 本地 false），语义与 tl1855 一致。
const segAfter = section()?.querySelectorAll(".set-seg__btn--on");
ok(segAfter?.length === 1 && segAfter[0] === segButtons[0], "② optimistic state moves the lit segment to 关 after the flip (no stale visual)");

root.unmount();
el.remove();

process.stdout.write(`\n  ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
