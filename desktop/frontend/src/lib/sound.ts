/**
 * 通知音效系统
 *
 * 支持合成音效和 WAV 文件播放两种模式，默认关闭。
 * 两个场景的偏好分别存入 localStorage:
 *   notificationSoundSuccess  —— 生成完成
 *   notificationSoundAttention —— AI 提问
 *   notificationSoundVolume —— 统一通知音量（0–100）
 *   值："off" | "synth" | "positive" | "correct" | "start" | "back"
 */

export type SoundWavPref = "off" | "synth" | "positive" | "correct" | "start" | "back";

const SUCCESS_KEY = "notificationSoundSuccess";
const ATTENTION_KEY = "notificationSoundAttention";
export const NOTIFICATION_VOLUME_STORAGE_KEY = "notificationSoundVolume";
export const NOTIFICATION_VOLUME_MIN = 0;
export const NOTIFICATION_VOLUME_MAX = 100;
export const DEFAULT_NOTIFICATION_VOLUME = 70;

function readPref(key: string): SoundWavPref {
  if (typeof localStorage === "undefined") return "off";
  const val = localStorage.getItem(key);
  if (val === "off" || val === "synth" || val === "positive" || val === "correct" || val === "start" || val === "back") return val;
  return "off";
}

function writePref(key: string, pref: SoundWavPref): void {
  if (typeof localStorage !== "undefined") {
    localStorage.setItem(key, pref);
  }
}

export function getSuccessPreference(): SoundWavPref { return readPref(SUCCESS_KEY); }
export function setSuccessPreference(pref: SoundWavPref): void { writePref(SUCCESS_KEY, pref); }
export function getAttentionPreference(): SoundWavPref { return readPref(ATTENTION_KEY); }
export function setAttentionPreference(pref: SoundWavPref): void { writePref(ATTENTION_KEY, pref); }

export function normalizeNotificationVolume(value: unknown): number {
  const raw = typeof value === "string" ? value.trim() : value;
  if (raw === "" || raw === null || raw === undefined) return DEFAULT_NOTIFICATION_VOLUME;
  const numeric = Number(raw);
  if (!Number.isFinite(numeric)) return DEFAULT_NOTIFICATION_VOLUME;
  return Math.min(NOTIFICATION_VOLUME_MAX, Math.max(NOTIFICATION_VOLUME_MIN, Math.round(numeric)));
}

export function getNotificationVolume(): number {
  if (typeof localStorage === "undefined") return DEFAULT_NOTIFICATION_VOLUME;
  try {
    const value = localStorage.getItem(NOTIFICATION_VOLUME_STORAGE_KEY);
    return value === null ? DEFAULT_NOTIFICATION_VOLUME : normalizeNotificationVolume(value);
  } catch {
    return DEFAULT_NOTIFICATION_VOLUME;
  }
}

export function setNotificationVolume(volume: number): number {
  const normalized = normalizeNotificationVolume(volume);
  try {
    if (typeof localStorage !== "undefined") {
      localStorage.setItem(NOTIFICATION_VOLUME_STORAGE_KEY, String(normalized));
    }
  } catch {
    // Private browsing and locked-down WebViews may reject localStorage writes.
  }
  return normalized;
}

export function notificationVolumeToGain(volume: unknown): number {
  return normalizeNotificationVolume(volume) / NOTIFICATION_VOLUME_MAX;
}

type WavSoundPref = Exclude<SoundWavPref, "off" | "synth">;

// The bundled WAV files differ by up to 4.1 LUFS. These trims normalize them
// to the quietest source (-18.9 LUFS) without boosting any asset above its
// recorded peak. The master volume is applied after the source trim.
const WAV_LOUDNESS_TRIM: Record<WavSoundPref, number> = {
  positive: 0.62,
  correct: 0.85,
  start: 1,
  back: 0.70,
};

export function notificationWavGain(pref: WavSoundPref, outputVolume: number): number {
  const safeVolume = Number.isFinite(outputVolume)
    ? Math.min(1, Math.max(0, outputVolume))
    : 0;
  return safeVolume * WAV_LOUDNESS_TRIM[pref];
}

function soundFilePath(pref: SoundWavPref): string {
  switch (pref) {
    case "positive": return "./sounds/mixkit-positive-notification-951.wav";
    case "correct":  return "./sounds/mixkit-correct-answer-tone-2870.wav";
    case "start":    return "./sounds/mixkit-software-interface-start-2574.wav";
    case "back":     return "./sounds/mixkit-software-interface-back-2575.wav";
    default:         return "";
  }
}

