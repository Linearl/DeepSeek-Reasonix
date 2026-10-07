// Task 512 acceptance (update chime: 1.25× rate + interaction cut + tune dial):
//  1. 1.25× playback — the decoded buffer source's playbackRate is 1.25;
//  2. interaction cut — the first qualifying mousemove/click starts a 10 s
//     countdown (task 598; was 3 s), after which the chime fades out over
//     200 ms and stops; the document-level listeners live only for this
//     playback and are detached on countdown-arm, natural end and cut alike;
//  3. move threshold — a mousemove only arms the countdown once per-event
//     travel exceeds UPDATE_CHIME_MOVE_THRESHOLD_PX (brushing the mouse is
//     ignored); any click always arms;
//  4. tune dial — "nokia" fetches the bundled Nokia wav; "mario" in a local
//     build fetches the guarded asset; "mario" in a public build falls back to
//     Nokia (Nintendo asset compiled out — copyright ruling, option A);
//  5. wiring guards — SettingsPanel select, config field/setter, render table,
//     bridge surface, three locales, and the __CHIME_LOCAL_ASSETS__ build gate.
//
// Existing semantics stay untouched (regressed by task277-update-chime.test):
// one-shot gate, off⇒zero-playback-but-record, fail-closed, same output path.
//
// Run: npx tsx src/__tests__/task512-chime-rate-interrupt.test.ts

import {
  playUpdateChimeSound,
  resolveUpdateChimeUrl,
  normalizeUpdateChimeTune,
  setMarioAssetLoaderForTests,
  setChimeLocalAssetsForTests,
  UPDATE_CHIME_PLAYBACK_RATE,
  UPDATE_CHIME_INTERRUPT_DELAY_MS,
  UPDATE_CHIME_FADE_OUT_S,
  UPDATE_CHIME_MOVE_THRESHOLD_PX,
} from "../lib/sound";
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

// ── environment seams (bare node run: no DOM, no WebAudio, no fetch) ─────────

const docListeners = new Map<string, Set<(event: MouseEvent) => void>>();
(globalThis as Record<string, unknown>).document = {
  addEventListener: (type: string, fn: (event: MouseEvent) => void) => {
    if (!docListeners.has(type)) docListeners.set(type, new Set());
    docListeners.get(type)!.add(fn);
  },
  removeEventListener: (type: string, fn: (event: MouseEvent) => void) => {
    docListeners.get(type)?.delete(fn);
  },
};
function listenerCount(type: string): number {
  return docListeners.get(type)?.size ?? 0;
}
function emitMouse(type: string, x: number, y: number): void {
  const event = { type, clientX: x, clientY: y } as unknown as MouseEvent;
  for (const fn of [...(docListeners.get(type) ?? [])]) fn(event);
}

type FakeSource = {
  buffer: unknown;
  playbackRate: { value: number };
  onended: (() => void) | null;
  connect: () => void;
  start: () => void;
  stop: (when?: number) => void;
};
function makeFakeCtx(events: string[], volume: number) {
  const source: FakeSource = {
    buffer: null,
    playbackRate: { value: 1 },
    onended: null,
    connect() { events.push("connect"); },
    start() { events.push("start"); },
    stop(when?: number) { events.push(`stop:${when}`); },
  };
  const gainNode = {
    gain: {
      value: volume,
      cancelScheduledValues(t: number) { events.push(`cancel:${t}`); },
      setValueAtTime(v: number, t: number) { events.push(`set:${v}@${t}`); },
      linearRampToValueAtTime(v: number, t: number) { events.push(`ramp:${v}@${t}`); },
    },
    connect() { events.push("connect-gain"); },
  };
  const ctx = {
    currentTime: 5,
    destination: { kind: "destination" },
    createBufferSource: () => source,
    createGain: () => gainNode,
    decodeAudioData: async () => ({ duration: 3.78 }),
    close: async () => { events.push("close"); },
  };
  return { ctx: ctx as unknown as AudioContext, source, gain: gainNode };
}

const fetchedUrls: string[] = [];
(globalThis as Record<string, unknown>).fetch = async (input: unknown) => {
  fetchedUrls.push(String(input));
  return { ok: true, arrayBuffer: async () => new ArrayBuffer(16) };
};

