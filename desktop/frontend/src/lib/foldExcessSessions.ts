// Task 102: whether project trees collapse long session lists behind an
// "expand (N)" control. Default is on (current behaviour); turning it off
// keeps expanded projects fully listed at startup.
// Stored client-side: it is a view preference, not shared state, and the
// settings panel and the tree need no backend round-trip to stay in sync -
// the custom event handles that within the document.
const KEY = "reasonix.foldExcessSessions";
const CHANGED = "reasonix:fold-excess-sessions-changed";

export function loadFoldExcessSessions(): boolean {
	try {
		return window.localStorage.getItem(KEY) !== "0";
	} catch {
		return true;
	}
}

export function saveFoldExcessSessions(value: boolean): void {
	try {
		window.localStorage.setItem(KEY, value ? "1" : "0");
	} catch {
		// Storage may be unavailable (embedded contexts); the toggle still works
		// for the current document lifetime via the event below.
	}
	window.dispatchEvent(new Event(CHANGED));
}

export function onFoldExcessSessionsChanged(handler: () => void): () => void {
	window.addEventListener(CHANGED, handler);
	return () => window.removeEventListener(CHANGED, handler);
}
