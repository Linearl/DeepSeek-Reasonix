// tl1817 — read-aloud MiMo TTS playback (frontend half of the transport).
//
// Division of labour: the Go bridge (desktop/mimo_tts_app.go) owns
// detection + synthesis and never hands out the credential; this module owns
// playback of the returned base64 wav plus the cancel/generation discipline.
// The tl1815 system chain (lib/speech.ts segment queue) is NOT touched — it
// stays the standing fallback: the caller degrades to it whenever this
// module reports "failed".
//
// Outcome contract (pinned in tl1817-mimo-tts.test.tsx):
//   - "played"    — audio finished naturally; caller settles the toggle
//   - "cancelled" — stop/close superseded the request or playback; the
//                   caller must NOT degrade (stop means stop)
//   - "failed"    — synthesis error (no key / 401 / network / malformed) or
//                   playback error; the caller shows the visible degrade
//                   notice and replays via speakSegments
//
// One audio element at a time (never stack two readings, same rule as the
// system chain). A new speak or a cancel bumps the generation: a
// late-resolving synthesis request is dropped instead of starting playback
// after the user stopped it, and any listener settling after a cancel maps
// to "cancelled" — cancel supersedes failure, never the reverse.

import { app } from "./bridge";

export type MimoTtsOutcome = "played" | "cancelled" | "failed";

let activeAudio: HTMLAudioElement | null = null;
let activeGeneration = 0;

// Invalidate the in-flight generation and stop any playing audio. Invoked by
// the result-card close path (alongside cancelSpeech) and by a new speak.
export function cancelMimoTts(): void {
  activeGeneration += 1;
  const element = activeAudio;
  activeAudio = null;
  if (!element) return;
  try {
    element.pause();
  } catch {
    // Stopping must never throw into the caller.
  }
  try {
    element.removeAttribute("src");
  } catch {
    // Detaching the source is best-effort.
  }
}

function isMimoTtsBridgeAvailable(): boolean {
  const candidate = app as Partial<Record<"MimoTtsSynthesize", unknown>>;
  return typeof candidate.MimoTtsSynthesize === "function";
}

// Synthesize + play `text`. Resolves with the outcome once playback finished
// naturally ("played"), the attempt was superseded/cancelled ("cancelled"),
// or anything failed ("failed"). Never rejects; never leaves a playing
// element behind on any path.
export async function speakViaMimo(text: string): Promise<MimoTtsOutcome> {
  if (!text.trim()) return "failed";
  if (!isMimoTtsBridgeAvailable()) return "failed";
  const generation = ++activeGeneration;
  let audioBase64 = "";
  try {
    audioBase64 = await app.MimoTtsSynthesize(text);
  } catch {
    return settleGenerationScoped(generation, "failed");
  }
  if (generation !== activeGeneration) return "cancelled";
  if (!audioBase64 || typeof Audio === "undefined") {
    return settleGenerationScoped(generation, "failed");
  }
  const element = new Audio(`data:audio/wav;base64,${audioBase64}`);
  activeAudio = element;
  return await new Promise<MimoTtsOutcome>((resolve) => {
    let settledOnce = false;
    const settle = (naturalOutcome: MimoTtsOutcome) => {
      if (settledOnce) return;
      settledOnce = true;
      if (activeAudio === element) activeAudio = null;
      // A cancel that raced any listener supersedes the natural outcome —
      // stop means stop, never a degrade notice.
      resolve(generation === activeGeneration ? naturalOutcome : "cancelled");
    };
    element.onended = () => settle("played");
    element.onerror = () => settle("failed");
    try {
      const playing = element.play();
      if (playing) playing.catch(() => settle("failed"));
    } catch {
      settle("failed");
    }
  });
}

function settleGenerationScoped(generation: number, naturalOutcome: MimoTtsOutcome): MimoTtsOutcome {
  return generation === activeGeneration ? naturalOutcome : "cancelled";
}
