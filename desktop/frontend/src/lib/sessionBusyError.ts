// tl1367 line 2: localized rendering of the lease-busy refusal family.
//
// The backend prefixes lease-busy detail text with a machine-readable holder
// block ([reasonix-holder class="live" pid="…" name="…" path="…" host="…"
// acquired="…" writer="…"], see desktop/session_lease_holder.go) and keeps the
// machine prefix channel `reasonix_error:topic_archive_forceable:` stable for
// the force-archive contract (tl 1103). This module parses that block and
// renders localized copy per holder class (self / live / zombie / reused /
// unknown) with the full holder identity — pid, process name and image path,
// hostname, acquire time, runtime id — so a Chinese-locale user never reads
// the raw English sentence, and a toast never leaks the machine prefix.
// Unparseable text passes through unchanged (older backend compatibility).

import { t } from "./i18n";
import { TOPIC_ARCHIVE_FORCEABLE_PREFIX } from "./projectTreeArchive";

export type SessionBusyHolderClass = "self" | "live" | "zombie" | "reused" | "unknown";

export interface SessionBusyHolder {
  cls: SessionBusyHolderClass;
  pid?: string;
  name?: string;
  path?: string;
  host?: string;
  /** RFC3339 (UTC) as published by the backend; rendered local HH:mm. */
  acquired?: string;
  writer?: string;
}

const HOLDER_BLOCK = /\[reasonix-holder ([^\]]+)\]/;
const HOLDER_PAIR = /(\w+)="([^"]*)"/g;

/** English sentinels of the busy family without a holder block (older backend
 * or probe paths). Matched exactly — the sentinels are stable error text. */
const BUSY_SENTINELS = [
  "this session is in use by another Reasonix window or process",
  "this session was left locked by a crashed Reasonix process",
  "this session is running in this Reasonix window",
  // tl1383: the identity-missing leftover form — the holder cannot be named,
  // so it gets its own localized copy instead of the "another window" one.
  "this session's lease is locked by a leftover record whose holder cannot be identified",
  // tl1392: the refusal that SURVIVES a force archive — force already ran, so
  // this copy never recommends force again.
  "force archive cleared the leftover lease records",
] as const;

/** The tl1383 leftover sentinel stem, recognized before the generic unknown
 * copy so an unidentifiable-holder refusal never reads as "another window". */
const LEFTOVER_SENTINEL = "leftover record whose holder cannot be identified";

/** The tl1392 after-force sentinel stem: matched BEFORE the plain leftover
 * stem so a still-refusing force archive gets the report-a-log copy, never a
 * second "use force archive" recommendation (the package-73 self-referential
 * loop). */
const LEFTOVER_AFTER_FORCE_SENTINEL = "force archive cleared the leftover lease records";

export function parseSessionBusyHolder(raw: string): SessionBusyHolder | null {
  const match = HOLDER_BLOCK.exec(raw);
  if (!match) return null;
  const fields: Record<string, string> = {};
  for (const pair of match[1].matchAll(HOLDER_PAIR)) {
    fields[pair[1]] = pair[2];
  }
  const cls = fields["class"];
  return {
    cls: (cls === "self" || cls === "live" || cls === "zombie" || cls === "reused" ? cls : "unknown"),
    pid: fields["pid"] || undefined,
    name: fields["name"] || undefined,
    path: fields["path"] || undefined,
    host: fields["host"] || undefined,
    acquired: fields["acquired"] || undefined,
    writer: fields["writer"] || undefined,
  };
}

export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function isSessionBusyError(error: unknown): boolean {
  const raw = errorText(error);
  if (HOLDER_BLOCK.test(raw)) return true;
  const lower = raw.toLowerCase();
  return BUSY_SENTINELS.some((s) => lower.includes(s.toLowerCase()));
}

/** Local HH:mm for the RFC3339 acquire stamp; "" when unparseable. */
function localAcquiredTime(acquired?: string): string {
  if (!acquired) return "";
  const at = new Date(acquired);
  if (Number.isNaN(at.getTime())) return "";
  const hh = String(at.getHours()).padStart(2, "0");
  const mm = String(at.getMinutes()).padStart(2, "0");
  return `${hh}:${mm}`;
}

/** The "name (path)" segment; empty when the platform resolved neither. */
function procSegment(holder: SessionBusyHolder): string {
  if (!holder.name) return "";
  return holder.path ? ` (${holder.name}, ${holder.path})` : ` (${holder.name})`;
}

/** formatSessionBusyError renders the localized busy text with the full
 * holder identity. Non-busy errors come back with the machine prefix stripped
 * (toasts must never print `reasonix_error:`); unknown busy shapes fall back
 * to the localized generic copy. */
export function formatSessionBusyError(error: unknown): string {
  const raw = errorText(error);
  const holder = parseSessionBusyHolder(raw);
  if (!holder) {
    if (!isSessionBusyError(raw)) {
      return raw.startsWith(TOPIC_ARCHIVE_FORCEABLE_PREFIX) ? raw.slice(TOPIC_ARCHIVE_FORCEABLE_PREFIX.length) : raw;
    }
    // tl1383: the identity-missing leftover form carries no holder block, so
    // it lands here — render its own copy, never the "another window" one.
    // tl1392: the after-force form is matched first so a refusal that survived
    // force archive never reads as "use force archive again".
    const lowerRaw = raw.toLowerCase();
    if (lowerRaw.includes(LEFTOVER_AFTER_FORCE_SENTINEL)) {
      return t("sessionBusy.leftoverAfterForce");
    }
    if (lowerRaw.includes(LEFTOVER_SENTINEL)) {
      return t("sessionBusy.leftover");
    }
    return t("sessionBusy.unknown");
  }
  const proc = procSegment(holder);
  return t(`sessionBusy.${holder.cls}`, {
    pid: holder.pid ?? "—",
    proc,
    host: holder.host ?? "—",
    time: localAcquiredTime(holder.acquired) || "—",
    writer: holder.writer ?? "—",
  });
}
