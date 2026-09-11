import { useCallback, useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";

import { app, type RecoveryCopyGroupView } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useConfirmDialog } from "./ConfirmDialog";

/** bytes → a short human size. */
function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function formatWhen(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

type Phase = "pending" | "unmerged" | "merged";

/** copiesNeedingMeasurement is what stands between "not measured" and "mergeable".
 *
 *  An orphan copy has no canonical transcript to be compared against, so it can
 *  never be measured and must not hold a merge back - counting it here is what
 *  previously left the merge button greyed out with nothing to explain why.
 */
function copiesNeedingMeasurement(group: RecoveryCopyGroupView) {
  return group.copies.filter((copy) => !copy.orphan && !copy.scanned);
}

/** phaseOf sorts a conversation by what a user would do about it.
 *
 *  Pending means not measured yet, so nothing is known and nothing should be
 *  merged. Unmerged means a copy holds events the canonical transcript does not,
 *  which is the case worth acting on. Merged means everything the copies hold is
 *  already in the canonical transcript, so there is nothing left to bring over.
 */
function phaseOf(group: RecoveryCopyGroupView): Phase {
  if (copiesNeedingMeasurement(group).length > 0) return "pending";
  if (group.copies.some((copy) => !copy.orphan && copy.unique > 0)) return "unmerged";
  return "merged";
}

/** RecoveryCopiesSection puts session versions and copies behind a button.
 *
 *  Three steps on purpose, because they cost very different amounts and the last
 *  one can lose work.
 *
 *  Opening the panel is one directory read per session folder, so it appears at once
 *  and shows the shape of the problem - which conversations have copies, how big
 *  those copies are, how recently they were written - without opening any
 *  transcript. Conversations start collapsed and are grouped by what they need:
 *  unmerged first, then not-yet-measured, then already merged.
 *
 *  Measuring a copy means reading and comparing whole transcripts, and one copy on a
 *  busy machine reaches a hundred megabytes. So it happens when asked, for one
 *  conversation or for all of them, and the numbers land in the rows afterwards.
 *
 *  Merging never leaves a click unanswered. It is enabled as soon as something is
 *  selected, and if a row has not been measured the click explains that instead of
 *  doing nothing - a control that silently ignores a click cannot be told apart
 *  from a broken one. Once it runs it reports per conversation what happened, with
 *  failures shown verbatim.
 */
export function RecoveryCopiesSection() {
  const t = useT();
  const { confirm, dialog } = useConfirmDialog();
  const [open, setOpen] = useState(false);
  const [groups, setGroups] = useState<RecoveryCopyGroupView[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [scanning, setScanning] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [errors, setErrors] = useState<string[]>([]);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const list = await app.ListRecoveryCopyGroups();
      setGroups(list);
      setSelected(new Set());
      // Collapsed by default: the summary line answers "which conversation has
      // copies", and the per-copy detail only matters once one is chosen.
      setExpanded(new Set());
    } catch {
      setGroups(null);
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const phased = useMemo(() => {
    const pending: RecoveryCopyGroupView[] = [];
    const unmerged: RecoveryCopyGroupView[] = [];
    const merged: RecoveryCopyGroupView[] = [];
    for (const group of groups ?? []) {
      const phase = phaseOf(group);
      if (phase === "pending") pending.push(group);
      else if (phase === "unmerged") unmerged.push(group);
      else merged.push(group);
    }
    return { pending, unmerged, merged };
  }, [groups]);

  const toggle = (path: string) =>
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const toggleExpanded = (path: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const scanOne = async (mainPath: string) => {
    setScanning((current) => new Set(current).add(mainPath));
    setErrors([]);
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
      // Scanning is the moment the detail becomes worth reading, so open the row.
      setExpanded((current) => new Set(current).add(mainPath));
    } catch (err) {
      setErrors([err instanceof Error ? err.message : String(err)]);
    } finally {
      setScanning((current) => {
        const next = new Set(current);
        next.delete(mainPath);
        return next;
      });
    }
  };

  const scanAll = async () => {
    for (const group of groups ?? []) {
      await scanOne(group.mainPath);
    }
  };

  const mergeSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;

    // Say why nothing can happen yet rather than ignoring the click.
    const waiting = chosen.filter((g) => copiesNeedingMeasurement(g).length > 0);
    if (waiting.length > 0) {
      await confirm({
        title: t("settings.recoveryCopiesScanFirst"),
        message: (
          <div>
            <p>{t("settings.recoveryCopiesScanFirstBody")}</p>
            <ul className="rc-preview">
              {waiting.map((g) => (
                <li key={g.mainPath}>
                  {g.mainLabel} ×{copiesNeedingMeasurement(g).length}
                </li>
              ))}
            </ul>
          </div>
        ),
        confirmLabel: t("settings.recoveryCopiesScan"),
        cancelLabel: t("common.cancel"),
      });
      return;
    }

    // Show what is about to happen before it happens: which conversations, how many
    // copies each, and how much unique work is at stake. Merging writes to session
    // files, so the user gets to read this first.
    const totalCopies = chosen.reduce((n, g) => n + g.copies.length, 0);
    const totalUnique = chosen.reduce(
      (n, g) => n + g.copies.reduce((m, c) => m + (c.orphan ? 0 : c.unique), 0),
      0,
    );
    const ok = await confirm({
      title: t("settings.recoveryCopiesMerge"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesPreviewLead")}：{chosen.length} ·{" "}
            {t("settings.recoveryCopiesPreviewCopies")} {totalCopies} ·{" "}
            {t("settings.recoveryCopiesUnique")} {totalUnique}
          </p>
          <ul className="rc-preview">
            {chosen.map((g) => (
              <li key={g.mainPath}>
                {g.mainLabel} ×{g.copies.length}
              </li>
            ))}
          </ul>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesPreviewWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesMerge"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!ok) return;

    setBusy(true);
    setNote("");
    setErrors([]);
    const mergedLabels: string[] = [];
    const blockedLabels: string[] = [];
    const failureMessages: string[] = [];
    try {
      for (const group of chosen) {
        try {
          const report = await app.ConsolidateSessionRecoveryCopies(group.mainPath);
          if (report?.blockedByDivergence) {
            blockedLabels.push(group.mainLabel);
            continue;
          }
          mergedLabels.push(group.mainLabel);
        } catch (err) {
          // Naming the conversation and carrying the message through is the point:
          // a bare counter cannot tell "nothing needed merging" from "the call
          // failed", and those call for opposite reactions.
          const detail = err instanceof Error ? err.message : String(err);
          failureMessages.push(`${group.mainLabel}: ${detail}`);
        }
      }
      setNote(
        `${t("settings.recoveryCopiesMerged")} ${mergedLabels.length}` +
          (blockedLabels.length ? ` · ${t("settings.recoveryCopiesBlocked")} ${blockedLabels.length}` : ""),
      );
      setErrors(failureMessages);
      setSelected(new Set());
      await load();
    } finally {
      setBusy(false);
    }
  };

  const scanningAll = scanning.size > 0;
  const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
  const waitingCount = chosen.reduce((n, g) => n + copiesNeedingMeasurement(g).length, 0);

  const renderGroup = (group: RecoveryCopyGroupView) => {
    const isOpen = expanded.has(group.mainPath);
    const isScanning = scanning.has(group.mainPath);
    const groupUnique = group.copies.every((c) => c.scanned)
      ? group.copies.reduce((n, c) => n + (c.orphan ? 0 : c.unique), 0)
      : null;
    return (
      <div className="rc-group" key={group.mainPath}>
        <div className="rc-row rc-row--group">
          <input
            type="checkbox"
            checked={selected.has(group.mainPath)}
            disabled={busy}
            onChange={() => toggle(group.mainPath)}
            aria-label={group.mainLabel}
          />
          <button
            className="rc-toggle"
            type="button"
            aria-expanded={isOpen}
            onClick={() => toggleExpanded(group.mainPath)}
          >
            <span className="rc-caret">{isOpen ? "▾" : "▸"}</span>
            <span className="rc-label">{group.mainLabel}</span>
          </button>
          <span>{formatWhen(group.mainModified)}</span>
          <span>{formatBytes(group.mainBytes)}</span>
          <span>{group.mainMessages || "—"}</span>
          <span>×{group.copies.length}</span>
          <span className={groupUnique ? "rc-unique" : "rc-muted"}>{groupUnique ?? "—"}</span>
          <span className="rc-action">
            <button
              className="btn btn--small"
              type="button"
              disabled={isScanning || scanningAll}
              onClick={() => void scanOne(group.mainPath)}
            >
              {isScanning ? "…" : t("settings.recoveryCopiesScan")}
            </button>
          </span>
        </div>

        {isOpen && (
          <>
            <div className="rc-row rc-row--main">
              <span />
              <span className="rc-label">
                {t("settings.recoveryCopiesMain")}
                {group.mainExists ? "" : ` · ${t("settings.recoveryCopiesOrphan")}`}
              </span>
              <span>{formatWhen(group.mainModified)}</span>
              <span>{formatBytes(group.mainBytes)}</span>
              <span>{group.mainMessages || "—"}</span>
              <span className="rc-muted">—</span>
              <span className="rc-muted">—</span>
              <span />
            </div>
            {group.copies.map((copy, index) => (
              <div className="rc-row rc-row--copy" key={copy.path}>
                <span />
                <span className="rc-label">
                  {t("settings.recoveryCopiesCopy")} {index + 1}
                </span>
                <span>{formatWhen(copy.modified)}</span>
                <span>{formatBytes(copy.bytes)}</span>
                <span>{copy.scanned ? copy.messages : "—"}</span>
                <span className="rc-muted">—</span>
                <span className={copy.unique > 0 ? "rc-unique" : "rc-muted"}>
                  {copy.scanned ? copy.unique : "—"}
                </span>
                <span />
              </div>
            ))}
          </>
        )}
      </div>
    );
  };

  const renderPhase = (title: string, rows: RecoveryCopyGroupView[], hint: string) =>
    rows.length ? (
      <div className="rc-phase" key={title}>
        <div className="rc-phase__head">
          <span className="rc-phase__title">{title}</span>
          <span className="rc-phase__count">{rows.length}</span>
          <span className="rc-phase__hint">{hint}</span>
        </div>
        {rows.map(renderGroup)}
      </div>
    ) : null;

  return (
    <>
      <section className="settings-section">
        <div className="settings-section__head">
          <div>
            <div className="settings-section__title">{t("settings.recoveryCopiesTitle")}</div>
            <div className="settings-section__desc">{t("settings.recoveryCopiesHint")}</div>
          </div>
        </div>
        <div className="settings-section__body">
          <button className="btn btn--small" type="button" onClick={() => setOpen(true)}>
            {t("settings.recoveryCopiesOpen")}
          </button>
        </div>
      </section>

      {open &&
        createPortal(
          <div className="modal-backdrop rc-backdrop" onClick={() => setOpen(false)}>
            <div
              className="modal rc-modal"
              role="dialog"
              aria-modal="true"
              onClick={(event) => event.stopPropagation()}
            >
              <div className="rc-modal__head">
                <span className="rc-modal__title">{t("settings.recoveryCopiesTitle")}</span>
                <button className="btn btn--small" type="button" onClick={() => setOpen(false)}>
                  {t("common.close")}
                </button>
              </div>

              <div className="rc-modal__body">
                {failed ? (
                  <div className="banner banner--error" role="alert">
                    <span>{t("settings.loadFailed")}</span>
                    <button className="btn btn--small" type="button" onClick={() => void load()}>
                      {t("common.retry")}
                    </button>
                  </div>
                ) : groups === null ? (
                  <div className="empty">{t("settings.loading")}</div>
                ) : groups.length === 0 ? (
                  <div className="empty">{t("settings.recoveryCopiesEmpty")}</div>
                ) : (
                  <>
                    {errors.length > 0 && (
                      <div className="banner banner--error" role="alert">
                        <ul className="rc-errors">
                          {errors.map((message) => (
                            <li key={message}>{message}</li>
                          ))}
                        </ul>
                      </div>
                    )}
                    {note ? <div className="rc-result">{note}</div> : null}
                    <div className="rc-table">
                      <div className="rc-row rc-row--head">
                        <span />
                        <span>{t("settings.recoveryCopiesColLine")}</span>
                        <span>{t("settings.recoveryCopiesColModified")}</span>
                        <span>{t("settings.recoveryCopiesColSize")}</span>
                        <span>{t("settings.recoveryCopiesColTurns")}</span>
                        <span>{t("settings.recoveryCopiesColCopies")}</span>
                        <span>{t("settings.recoveryCopiesColUnique")}</span>
                        <span />
                      </div>
                      {renderPhase(
                        t("settings.recoveryCopiesPhaseUnmerged"),
                        phased.unmerged,
                        t("settings.recoveryCopiesPhaseUnmergedHint"),
                      )}
                      {renderPhase(
                        t("settings.recoveryCopiesPhasePending"),
                        phased.pending,
                        t("settings.recoveryCopiesPhasePendingHint"),
                      )}
                      {renderPhase(
                        t("settings.recoveryCopiesPhaseMerged"),
                        phased.merged,
                        t("settings.recoveryCopiesPhaseMergedHint"),
                      )}
                    </div>
                  </>
                )}
              </div>

              <div className="rc-modal__foot">
                <button
                  className="btn btn--small btn--primary"
                  type="button"
                  disabled={busy || selected.size === 0}
                  onClick={() => void mergeSelected()}
                >
                  {t("settings.recoveryCopiesMerge")} ({selected.size})
                </button>
                <button
                  className="btn btn--small"
                  type="button"
                  disabled={busy || scanningAll}
                  onClick={() => void scanAll()}
                >
                  {t("settings.recoveryCopiesScanAll")}
                </button>
                <button
                  className="btn btn--small"
                  type="button"
                  disabled={busy || scanningAll}
                  onClick={() => void load()}
                >
                  {t("settings.recoveryCopiesRefresh")}
                </button>
                {selected.size === 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesPickFirst")}</span>
                ) : waitingCount > 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesScanFirst")}</span>
                ) : note ? (
                  <span className="rc-note">{note}</span>
                ) : null}
              </div>
            </div>
          </div>,
          document.body,
        )}

      {dialog}
    </>
  );
}
