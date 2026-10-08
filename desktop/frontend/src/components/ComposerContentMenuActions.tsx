import { AtSign, FilePlus2, Hash, Plus, Zap } from "lucide-react";
import { useState } from "react";
import { useT } from "../lib/i18n";
import type { QuickCommandEntry } from "../lib/settingsViewTypes";

export function ComposerContentMenuActions({
  attachmentInputEnabled,
  running = false,
  textPresent,
  onChooseAttachment,
  onInsertTrigger,
  quickCommands = [],
  onChooseQuickCommand,
  onAddQuickCommand,
}: {
  attachmentInputEnabled: boolean;
  running?: boolean;
  textPresent: boolean;
  onChooseAttachment: () => void;
  onInsertTrigger: (trigger: "@" | "#" | "/") => void;
  quickCommands?: QuickCommandEntry[];
  onChooseQuickCommand?: (text: string) => void;
  onAddQuickCommand?: (title: string, text: string) => void;
}) {
  const t = useT();
  const [picking, setPicking] = useState(false);
  const [query, setQuery] = useState("");
  // Task 656: an inline create step reachable straight from the picker's bottom
  // entry — while it is open it replaces the list so the small popover stays
  // compact; cancel returns to the list untouched.
  const [draft, setDraft] = useState<{ title: string; text: string } | null>(null);

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
          onClick={() => { setPicking(false); setQuery(""); setDraft(null); }}
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
        {/* Task 593: surface the "!!" line-head trigger next to the snippets. */}
        <div className="composer-access-menu__hint">{t("composer.quickCommandsBangHint")}</div>
        {draft ? (
          <div className="composer-content-menu__draft">
            <input
              className="mem-input"
              autoFocus
              value={draft.title}
              placeholder={t("settings.quickCommandsTitlePlaceholder")}
              onChange={(event) => setDraft({ ...draft, title: event.target.value })}
            />
            <textarea
              className="mem-input"
              rows={3}
              value={draft.text}
              placeholder={t("settings.quickCommandsTextPlaceholder")}
              onChange={(event) => setDraft({ ...draft, text: event.target.value })}
            />
            <div className="composer-content-menu__draft-actions">
              <button
                type="button"
                className="btn btn--small btn--primary"
                disabled={!draft.title.trim()}
                onClick={() => { onAddQuickCommand?.(draft.title.trim(), draft.text); setDraft(null); setQuery(""); }}
              >
                {t("settings.quickCommandsSave")}
              </button>
              <button
                type="button"
                className="btn btn--small"
                onClick={() => setDraft(null)}
              >
                {t("common.cancel")}
              </button>
            </div>
          </div>
        ) : (
          <>
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
            {/* Task 656: the create entry sits at the bottom of the list — also
                reachable when the search found nothing, which is exactly when a
                "not stored yet, add it" path matters most. */}
            {onAddQuickCommand ? (
              <button
                type="button"
                role="menuitem"
                className="composer-access-menu__item composer-content-menu__item composer-content-menu__add"
                onClick={() => setDraft({ title: "", text: "" })}
              >
                <Plus size={16} aria-hidden="true" />
                <span className="composer-access-menu__copy">
                  <span className="composer-access-menu__title">{t("composer.contentQuickCommandsAdd")}</span>
                </span>
              </button>
            ) : null}
          </>
        )}
      </div>
    );
  }

  return (
    <div className="composer-access-menu__section" role="menu" aria-label={t("composer.contentMenuTitle")}>
      <div className="composer-access-menu__label">{t("composer.contentMenuTitle")}</div>
      {attachmentInputEnabled ? <>
        {/* Task 594: attachments stay turn-gated (grayed with a reason) while
            the text-only inserts below remain usable mid-turn. */}
        <button type="button" role="menuitem" className="composer-access-menu__item composer-content-menu__item"
          onClick={onChooseAttachment}
          disabled={running}
          title={running ? t("composer.runningGateHint") : undefined}>
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