// ── WAV audio cache ──────────────────────────────────────────────────────────
const audioBufferCache = new Map<string, AudioBuffer>();

async function loadBuffer(ctx: AudioContext, url: string): Promise<AudioBuffer | null> {
  const cached = audioBufferCache.get(url);
  if (cached) return cached;
  try {
    const resp = await fetch(url);
    if (!resp.ok) return null;
    const arrayBuffer = await resp.arrayBuffer();
    const decoded = await ctx.decodeAudioData(arrayBuffer);
    audioBufferCache.set(url, decoded);
    return decoded;
  } catch {
    return null;
  }
}

function playBuffer(ctx: AudioContext, buffer: AudioBuffer, volume: number): void {
  const src = ctx.createBufferSource();
  src.buffer = buffer;
  const gain = ctx.createGain();
  gain.gain.value = volume;
  src.connect(gain);
  gain.connect(ctx.destination);
  src.start();
}

// ── Synthesised sounds ───────────────────────────────────────────────────────

function playSynthNote(ctx: AudioContext, dest: AudioNode, freq: number, startTime: number, duration: number, volume: number): void {
  const osc = ctx.createOscillator();
  osc.type = "sine";
  osc.frequency.setValueAtTime(freq, startTime);
  const gain = ctx.createGain();
  gain.gain.setValueAtTime(0, startTime);
  gain.gain.linearRampToValueAtTime(volume, startTime + 0.002);
  gain.gain.exponentialRampToValueAtTime(0.001, startTime + duration);
  osc.connect(gain);
  gain.connect(dest);
  osc.start(startTime);
  osc.stop(startTime + duration);

  const shimmer = ctx.createOscillator();
  shimmer.type = "sine";
  shimmer.frequency.setValueAtTime(freq * 4, startTime);
  const sGain = ctx.createGain();
  sGain.gain.setValueAtTime(0, startTime);
  sGain.gain.linearRampToValueAtTime(volume * 0.12, startTime + 0.002);
  sGain.gain.exponentialRampToValueAtTime(0.001, startTime + duration * 0.6);
  shimmer.connect(sGain);
  sGain.connect(dest);
  shimmer.start(startTime);
  shimmer.stop(startTime + duration);
}

function playSynthSuccess(ctx: AudioContext, outputVolume: number): void {
  playSynthNote(ctx, ctx.destination, 1318.5, 0, 0.20, outputVolume * 0.35);
  playSynthNote(ctx, ctx.destination, 1568.0, 0.07, 0.22, outputVolume * 0.30);
  playSynthNote(ctx, ctx.destination, 2093.0, 0.14, 0.30, outputVolume * 0.24);
}

function playSynthAttention(ctx: AudioContext, outputVolume: number): void {
  playSynthNote(ctx, ctx.destination, 1760.0, 0, 0.14, outputVolume * 0.40);
  playSynthNote(ctx, ctx.destination, 1318.5, 0.09, 0.22, outputVolume * 0.34);
}

// ── Play helpers ─────────────────────────────────────────────────────────────

async function playWav(pref: WavSoundPref, volume: number, fallback: (ctx: AudioContext, outputVolume: number) => void): Promise<void> {
  const url = soundFilePath(pref);
  if (!url) return;
  const ctx = new AudioContext();
  try {
    const buf = await loadBuffer(ctx, url);
    if (buf) {
      playBuffer(ctx, buf, notificationWavGain(pref, volume));
    } else {
      fallback(ctx, volume);
    }
  } catch {
    fallback(ctx, volume);
  }
  setTimeout(() => ctx.close(), 2000);
}

// ── Public API ───────────────────────────────────────────────────────────────

export function playSuccessChime(): void {
  const pref = getSuccessPreference();
  if (pref === "off") return;
  const volume = notificationVolumeToGain(getNotificationVolume());
  if (volume <= 0) return;
  if (pref === "synth") {
    try {
      const ctx = new AudioContext();
      playSynthSuccess(ctx, volume);
      setTimeout(() => ctx.close(), 600);
    } catch { /* silent */ }
  } else {
    void playWav(pref, volume, playSynthSuccess);
  }
}

