// QuestionJumpBar: the question navigator rail along the transcript edge.

import { useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent } from "react";
import { useT } from "../lib/i18n";
import type { QuestionAnchor } from "../lib/transcriptGrouping";
import { buildJumpPreviewContent, jumpPreviewPlacement, JUMP_PREVIEW_ESTIMATED_HEIGHT } from "../lib/jumpPreview";

export const QUESTION_JUMP_MAX_MARKERS = 60;

export function sampledQuestionTurns(totalQuestions: number, activeTurn: number | null, limit = QUESTION_JUMP_MAX_MARKERS): number[] {
  const total = Math.max(0, Math.floor(totalQuestions));
  const maxMarkers = Math.max(2, Math.floor(limit));
  if (total <= maxMarkers) return Array.from({ length: total }, (_, turn) => turn);

  const turns = Array.from({ length: maxMarkers }, (_, index) => (
    Math.round(index * (total - 1) / (maxMarkers - 1))
  ));
  if (maxMarkers <= 2 || activeTurn == null || activeTurn <= 0 || activeTurn >= total - 1 || turns.includes(activeTurn)) return turns;

  let replaceIndex = 1;
  let closestDistance = Number.POSITIVE_INFINITY;
  for (let index = 1; index < turns.length - 1; index += 1) {
    const distance = Math.abs(turns[index] - activeTurn);
    if (distance < closestDistance) {
      closestDistance = distance;
      replaceIndex = index;
    }
  }
  turns[replaceIndex] = activeTurn;
  turns.sort((left, right) => left - right);
  return turns;
}

export function questionTurnFromRailY(clientY: number, top: number, height: number, totalQuestions: number): number | null {
  const total = Math.max(0, Math.floor(totalQuestions));
  if (total === 0 || !Number.isFinite(height) || height <= 0) return null;
  const ratio = Math.max(0, Math.min(1, (clientY - top) / height));
  return Math.min(total - 1, Math.floor(ratio * total));
}

