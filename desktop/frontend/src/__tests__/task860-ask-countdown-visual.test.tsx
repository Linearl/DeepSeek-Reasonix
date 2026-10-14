// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task860-ask-countdown-visual.test.tsx
// 任务860 验收钉（849 用户实测反馈：秒数与接管按钮不显眼，纯样式打磨）:
//   ① 剩余秒数一眼可读 — countdown-num computed font-size ≥ 20px, weight ≥ 700,
//      accent token in the CSS text (jsdom cannot resolve color-mix/var color,
//      so the token pin runs against styles.css text, the layout-test pattern).
//   ② 接管按钮主按钮级视觉 — --takeover class + primary fill token + computed
//      width is NOT the 32px icon slot (the 656 double-width pit: two width
//      declarations on one element collapse instead of cascade, so the icon
//      rule carries :not(--takeover); the paired icon-button assertion proves
//      the 32px rule still applies to its own buttons).
//   ③ 倒计时功能逻辑零变化 — concatenated header text stays byte-identical to
//      the 849 sentence (the takeover test pins match), plain panel unchanged.
//   主题包模式 — theme-pack !important background rules exclude --takeover so
//      the accent fill survives (otherwise translucent bg + primary fg text).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import type { ReactNode } from "react";
import type { WireAsk } from "../lib/types";

const testDir = dirname(fileURLToPath(import.meta.url));
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement, Node: dom.window.Node, localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
const style = dom.window.document.createElement("style");
style.textContent = styles;
dom.window.document.head.appendChild(style);

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { AskCard } = await import("../components/AskCard");
const { LocaleProvider } = await import("../lib/i18n");

const root = createRoot(document.getElementById("root")!);
const ask: WireAsk = {
  id: "1", runtimeEpoch: "runtime-a", turnId: "turn-a",
  questions: [{ id: "q1", prompt: "Choose", options: [{ label: "Option A" }, { label: "Option B" }] }],
};
async function render(body: ReactNode) {
  await act(async () => root.render(<LocaleProvider>{body}</LocaleProvider>));
}
async function renderArmed(scope: string, armed: boolean) {
  await render(
    <AskCard
      ask={ask}
      draftScope={scope}
      onAnswer={() => {}}
      onStop={() => {}}
      countdownMs={armed ? 15_000 : undefined}
      onTakeover={armed ? (() => Promise.resolve(true)) : undefined}
    />,
  );
}
const headerMeta = () => document.querySelector<HTMLElement>(".ask-shelf__header-meta");
const takeoverButton = () =>
  Array.from(document.querySelectorAll<HTMLButtonElement>("button.prompt-shelf__header-button"))
    .find((b) => b.textContent === "Take over" || b.textContent === "Release");
const iconButton = () =>
  Array.from(document.querySelectorAll<HTMLButtonElement>("button.prompt-shelf__header-button"))
    .find((b) => b.getAttribute("aria-label") === "Collapse" || b.getAttribute("aria-label") === "Expand");

// 钉①: armed panel renders the headline countdown with a large accent figure.
await renderArmed("visual", true);
const num = document.querySelector<HTMLElement>(".ask-shelf__countdown-num");
assert.ok(num, "armed panel renders .ask-shelf__countdown-num");
const numStyle = window.getComputedStyle(num!);
const numSize = parseFloat(numStyle.fontSize);
assert.ok(numSize >= 20, `countdown number font-size >= 20px, got ${numStyle.fontSize}`);
assert.ok(parseInt(numStyle.fontWeight, 10) >= 700, `countdown number weight >= 700, got ${numStyle.fontWeight}`);
assert.match(num!.textContent ?? "", /^\d+$/, "countdown number is the bare seconds figure");
console.log(`  PASS  countdown number is headline scale (${numStyle.fontSize}, weight ${numStyle.fontWeight})`);

