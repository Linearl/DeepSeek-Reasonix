// Run: tsx src/__tests__/tl1446-action-errors.test.ts
//
// tl1446-3: formatActionError closes the bare-error toast gap (tl1436 §3.8).
// Dual-track contract: the human half (action name) goes first, the raw
// backend text stays verbatim for troubleshooting. The sessionBusy holder
// family already speaks for itself (pid/path/host inside the localized copy)
// and must not gain a redundant "{action} failed:" prefix.

import assert from "node:assert/strict";
import { formatActionError, formatActionErrorWith } from "../lib/actionErrors";

// Unknown backend error: template = "{action} failed: {raw}" — raw preserved.
const plain = formatActionError("action.renameSession", new Error("file locked by another process"));
assert.equal(plain, "Rename session failed: file locked by another process", `unknown error keeps the raw text after the action name: ${plain}`);

// String errors (wails bridge rejects with strings) take the same path.
const asString = formatActionError("action.themeApply", "zip: not a valid theme pack");
assert.equal(asString, "Apply theme failed: zip: not a valid theme pack", `string errors keep the raw text: ${asString}`);

// The sessionBusy holder family renders through formatSessionBusyError —
// its own localized copy, no redundant prefix.
const busy = formatActionError("action.batchArchive", 'reasonix_error:session_busy [reasonix-holder class="live" pid="1234" name="reasonix.exe" path="D:/s" host="H" acquired="2026-11-06T10:00:00Z" writer="w-1"]');
assert.ok(!busy.includes("failed:"), `busy family keeps its own copy without the action prefix: ${busy}`);
assert.ok(busy.includes("1234"), `busy family still carries the holder pid for troubleshooting: ${busy}`);

// Dynamic-label variant for call sites holding an already-localized name.
const dynamic = formatActionErrorWith("复制主题", new Error("disk full"));
assert.equal(dynamic, "复制主题 failed: disk full", `dynamic label interpolates: ${dynamic}`);

// Empty error text falls to the no-detail form instead of a dangling colon.
assert.equal(formatActionError("action.diagExport", ""), "Export diagnostics failed.", `empty raw renders the no-detail form: ${formatActionError("action.diagExport", "")}`);

console.log("  PASS  tl1446-3 formatActionError keeps raw text, adds the action name, and lets busy errors speak");
