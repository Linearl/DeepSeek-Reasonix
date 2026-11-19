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
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.speak !== "function") return "unsupported";
  let voices: Array<{ lang?: string }> = [];
  try {
    voices = typeof synth.getVoices === "function" ? synth.getVoices() : [];
  } catch {
    return "no-voices";
  }
  if (!voices || voices.length === 0) return "no-voices";
  const want = lang.slice(0, 2).toLowerCase();
  const matched = voices.some((voice) => voiceLangKey(voice.lang).startsWith(want));
  return matched ? "ready" : "no-match";
}

// Speak `text` with the OS voice for `lang`. Cancels any in-flight playback
// first (toggle semantics + never stack two readings), then queues a single
// truncated utterance. `onDone` fires on natural end and on error alike, so
// the caller's speaking state always settles.
export function speakSelection(text: string, lang: string, onDone?: () => void): void {
  const synth = typeof window !== "undefined" ? window.speechSynthesis : undefined;
  if (!synth || typeof synth.speak !== "function") return;
  const capped = text.length > MAX_SPEAK_CHARS ? text.slice(0, MAX_SPEAK_CHARS) : text;
  let utterance: SpeechSynthesisUtterance;
  try {
    utterance = new window.SpeechSynthesisUtterance(capped);
  } catch {
    // Constructor missing or throwing (jsdom, hardened webviews) — surface as
    // "nothing played" via onDone; the caller already validated support.
    onDone?.();
    return;
  }
  utterance.lang = lang;
  if (onDone) {
    utterance.onend = () => onDone();
    utterance.onerror = () => onDone();
  }
  try {
    synth.cancel();
    synth.speak(utterance);
  } catch {
    onDone?.();
  }
}

export function cancelSpeech(): void {
  try {
    if (typeof window === "undefined") return;
    window.speechSynthesis?.cancel();
  } catch {
    // Stopping must never throw into the caller.
  }
}

// Human-readable content-language name for the no-match notice. Kept in the
// content language itself (a Chinese pack is "中文" in every UI locale).
export function speakLangDisplayName(lang: SpeakLang): string {
  return lang === "zh-CN" ? "中文" : "English";
}
