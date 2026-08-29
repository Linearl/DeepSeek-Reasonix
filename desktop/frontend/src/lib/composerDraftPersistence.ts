// Cross-restart persistence for composer drafts (#9580).
//
// The per-session draft switching (#5158) lives in Composer's
// draftsBySessionRef, which is component memory only — closing the app loses
// every unsent draft. This module is the cold-start layer: the same draftKey
// (composerDraftKeyForTab) maps to the persisted draft in localStorage,
// written debounced on every edit and read when a session's draft is first
// restored. Persisted fields cover what a restart can faithfully restore:
// text, pasted text blocks, file attachments (path only — previewUrl is
// regenerated on demand), attachment dedup keys, and workspace refs.
// Guidance queues / submit flags stay runtime-only. Every storage error
// degrades to the previous in-memory behavior.

const STORAGE_KEY = "composer:drafts:v1";
const MAX_PERSISTED_BYTES = 256 * 1024; // per-draft JSON budget
const DEBOUNCE_MS = 150;

export type PersistedComposerDraft = {
  text: string;
  pastedBlocks: { label: string; text: string }[];
  attachments: { path: string; displayName?: string }[];
  attachmentDedupKeys: Record<string, { hash: string; source: string }>;
  workspaceRefs: { path: string; isDir?: boolean }[];
};

type PersistedDrafts = Record<string, PersistedComposerDraft>;

let flushTimer: number | null = null;
let pendingWrites: PersistedDrafts | null = null;

// flushNow writes pending draft edits immediately. Wired to pagehide so a
// window closed right after typing still keeps the last input (#9580).
function flushNow(): void {
	if (flushTimer !== null) {
		window.clearTimeout(flushTimer);
		flushTimer = null;
	}
	if (pendingWrites === null) return;
	writeAll(pendingWrites);
	pendingWrites = null;
}

// A closed WebView never fires the debounce; pagehide/visibilitychange are
// the last reliable moments to flush.
if (typeof window !== "undefined") {
	window.addEventListener("pagehide", flushNow);
	document.addEventListener("visibilitychange", () => {
		if (document.visibilityState === "hidden") flushNow();
	});
}

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

export function loadPersistedComposerDraft(draftKey: string): PersistedComposerDraft | null {
  if (!draftKey) return null;
  const draft = readAll()[draftKey];
  if (!draft || typeof draft !== "object") return null;
  return {
    text: typeof draft.text === "string" ? draft.text : "",
    pastedBlocks: Array.isArray(draft.pastedBlocks) ? draft.pastedBlocks : [],
    attachments: Array.isArray(draft.attachments) ? draft.attachments : [],
    attachmentDedupKeys:
      draft.attachmentDedupKeys && typeof draft.attachmentDedupKeys === "object"
        ? draft.attachmentDedupKeys
        : {},
    workspaceRefs: Array.isArray(draft.workspaceRefs) ? draft.workspaceRefs : [],
  };
}

function normalize(draft: PersistedComposerDraft): PersistedComposerDraft {
  return {
    text: draft.text,
    pastedBlocks: draft.pastedBlocks ?? [],
    attachments: (draft.attachments ?? []).map((a) => ({
      path: a.path,
      ...(a.displayName ? { displayName: a.displayName } : {}),
    })),
    attachmentDedupKeys: draft.attachmentDedupKeys ?? {},
    workspaceRefs: draft.workspaceRefs ?? [],
  };
}

export function persistComposerDraft(draftKey: string, draft: PersistedComposerDraft, immediate = false): void {
  if (!draftKey) return;
  if (flushTimer !== null) {
    window.clearTimeout(flushTimer);
    flushTimer = null;
  }
  const drafts = pendingWrites ?? readAll();
  pendingWrites = drafts;
  const clean = normalize(draft);
  const empty =
    clean.text === "" && clean.pastedBlocks.length === 0 && clean.attachments.length === 0
      && clean.workspaceRefs.length === 0;
  if (empty || JSON.stringify(clean).length > MAX_PERSISTED_BYTES) {
    // Sent/emptied drafts and oversize drafts are not persisted.
    delete drafts[draftKey];
  } else {
    drafts[draftKey] = clean;
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

export function deletePersistedComposerDraft(draftKey: string): void {
  if (!draftKey) return;
  persistComposerDraft(
    draftKey,
    { text: "", pastedBlocks: [], attachments: [], attachmentDedupKeys: {}, workspaceRefs: [] },
    true,
  );
}
