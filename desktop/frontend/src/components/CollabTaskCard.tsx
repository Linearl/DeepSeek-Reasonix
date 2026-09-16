// CollabTaskCard renders a task-card fence inside the conversation (task 145).
// It mirrors CompletionReportCard's presentation so the two task-shaped surfaces
// read as one family: icon + title head, labeled rows, explicit empty styling.
// A failed or blocked card states that fact in its own row — silence is the one
// outcome this surface must not produce.

import { useState } from "react";
import { ListChecks, ChevronDown, ChevronRight } from "lucide-react";
import { useT } from "../lib/i18n";
import { collabCardIsFailed, collabCardNodeLabel, type CollabTaskCard as Card } from "../lib/collabTaskCard";

const STATUS_LABEL_KEY: Record<string, "collabCard.status.pending" | "collabCard.status.running" | "collabCard.status.blocked" | "collabCard.status.done" | "collabCard.status.failed"> = {
  pending: "collabCard.status.pending",
  running: "collabCard.status.running",
  blocked: "collabCard.status.blocked",
  done: "collabCard.status.done",
  failed: "collabCard.status.failed",
};

export function CollabTaskCard({ card }: { card: Card }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const failed = collabCardIsFailed(card);
  const rows: Array<{ label: string; value: string }> = [
    { label: t("collabCard.assignee"), value: card.assigneeContactId ?? "" },
    { label: t("collabCard.initiator"), value: card.initiatorContactId ?? "" },
    { label: t("collabCard.body"), value: card.body ?? "" },
    { label: t("collabCard.result"), value: card.result ?? "" },
  ];
  if (failed) rows.push({ label: t("collabCard.error"), value: card.error ?? "" });

  return (
    <div
      className={`collab-card${failed ? " collab-card--failed" : ""} collab-card--${card.status}`}
      role="note"
      aria-label={t("collabCard.title")}
    >
      <div className="collab-card__head">
        <ListChecks size={15} aria-hidden="true" />
        <span className="collab-card__title">{t("collabCard.title")}</span>
        <span className={`collab-card__status collab-card__status--${card.status}`}>
          {t(STATUS_LABEL_KEY[card.status] ?? "collabCard.status.pending")}
        </span>
        <span className="collab-card__name">{card.title}</span>
      </div>
      <dl className="collab-card__fields">
        {rows.map((row) => (
          <div className="collab-card__row" key={row.label}>
            <dt>{row.label}</dt>
            <dd className={row.value ? "" : "collab-card__row--empty"}>{row.value || t("collabCard.none")}</dd>
          </div>
        ))}
      </dl>
      <button
        type="button"
        className="collab-card__toggle"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        {open ? <ChevronDown size={13} aria-hidden="true" /> : <ChevronRight size={13} aria-hidden="true" />}
        {t("collabCard.chain")}
        {card.nodes.length > 0 ? ` (${card.nodes.length})` : ""}
      </button>
      {open && (
        card.nodes.length === 0 ? (
          <div className="collab-card__empty">{t("collabCard.noNodes")}</div>
        ) : (
          <ol className="collab-card__nodes">
            {card.nodes.map((node, index) => (
              <li className="collab-card__node" key={`${index}-${node.at ?? ""}`}>
                <span className="collab-card__node-who">{collabCardNodeLabel(node) || t("collabCard.none")}</span>
                {node.note ? <span className="collab-card__node-note">{node.note}</span> : null}
              </li>
            ))}
          </ol>
        )
      )}
    </div>
  );
}