export function QuestionJumpBar({
  loadedQuestions,
  totalQuestions,
  activeTurn,
  onJump,
}: {
  loadedQuestions: QuestionAnchor[];
  totalQuestions: number;
  activeTurn: number | null;
  onJump: (question: QuestionAnchor) => void;
}) {
  const t = useT();
  const total = Math.max(0, Math.floor(totalQuestions));
  const [hovered, setHovered] = useState<number | null>(null);
  const barRef = useRef<HTMLElement>(null);
  const railRef = useRef<HTMLDivElement>(null);
  const previewTop = useRef(0);
  const previewRef = useRef<HTMLDivElement>(null);
  const [showPreview, setShowPreview] = useState(false);
  // The card is measured after paint-free layout so the edge-flip placement
  // works with the real card height, not a guess. The estimate only covers the
  // very first frame; useLayoutEffect corrects it before the browser paints.
  const [cardHeight, setCardHeight] = useState(JUMP_PREVIEW_ESTIMATED_HEIGHT);

  useLayoutEffect(() => {
    if (!showPreview) return;
    const element = previewRef.current;
    if (!element) return;
    const measured = Math.round(element.getBoundingClientRect().height);
    if (measured > 0 && measured !== cardHeight) setCardHeight(measured);
  }, [showPreview, hovered, cardHeight]);

  const loadedByTurn = useMemo(() => {
    const loaded = new Map<number, QuestionAnchor>();
    for (const question of loadedQuestions) loaded.set(question.turn, question);
    return loaded;
  }, [loadedQuestions]);
  const active = activeTurn != null && activeTurn >= 0 && activeTurn < total
    ? activeTurn
    : total > 0 ? total - 1 : null;

  const markerTurns = useMemo(() => sampledQuestionTurns(total, active), [active, total]);
  const hoverIdx = hovered === null
    ? -1
    : markerTurns.reduce((closest, turn, index) => (
        closest < 0 || Math.abs(turn - hovered) < Math.abs(markerTurns[closest] - hovered) ? index : closest
      ), -1);

  const questionAt = (turn: number): QuestionAnchor => loadedByTurn.get(turn) ?? {
    id: `history-question-${turn + 1}`,
    text: t("questionNav.notLoaded", { n: turn + 1 }),
    turn,
    loaded: false,
  };
  const hoveredQuestion = hovered === null ? undefined : questionAt(hovered);

  const questionFromY = (clientY: number): { question: QuestionAnchor; previewY: number } | null => {
    const rail = railRef.current;
    const bar = barRef.current;
    if (!rail || !bar) return null;
    const railRect = rail.getBoundingClientRect();
    const turn = questionTurnFromRailY(clientY, railRect.top, railRect.height, total);
    if (turn === null) return null;
    const barRect = bar.getBoundingClientRect();
    const turnCenter = railRect.top - barRect.top + ((turn + 0.5) / total) * railRect.height;
    return {
      question: questionAt(turn),
      previewY: Math.max(0, Math.min(barRect.height, turnCenter)),
    };
  };

  const scrollTo = (question: QuestionAnchor) => {
    onJump(question);
  };

  const onMove = (event: ReactMouseEvent<HTMLDivElement>) => {
    const target = questionFromY(event.clientY);
    if (!target) return;
    previewTop.current = target.previewY;
    setHovered(target.question.turn);
    setShowPreview(true);
  };

  const onRailMouseDown = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const target = questionFromY(event.clientY);
    if (!target) return;
    event.preventDefault();
    previewTop.current = target.previewY;
    setHovered(target.question.turn);
    setShowPreview(true);
    scrollTo(target.question);
  };

  const onRailKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (total === 0) return;
    const current = active ?? Math.max(0, total - 1);
    const page = Math.max(1, Math.round(total / 10));
    let next: number | null = null;
    switch (event.key) {
      case "ArrowUp":
      case "ArrowLeft": next = current - 1; break;
      case "ArrowDown":
      case "ArrowRight": next = current + 1; break;
      case "PageUp": next = current - page; break;
      case "PageDown": next = current + page; break;
      case "Home": next = 0; break;
      case "End": next = total - 1; break;
      case "Enter":
      case " ": next = current; break;
      default: return;
    }
    event.preventDefault();
    scrollTo(questionAt(Math.max(0, Math.min(total - 1, next))));
  };

  const dotProps = (idx: number, turn: number): { style: CSSProperties; "data-d"?: string } => {
    const isActive = active === turn;
    if (hoverIdx < 0) {
      return { style: { width: isActive ? 18 : 12, background: isActive ? "var(--accent)" : undefined } };
    }
    const d = Math.abs(idx - hoverIdx);
    const width = d === 0 ? 32 : d === 1 ? 20 : d === 2 ? 14 : isActive ? 18 : 12;
    const background = d <= 2 ? undefined : isActive ? "var(--accent)" : undefined;
    return {
      style: { width, transitionDelay: `${Math.min(d, 3) * 20}ms`, background },
      "data-d": d <= 2 ? String(d) : undefined,
    };
  };

  const density = markerTurns.length > 40 ? "packed" : markerTurns.length > 20 ? "compact" : "normal";
  const activeValue = active ?? Math.max(0, total - 1);

  // Rich hover card content (task 149): bold lead + body lines + tool tags.
  // The anchor is forwarded as-is — the pure builder reads `text`/`loaded`
  // today and will pick up optional `title`/`body`/`tools` fields on the
  // anchor automatically once the data path attaches them.
  const preview = hoveredQuestion ? buildJumpPreviewContent(hoveredQuestion) : null;
  const placement = preview
    ? jumpPreviewPlacement(previewTop.current, cardHeight, barRef.current?.clientHeight ?? 0)
    : null;

  // When many history turns are marked, grow the rail's vertical extent so each
  // marker keeps a comfortable ~12px gap instead of being packed into a fixed
  // 240px column. The height grows with the marker count, capped at 70vh, so a
  // long session stays readable without overflowing the transcript shell (#9218).
  const RAIL_BASE_HEIGHT = 240;
  const RAIL_MARKER_GAP = 12;
  const railHeight = markerTurns.length > 20
    ? `min(${Math.max(RAIL_BASE_HEIGHT, markerTurns.length * RAIL_MARKER_GAP + 24)}px, 70vh)`
    : undefined;

  return (
    <nav
      className="jump-bar"
      ref={barRef}
      style={railHeight ? { height: railHeight } : undefined}
      aria-label={t("questionNav.label")}
      onMouseLeave={() => {
        setHovered(null);
        setShowPreview(false);
      }}
    >
      <div
        className="jump-scroll"
        ref={railRef}
        role="slider"
        tabIndex={0}
        aria-label={t("questionNav.label")}
        aria-orientation="vertical"
        aria-valuemin={1}
        aria-valuemax={Math.max(1, total)}
        aria-valuenow={activeValue + 1}
        aria-valuetext={t("questionNav.progress", { current: activeValue + 1, total })}
        data-density={density}
        onMouseMove={onMove}
        onMouseDown={onRailMouseDown}
        onKeyDown={onRailKeyDown}
      >
        {markerTurns.map((turn, index) => (
          <span
            className="jump-item"
            key={turn}
            data-turn={turn}
            data-loaded={loadedByTurn.has(turn) ? "true" : "false"}
            aria-hidden="true"
            style={{ top: `${((turn + 0.5) / total) * 100}%` }}
          >
            <span className="jump-dot" {...dotProps(index, turn)} />
          </span>
        ))}
      </div>
      {showPreview && preview && placement && (
        <div
          className="jump-preview"
          ref={previewRef}
          style={{ top: placement.top }}
          role="tooltip"
          data-flip={placement.flip}
          data-placeholder={preview.placeholder || undefined}
        >
          <span className="jump-preview-title">{preview.title}</span>
          {preview.bodyLines.length > 0 && (
            <span className="jump-preview-body">
              {preview.bodyLines.map((line, index) => (
                <span className="jump-preview-line" key={index}>{line}</span>
              ))}
            </span>
          )}
          {preview.tools.length > 0 && (
            <span className="jump-preview-tools">
              {preview.tools.map((tool) => (
                <span className="jump-preview-tool" key={tool}>{tool}</span>
              ))}
            </span>
          )}
        </div>
      )}
    </nav>
  );
}

export default QuestionJumpBar;