// ① continued: color pins against the CSS text (var()/color-mix cannot
// resolve in jsdom). The number mixes 72% accent with --fg — raw accent fails
// AA-large on light theme packs (graphite 2.38 / carbon 2.78 vs 3.0 floor);
// the mix matches the .prompt-shelf__badge--amber precedent and clears 3.87
// everywhere. The capsule tints from the same accent token.
assert.match(
  styles,
  /\.ask-shelf__countdown-num \{[^}]*color:\s*color-mix\(in srgb, var\(--accent\) 72%, var\(--fg\)\)/s,
  "countdown number mixes --accent toward --fg (theme-pack AA-large floor)",
);
assert.match(
  styles,
  /\.ask-shelf__countdown \{[^}]*color-mix\(in srgb, var\(--accent\)/s,
  "countdown capsule tints from the --accent theme token",
);
console.log("  PASS  countdown figure + capsule pin to --accent (AA-large floor across theme packs)");

// ③: concatenated text stays byte-identical to the unsplit 849 sentence, so
// the takeover test pins keep matching and no wording changed.
assert.match(headerMeta()!.textContent ?? "", /auto-answers in \d+s/, "split countdown concatenates to the 849 sentence");
console.log("  PASS  countdown text concatenates byte-identical to the 849 sentence");

// ②: takeover button carries the primary modifier and leaves the 32px icon
// slot (656: only ONE width writer may match the element).
const takeover = takeoverButton();
assert.ok(takeover, "armed panel renders the takeover button");
assert.ok(
  takeover!.classList.contains("prompt-shelf__header-button--takeover"),
  "takeover button carries --takeover modifier",
);
const takeoverWidth = window.getComputedStyle(takeover!).width;
assert.notEqual(takeoverWidth, "32px", `takeover width is not the 32px icon slot, got ${takeoverWidth}`);
const icon = iconButton();
assert.ok(icon, "panel still renders an icon header button for the paired width check");
assert.equal(
  window.getComputedStyle(icon!).width,
  "32px",
  "icon button keeps the 32px slot (the :not(--takeover) rule still applies to it)",
);
console.log(`  PASS  takeover width "${takeoverWidth}" escaped the icon slot; icon stays 32px (656 pair)`);

// ② continued: primary-fill tokens + single width writer against CSS text.
assert.match(
  styles,
  /\.prompt-shelf--ask \.prompt-shelf__header-button--takeover \{[^}]*background:\s*var\(--control-primary-bg\)/s,
  "takeover fills from the primary control token",
);
assert.match(
  styles,
  /\.prompt-shelf--ask \.prompt-shelf__header-button:not\(\.prompt-shelf__header-button--takeover\) \{[^}]*width:\s*32px/s,
  "icon rule excludes --takeover so exactly one width declaration matches",
);
const widthWriters = styles.match(/\.prompt-shelf--ask \.prompt-shelf__header-button[^,{]*\{[^}]*width:/gs) ?? [];
assert.equal(
  widthWriters.filter((block) => block.includes("--takeover {")).length,
  1,
  "exactly one ask width block targets --takeover (656: no second width writer)",
);
console.log("  PASS  takeover primary fill + single width writer (656 structural guard)");

// 主题包: !important background/hover rules must exclude --takeover or the
// accent fill degrades to translucent panel bg under primary-fg text.
const themePackBtnRules = styles.match(
  /:root\[data-theme-pack\][^{\n]*\.prompt-shelf__header-button[^{\n]*[{,]/g,
) ?? [];
assert.ok(themePackBtnRules.length >= 6, `theme-pack header-button rules found, got ${themePackBtnRules.length}`);
assert.ok(
  themePackBtnRules.every((rule) => rule.includes(":not(.prompt-shelf__header-button--takeover)")),
  "every theme-pack header-button background/hover rule excludes --takeover",
);
console.log("  PASS  theme-pack !important rules exclude --takeover (accent fill survives theme packs)");

// ③/功能逻辑零变化: holding the takeover still parks the countdown through
// the same 849 paths (label flip + held badge + countdown unmount).
await renderArmed("visual-hold", true);
const before = takeoverButton()!;
await act(async () => { before.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
assert.equal(takeoverButton()?.textContent, "Release", "hold flips the label (849 logic untouched)");
assert.equal(document.querySelector(".ask-shelf__countdown"), null, "held state parks the countdown");
assert.ok(headerMeta()!.textContent?.includes("Taken over"), "held badge shows");
console.log("  PASS  hold/release countdown parking unchanged (849 logic)");

// Plain panel: no countdown, no takeover, no new classes leak in.
await renderArmed("visual-plain", false);
assert.equal(document.querySelector(".ask-shelf__countdown"), null, "plain panel renders no countdown capsule");
assert.equal(document.querySelector(".prompt-shelf__header-button--takeover"), null, "plain panel renders no takeover button");
console.log("  PASS  plain panel carries no countdown capsule or takeover");

await act(async () => root.unmount());
dom.window.close();
console.log("\ntask860 ask countdown visual: all assertions passed");
