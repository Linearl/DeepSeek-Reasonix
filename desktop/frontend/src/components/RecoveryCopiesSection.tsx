import { useCallback, useEffect, useState } from "react";
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

/** RecoveryCopiesSection puts session versions and copies behind a button.
 *
 *  Three steps on purpose, because they cost very different amounts and the last
 *  one can lose work.
 *
 *  Opening the panel is one directory read per session folder, so it appears at once
 *  and shows the shape of the problem - which conversations have copies, how big
 *  those copies are, how recently they were written - without opening any
 *  transcript. Conversations start collapsed: the usual question is "which
 *  conversation has copies worth merging", and one line each keeps that scannable.
 *  Expanding one shows its lines as rows - the canonical transcript first, then each
 *  copy beneath it.
 *
 *  Measuring a copy means reading and comparing whole transcripts, and one copy on a
 *  busy machine reaches a hundred megabytes. So it happens when asked, for one
 *  conversation or for all of them, and the numbers land in the rows afterwards.
 *
 *  Merging asks first and shows what it is about to do, because it writes to the
 *  session files.
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
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
      // Scanning is the moment the detail becomes worth reading, so open the row.
      setExpanded((current) => new Set(current).add(mainPath));
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
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;

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
    let merged = 0;
    let leftBehind = 0;
    try {
      for (const group of chosen) {
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
          <div className="modal-backdrop" onClick={() => setOpen(false)}>
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
                  <div className="rc-table">
                    <div className="rc-row rc-row--head">
                      <span />
                      <span>{t("settings.recoveryCopiesColLine")}</span>
                      <span>{t("settings.recoveryCopiesColModified")}</span>
                      <span>{t("settings.recoveryCopiesColSize")}</span>
                      <span>{t("settings.recoveryCopiesColTotal")}</span>
                      <span>{t("settings.recoveryCopiesColShared")}</span>
                      <span>{t("settings.recoveryCopiesColUnique")}</span>
                    </div>

                    {groups.map((group) => {
                      const isOpen = expanded.has(group.mainPath);
                      const isScanning = scanning.has(group.mainPath);
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
                            <span />
                            <span />
                            <span className="rc-muted">×{group.copies.length}</span>
                            <span />
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
                                  <span className="rc-muted">{copy.scanned ? copy.shared : "—"}</span>
                                  <span className={copy.unique > 0 ? "rc-unique" : "rc-muted"}>
                                    {copy.scanned ? copy.unique : "—"}
                                  </span>
                                </div>
                              ))}
                            </>
                          )}
                        </div>
                      );
                    })}
                  </div>
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
                {note ? <span className="rc-note">{note}</span> : null}
              </div>
            </div>
          </div>,
          document.body,
        )}

      {dialog}
    </>
  );
}