export function playAttentionChime(): void {
  const pref = getAttentionPreference();
  if (pref === "off") return;
  const volume = notificationVolumeToGain(getNotificationVolume());
  if (volume <= 0) return;
  if (pref === "synth") {
    try {
      const ctx = new AudioContext();
      playSynthAttention(ctx, volume);
      setTimeout(() => ctx.close(), 500);
    } catch { /* silent */ }
  } else {
    void playWav(pref, volume, playSynthAttention);
  }
}

// ── Task 277 + 512: update-complete chime ────────────────────────────────────

/** localStorage key remembering which version already chimed (one-shot). */
export const UPDATE_CHIME_LAST_VERSION_KEY = "updateChimeLastVersion";

// ── Task 512: tune picker, playback rate, interaction cut ────────────────────

/** The two selectable melodies (settings update_chime_tune). */
export type UpdateChimeTune = "nokia" | "mario";

export const UPDATE_CHIME_TUNES: readonly UpdateChimeTune[] = ["nokia", "mario"];

export function normalizeUpdateChimeTune(value: unknown): UpdateChimeTune {
  return value === "mario" ? "mario" : "nokia";
}

/** Task 512: the chime plays at 1.25× so it reads as a prompt, not a concert. */
export const UPDATE_CHIME_PLAYBACK_RATE = 1.25;
/** Task 678 (user-finalized semantics, inverting task 512/598): the chime has
 *  a 10 s hard cap counted from playback start — with nobody around it is cut
 *  after 10 s regardless of how long the decoded buffer is. */
export const UPDATE_CHIME_MAX_PLAY_MS = 10000;
/** Task 512: the cut is a short fade instead of a hard stop — an aborted
 *  buffer otherwise ends with an audible click/pop. */
export const UPDATE_CHIME_FADE_OUT_S = 0.2;

// Nintendo owns the Mario theme (task 512 copyright ruling, option A): the
// asset is loaded through a build-time-guarded dynamic import. The
// __CHIME_LOCAL_ASSETS__ define is only true when the local build sets
// REASONIX_CHIME_LOCAL_ASSETS=1; every public packaging path leaves it unset,
// so the guarded import is dead code, the wav never reaches dist, and the
// "mario" selection resolves back to the Nokia tune at runtime.
declare const __CHIME_LOCAL_ASSETS__: boolean;

// Test seam: the bare node runner never sees the build-time define, so tests
// override the flag to exercise both build flavors. Production never sets it.
let chimeLocalAssetsOverride: boolean | null = null;

export function setChimeLocalAssetsForTests(value: boolean | null): void {
  chimeLocalAssetsOverride = value;
}

export function chimeLocalAssetsEnabled(): boolean {
  if (chimeLocalAssetsOverride !== null) return chimeLocalAssetsOverride;
  try {
    return typeof __CHIME_LOCAL_ASSETS__ !== "undefined" && __CHIME_LOCAL_ASSETS__ === true;
  } catch {
    return false;
  }
}

let marioAssetUrlPromise: Promise<string | null> | null = null;

async function loadMarioAssetUrlDefault(): Promise<string | null> {
  // Task 598 (found while verifying the local-build fix): the UI-side
  // chimeLocalAssetsEnabled() gate below is a runtime function, so the
  // bundler could not prove this import dead and every build — public ones
  // included — emitted the Nintendo wav into dist/assets, defeating the
  // task-512 copyright ruling. Folding the raw define here (the typeof
  // guard keeps the bare-node test runner, which has no define, alive) makes
  // the guarded import statically unreachable in public builds: after the
  // define is substituted, `!__CHIME_LOCAL_ASSETS__` folds to `!false` and
  // the wav is tree-shaken out of dist for real.
  if (typeof __CHIME_LOCAL_ASSETS__ === "undefined") return null;
  if (!__CHIME_LOCAL_ASSETS__) return null;
  marioAssetUrlPromise ??= import("../assets/sounds/mario-theme.wav?url")
    .then((mod) => mod.default)
    .catch(() => null);
  return marioAssetUrlPromise;
}

// Test seam: the real loader dynamic-imports the wav, which a bare node test
// runner cannot parse. Production code never calls the setter.
let marioAssetUrlLoader: () => Promise<string | null> = loadMarioAssetUrlDefault;

export function setMarioAssetLoaderForTests(loader: (() => Promise<string | null>) | null): void {
  marioAssetUrlLoader = loader ?? loadMarioAssetUrlDefault;
  if (loader) marioAssetUrlPromise = null;
}

