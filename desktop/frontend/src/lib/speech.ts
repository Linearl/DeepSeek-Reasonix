// tl1709: read-aloud support for the transcript selection menu. Pure frontend
// Web Speech API (`window.speechSynthesis`) — zero backend, zero network: the
// OS-provided TTS voices play the selected text directly.
//
// Contract kept here (unit-pinned in tl1709-selection-speak.test.tsx):
// - language is auto-detected from the text (CJK share threshold), not configured
// - oversized selections are truncated to MAX_SPEAK_CHARS (a single utterance
//   must stay well under the Chromium per-utterance limit; "simple first" per
//   the task spec — truncation over queueing many chunks)
// - every failure lands in a visible state, never a silent no-op:
//   unsupported environment / no voices at all / no voice for the language
// - the caller owns the toggle: speakSelection cancels any queued speech first,
//   cancelSpeech stops the current playback
//
// tl1815: mixed zh/en selections no longer ride a single utterance. Text is
// split into CJK / non-CJK runs and each run is spoken with a voice whose
// language matches (zh run -> zh voice, en run -> en voice), so an English
// word inside Chinese prose no longer sounds stiff and vice versa. When no
// voice exists for a segment's language the segment still speaks (utterance
// lang hint, engine best effort) and the caller shows install guidance —
// a missing English pack is no longer a hard error.

export type SpeakLang = "zh-CN" | "en-US";

// Chromium engines accept far more than this, but voices themselves can choke
// on very long utterances; 5k chars (~minutes of audio) keeps one utterance
// safely inside every observed limit.
export const MAX_SPEAK_CHARS = 5000;

export type SpeechSupport = "unsupported" | "no-voices" | "no-match" | "ready";

// Han + kana + hangul ranges count toward the CJK share. kana/hangul ride the
// zh voice family when no dedicated pack exists — a best-effort default the
// selection menu treats as "Chinese speech".
const CJK_CHAR_RE = /[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uac00-\ud7af]/g;

// CJK share above ~25% switches to the Chinese voice; anything else reads as
// English. Mixed CJK prose with stray latin words/numbers still reads zh.
export function detectSpeakLang(text: string): SpeakLang {
  const cjk = text.match(CJK_CHAR_RE)?.length ?? 0;
  return cjk * 3 > text.length ? "zh-CN" : "en-US";
}

// Prime the voice list early: Chromium loads voices asynchronously and the
// very first getVoices() call can return an empty list. Calling this on menu
// mount means the click-time query below almost never races the load.
export function primeSpeechVoices(): void {
  try {
    if (typeof window === "undefined") return;
    window.speechSynthesis?.getVoices();
  } catch {
    // Reading voices must never throw into the caller.
  }
}

function voiceLangKey(lang: unknown): string {
  return String(lang ?? "").replace("_", "-").toLowerCase();
}

// Click-time support check. `lang` is the BCP-47 target ("zh-CN" / "en-US");
// a voice matches when its normalized language prefix agrees (zh-CN voice
// serves zh-HK text and vice versa).
export function speechSupport(lang: string): SpeechSupport {
  const availability = speechAvailability();
  if (availability !== "ready") return availability;
  return findVoiceFor(lang) ? "ready" : "no-match";
}

export type SpeechAvailability = "unsupported" | "no-voices" | "ready";

// tl1815: environment-level check without the per-language match — the caller
// degrades per segment language instead of failing on the first no-match.
export function speechAvailability(): SpeechAvailability {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.speak !== "function") return "unsupported";
  let voices: Array<{ lang?: string }> = [];
  try {
    voices = typeof synth.getVoices === "function" ? synth.getVoices() : [];
  } catch {
    return "no-voices";
  }
  if (!voices || voices.length === 0) return "no-voices";
  return "ready";
}

