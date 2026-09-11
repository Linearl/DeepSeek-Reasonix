import { useCallback, useEffect, useMemo, useState } from "react";
import { app, type RecoveryCopyGroupView } from "../lib/bridge";
import { useT } from "../lib/i18n";

/** RecoveryCopiesSection lists every conversation that has recovery copies beside
 *  it, with the split that decides what merging each one means.
 *
 *  Two steps on purpose. The listing is inert: it shows how many events a copy
 *  shares with its canonical transcript and how many are its own, so a copy that is
 *  pure duplication can be told apart from one holding work that was lost. Only
 *  then does a selected group get merged, which is the step that can lose
 *  something.
 */
export function RecoveryCopiesSection() {
  const t = useT();
  const [groups, setGroups] = useState<RecoveryCopyGroupView[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      setGroups(await app.ListRecoveryCopyGroups());
    } catch {
      setGroups(null);
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const totalUnique = useMemo(
    () => (groups ?? []).reduce((sum, group) => sum + group.copies.reduce((n, copy) => n + (copy.orphan ? 0 : copy.unique), 0), 0),
    [groups],
  );

  const toggle = (path: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
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
            <div className="settings-recovery-copies__summary">
              {t("settings.recoveryCopiesSummary")} {groups.length} / {totalUnique}
            </div>
            {groups.map((group) => (
              <div className="settings-recovery-copies__group" key={group.mainPath}>
                <label className="settings-recovery-copies__group-head">
                  <input
                    type="checkbox"
                    checked={selected.has(group.mainPath)}
                    disabled={busy}
                    onChange={() => toggle(group.mainPath)}
                  />
                  <span className="settings-recovery-copies__group-title">{group.mainPath.split(/[\\/]/).pop()}</span>
                  <span className="settings-recovery-copies__group-meta">
                    {t("settings.recoveryCopiesMain")} {group.mainMessages} · {group.copies.length}
                  </span>
                </label>
                <div className="settings-recovery-copies__copies">
                  {group.copies.map((copy) => (
                    <div className="settings-recovery-copies__copy" key={copy.path}>
                      <span className="settings-recovery-copies__copy-name">{copy.path.split(/[\\/]/).pop()}</span>
                      <span className="settings-recovery-copies__copy-meta">
                        {t("settings.recoveryCopiesTotal")} {copy.messages} · {t("settings.recoveryCopiesShared")} {copy.shared} · {t("settings.recoveryCopiesUnique")} {copy.unique}
                        {copy.orphan ? ` · ${t("settings.recoveryCopiesOrphan")}` : ""}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            ))}
            <div className="settings-recovery-copies__actions">
              <button
                className="btn btn--small btn--primary"
                type="button"
                disabled={busy || selected.size === 0}
                onClick={() => void mergeSelected()}
              >
                {t("settings.recoveryCopiesMerge")}
              </button>
              <button className="btn btn--small" type="button" disabled={busy} onClick={() => void load()}>
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
