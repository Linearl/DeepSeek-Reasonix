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
import { createPortal } from "react-dom";
import { ChevronDown, Folder, Search } from "lucide-react";
import { useT } from "../lib/i18n";
import { useMountTransition } from "../lib/useMountTransition";
import { reportFrontendLog } from "../lib/frontendLog";
import { paletteSessionDisplayTitle, paletteSessionHint, sessionActivityTime } from "../lib/session";
import { topicActivityLabel } from "../lib/projectTreeTopic";
import {
  applySessionWallQuery,
  applySessionWallOpenOnly,
  groupSessionsForWall,
  SESSION_WALL_EXCERPT_TARGET,
  SESSION_WALL_HOVER_OPEN_DELAY_MS,
  sessionWallHoverLayerOrigin,
  type SessionWallGroupMode,
} from "../lib/sessionWall";
import { loadSessionWallCollapsed, loadSessionWallOpenOnly, saveSessionWallCollapsed, saveSessionWallOpenOnly } from "../lib/sessionWallPrefs";
import type { SessionMeta } from "../lib/types";

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

export function SessionWallPanel({
  open,
  load,
  loadExcerpts,
  onClose,
  onResume,
  initialOpenOnly,
}: {
  open: boolean;
  load: () => Promise<SessionMeta[]>;
  // tl 1177b 方案B: on-demand hover excerpts — the panel pulls the newest user
  // messages per card when it is hovered/focused, so sessions saved before any
  // excerpts sidecar existed answer on the first hover. Absent prop (tests)
  // keeps the panel rendering from the list field only.
  loadExcerpts?: (session: SessionMeta) => Promise<string[]>;
  onClose: () => void;
  onResume: (session: SessionMeta) => void;
  // tl 1175: true forces the「仅已打开」filter on for this open (the「跳转标签页」
  // entry presets it); undefined falls back to the persisted preference.
  initialOpenOnly?: boolean;
}) {
  const t = useT();
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [query, setQuery] = useState("");
  const [mode, setMode] = useState<SessionWallGroupMode>("project");
  // tl 1175: the「仅已打开」filter. The persisted value is the user's last
  // manual toggle; `initialOpenOnly` (when defined) presets this open without
  // rewriting what the user persisted.
  const [openOnly, setOpenOnly] = useState(false);
  // tl 1176: collapsed project groups (project mode only — time-mode day
  // buckets are not foldable: their keys are dates, so a persisted "collapsed
  // yesterday" would silently mismatch after midnight, and the browsing intent
  // there is a timeline, not "hide this workspace"). Reloaded from prefs on
  // every open edge, so the state survives restarts.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  // tl 1177b 方案B: per-card hover excerpt cache, reset on every open edge.
  // "pending" holds the card's native title back so the conversation-name
  // tooltip cannot flash while the fetch is in flight; "ready" settles the
  // overlay (possibly empty — the title comes back for noise-only sessions).
  const [tailExcerpts, setTailExcerpts] = useState<Map<string, { status: "pending" } | { status: "ready"; lines: string[] }>>(
    () => new Map(),
  );
  const inputRef = useRef<HTMLInputElement>(null);
  const isOpenRef = useRef(false);
  isOpenRef.current = open;
  // tl 1188: the hover excerpt layer — a body portal floating OUTSIDE the
  // card (the user: hover content must live outside the card, "显示的区域
  // 不局限于卡片本身的大小"). Same shape as the project tree's hover card:
  // a hover timer opens it, the rect fixes its anchor, mouseleave cancels.
  // The excerpt data itself is NOT re-fetched here — the layer reads the
  // same tailExcerpts cache the card feeds, so a late fetch updates it live.
  const [hoverLayer, setHoverLayer] = useState<{ session: SessionMeta; groupLabel: string; left: number; top: number } | null>(null);
  const hoverLayerTimerRef = useRef<number | null>(null);
  // Keep the wall mounted through its exit animation after `open` flips
  // false; `status` drives the enter/exit keyframes via data-state.
  const { mounted, status } = useMountTransition(open, 200);

  // (Re)load the session snapshot on every open edge; a failed load degrades
  // to the empty state rather than a dead panel.
  // Task 627: both outcomes leave a desktop.log breadcrumb (feature=sessionWall)
  // so "the wall opened but showed nothing" is separable from "never mounted".
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoaded(false);
    void load()
      .then((list) => {
        if (cancelled) return;
        setSessions(list);
        setLoaded(true);
        reportFrontendLog("sessionWall", "panel loaded", `${list.length} sessions`);
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        setSessions([]);
        setLoaded(true);
        reportFrontendLog("sessionWall", "session list load failed", error instanceof Error ? error.message : String(error));
      });
    return () => {
      cancelled = true;
    };
  }, [open, load]);

  // Re-init whenever the wall opens: clear the query and mode, resolve the
  // open-only filter (preset beats persisted), reload the collapsed groups,
  // steal focus.
  useLayoutEffect(() => {
    if (open) {
      setQuery("");
      setMode("project");
      setOpenOnly(initialOpenOnly ?? loadSessionWallOpenOnly());
      setCollapsed(new Set(loadSessionWallCollapsed()));
      setTailExcerpts(new Map());
      requestedExcerptsRef.current = new Set();
      setHoverLayer(null);
      if (hoverLayerTimerRef.current !== null) {
        window.clearTimeout(hoverLayerTimerRef.current);
        hoverLayerTimerRef.current = null;
      }
      inputRef.current?.focus();
    }
    // loadSessionWallOpenOnly / loadSessionWallCollapsed are read once per
    // open edge on purpose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  // tl 1188: a pending hover timer must not survive the panel unmounting.
  useEffect(() => {
    return () => {
      if (hoverLayerTimerRef.current !== null) window.clearTimeout(hoverLayerTimerRef.current);
    };
  }, []);

  // tl 1188: the hover layer opens on a delay (anti-flicker, same 350ms gate
  // as the project tree's hover card) and closes the moment the pointer or
  // focus leaves the card. Re-scheduling on another card replaces both the
  // timer and — when the new timer fires — the layer content, so switching
  // cards never leaves a stale layer behind.
  const cancelHoverLayer = useCallback(() => {
    if (hoverLayerTimerRef.current !== null) {
      window.clearTimeout(hoverLayerTimerRef.current);
      hoverLayerTimerRef.current = null;
    }
    setHoverLayer((current) => (current === null ? current : null));
  }, []);
  const scheduleHoverLayer = useCallback((element: HTMLElement, session: SessionMeta, groupLabel: string) => {
    if (hoverLayerTimerRef.current !== null) window.clearTimeout(hoverLayerTimerRef.current);
    hoverLayerTimerRef.current = window.setTimeout(() => {
      hoverLayerTimerRef.current = null;
      if (!element.isConnected) return;
      const rect = element.getBoundingClientRect();
      const { left, top } = sessionWallHoverLayerOrigin(rect, { width: window.innerWidth, height: window.innerHeight });
      setHoverLayer({ session, groupLabel, left, top });
    }, SESSION_WALL_HOVER_OPEN_DELAY_MS);
  }, []);

  const dayLabel = useCallback(
    (ms: number) => {
      const days = Math.round((startOfDay(new Date()) - startOfDay(new Date(ms))) / 86_400_000);
      if (days <= 0) return t("history.today");
      if (days === 1) return t("history.yesterday");
      return new Date(ms).toLocaleDateString();
    },
    [t],
  );

  // tl 1177b 方案B: cards the list field cannot answer pull their excerpts on
  // first hover/focus. The ref dedupes requests synchronously (one fetch per
  // card per open even under double render); a failed fetch degrades to "no
  // excerpts" — the native title returns rather than the card going mute.
  // tl 1188: "cannot answer" now means "fewer than the layer's full width" —
  // the external layer shows up to 8 excerpts, so a sidecar carrying the old
  // 3-wide field still pulls the tail instead of starving the layer.
  const requestedExcerptsRef = useRef<Set<string>>(new Set());
  const requestExcerpts = useCallback(
    (session: SessionMeta) => {
      if (!loadExcerpts) return;
      if ((session.recentExcerpts?.filter(Boolean).length ?? 0) >= SESSION_WALL_EXCERPT_TARGET) return;
      if (requestedExcerptsRef.current.has(session.path)) return;
      requestedExcerptsRef.current.add(session.path);
      setTailExcerpts((prev) => new Map(prev).set(session.path, { status: "pending" }));
      void loadExcerpts(session)
        .then((lines) => {
          setTailExcerpts((prev) => new Map(prev).set(session.path, { status: "ready", lines: Array.isArray(lines) ? lines : [] }));
        })
        .catch((error: unknown) => {
          reportFrontendLog("sessionWall", "excerpt load failed", error instanceof Error ? error.message : String(error));
          setTailExcerpts((prev) => new Map(prev).set(session.path, { status: "ready", lines: [] }));
        });
    },
    [loadExcerpts],
  );

  // tl 1175: toggling the「仅已打开」filter persists it for the next restart.
  const toggleOpenOnly = useCallback(() => {
    setOpenOnly((current) => {
      const next = !current;
      saveSessionWallOpenOnly(next);
      return next;
    });
  }, []);

  // tl 1176: collapsing a project group persists the whole set. The group key
  // (workspace root / the global marker) is restart-stable, so the array can
  // be rewritten verbatim on every toggle.
  const toggleGroupCollapsed = useCallback((key: string) => {
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      saveSessionWallCollapsed([...next]);
      return next;
    });
  }, []);

  // tl 1176: while searching, every matching group expands automatically —
  // a group the query hits is by definition what the user is looking for, so
  // hiding its cards behind a fold would defeat the search. Clearing the
  // query restores the persisted folds (this is a derived override, never
  // written back, so the user's fold memory is untouched).
  const foldsActive = mode === "project" && !query.trim();

  const filtered = useMemo(
    () => applySessionWallOpenOnly(applySessionWallQuery(sessions, query), openOnly),
    [sessions, query, openOnly],
  );
  const groups = useMemo(
    () => groupSessionsForWall(filtered, mode, { globalLabel: t("sessionWall.globalGroup"), dayLabel }),
    [filtered, mode, dayLabel, t],
  );

  if (!mounted) return null;

  // tl 1188: the external hover layer (project-tree hover-card pattern: a
  // body portal the card's own box can never clip). It reads the same
  // tailExcerpts cache the cards feed, so a tail fetch landing after the
  // layer opened fills it live; an excerpt-less session still shows the
  // meta block (turns · relative time · date · group folder).
  const renderHoverLayer = () => {
    if (!hoverLayer) return null;
    const s = hoverLayer.session;
    const tail = tailExcerpts.get(s.path);
    const excerpts = (tail?.status === "ready" ? tail.lines : s.recentExcerpts)?.filter(Boolean) ?? [];
    const activityAt = sessionActivityTime(s);
    const relative = topicActivityLabel(activityAt, t);
    const date = dayLabel(activityAt);
    return createPortal(
      <div className="session-wall-hover-layer" style={{ left: hoverLayer.left, top: hoverLayer.top }} aria-hidden="true">
        <div className="session-wall-hover-layer__title">{paletteSessionDisplayTitle(s, t("history.emptySession"))}</div>
        <div className="session-wall-hover-layer__meta">
          {[
            t(s.turns === 1 ? "history.turnOne" : "history.turnOther", { n: s.turns }),
            // Past 7 days the relative label IS the date — showing both would
            // repeat the same string (10/20 · 10/20).
            relative !== date ? relative : "",
            date,
          ]
            .filter(Boolean)
            .join(" · ")}
        </div>
        <div className="session-wall-hover-layer__project">
          <Folder size={12} aria-hidden="true" />
          <span>{hoverLayer.groupLabel}</span>
        </div>
        {excerpts.length > 0 && (
          <div className="session-wall-hover-layer__excerpts">
            {excerpts.map((line, i) => (
              <div className="session-wall-hover-layer__excerpt" key={i}>
                {line}
              </div>
            ))}
          </div>
        )}
      </div>,
      document.body,
    );
  };

  return (
    <>
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
            type="button"
            className={`session-wall__filter${openOnly ? " session-wall__filter--on" : ""}`}
            aria-pressed={openOnly}
            onClick={toggleOpenOnly}
          >
            {t("sessionWall.openOnly")}
          </button>
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
            groups.map((g) => {
              // tl 1176: folds apply in project mode only, and never while
              // searching (hit groups must expand — see foldsActive).
              const isFolded = foldsActive && collapsed.has(g.key);
              const head =
                mode === "project" ? (
                  <button
                    type="button"
                    className={`session-wall__group-head${isFolded ? " session-wall__group-head--folded" : ""}`}
                    aria-expanded={!isFolded}
                    onClick={() => toggleGroupCollapsed(g.key)}
                  >
                    <ChevronDown className="session-wall__group-chevron" size={14} aria-hidden="true" />
                    <span className="session-wall__group-name">{g.label}</span>
                    {/* Folded groups keep their count — that number is the
                        whole point of the collapsed row. */}
                    <span className="session-wall__group-count">{t("sessionWall.count", { n: g.sessions.length })}</span>
                  </button>
                ) : (
                  <div className="session-wall__group-title">
                    <span className="session-wall__group-name">{g.label}</span>
                    <span className="session-wall__group-count">{t("sessionWall.count", { n: g.sessions.length })}</span>
                  </div>
                );
              return (
                <div className="session-wall__group" key={g.key}>
                  {head}
                  {!isFolded && (
                    <div className="session-wall__grid">
                      {g.sessions.map((s) => {
                        const title = paletteSessionDisplayTitle(s, t("history.emptySession"));
                        const hint = paletteSessionHint(s);
                        // tl 1177: hover excerpts — the newest user messages,
                        // newest first, so the card answers "is this the one?"
                        // without opening the transcript. tl 1177b 方案B: the
                        // list field stays as the instant source; cards it
                        // cannot answer pull the tail on first hover. tl 1188:
                        // the excerpts render in the EXTERNAL hover layer (the
                        // body portal below) — the card itself only suppresses
                        // its native title while the excerpts exist or a fetch
                        // is in flight, so the browser tooltip cannot stack on
                        // top of the layer.
                        const tail = tailExcerpts.get(s.path);
                        const hasExcerpts =
                          ((tail?.status === "ready" ? tail.lines : s.recentExcerpts)?.filter(Boolean).length ?? 0) > 0;
                        const holdTitle = hasExcerpts || tail?.status === "pending";
                        return (
                          <button
                            type="button"
                            key={s.path}
                            className="session-wall__card"
                            title={holdTitle ? undefined : title}
                            onMouseEnter={(e) => {
                              requestExcerpts(s);
                              scheduleHoverLayer(e.currentTarget, s, g.label);
                            }}
                            onMouseLeave={cancelHoverLayer}
                            onFocus={(e) => {
                              requestExcerpts(s);
                              scheduleHoverLayer(e.currentTarget, s, g.label);
                            }}
                            onBlur={cancelHoverLayer}
                            onClick={() => {
                              cancelHoverLayer();
                              onResume(s);
                            }}
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
                  )}
                </div>
              );
            })
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
      {renderHoverLayer()}
    </>
  );
}
