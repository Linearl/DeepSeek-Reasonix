import { useMemo, useState } from "react";
import type { RefObject } from "react";

import { writeClipboardText } from "../lib/clipboard";
import { useT, type DictKey } from "../lib/i18n";
import type { ServerView } from "../lib/types";
import { AnchoredPopover } from "./AnchoredPopover";
import { failureKind, type FailureKind } from "./CapabilitiesPanel";

// tl1481 — the status-bar MCP failed chip drills down: click opens this
// popover (level 2), each failed server carries a handling suggestion, and
// the raw error stays one "错误详情" hop below (level 3). The advice copy is
// keyed off the EXISTING failure taxonomy (CapabilitiesPanel.failureKind:
// auth / missing-command / command-unavailable / network / other) — no
// second classification lives here, so the drawer, the status chip and this
// popover always agree on why a server failed.
const KIND_LABEL_KEYS: Record<FailureKind, DictKey> = {
  auth: "mcp.advice.kindAuth",
  "missing-command": "mcp.advice.kindMissingCommand",
  "command-unavailable": "mcp.advice.kindCommandUnavailable",
  network: "mcp.advice.kindNetwork",
  other: "mcp.advice.kindOther",
};

const KIND_SUGGESTION_KEYS: Record<FailureKind, DictKey> = {
  auth: "mcp.advice.suggestAuth",
  "missing-command": "mcp.advice.suggestMissingCommand",
  "command-unavailable": "mcp.advice.suggestCommandUnavailable",
  network: "mcp.advice.suggestNetwork",
  other: "mcp.advice.suggestOther",
};

export function McpFailureAdvicePopover({
  open,
  anchorRef,
  onClose,
  servers,
  onOpenManager,
}: {
  open: boolean;
  anchorRef: RefObject<HTMLElement | null>;
  onClose: () => void;
  servers: readonly ServerView[];
  onOpenManager?: () => void;
}) {
  const t = useT();
  const [detailsFor, setDetailsFor] = useState<string | null>(null);
  const [copiedFor, setCopiedFor] = useState<string | null>(null);
  // Same ordering the management drawer uses: name, case-insensitive, so the
  // popover and the drawer list the same failures in the same order.
  const failed = useMemo(
    () =>
      (servers ?? [])
        .filter((server) => server.status === "failed")
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" })),
    [servers],
  );

  const copyError = async (server: ServerView) => {
    const copied = await writeClipboardText(server.error || "");
    if (copied) setCopiedFor(server.name);
  };

  return (
    <AnchoredPopover open={open} anchorRef={anchorRef} onClose={onClose} className="mcp-advice-popover" align="start">
      <section role="dialog" aria-label={t("mcp.advice.title", { count: failed.length })}>
        <header className="mcp-advice-popover__header">{t("mcp.advice.title", { count: failed.length })}</header>
        <div className="mcp-advice-popover__list">
          {failed.map((server) => {
            const kind = failureKind(server);
            const detailsOpen = detailsFor === server.name;
            const error = server.error || t("caps.failed");
            return (
              <div className="mcp-advice-popover__item" key={server.name}>
                <div className="mcp-advice-popover__row">
                  <span className="cap-dot cap-dot--failed" aria-hidden="true" />
                  <strong className="mcp-advice-popover__name">{server.name}</strong>
                  <span className="mcp-advice-popover__kind">{t(KIND_LABEL_KEYS[kind])}</span>
                </div>
                <p className="mcp-advice-popover__suggest">{t(KIND_SUGGESTION_KEYS[kind])}</p>
                <div className="mcp-advice-popover__detail-actions">
                  <button
                    type="button"
                    className="btn btn--small"
                    aria-expanded={detailsOpen}
                    onClick={() => setDetailsFor(detailsOpen ? null : server.name)}
                  >
                    {detailsOpen ? t("common.collapse") : t("mcp.advice.details")}
                  </button>
                </div>
                {detailsOpen && (
                  <div className="mcp-advice-popover__logbox">
                    <div className="mcp-advice-popover__logbar">
                      <span>{t("caps.rawLog")}</span>
                      <button type="button" className="btn btn--secondary btn--small" onClick={() => void copyError(server)}>
                        {copiedFor === server.name ? t("mcp.advice.copied") : t("caps.copyLog")}
                      </button>
                    </div>
                    <pre className="mcp-advice-popover__log">{error}</pre>
                  </div>
                )}
              </div>
            );
          })}
        </div>
        {onOpenManager && (
          <footer className="mcp-advice-popover__footer">
            <button
              type="button"
              className="btn btn--secondary btn--small"
              onClick={() => {
                onClose();
                onOpenManager();
              }}
            >
              {t("mcp.advice.openManager")}
            </button>
          </footer>
        )}
      </section>
    </AnchoredPopover>
  );
}