const pendingTimers = new Map<number, { fn: () => void; delay: number }>();
let timerSeq = 1;
const realSetTimeout = globalThis.setTimeout.bind(globalThis);
const realClearTimeout = globalThis.clearTimeout.bind(globalThis);
(globalThis as Record<string, unknown>).setTimeout = (fn: () => void, delay?: number) => {
  const id = timerSeq++;
  pendingTimers.set(id, { fn, delay: delay ?? 0 });
  return id as unknown as ReturnType<typeof setTimeout>;
};
(globalThis as Record<string, unknown>).clearTimeout = (id: number) => {
  pendingTimers.delete(Number(id));
};
function fireTimersWithDelay(delay: number): number {
  let fired = 0;
  for (const [id, timer] of [...pendingTimers]) {
    if (timer.delay === delay) {
      pendingTimers.delete(id);
      timer.fn();
      fired += 1;
    }
  }
  return fired;
}

function teardown(): void {
  docListeners.clear();
  pendingTimers.clear();
  fetchedUrls.length = 0;
  setMarioAssetLoaderForTests(null);
  setChimeLocalAssetsForTests(null);
}

// ── 1. playbackRate is 1.25 (acceptance ①) ───────────────────────────────────
{
  teardown();
  const events: string[] = [];
  const { ctx, source } = makeFakeCtx(events, 0.7);
  await playUpdateChimeSound(0.7, "nokia", () => ctx);
  ok(source.playbackRate.value === UPDATE_CHIME_PLAYBACK_RATE, `playbackRate = ${source.playbackRate.value} (want ${UPDATE_CHIME_PLAYBACK_RATE})`);
  ok(events.includes("start"), "buffer source started");
  ok(fetchedUrls[0] === "./sounds/nokia-tune.wav", "nokia tune fetched from the bundled public wav");
  ok(listenerCount("mousemove") === 1 && listenerCount("click") === 1, "listening window: mousemove + click armed during playback");
  teardown();
}

// ── 2. interaction cut (acceptance ②) ────────────────────────────────────────
{
  teardown();
  const events: string[] = [];
  const { ctx, source } = makeFakeCtx(events, 0.7);
  await playUpdateChimeSound(0.7, "nokia", () => ctx);

  // A small travel (hand brushing the mouse) must NOT arm the countdown.
  emitMouse("mousemove", 100, 100);
  emitMouse("mousemove", 105, 103); // travel ≈ 5.8px < 12px threshold
  ok(listenerCount("mousemove") === 1 && listenerCount("click") === 1, "sub-threshold mousemove does not arm the cut");
  ok(![...pendingTimers.values()].some((t) => t.delay === UPDATE_CHIME_INTERRUPT_DELAY_MS), "no countdown armed yet");

  // A qualifying mousemove (≥ threshold) arms the 3 s countdown and detaches
  // the listeners immediately.
  emitMouse("mousemove", 130, 118); // travel ≈ 32px ≥ 12px
  ok(listenerCount("mousemove") === 0 && listenerCount("click") === 0, "first qualifying mousemove detaches the listening window");
  ok(events.every((e) => !e.startsWith("stop:")), "cut not fired before the delay elapses");

  // Later events cannot restart anything (listeners are gone).
  emitMouse("mousemove", 500, 500);
  emitMouse("click", 500, 500);

  // 3 s later: fade out over 200 ms, then stop. (The ctx-close timer at 2 s
  // has its own delay and is not part of the countdown.)
  const fired = fireTimersWithDelay(UPDATE_CHIME_INTERRUPT_DELAY_MS);
  ok(fired === 1, "exactly one countdown timer fired");
  ok(events.includes(`cancel:5`) && events.includes("set:0.7@5") && events.includes(`ramp:0@${5 + UPDATE_CHIME_FADE_OUT_S}`), `fade-out scheduled over ${UPDATE_CHIME_FADE_OUT_S}s (no hard stop)`);
  ok(events.includes(`stop:${5 + UPDATE_CHIME_FADE_OUT_S}`), `source stopped at +${UPDATE_CHIME_FADE_OUT_S}s`);
  ok(source.onended !== null, "onended handler present (natural-end cleanup path)");

  // The stop's onended detach is idempotent.
  source.onended?.();
  ok(listenerCount("mousemove") === 0 && listenerCount("click") === 0, "post-cut onended keeps the window closed");
  teardown();
}

