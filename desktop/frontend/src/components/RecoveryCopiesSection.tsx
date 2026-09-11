import { useCallback, useEffect, useState } from "react";

import { app, type RecoveryCopyGroupView } from "../lib/bridge";
import { useT } from "../lib/i18n";

/** bytes → a short human size. Local on purpose: the panel needs one form only. */
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

/** RecoveryCopiesSection reviews conversations that have recovery copies beside them.
 *
 *  Three steps on purpose, because they cost very different amounts.
 *
 *  The list is one directory read per session folder. It appears at once and shows
 *  the shape of the problem - which conversations have copies, how big those copies
 *  are, how recently they were written - without opening any transcript.
 *
 *  Measuring a copy means reading and comparing whole transcripts, and one copy on a
 *  busy machine reaches a hundred megabytes. That is why it is not part of the list:
 *  it happens when asked, for a single conversation or for all of them, and the
 *  numbers appear in the rows afterwards.
 *
 *  Merging comes last and stays behind the measurement, because the numbers are what
 *  tell a copy that is pure duplication apart from one holding work that would be
 *  lost by ignoring it.
 */
export function RecoveryCopiesSection() {
  const t = useT();
  const [groups, setGroups] = useState<RecoveryCopyGroupView[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [scanning, setScanning] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const list = await app.ListRecoveryCopyGroups();
      setGroups(list);
      setSelected(new Set());
      // Open a few: which copies are recent is the usual reason to come here, and
      // expanding costs nothing because the row data is already in hand.
      setExpanded(new Set(list.slice(0, 3).map((g) => g.mainPath)));
    } catch {
      setGroups(null);
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const toggle = (path: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const toggleExpanded = (path: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const scanOne = async (mainPath: string) => {
    setScanning((current) => new Set(current).add(mainPath));
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
    } catch {
      setNote(t("settings.loadFailed"));
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
    if (selected.size === 0) return;
    setBusy(true);
    setNote("");
    let merged = 0;
    let leftBehind = 0;
    try {
      for (const group of groups ?? []) {
        if (!selected.has(group.mainPath)) continue;
        try {
          const report = await app.ConsolidateSessionRecoveryCopies(group.mainPath);
          if (report?.blockedByDivergence) {
            leftBehind += 1;
            continue;
          }
          merged += 1;
          for (const detail of report?.notCoveredDetail ?? []) {
            if (detail.unique > 0) leftBehind += 1;
          }
        } catch {
          leftBehind += 1;
        }
      }
      setNote(`${t("settings.recoveryCopiesMerged")} ${merged} / ${leftBehind}`);
      setSelected(new Set());
      await load();
    } finally {
      setBusy(false);
    }
  };

  const scanningAll = scanning.size > 0;

  return (
    <section className="settings-section">
      <div className="settings-section__head">
        <div>
          <div className="settings-section__title">{t("settings.recoveryCopiesTitle")}</div>
          <div className="settings-section__desc">{t("settings.recoveryCopiesHint")}</div>
        </div>
      </div>
      <div className="settings-section__body">
        {failed ? (
          <div className="banner banner--error" role="alert">
            <span>{t("settings.loadFailed")}</span>
            <button className="btn btn--small" type="button" onClick={() => void load()}>{t("common.retry")}</button>
          </div>
        ) : groups === null ? (
          <div className="empty">{t("settings.loading")}</div>
        ) : groups.length === 0 ? (
          <div className="empty">{t("settings.recoveryCopiesEmpty")}</div>
        ) : (
          <>
            {groups.map((group) => {
              const open = expanded.has(group.mainPath);
              const isScanning = scanning.has(group.mainPath);
              return (
                <div className="settings-recovery-copies__group" key={group.mainPath}>
                  <div className="settings-recovery-copies__group-head">
                    <input
                      type="checkbox"
                      checked={selected.has(group.mainPath)}
                      disabled={busy}
                      onChange={() => toggle(group.mainPath)}
                      aria-label={group.mainLabel}
                    />
                    <button
                      className="settings-recovery-copies__toggle"
                      type="button"
                      onClick={() => toggleExpanded(group.mainPath)}
                      aria-expanded={open}
                    >
                      {open ? "▾" : "▸"} {group.mainLabel}
                    </button>
                    <span className="settings-recovery-copies__group-meta">×{group.copies.length}</span>
                    <button
                      className="btn btn--small"
                      type="button"
                      disabled={isScanning || scanningAll}
                      onClick={() => void scanOne(group.mainPath)}
                    >
                      {isScanning ? "…" : t("settings.recoveryCopiesScan")}
                    </button>
                  </div>
                  {open && (
                    <div className="settings-recovery-copies__copies">
                      {group.copies.map((copy) => (
                        <div className="settings-recovery-copies__copy" key={copy.path}>
                          <span className="settings-recovery-copies__copy-name">{formatWhen(copy.modified)}</span>
                          <span className="settings-recovery-copies__copy-size">{formatBytes(copy.bytes)}</span>
                          <span className="settings-recovery-copies__copy-meta">
                            {copy.scanned
                              ? `${t("settings.recoveryCopiesTotal")} ${copy.messages} · ${t("settings.recoveryCopiesShared")} ${copy.shared} · ${t("settings.recoveryCopiesUnique")} ${copy.unique}${copy.orphan ? ` · ${t("settings.recoveryCopiesOrphan")}` : ""}`
                              : t("settings.recoveryCopiesUnscanned")}
                          </span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
            <div className="settings-recovery-copies__actions">
              <button
                className="btn btn--small btn--primary"
                type="button"
                disabled={busy || selected.size === 0}
                onClick={() => void mergeSelected()}
              >
                {t("settings.recoveryCopiesMerge")}
              </button>
              <button
                className="btn btn--small"
                type="button"
                disabled={busy || scanningAll}
                onClick={() => void scanAll()}
              >
                {t("settings.recoveryCopiesScanAll")}
              </button>
              <button className="btn btn--small" type="button" disabled={busy || scanningAll} onClick={() => void load()}>
                {t("settings.recoveryCopiesRefresh")}
              </button>
              {note ? <span className="settings-recovery-copies__note">{note}</span> : null}
            </div>
          </>
        )}
      </div>
    </section>
  );
}