// tl1815: pick the best installed voice for `lang` — exact BCP-47 match wins,
// then the first language-prefix match (zh-CN voice serves zh-HK text and
// vice versa). Returns null when the pack is missing; the caller degrades.
export function findVoiceFor(lang: string): SpeechSynthesisVoice | null {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.getVoices !== "function") return null;
  let voices: Array<{ lang?: string }> = [];
  try {
    voices = synth.getVoices();
  } catch {
    return null;
  }
  const want = lang.slice(0, 2).toLowerCase();
  const matched = voices.filter((voice) => voiceLangKey(voice.lang).startsWith(want));
  if (matched.length === 0) return null;
  const target = voiceLangKey(lang);
  const exact = matched.find((voice) => voiceLangKey(voice.lang) === target);
  return (exact ?? matched[0]) as SpeechSynthesisVoice;
}

export type SpeakSegment = { text: string; lang: SpeakLang };

// zh "letters" — the speakable CJK scripts, excluding the punctuation block.
const ZH_LETTER_RE = /[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uac00-\ud7af]/;

// Runs shorter than this that carry no letters of their own language ride the
// neighboring voice instead of flipping it. Covers bare numbers with their
// surrounding spaces (" 123 ", " 2024 "), decimals (" 2.0 "), list markers
// ("1. "), and lone CJK punctuation ("。" 「」). Runs with latin letters never
// merge — real English words keep the en voice.
const MERGE_MAX_CHARS = 8;

// tl1815 defensive cap: pathological alternation (one char zh, one char en,
// repeated) could otherwise queue thousands of one-char utterances. Beyond
// this the shortest adjacent pair merges until under the cap — speech stays
// finite no matter the input shape.
export const MAX_SPEAK_SEGMENTS = 200;

// Segmentation character classes. zh = CJK text (han/kana/hangul, same ranges
// detectSpeakLang uses) plus the punctuation that only appears in CJK prose
// (CJK symbols block, fullwidth forms, and the general-punct marks the zh IME
// emits: em dash, curly quotes, ellipsis, middle dot). Ambiguous marks are
// deliberately zh-classed: the merge pass below reattaches them to a latin
// run whenever their context is English, so misclassification self-heals.
const ZH_CHAR_RE = /[\u3040-\u30ff\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uac00-\ud7af\u3000-\u303f\uff01-\uff65\u2014\u2018\u2019\u201c\u201d\u2026\u00b7]/;
const LATIN_LETTER_RE = /[A-Za-z]/;

function isZhChar(ch: string): boolean {
  return ZH_CHAR_RE.test(ch);
}

// Build same-language runs, then apply two merge rules so stray punctuation
// and bare numbers do not spawn their own voice flip:
// 1. a short non-CJK run with no latin letters (digits "123", "2.0", spacing,
//    stray symbols) merges into an adjacent zh run — the zh voice reads
//    digits naturally in context and no voice flapping mid-sentence;
// 2. a short zh run with no CJK letters (a lone "。" or 「」 around English)
//    merges into an adjacent en run for the same reason.
// Real words never merge: "git" in 用 git 提交 stays on the en voice.
export function segmentSpeakText(text: string): SpeakSegment[] {
  const runs: SpeakSegment[] = [];
  for (const ch of text) {
    const lang: SpeakLang = isZhChar(ch) ? "zh-CN" : "en-US";
    const last = runs[runs.length - 1];
    if (last && last.lang === lang) last.text += ch;
    else runs.push({ text: ch, lang });
  }
  // Fixpoint merge pass: each iteration merges the first mergeable run into
  // its preferred neighbor (left wins; right when the run is at an edge).
  // Bounded: every merge shrinks the list, so it terminates.
  let changed = true;
  while (changed && runs.length > 1) {
    changed = false;
    for (let i = 0; i < runs.length; i++) {
      const run = runs[i];
      const hasZhLetters = ZH_LETTER_RE.test(run.text);
      const hasLatinLetters = LATIN_LETTER_RE.test(run.text);
      const mergeable = run.lang === "en-US"
        ? !hasLatinLetters && run.text.length <= MERGE_MAX_CHARS
        : !hasZhLetters && run.text.length <= MERGE_MAX_CHARS;
      if (!mergeable) continue;
      const target = i > 0 ? i - 1 : i + 1;
      if (target >= runs.length) break;
      const mergedLang = runs[target].lang;
      const mergedText = target < i
        ? runs[target].text + run.text
        : run.text + runs[target].text;
      runs.splice(Math.min(i, target), 2, { text: mergedText, lang: mergedLang });
      changed = true;
      break;
    }
  }
  // Coalesce neighbors that ended up the same language after merging.
  for (let i = runs.length - 1; i > 0; i--) {
    if (runs[i].lang === runs[i - 1].lang) {
      runs[i - 1].text += runs[i].text;
      runs.splice(i, 1);
    }
  }
  // Defensive: cap the utterance count (see MAX_SPEAK_SEGMENTS).
  while (runs.length > MAX_SPEAK_SEGMENTS) {
    let shortest = 0;
    let shortestLen = Number.POSITIVE_INFINITY;
    for (let i = 0; i + 1 < runs.length; i++) {
      const len = runs[i].text.length + runs[i + 1].text.length;
      if (len < shortestLen) { shortestLen = len; shortest = i; }
    }
    const first = runs[shortest];
    const second = runs[shortest + 1];
    runs.splice(shortest, 2, { text: first.text + second.text, lang: first.lang });
  }
  return runs;
}

