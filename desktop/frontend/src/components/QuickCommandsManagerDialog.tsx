import { useState } from "react";
import { Trash2 } from "lucide-react";
import { useT } from "../lib/i18n";
import type { QuickCommandEntry } from "../lib/settingsViewTypes";
import { ProviderDialog } from "./ProviderDialog";

/** The quick-command manager dialog — the single implementation shared by the
 *  settings entry (SettingsPanel) and the composer picker's bottom "add"
 *  entry (task 721; the picker no longer owns an inline create form).
 *
 *  Callers mount it conditionally (unmount on close resets the search/draft
 *  state, matching the historical per-open behavior) and provide the entries
 *  plus a `save` callback that persists the full next list. The shell renders
 *  through ProviderDialog's wide variant — the width chain lives in
 *  ProviderAccessSettings.css (.provider-dialog--wide, dual width/max-width
 *  per task 721) and the content block in styles.css. */
export function QuickCommandsManagerDialog({ entries, busy, save, onClose }: {
  entries: QuickCommandEntry[];
  busy: boolean;
  save: (next: QuickCommandEntry[]) => Promise<unknown>;
  onClose: () => void;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [fresh, setFresh] = useState(-1);
  // A draft is the explicit create/edit step: fill title and text, then
  // confirm. editingIndex switches the same form into "replace row" mode.
  const [draft, setDraft] = useState<{ title: string; text: string; editingIndex?: number } | null>(null);
  const terms = query.trim().toLowerCase();
  const rows = entries
    .map((entry, index) => ({ entry, index }))
    .filter(({ entry }) => !terms || `${entry.title} ${entry.text}`.toLowerCase().includes(terms));
  return (
    <ProviderDialog title={t("settings.quickCommandsManage")} onClose={onClose} wide>
      <div className="settings-quick-commands settings-quick-commands--panel settings-quick-commands--wide">
        <input
          className="mem-input"
          value={query}
          placeholder={t("settings.quickCommandsSearch")}
          disabled={busy}
          onChange={(e) => { setQuery(e.target.value); setFresh(-1); }}
        />
        {rows.length === 0 ? (
          <div className="settings-quick-commands__empty">
            {entries.length === 0 ? t("settings.quickCommandsEmpty") : t("settings.quickCommandsNoMatch")}
          </div>
        ) : (
          rows.map(({ entry, index }) => (
            <div className={`settings-quick-commands__row${index === fresh ? " settings-quick-commands__row--fresh" : ""}`} key={`qc-${index}`}>
              <button
                type="button"
                className={`btn btn--small${entry.enabled === false ? "" : " btn--primary"}`}
                disabled={busy}
                title={t(entry.enabled === false ? "settings.quickCommandsEnable" : "settings.quickCommandsDisable")}
                onClick={() => void save(
                  entries.map((item, i) => (i === index ? { ...item, enabled: item.enabled === false } : item)),
                )}
              >
                {entry.enabled === false ? t("settings.quickCommandsOff") : t("settings.quickCommandsOn")}
              </button>
              <input
                className="mem-input"
                value={entry.title}
                placeholder={t("settings.quickCommandsTitlePlaceholder")}
                disabled={busy}
                onChange={(e) => {
                  const next = entries.map((item, i) => (i === index ? { ...item, title: e.target.value } : item));
                  void save(next);
                }}
              />
              <textarea
                className="mem-input"
                value={entry.text}
                rows={3}
                placeholder={t("settings.quickCommandsTextPlaceholder")}
                disabled={busy}
                onChange={(e) => {
                  const next = entries.map((item, i) => (i === index ? { ...item, text: e.target.value } : item));
                  void save(next);
                }}
              />
              <button
                type="button"
                className="btn btn--secondary btn--small"
                disabled={busy || draft !== null}
                title={t("settings.quickCommandsEdit")}
                onClick={() => setDraft({ title: entry.title, text: entry.text, editingIndex: index })}
              >
                {t("settings.quickCommandsEdit")}
              </button>
              <button
                type="button"
                className="btn btn--small"
                disabled={busy}
                onClick={() => void save(entries.filter((_, i) => i !== index))}
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))
        )}
        {draft && (
          <div className="settings-quick-commands__draft">
            <input
              className="mem-input"
              autoFocus
              value={draft.title}
              placeholder={t("settings.quickCommandsTitlePlaceholder")}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, title: e.target.value })}
            />
            <textarea
              className="mem-input"
              rows={3}
              value={draft.text}
              placeholder={t("settings.quickCommandsTextPlaceholder")}
              disabled={busy}
              onChange={(e) => setDraft({ ...draft, text: e.target.value })}
            />
            <div className="settings-quick-commands__draft-actions">
              <button
                type="button"
                className="btn btn--small btn--primary"
                disabled={busy || !draft.title.trim()}
                onClick={() => {
                  const next = { title: draft.title.trim(), text: draft.text, enabled: true };
                  const editing = draft.editingIndex;
                  setDraft(null);
                  setQuery("");
                  // Edit replaces the row in place; create appends and makes
                  // the appended row visible by clearing the search filter.
                  if (editing !== undefined) {
                    setFresh(editing);
                    void save(entries.map((item, i) => (i === editing ? next : item)));
                  } else {
                    setFresh(entries.length);
                    void save([...entries, next]);
                  }
                }}
              >
                {t("settings.quickCommandsSave")}
              </button>
              <button
                type="button"
                className="btn btn--small"
                disabled={busy}
                onClick={() => setDraft(null)}
              >
                {t("common.cancel")}
              </button>
            </div>
          </div>
        )}
        <button
          type="button"
          className="btn btn--secondary btn--small"
          disabled={busy || draft !== null}
          onClick={() => setDraft({ title: "", text: "" })}
        >
          {t("settings.quickCommandsAdd")}
        </button>
      </div>
    </ProviderDialog>
  );
}