// ── 3. any click arms the cut without a threshold ────────────────────────────
{
  teardown();
  const events: string[] = [];
  const { ctx } = makeFakeCtx(events, 0.7);
  await playUpdateChimeSound(0.7, "nokia", () => ctx);
  emitMouse("click", 42, 42);
  ok(listenerCount("mousemove") === 0 && listenerCount("click") === 0, "any click arms the cut (no threshold on click)");
  const armed = [...pendingTimers.values()].some((t) => t.delay === UPDATE_CHIME_INTERRUPT_DELAY_MS);
  ok(armed, `countdown armed at ${UPDATE_CHIME_INTERRUPT_DELAY_MS}ms`);
  teardown();
}

// ── 4. natural end detaches the listening window ─────────────────────────────
{
  teardown();
  const events: string[] = [];
  const { ctx, source } = makeFakeCtx(events, 0.7);
  await playUpdateChimeSound(0.7, "nokia", () => ctx);
  source.onended?.();
  ok(listenerCount("mousemove") === 0 && listenerCount("click") === 0, "natural end detaches the listeners");
  ok(pendingTimers.size <= 1, "no stray countdown after natural end (only the ctx-close timer may remain)");
  teardown();
}

// ── 5. tune dial (acceptance ③ wiring + ⑤ copyright fallback) ────────────────
{
  ok(normalizeUpdateChimeTune("nokia") === "nokia" && normalizeUpdateChimeTune("mario") === "mario", "normalize keeps the two known tunes");
  ok(normalizeUpdateChimeTune("bogus") === "nokia" && normalizeUpdateChimeTune(undefined) === "nokia" && normalizeUpdateChimeTune("") === "nokia", "normalize falls back to nokia on empty/unknown");
}
{
  teardown();
  // Public build (no local assets — the tsx runner never defines the flag):
  // even with a Mario loader present, the guarded resolve never touches it.
  let marioTouched = false;
  setMarioAssetLoaderForTests(async () => { marioTouched = true; return "/assets/mario-theme-HASH.wav"; });
  ok(await resolveUpdateChimeUrl("mario") === "./sounds/nokia-tune.wav", "public build: mario selection resolves back to the Nokia wav");
  ok(!marioTouched, "public build: the guarded Mario loader is never invoked");
  ok(await resolveUpdateChimeUrl("nokia") === "./sounds/nokia-tune.wav", "nokia resolves to the bundled wav");
  teardown();
}
{
  teardown();
  // Local build seam: the flag override + the loader run and its asset URL is
  // fetched.
  setChimeLocalAssetsForTests(true);
  setMarioAssetLoaderForTests(async () => "/assets/mario-theme-HASH.wav");
  ok(await resolveUpdateChimeUrl("mario") === "/assets/mario-theme-HASH.wav", "local build: mario resolves to the guarded asset url");
  const events: string[] = [];
  const { ctx, source } = makeFakeCtx(events, 0.7);
  await playUpdateChimeSound(0.7, "mario", () => ctx);
  ok(fetchedUrls[0] === "/assets/mario-theme-HASH.wav", "local build: playback fetches the mario asset");
  ok(source.playbackRate.value === UPDATE_CHIME_PLAYBACK_RATE, "1.25× applies to the mario tune too");
  teardown();
}

