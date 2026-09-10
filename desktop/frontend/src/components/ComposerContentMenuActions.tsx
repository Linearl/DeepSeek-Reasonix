import { AtSign, FilePlus2, Hash, Zap } from "lucide-react";
import { useState } from "react";
import { useT } from "../lib/i18n";
import type { QuickCommandEntry } from "../lib/types";

export function ComposerContentMenuActions({
  attachmentInputEnabled,
  textPresent,
  onChooseAttachment,
  onInsertTrigger,
  quickCommands = [],
  onChooseQuickCommand,
}: {
  attachmentInputEnabled: boolean;
  textPresent: boolean;
  onChooseAttachment: () => void;
  onInsertTrigger: (trigger: "@" | "#" | "/") => void;
  quickCommands?: QuickCommandEntry[];
  onChooseQuickCommand?: (text: string) => void;
}) {
  const t = useT();
  const [picking, setPicking] = useState(false);
  const [query, setQuery] = useState("");

  // The quick-command picker swaps in inside the same popover the content menu
  // lives in, so choosing a snippet stays one click away from the "+" button.
  if (picking) {
    const terms = query.trim().toLowerCase();
    const matches = terms
      ? quickCommands.filter((entry) => `${entry.title} ${entry.text}`.toLowerCase().includes(terms))
      : quickCommands;
    return (
      <div className="composer-access-menu__section" role="menu" aria-label={t("composer.contentQuickCommands")}>
        <button
          type="button"
          className="composer-access-menu__label composer-content-menu__back"
          onClick={() => { setPicking(false); setQuery(""); }}
        >
          ← {t("composer.contentMenuTitle")}
        </button>
        <input
          className="mem-input composer-content-menu__search"
          autoFocus
          value={query}
          placeholder={t("composer.contentQuickCommandsSearch")}
          onChange={(event) => setQuery(event.target.value)}
        />
        {matches.length === 0 ? (
          <div className="composer-access-menu__hint">{t("composer.contentQuickCommandsEmpty")}</div>
        ) : (
          matches.map((entry, index) => (
            <button
              key={`qc-${index}`}
              type="button"
              role="menuitem"
              className="composer-access-menu__item composer-content-menu__item"
              onClick={() => { onChooseQuickCommand?.(entry.text); setPicking(false); setQuery(""); }}
            >
              <Zap size={16} aria-hidden="true" />
              <span className="composer-access-menu__copy">
                <span className="composer-access-menu__title">{entry.title}</span>
                <span className="composer-access-menu__hint">{entry.text}</span>
              </span>
            </button>
          ))
        )}
      </div>
    );
  }

  return (
    <div className="composer-access-menu__section" role="menu" aria-label={t("composer.contentMenuTitle")}>
      <div className="composer-access-menu__label">{t("composer.contentMenuTitle")}</div>
      {attachmentInputEnabled ? <>
        <button type="button" role="menuitem" className="composer-access-menu__item composer-content-menu__item" onClick={onChooseAttachment}>
          <FilePlus2 size={16} aria-hidden="true" />
          <span className="composer-access-menu__copy">
            <span className="composer-access-menu__title">{t("composer.contentAddAttachment")}</span>
          </span>
        </button>
        <button type="button" role="menuitem" className="composer-access-menu__item composer-content-menu__item" onClick={() => onInsertTrigger("@")}>
          <AtSign size={16} aria-hidden="true" />
          <span className="composer-access-menu__copy">
            <span className="composer-access-menu__title">{t("composer.contentReferenceFiles")}</span>
          </span>
        </button>
      </> : null}
      <button type="button" role="menuitem" className="composer-access-menu__item composer-content-menu__item" onClick={() => onInsertTrigger("#")}>
        <Hash size={16} aria-hidden="true" />
        <span className="composer-access-menu__copy">
          <span className="composer-access-menu__title">{t("composer.contentReferenceSessions")}</span>
        </span>
      </button>
      <button type="button" role="menuitem" className="composer-access-menu__item composer-content-menu__item"
        onClick={() => onInsertTrigger("/")} disabled={textPresent}
        title={textPresent ? t("composer.contentUseCommandsEmptyOnly") : undefined}>
        <span className="composer-content-menu__trigger-icon" aria-hidden="true">/</span>
        <span className="composer-access-menu__copy">
          <span className="composer-access-menu__title">{t("composer.contentUseCommands")}</span>
        </span>
      </button>
      {quickCommands.length > 0 ? (
        <button
          type="button"
          role="menuitem"
          className="composer-access-menu__item composer-content-menu__item"
          onClick={() => setPicking(true)}
        >
          <Zap size={16} aria-hidden="true" />
          <span className="composer-access-menu__copy">
            <span className="composer-access-menu__title">{t("composer.contentQuickCommands")}</span>
          </span>
        </button>
      ) : null}
    </div>
  );
}