function loadMarioAssetUrl(): Promise<string | null> {
  if (!chimeLocalAssetsEnabled()) return Promise.resolve(null);
  return marioAssetUrlLoader();
}

/** Resolve the playable URL for a tune. Public builds (no local assets) fall
 *  back from Mario to the bundled Nokia wav rather than muting the chime. */
export async function resolveUpdateChimeUrl(tune: UpdateChimeTune): Promise<string> {
  if (tune === "mario") {
    const mario = await loadMarioAssetUrl();
    if (mario) return mario;
  }
  return "./sounds/nokia-tune.wav";
}

export type UpdateChimePlayOptions = {
  /** Tune chosen in settings (config update_chime_tune). Defaults to nokia. */
  tune?: UpdateChimeTune;
  /** Test seam: override the AudioContext factory. */
  audioCtxFactory?: () => AudioContext;
  /** Test seam: override the shared notification volume. */
  volume?: number;
};

/**
 * Play the update chime buffer at 1.25× with the task-678 cut armed: a 10 s
 * hard cap counts from playback start, and any document-level
 * pointermove/mousemove/click cuts the chime immediately (movement means the user is present — no
 * need to play it out). The listeners exist only while this playback lives —
 * natural end or cut detaches them immediately. Output rides the same
 * AudioContext → default-output path as every notification chime (system mute
 * applies). Load failure keeps the old fail-open-to-synth fallback.
 * Exported for the task-512 tests (they need a awaitable, seam-injected run).
 */
export async function playUpdateChimeSound(volume: number, tune: UpdateChimeTune, ctxFactory?: () => AudioContext): Promise<void> {
  const ctx = ctxFactory ? ctxFactory() : new AudioContext();
  try {
    const url = await resolveUpdateChimeUrl(tune);
    const buf = await loadBuffer(ctx, url);
    if (buf) {
      playUpdateChimeBuffer(ctx, buf, volume);
    } else {
      playSynthSuccess(ctx, volume);
    }
  } catch {
    playSynthSuccess(ctx, volume);
  }
  setTimeout(() => ctx.close(), 2000);
}

function playUpdateChimeBuffer(ctx: AudioContext, buffer: AudioBuffer, volume: number): void {
  const src = ctx.createBufferSource();
  src.buffer = buffer;
  src.playbackRate.value = UPDATE_CHIME_PLAYBACK_RATE;
  const gain = ctx.createGain();
  gain.gain.value = volume;
  src.connect(gain);
  gain.connect(ctx.destination);

  let fadeStarted = false;  // the fade ramp has been scheduled
  let cutTimer: ReturnType<typeof setTimeout> | null = null;

  // detach closes the listening window; safe to call from every path.
  const detach = () => {
    document.removeEventListener("pointermove", onUserPresent);
    document.removeEventListener("mousemove", onUserPresent);
    document.removeEventListener("click", onUserPresent);
    if (cutTimer !== null) clearTimeout(cutTimer);
    cutTimer = null;
  };

  const startFade = () => {
    if (fadeStarted) return;
    fadeStarted = true;
    try {
      const now = ctx.currentTime;
      const current = gain.gain.value;
      gain.gain.cancelScheduledValues(now);
      gain.gain.setValueAtTime(current, now);
      gain.gain.linearRampToValueAtTime(0, now + UPDATE_CHIME_FADE_OUT_S);
      src.stop(now + UPDATE_CHIME_FADE_OUT_S);
    } catch { /* the source may have finished already */ }
  };

  // Natural end — or the fade-stop firing onended — closes the window.
  src.onended = () => detach();

  // Task 678: any pointer/mouse movement or click means someone is at the
  // machine — cut right away instead of playing on. No travel threshold: the
  // first movement event itself is the presence signal (the task-512 12 px
  // threshold existed to keep the chime alive under the old "play on after
  // interaction" semantics and is meaningless here). pointermove is listed
  // alongside mousemove so touch/pen input counts too; for a plain mouse the
  // browser fires both per move and the first hit detaches all listeners, so
  // there is no double-cut path.
  const onUserPresent = () => {
    detach();
    startFade();
  };

  document.addEventListener("pointermove", onUserPresent, { passive: true });
  document.addEventListener("mousemove", onUserPresent, { passive: true });
  document.addEventListener("click", onUserPresent, { passive: true });

  // Task 678: the hard cap is armed at playback start, not by an interaction —
  // even with nobody around the chime stops after UPDATE_CHIME_MAX_PLAY_MS.
  cutTimer = setTimeout(() => {
    detach();
    startFade();
  }, UPDATE_CHIME_MAX_PLAY_MS);

  src.start();
}