// Speak a mixed-language segment queue. Cancels any in-flight playback first
// (toggle semantics + never stack two readings), then queues one utterance
// per segment with its voice pinned when the pack exists — or left to the
// engine's lang-hint best effort when it does not (tl1815 degradation).
// `onDone` settles exactly once: after the last utterance ends naturally, or
// on the first failure (remaining utterances are cancelled — the caller's
// speaking state must always land, never hang mid-queue).
export function speakSegments(segments: SpeakSegment[], onDone?: () => void): void {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.speak !== "function") {
    onDone?.();
    return;
  }
  try {
    synth.cancel();
  } catch {
    // Cancel is best-effort; speaking must proceed regardless.
  }
  if (segments.length === 0) {
    onDone?.();
    return;
  }
  let pending = segments.length;
  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    onDone?.();
  };
  const step = () => {
    pending -= 1;
    if (pending <= 0) settle();
  };
  const abort = () => {
    try {
      synth.cancel();
    } catch {
      // Dropping the rest of the queue is best-effort.
    }
    settle();
  };
  for (const segment of segments) {
    let utterance: SpeechSynthesisUtterance;
    try {
      utterance = new window.SpeechSynthesisUtterance(segment.text);
    } catch {
      // Constructor missing or throwing (jsdom, hardened webviews) — surface
      // as "nothing played" via onDone; the caller already validated support.
      abort();
      return;
    }
    utterance.lang = segment.lang;
    const voice = findVoiceFor(segment.lang);
    if (voice) {
      try {
        utterance.voice = voice;
      } catch {
        // Voice pinning is an optimization; the lang hint still stands.
      }
    }
    utterance.onend = () => step();
    utterance.onerror = () => abort();
    try {
      synth.speak(utterance);
    } catch {
      abort();
      return;
    }
  }
}

// Speak `text` with the OS voice for `lang`. tl1709 single-utterance entry,
// kept as the degenerate case of the segment queue (one segment, one lang).
// Cancels any in-flight playback first (toggle semantics + never stack two
// readings), then queues a single truncated utterance. `onDone` fires on
// natural end and on error alike, so the caller's speaking state always
// settles.
export function speakSelection(text: string, lang: string, onDone?: () => void): void {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.speak !== "function") return;
  const capped = text.length > MAX_SPEAK_CHARS ? text.slice(0, MAX_SPEAK_CHARS) : text;
  speakSegments([{ text: capped, lang: lang as SpeakLang }], onDone);
}

export function cancelSpeech(): void {
  try {
    if (typeof window === "undefined") return;
    window.speechSynthesis?.cancel();
  } catch {
    // Stopping must never throw into the caller.
  }
}

// Human-readable content-language name for the missing-voice guidance.
// Kept in the content language itself (a Chinese pack is "中文" in every UI
// locale).
export function speakLangDisplayName(lang: SpeakLang): string {
  return lang === "zh-CN" ? "中文" : "English";
}
