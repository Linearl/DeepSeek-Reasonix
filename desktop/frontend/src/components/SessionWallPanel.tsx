// SessionWallPanel is the task-505 session graph wall: a grid of session cards
// (dozens at once) opened from the command palette's "跳转会话" entry, the
// enhancement half of the palette's legacy recent-sessions slice (fork rule 8).
// The whole surface is gated by the labFlags "sessionWall" switch in App —
// with the switch off this component is never imported, let alone mounted.
//
// Interaction model:
//   - Opening snapshots the session list via `load` (same freshness rule as
//     the palette's open-time snapshot).
//   - The search input auto-focuses; typing filters cards live (same token
//     semantics as the palette fuzzy scorer).
//   - Two group modes: by project (workspace folder buckets) or by recent
//     activity (day buckets); groups and cards are activity-ranked.
//   - Clicking a card jumps straight into that session (including ones not
//     currently open — resume handles the hydration); Esc / backdrop closes.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Search } from "lucide-react";
import { useT } from "../lib/i18n";
import { useMountTransition } from "../lib/useMountTransition";
import { paletteSessionDisplayTitle, paletteSessionHint, sessionActivityTime } from "../lib/session";
import { applySessionWallQuery, groupSessionsForWall, type SessionWallGroupMode } from "../lib/sessionWall";
import type { SessionMeta } from "../lib/types";

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

export function SessionWallPanel({
  open,
  load,
  onClose,
  onResume,
}: {
  open: boolean;
  load: () => Promise<SessionMeta[]>;
  onClose: () => void;
  onResume: (session: SessionMeta) => void;
}) {
  const t = useT();
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [query, setQuery] = useState("");
  const [mode, setMode] = useState<SessionWallGroupMode>("project");
  const inputRef = useRef<HTMLInputElement>(null);
  const isOpenRef = useRef(false);
  isOpenRef.current = open;
  // Keep the wall mounted through its exit animation after `open` flips
  // false; `status` drives the enter/exit keyframes via data-state.
  const { mounted, status } = useMountTransition(open, 200);

  // (Re)load the session snapshot on every open edge; a failed load degrades
  // to the empty state rather than a dead panel.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoaded(false);
    void load()
      .then((list) => {
        if (cancelled) return;
        setSessions(list);
        setLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setSessions([]);
        setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [open, load]);

  // Re-init whenever the wall opens: clear the query and mode, steal focus.
  useLayoutEffect(() => {
    if (open) {
      setQuery("");
      setMode("project");
      inputRef.current?.focus();
    }
  }, [open]);

  // Callback ref: focus the input as soon as it mounts while the wall is open.
  const inputCallbackRef = useCallback(
    (el: HTMLInputElement | null) => {
      inputRef.current = el;
      if (el && isOpenRef.current) el.focus();
    },
    [],
  );

  // Esc closes (document-level so it works even if focus drifted into a card).
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  const dayLabel = useCallback(
    (ms: number) => {
      const days = Math.round((startOfDay(new Date()) - startOfDay(new Date(ms))) / 86_400_000);
      if (days <= 0) return t("history.today");
      if (days === 1) return t("history.yesterday");
      return new Date(ms).toLocaleDateString();
    },
    [t],
  );

  const filtered = useMemo(() => applySessionWallQuery(sessions, query), [sessions, query]);
  const groups = useMemo(
    () => groupSessionsForWall(filtered, mode, { globalLabel: t("sessionWall.globalGroup"), dayLabel }),
    [filtered, mode, dayLabel, t],
  );

  if (!mounted) return null;

  return (
    <div
      className="drawer-backdrop drawer-backdrop--subtle session-wall-backdrop"
      data-state={status}
      onClick={onClose}
      role="presentation"
    >
      <div
        className="session-wall"
        data-state={status}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={t("sessionWall.title")}
      >
        <div className="session-wall__head">
          <Search className="session-wall__search-icon" size={18} aria-hidden="true" />
          <input
            ref={inputCallbackRef}
            className="session-wall__input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("sessionWall.placeholder")}
            spellCheck={false}
            autoComplete="off"
          />
          <div className="session-wall__modes" role="group" aria-label={t("sessionWall.groupMode")}>
            {(["project", "time"] as const).map((m) => (
              <button
                key={m}
                type="button"
                className={`session-wall__mode${mode === m ? " session-wall__mode--on" : ""}`}
                aria-pressed={mode === m}
                onClick={() => setMode(m)}
              >
                {t(m === "project" ? "sessionWall.modeProject" : "sessionWall.modeTime")}
              </button>
            ))}
          </div>
          <button
            className="palette__esc"
            type="button"
            onClick={onClose}
            aria-label={t("common.close")}
            title={t("common.close")}
          >
            esc
          </button>
        </div>
        <div className="session-wall__meta">
          {loaded ? t("sessionWall.count", { n: filtered.length }) : t("sessionWall.loading")}
        </div>
        <div className="session-wall__list">
          {!loaded ? null : filtered.length === 0 ? (
            <div className="session-wall__empty">{sessions.length === 0 ? t("sessionWall.empty") : t("palette.empty")}</div>
          ) : (
            groups.map((g) => (
              <div className="session-wall__group" key={g.key}>
                <div className="session-wall__group-title">
                  <span className="session-wall__group-name">{g.label}</span>
                  <span className="session-wall__group-count">{t("sessionWall.count", { n: g.sessions.length })}</span>
                </div>
                <div className="session-wall__grid">
                  {g.sessions.map((s) => {
                    const title = paletteSessionDisplayTitle(s, t("history.emptySession"));
                    const hint = paletteSessionHint(s);
                    return (
                      <button
                        type="button"
                        key={s.path}
                        className="session-wall__card"
                        title={title}
                        onClick={() => onResume(s)}
                      >
                        <span className="session-wall__card-title">{title}</span>
                        {hint && <span className="session-wall__card-hint">{hint}</span>}
                        <span className="session-wall__card-meta">
                          <span>{dayLabel(sessionActivityTime(s))}</span>
                          <span>{t(s.turns === 1 ? "history.turnOne" : "history.turnOther", { n: s.turns })}</span>
                          {s.open && <span className="session-wall__card-open">{t("sessionWall.openBadge")}</span>}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </div>
            ))
          )}
        </div>
        <div className="session-wall__foot">
          <span>{t("sessionWall.footJump")}</span>
          <span>
            <kbd>esc</kbd> {t("common.close")}
          </span>
        </div>
      </div>
    </div>
  );
}
