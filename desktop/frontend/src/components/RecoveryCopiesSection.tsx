import { useCallback, useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";

import { app, type ConsolidationReport, type RecoveryCopyGroupView, type RecoveryChainPreview, type RecoveryChainSet, type RecoveryChainView } from "../lib/bridge";
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
 *  never be measured and must not hold a merge back.
 */
function copiesNeedingMeasurement(group: RecoveryCopyGroupView) {
  return group.copies.filter((copy) => !copy.orphan && !copy.scanned);
}

/** uniqueIn is the count that decides whether merging is worth anything: events a
 *  copy holds that the canonical transcript does not. */
function uniqueIn(group: RecoveryCopyGroupView): number {
  return group.copies.reduce((n, c) => n + (c.orphan ? 0 : c.unique), 0);
}

/** Winner describes which transcript a merge would end up with.
 *
 *  The backend promotes the fullest loadable candidate, whether that is the main
 *  transcript or one of the copies, so the panel can say in advance which one wins
 *  instead of surprising the user afterwards. The turns of a copy are only known
 *  once it has been measured, so a group that has not been measured has no winner
 *  to name yet.
 */
type Winner = { isMain: boolean; turns: number; copyIndex: number };

function predictedWinner(group: RecoveryCopyGroupView): Winner | null {
  if (copiesNeedingMeasurement(group).length > 0) return null;
  let best: Winner = { isMain: true, turns: group.mainMessages, copyIndex: -1 };
  group.copies.forEach((copy, index) => {
    if (copy.orphan) return;
    if (copy.messages > best.turns) best = { isMain: false, turns: copy.messages, copyIndex: index };
  });
  return best;
}

/** phaseOf sorts a conversation by what a user would do about it. */
function phaseOf(group: RecoveryCopyGroupView): Phase {
  if (copiesNeedingMeasurement(group).length > 0) return "pending";
  if (uniqueIn(group) > 0) return "unmerged";
  return "merged";
}

/** Outcome is one conversation's result. Kept per conversation because a count
 *  cannot say which conversation was refused, nor why. */
type Outcome = {
  label: string;
  kind: "merged" | "forced" | "blocked" | "failed";
  before?: number;
  after?: number;
  trashed?: number;
  notCovered?: number;
  reasons?: string[];
  detail?: string;
};

/** RecoveryCopiesSection puts session versions and copies behind a button.
 *
 *  Opening the panel is one directory read per session folder, so it appears at
 *  once and shows the shape of the problem without opening any transcript.
 *  Conversations start collapsed and are grouped by what they need.
 *
 *  Measuring a copy means reading and comparing whole transcripts. The panel offers
 *  it on demand, and the merge performs it itself when needed - the preview is built
 *  from those counts, so the button can produce them rather than sending the user
 *  back to press Scan first.
 *
 *  This is a promotion, not a splice: the fullest candidate becomes the canonical
 *  transcript and the rest are folded into the recoverable trash. So the preview
 *  names the winner in advance - including when that winner is a copy - because
 *  "which transcript will I be left with" is the question the merge decides.
 *
 *  Merging asks twice because the backend has two modes. A plain merge refuses when
 *  the fullest copy and the main transcript each hold turns the other lacks
 *  (typical after a main-side compaction). That refusal is reported, and the user is
 *  then asked whether the copy should win. Neither mode runs without its own
 *  confirmation.
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
  const [status, setStatus] = useState("");
  const [outcomes, setOutcomes] = useState<Outcome[]>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const [failed, setFailed] = useState(false);
  // chains holds each expanded conversation's candidate chains, keyed by main path.
  // Loading them replays every log for that conversation, so it happens when a row
  // is opened rather than for the whole list at once.
  const [chains, setChains] = useState<Map<string, RecoveryChainSet>>(new Map());
  // pickedChain names the branch the user chose to win, keyed by main path. Empty
  // means "let the engine take the fullest one". Keyed per conversation so a pick
  // in one group never leaks into another.
  const [pickedChain, setPickedChain] = useState<Record<string, string>>({});
  const pickedChainCount = Object.values(pickedChain).filter(Boolean).length;
  // Tail reveal: the summary carries ~12k chars of trailing messages; the first
  // click shows the last 3k (recognize the scene), each further click widens by
  // another 3k until the budget is spent.
  const [showTail, setShowTail] = useState(false);
  const [tailChars, setTailChars] = useState(3000);
  // Clicking a chain previews it in a wide dialog - sizes, how it starts and
  // ends, and what picking it keeps or drops against the main - and only a
  // confirm inside that dialog names it as the winner. Clicking the picked
  // chain again un-picks it. The main chain is pickable too: it means keep the
  // current main and archive the rest. Nothing is written on preview.
  const togglePick = async (mainPath: string, chain: RecoveryChainView) => {
    if (pickedChain[mainPath] === chain.path) {
      setPickedChain((current) => {
        const next = { ...current };
        delete next[mainPath];
        return next;
      });
      return;
    }
    let preview: RecoveryChainPreview;
    setBusy(true);
    setShowTail(false);
    setTailChars(3000);
    try {
      preview = await app.PreviewRecoveryChain(mainPath, chain.path);
    } catch (err) {
      setBusy(false);
      // Surface preview failures where the click happened - burying them in the
      // page-level error list is why "some branches do nothing on click" read as
      // a dead button instead of a diagnosable failure.
      const message = err instanceof Error ? err.message : String(err);
      await confirm({
        title: t("settings.recoveryCopiesPreviewFailedTitle"),
        message: (
          <div>
            <p>{t("settings.recoveryCopiesPreviewFailedLead")}</p>
            <p className="rc-outcome__reasons">{message}</p>
          </div>
        ),
        confirmLabel: t("common.close"),
        cancelLabel: t("common.cancel"),
        tone: "danger",
      });
      return;
    }
    setBusy(false);
    const dropping = preview.sharedWithMain < preview.messageCount && !preview.isMain;
    const ok = await confirm({
      title: t("settings.recoveryCopiesPreviewTitle"),
      wide: true,
      message: (
        <div className="rc-preview">
          <div className="rc-preview__stats">
            <span>
              {t("settings.recoveryCopiesColMessages")} {preview.messageCount}
            </span>
            <span>
              {preview.turns} {t("settings.recoveryCopiesColTurns")}
            </span>
            {!preview.isMain && !preview.degraded ? (
              <>
                <span>
                  {t("settings.recoveryCopiesPreviewShared")} {preview.sharedWithMain}
                </span>
                <span>
                  {t("settings.recoveryCopiesPreviewUnique")} {preview.uniqueToChain}
                </span>
              </>
            ) : null}
          </div>
          {preview.degraded ? (
            <div className="rc-preview__warn">{t("settings.recoveryCopiesPreviewDegraded")}</div>
          ) : null}
          {/* The trailing messages, revealed on demand: zero-latency because they
              arrive with the summary, and enough of the ending to recognize the
              scene without replaying the whole log. */}
          {(() => {
            const all = preview.tailLines ?? [];
            const total = all.reduce((n, line) => n + line.length, 0);
            if (total === 0) return null;
            const shown = showTail ? Math.min(total, tailChars) : Math.min(total, 3000);
            const lines: string[] = [];
            let spent = 0;
            for (let i = all.length - 1; i >= 0 && spent < shown; i--) {
              lines.unshift(all[i]);
              spent += all[i].length;
            }
            const remaining = total - spent;
            return (
              <>
                <button
                  className="btn btn--small"
                  type="button"
                  onClick={() => (showTail ? setTailChars((n) => n + 3000) : setShowTail(true))}
                >
                  {!showTail
                    ? t("settings.recoveryCopiesPreviewTailShow")
                    : remaining > 0
                      ? `${t("settings.recoveryCopiesPreviewTailMore")} (${remaining})`
                      : t("settings.recoveryCopiesPreviewTailHide")}
                </button>
                {showTail ? (
                  <div className="rc-preview__section rc-preview__section--tail">
                    <div className="rc-preview__label">{t("settings.recoveryCopiesPreviewEnd")}</div>
                    {lines.map((line, i) => (
                      <div className="rc-preview__line" key={i}>
                        {line}
                      </div>
                    ))}
                  </div>
                ) : null}
              </>
            );
          })()}
          {dropping ? (
            <div className="rc-preview__warn">
              {t("settings.recoveryCopiesPreviewDropWarn").replace("{n}", String(preview.uniqueToChain))}
            </div>
          ) : null}
        </div>
      ),
      confirmLabel: preview.isMain
        ? t("settings.recoveryCopiesPickKeepMain")
        : t("settings.recoveryCopiesPickConfirm"),
      cancelLabel: t("common.cancel"),
    });
    if (ok) {
      setPickedChain((current) => ({ ...current, [mainPath]: chain.path }));
    }
  };

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

  /** loadChains replays one conversation's logs to list its candidate chains. A
   *  merge promotes the chain with the most messages, so the list is what tells the
   *  user which transcript they are about to be left with. */
  const loadChains = useCallback(async (mainPath: string) => {
    try {
      const set = await app.ListRecoveryChains(mainPath);
      setChains((current) => new Map(current).set(mainPath, set));
    } catch (err) {
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
    }
  }, []);

  const toggleExpanded = (path: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
        if (!chains.has(path)) void loadChains(path);
      }
      return next;
    });
  };

  /** scanOne measures a conversation and returns the fresh row, so callers that
   *  need the numbers immediately do not have to wait for a re-render. */
  const scanOne = async (mainPath: string): Promise<RecoveryCopyGroupView | null> => {
    setScanning((current) => new Set(current).add(mainPath));
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
      // Scanning is the moment the detail becomes worth reading, so open the row.
      setExpanded((current) => new Set(current).add(mainPath));
      return fresh;
    } catch (err) {
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
      return null;
    } finally {
      setScanning((current) => {
        const next = new Set(current);
        next.delete(mainPath);
        return next;
      });
    }
  };

  const scanAll = async () => {
    setErrors([]);
    for (const group of groups ?? []) {
      await scanOne(group.mainPath);
    }
  };

  // Scan only the rows the user has checked. On a project with dozens of
  // conversation groups the full sweep replays every log of every session; the
  // checked subset is the one the user is actually deciding about.
  const scanSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;
    setErrors([]);
    for (const group of chosen) {
      await scanOne(group.mainPath);
    }
  };

  const winnerText = (group: RecoveryCopyGroupView) => {
    const winner = predictedWinner(group);
    if (!winner) return "—";
    return winner.isMain
      ? `${t("settings.recoveryCopiesWinnerMain")} ${winner.turns}`
      : `${t("settings.recoveryCopiesCopy")} ${winner.copyIndex + 1} ${winner.turns}`;
  };

  const mergeSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;

    setBusy(true);
    setErrors([]);
    setOutcomes([]);
    try {
      // Measure anything unmeasured here rather than sending the user back to press
      // Scan. The preview is built out of these counts, so it cannot be shown first.
      const measured: RecoveryCopyGroupView[] = [];
      for (const group of chosen) {
        if (copiesNeedingMeasurement(group).length > 0) {
          setStatus(`${t("settings.recoveryCopiesMeasuring")} ${group.mainLabel}`);
          const fresh = await scanOne(group.mainPath);
          measured.push(fresh ?? group);
        } else {
          measured.push(group);
        }
      }
      setStatus("");

      // The preview states the change, not just the inputs: which transcript is
      // left after the merge, its turn count against the current main's, and how
      // much unique work would be folded away.
      const totalUnique = measured.reduce((n, g) => n + uniqueIn(g), 0);
      const totalCopies = measured.reduce((n, g) => n + g.copies.length, 0);
      const ok = await confirm({
        title: t("settings.recoveryCopiesMerge"),
        message: (
          <div>
            <p>
              {t("settings.recoveryCopiesPreviewLead")}：{measured.length} ·{" "}
              {t("settings.recoveryCopiesPreviewCopies")} {totalCopies} ·{" "}
              {t("settings.recoveryCopiesUnique")} {totalUnique}
            </p>
            <table className="rc-preview-table">
              <thead>
                <tr>
                  <th>{t("settings.recoveryCopiesColLine")}</th>
                  <th>{t("settings.recoveryCopiesColTurns")}</th>
                  <th>{t("settings.recoveryCopiesColWinner")}</th>
                  <th>{t("settings.recoveryCopiesColUnique")}</th>
                </tr>
              </thead>
              <tbody>
                {measured.map((g) => (
                  <tr key={g.mainPath}>
                    <td>{g.mainLabel}</td>
                    <td>{g.mainMessages || "—"}</td>
                    <td>{winnerText(g)}</td>
                    <td>{uniqueIn(g)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="rc-preview__warn">{t("settings.recoveryCopiesPreviewWarn")}</p>
          </div>
        ),
        confirmLabel: t("settings.recoveryCopiesMerge"),
        cancelLabel: t("common.cancel"),
        tone: "danger",
      });
      if (!ok) return;

      const collected: Outcome[] = [];
      // Report per conversation as it finishes. A merge over a dozen copies takes long
      // enough that a silent run reads as "nothing happened", and the per-conversation
      // result is what tells the user whether it worked.
      for (const [index, group] of measured.entries()) {
        setStatus(
          `${t("settings.recoveryCopiesMerging")} ${index + 1}/${measured.length} · ${group.mainLabel}`,
        );
        let report: ConsolidationReport;
        try {
          report = await app.ConsolidateSessionRecoveryCopies(group.mainPath, pickedChain[group.mainPath] ?? "");
        } catch (err) {
          collected.push({
            label: group.mainLabel,
            kind: "failed",
            detail: err instanceof Error ? err.message : String(err),
          });
          setOutcomes([...collected]);
          continue;
        }

        if (report.blockedByDivergence) {
          // The backend refuses when the main and the fullest copy each hold turns
          // the other lacks. Ask before letting the copy win, and say what that
          // costs: the current main is archived whole and stays recoverable.
          setBusy(false);
          const winner = predictedWinner(group);
          const forceOk = await confirm({
            title: t("settings.recoveryCopiesForceTitle"),
            message: (
              <div>
                <p>
                  {t("settings.recoveryCopiesForceLead")}：{group.mainLabel}
                </p>
                <p>
                  {t("settings.recoveryCopiesForceTurns")} {group.mainMessages || "—"} →{" "}
                  {winner?.turns || "—"}
                </p>
                <p className="rc-preview__warn">{t("settings.recoveryCopiesForceWarn")}</p>
              </div>
            ),
            confirmLabel: t("settings.recoveryCopiesForceConfirm"),
            cancelLabel: t("common.cancel"),
            tone: "danger",
          });
          setBusy(true);
          if (!forceOk) {
            collected.push({ label: group.mainLabel, kind: "blocked", notCovered: uniqueIn(group) });
            setOutcomes([...collected]);
            continue;
          }
          try {
            const forced = await app.ForceConsolidateSessionRecoveryCopies(group.mainPath, pickedChain[group.mainPath] ?? "");
            collected.push({
              label: group.mainLabel,
              kind: "forced",
              before: forced.mainMessageCount,
              after: forced.winnerMessageCount,
              trashed: forced.trashed?.length ?? 0,
            });
          } catch (err) {
            collected.push({
              label: group.mainLabel,
              kind: "failed",
              detail: err instanceof Error ? err.message : String(err),
            });
          }
          setOutcomes([...collected]);
          continue;
        }

        collected.push({
          label: group.mainLabel,
          kind: "merged",
          before: report.mainMessageCount,
          after: report.winnerMessageCount,
          trashed: report.trashed?.length ?? 0,
          notCovered: report.notCoveredDetail?.filter((d) => d.unique > 0).length ?? 0,
          reasons: report.notCoveredDetail?.map((d) => d.reason).filter((r): r is string => Boolean(r)),
        });
        setOutcomes([...collected]);
      }

      setOutcomes(collected);
      setSelected(new Set());
      // Named winners were consumed by the merge just run; the picked branches
      // no longer exist as copies, so the next run starts neutral.
      setPickedChain({});
      await load();
    } finally {
      setStatus("");
      setBusy(false);
    }
  };

  const scanningAll = scanning.size > 0;
  const selectedGroups = (groups ?? []).filter((g) => selected.has(g.mainPath));
  const needingCount = selectedGroups.reduce((n, g) => n + copiesNeedingMeasurement(g).length, 0);
  const anyMerged = outcomes.some((o) => o.kind === "merged" || o.kind === "forced");

  const renderGroup = (group: RecoveryCopyGroupView) => {
    const isOpen = expanded.has(group.mainPath);
    const isScanning = scanning.has(group.mainPath);
    const measured = copiesNeedingMeasurement(group).length === 0;
    const groupUnique = measured ? uniqueIn(group) : null;
    const winner = measured ? predictedWinner(group) : null;
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
            <div className={`rc-row rc-row--main${winner?.isMain ? " rc-row--winner" : ""}`}>
              <span />
              <span className="rc-label">
                {t("settings.recoveryCopiesMain")}
                {winner?.isMain ? ` · ${t("settings.recoveryCopiesWins")}` : ""}
                {group.mainExists ? "" : ` · ${t("settings.recoveryCopiesOrphan")}`}
              </span>
              <span>{formatWhen(group.mainModified)}</span>
              <span>{formatBytes(group.mainBytes)}</span>
              <span>{group.mainMessages || "—"}</span>
              <span className="rc-muted">—</span>
              <span className="rc-muted">—</span>
              <span />
            </div>
            {group.copies.map((copy, index) => {
              const wins = winner ? !winner.isMain && winner.copyIndex === index : false;
              return (
                <div
                  className={`rc-row rc-row--copy${wins ? " rc-row--winner" : ""}`}
                  key={copy.path}
                >
                  <span />
                  <span className="rc-label">
                    {t("settings.recoveryCopiesCopy")} {index + 1}
                    {wins ? ` · ${t("settings.recoveryCopiesWins")}` : ""}
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
              );
            })}
            {(() => {
              const set = chains.get(group.mainPath);
              if (!set || set.chains.length === 0) return null;
              return (
                <div className="rc-chains">
                  <div className="rc-chains__head">{t("settings.recoveryCopiesChains")}</div>
                  {set.chains.map((chain, index) => {
                    const recommended =
                      chain.path === set.longestPath && chain.headId === set.longestHead;
                    return (
                      <div
                        className={`rc-chain${recommended ? " rc-chain--recommended" : ""}${
                          pickedChain[group.mainPath] === chain.path ? " rc-chain--picked" : ""
                        }`}
                        key={`${chain.path}#${chain.headId}`}
                      >
                        <span className="rc-chain__name">
                          {chain.path === set.mainPath
                            ? t("settings.recoveryCopiesChainCurrent")
                            : `${t("settings.recoveryCopiesChainCandidate")} ${index}`}
                        </span>
                        <span>
                          {chain.messageCount} · {chain.turns} {t("settings.recoveryCopiesColTurns")}
                        </span>
                        <span>{formatWhen(chain.lastActivity)}</span>
                        <span>{formatBytes(chain.bytes)}</span>
                        {recommended ? (
                          <span className="rc-chain__badge">{t("settings.recoveryCopiesRecommended")}</span>
                        ) : null}
                        {/* The head's own preview text: what this chain would leave the
                            conversation looking like, which is the thing a user needs in
                            order to choose. Without it the list showed numbers only. */}
                        <span className="rc-chain__preview" title={chain.preview}>
                          {chain.preview?.trim() || t("settings.recoveryCopiesPreviewEmpty")}
                        </span>
                        {pickedChain[group.mainPath] === chain.path ? (
                          <span className="rc-chain__picked-badge">{t("settings.recoveryCopiesPickedBadge")}</span>
                        ) : null}
                        <button
                          className="btn btn--small rc-chain__preview-btn"
                          type="button"
                          disabled={busy || scanning.size > 0}
                          onClick={() => { void togglePick(group.mainPath, chain); }}
                        >
                          {pickedChain[group.mainPath] === chain.path
                            ? t("settings.recoveryCopiesUnpick")
                            : t("settings.recoveryCopiesPreviewBtn")}
                        </button>
                      </div>
                    );
                  })}
                </div>
              );
            })()}
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

  const outcomeText = (outcome: Outcome) => {
    switch (outcome.kind) {
      case "merged":
        return `${t("settings.recoveryCopiesOutcomeMerged")} ${outcome.before} → ${outcome.after}`;
      case "forced":
        return `${t("settings.recoveryCopiesOutcomeForced")} ${outcome.before} → ${outcome.after}`;
      case "blocked":
        return t("settings.recoveryCopiesOutcomeBlocked");
      default:
        return outcome.detail ?? "";
    }
  };

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
                    {outcomes.length > 0 && (
                      <div className="rc-result">
                        <ul className="rc-outcomes">
                          {outcomes.map((outcome) => (
                            <li
                              key={outcome.label}
                              className={
                                outcome.kind === "failed"
                                  ? "rc-outcome--failed"
                                  : outcome.kind === "blocked"
                                    ? "rc-outcome--blocked"
                                    : "rc-outcome--ok"
                              }
                            >
                              <span className="rc-outcome__label">{outcome.label}</span>
                              <span>{outcomeText(outcome)}</span>
                              {outcome.trashed ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesArchived")} {outcome.trashed}
                                </span>
                              ) : null}
                              {outcome.notCovered ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesLeftBehind")} {outcome.notCovered}
                                </span>
                              ) : null}
                              {outcome.reasons?.length ? (
                                <div className="rc-outcome__reasons">
                                  {outcome.reasons.map((r, i) => (
                                    <div key={i}>{r}</div>
                                  ))}
                                </div>
                              ) : null}
                            </li>
                          ))}
                        </ul>
                        {anyMerged ? (
                          <p className="rc-result__hint">{t("settings.recoveryCopiesReopen")}</p>
                        ) : null}
                      </div>
                    )}
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
                  {pickedChainCount > 0
                    ? `${t("settings.recoveryCopiesMergePicked")} (${pickedChainCount})`
                    : `${t("settings.recoveryCopiesMerge")} (${selected.size})`}
                </button>
                <button
                  className="btn btn--small"
                  type="button"
                  disabled={busy || scanningAll}
                  onClick={() => void scanAll()}
                >
                  {t("settings.recoveryCopiesScanAll")}
                </button>
                {selected.size > 0 ? (
                  <button
                    className="btn btn--small"
                    type="button"
                    disabled={busy || scanningAll}
                    onClick={() => void scanSelected()}
                  >
                    {`${t("settings.recoveryCopiesScanSelected")} (${selected.size})`}
                  </button>
                ) : null}
                <button
                  className="btn btn--small"
                  type="button"
                  disabled={busy || scanningAll}
                  onClick={() => void load()}
                >
                  {t("settings.recoveryCopiesRefresh")}
                </button>
                {busy ? (
                  <span className="rc-note">{status || t("settings.loading")}</span>
                ) : selectedGroups.length === 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesPickFirst")}</span>
                ) : needingCount > 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesWillMeasure")}</span>
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