/** Fire-and-forget entry used by the startup gate in App.tsx. */
export function playUpdateChime(options?: UpdateChimePlayOptions): void {
  const volume = options?.volume ?? notificationVolumeToGain(getNotificationVolume());
  if (volume <= 0) return;
  const tune = normalizeUpdateChimeTune(options?.tune);
  void playUpdateChimeSound(volume, tune, options?.audioCtxFactory);
}

export type UpdateChimeOutcome =
  | "played"
  | "disabled"
  | "already-chimed"
  | "no-active-version"
  | "no-storage";

export type UpdateChimeOptions = {
  /** Lab switch (config update_chime). Off ⇒ zero playback, but the seen
   *  version is still recorded so flipping the switch later never replays a
   *  version the user already (knowingly) skipped. */
  enabled: boolean;
  listVersions: () => Promise<Array<{ version: string; active: boolean }>>;
  play?: () => void;
  storage?: Pick<Storage, "getItem" | "setItem">;
};

/**
 * One-shot gate for the update chime: plays only when this launch sees a
 * version that has never chimed before (first launch after a version swap).
 * The seen-version record is written BEFORE playback so a disabled switch or
 * a crash can never arm a replay on a later launch.
 */
export async function maybePlayUpdateChime(options: UpdateChimeOptions): Promise<UpdateChimeOutcome> {
  const storage = options.storage
    ?? (typeof localStorage === "undefined" ? undefined : localStorage);
  if (!storage) return "no-storage";
  let versions: Array<{ version: string; active: boolean }>;
  try {
    versions = await options.listVersions();
  } catch {
    return "no-active-version";
  }
  const active = versions.find((entry) => entry.active)?.version;
  if (!active) return "no-active-version";
  if (storage.getItem(UPDATE_CHIME_LAST_VERSION_KEY) === active) return "already-chimed";
  try {
    storage.setItem(UPDATE_CHIME_LAST_VERSION_KEY, active);
  } catch {
    // Storage full/blocked: never let the chime gate fail open into a replay.
    return "no-storage";
  }
  if (!options.enabled) return "disabled";
  options.play?.();
  return "played";
}

export type AttentionChimeEvent = {
  kind?: string;
  tabId?: string;
  approval?: { id?: string };
  ask?: { id?: string };
};

export function attentionChimeEventKey(event: AttentionChimeEvent): string | undefined {
  if (event.kind === "approval_request" && event.approval?.id) return `approval:${event.tabId ?? ""}:${event.approval.id}`;
  if (event.kind === "ask_request" && event.ask?.id) return `ask:${event.tabId ?? ""}:${event.ask.id}`;
  return undefined;
}

// attentionChimeSeenCap bounds the dedupe set. Prompt ids are unique per
// prompt, so the set only ever grows; past the cap the oldest half is dropped
// (insertion order) — replay dedupe only needs to cover recently replayed
// prompts, not the whole session history.
const attentionChimeSeenCap = 512;

// clearAttentionChimeKeys drops dedupe keys after a runtime rebuild. Approval
// and ask ids are per-controller counters starting at "1", so a rebuilt
// controller (model/effort/settings switch) reissues ids an earlier prompt on
// the same tab already used — without this, the first prompt after a rebuild
// is misread as a replay and stays silent. A ready event without a tab id
// (settings rebuilds emit tab-less ready) clears everything: over-clearing
// only re-chimes a replayed pending prompt, which is a desirable reminder,
// while under-clearing mutes a live prompt.
export function clearAttentionChimeKeys(seen: Set<string>, tabId?: string): void {
  if (tabId === undefined || tabId === "") {
    seen.clear();
    return;
  }
  for (const key of [...seen]) {
    if (key.startsWith(`approval:${tabId}:`) || key.startsWith(`ask:${tabId}:`)) {
      seen.delete(key);
    }
  }
}

export function shouldPlayAttentionChimeForEvent(event: AttentionChimeEvent, seen: Set<string>): boolean {
  const key = attentionChimeEventKey(event);
  if (!key || seen.has(key)) return false;
  if (seen.size >= attentionChimeSeenCap) {
    let drop = seen.size - attentionChimeSeenCap / 2;
    for (const k of seen) {
      if (drop-- <= 0) break;
      seen.delete(k);
    }
  }
  seen.add(key);
  return true;
}
