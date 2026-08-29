// Cross-restart persistence for composer drafts (#9580).
//
// The per-session draft switching (#5158) lives in Composer's
// draftsBySessionRef, which is component memory only — closing the app loses
// every unsent draft. This module is the cold-start layer: the same draftKey
// (composerDraftKeyForTab) maps to the draft's text in localStorage, written
// debounced on every edit and read when a session's draft is first restored.
// Only the text survives; attachments/invocations/guidance stay runtime-only.
// Every storage error degrades to the previous in-memory behavior.

const STORAGE_KEY = "composer:drafts:v1";
const MAX_DRAFT_CHARS = 64_000; // oversize drafts are not persisted
const DEBOUNCE_MS = 500;

type PersistedDrafts = Record<string, string>;

let flushTimer: number | null = null;
let pendingWrites: PersistedDrafts | null = null;

function readAll(): PersistedDrafts {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as PersistedDrafts;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writeAll(drafts: PersistedDrafts): boolean {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(drafts));
    return true;
  } catch {
    // Quota exceeded or storage unavailable — degrade to memory-only drafts.
    return false;
  }
}

export function loadPersistedComposerText(draftKey: string): string {
  if (!draftKey) return "";
  return readAll()[draftKey] ?? "";
}

export function persistComposerText(draftKey: string, text: string, immediate = false): void {
  if (!draftKey) return;
  if (flushTimer !== null) {
    window.clearTimeout(flushTimer);
    flushTimer = null;
  }
  const drafts = pendingWrites ?? readAll();
  pendingWrites = drafts;
  if (text === "" || text.length > MAX_DRAFT_CHARS) {
    delete drafts[draftKey]; // empty (sent/cleared) or oversize — don't keep
  } else {
    drafts[draftKey] = text;
  }
  if (immediate) {
    pendingWrites = null;
    writeAll(drafts);
    return;
  }
  flushTimer = window.setTimeout(() => {
    flushTimer = null;
    if (pendingWrites === null) return;
    writeAll(pendingWrites);
    pendingWrites = null;
  }, DEBOUNCE_MS);
}

export function deletePersistedComposerText(draftKey: string): void {
  if (!draftKey) return;
  persistComposerText(draftKey, "", true);
}