// ── 6. wiring guards (acceptance ③④⑤ source-level) ──────────────────────────
{
  const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");

  const sound = read("../lib/sound.ts");
  ok(sound.includes("src.playbackRate.value = UPDATE_CHIME_PLAYBACK_RATE;"), "sound: buffer source carries the playback rate");
  ok(sound.includes("setTimeout(startFade, UPDATE_CHIME_INTERRUPT_DELAY_MS)"), "sound: countdown arms the cut");
  ok(sound.includes("document.removeEventListener(\"mousemove\", onInteract)") && sound.includes("document.removeEventListener(\"click\", onInteract)"), "sound: listening window is always detached");
  const guardIdx = sound.indexOf("if (!chimeLocalAssetsEnabled()) return Promise.resolve(null);");
  const loaderCallIdx = sound.indexOf("return marioAssetUrlLoader();");
  const importIdx = sound.indexOf("../assets/sounds/mario-theme.wav?url");
  ok(guardIdx >= 0 && loaderCallIdx > guardIdx && importIdx >= 0, "sound: mario asset only reachable through the local-assets guard");
  ok(!sound.includes('playWav("positive", volume, playSynthSuccess)'), "sound: update chime no longer rides the old positive wav");

  const panel = read("../components/SettingsPanel.tsx");
  ok(panel.includes("app.SetUpdateChimeTune(value)"), "panel: tune select calls SetUpdateChimeTune");
  ok(panel.includes('t("settings.updateChimeTune")') && panel.includes('settings.updateChimeTune.${tune}'), "panel: tune labels are locale-driven");
  ok(panel.includes('chimeLocalAssetsEnabled() ? UPDATE_CHIME_TUNES : ["nokia"] as const'), "panel: public builds list Nokia only");

  const appTsx = read("../App.tsx");
  ok(appTsx.includes("playUpdateChime({ tune: normalizeUpdateChimeTune(view.updateChimeTune) })"), "App: startup gate passes the configured tune through");

  const bridge = read("../lib/bridge.ts");
  ok(bridge.includes("SetUpdateChimeTune(tune: string): Promise<void>;"), "bridge: interface carries SetUpdateChimeTune");
  ok(bridge.includes("async SetUpdateChimeTune() {}"), "bridge: mock carries SetUpdateChimeTune");

  const viewTypes = read("../lib/settingsViewTypes.ts");
  ok(viewTypes.includes("updateChimeTune?: string;"), "SettingsView carries updateChimeTune");
  const startupView = read("../lib/types.ts");
  ok(startupView.includes("updateChimeTune?: string;"), "DesktopStartupSettingsView carries updateChimeTune");

  const config = read("../../../../internal/config/desktop_preferences.go");
  ok(config.includes('toml:"update_chime_tune"'), "config: update_chime_tune persisted");
  const edit = read("../../../../internal/config/edit.go");
  ok(edit.includes("func (c *Config) SetUpdateChimeTune"), "config: tune setter exists");
  const prefs = read("../../../settings_preferences.go");
  ok(prefs.includes("func (a *App) SetUpdateChimeTune"), "App: tune setter exists");

  const settingsApp = read("../../../settings_app.go");
  const viewFields = (settingsApp.match(/UpdateChimeTune\s+string\s+`json:"updateChimeTune"`/g) ?? []).length;
  const viewAssigns = (settingsApp.match(/UpdateChimeTune:?\s*=?\s*cfg\.UpdateChimeTuneMode\(\)/g) ?? []).length;
  ok(viewFields === 2, `render table: 2 view structs carry UpdateChimeTune (got ${viewFields}) — 81/123 lesson`);
  ok(viewAssigns === 2, `render table: 2 assignments wired (got ${viewAssigns})`);

  const viteConfig = read("../../vite.config.ts");
  ok(viteConfig.includes('process.env.REASONIX_CHIME_LOCAL_ASSETS === "1"'), "vite: local-assets define is env-gated (public builds default false)");
  ok(viteConfig.includes("__CHIME_LOCAL_ASSETS__: JSON.stringify(chimeLocalAssets)"), "vite: define reaches the bundle");

  for (const dialect of ["zh", "en", "zh-TW"]) {
    const locale = read(`../locales/${dialect}.ts`);
    ok(locale.includes('"settings.updateChimeTune"') && locale.includes('"settings.updateChimeTuneHint"'), `locale ${dialect}: tune label + hint keys`);
    ok(locale.includes('"settings.updateChimeTune.nokia"') && locale.includes('"settings.updateChimeTune.mario"'), `locale ${dialect}: both tune options`);
    ok(!locale.includes("约 3 秒") && !locale.includes("~3 second") && !locale.includes("約 3 秒"), `locale ${dialect}: "about 3 seconds" wording removed`);
  }

  // Public builds must not ship the Nintendo asset (acceptance ⑤ evidence).
  const publicSounds = fileURLToPath(new URL("../../public/sounds/", import.meta.url));
  const publicFiles = existsSync(publicSounds) ? readdirSync(publicSounds) : [];
  ok(!publicFiles.some((name) => name.toLowerCase().includes("mario")), "public/sounds carries no Mario asset");
  ok(publicFiles.includes("nokia-tune.wav"), "public/sounds carries the Nokia wav");
  ok(existsSync(fileURLToPath(new URL("../../src/assets/sounds/mario-theme.wav", import.meta.url))), "src asset tree carries the guarded mario wav (imported only under the build flag)");
}

// ── restore the real timers/fetch/document ───────────────────────────────────
(globalThis as Record<string, unknown>).setTimeout = realSetTimeout;
(globalThis as Record<string, unknown>).clearTimeout = realClearTimeout;
delete (globalThis as Record<string, unknown>).document;
delete (globalThis as Record<string, unknown>).fetch;

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
