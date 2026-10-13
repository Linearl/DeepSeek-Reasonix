// 任务 869：composer emoji 选择面板（类微信表情面板，Unicode 原生形态）。
// 面板本体 + emojiCatalog 数据走本组件的 lazy chunk，不进 initial bundle；
// Composer 侧只保留入口按钮与 AnchoredPopover 壳。点选仅插入输入框光标处
// （不直接发送），面板保持打开以支持连选；关闭交给 AnchoredPopover 的
// Esc / 外点统一语义（与历史提示词面板一致）。

import { useMemo, useState } from "react";
import { Search, X } from "lucide-react";
import type { DictKey } from "../lib/i18n";
import type { Translator } from "../lib/i18n";
import { EMOJI_CATEGORIES, FREQUENT_EMOJIS, searchEmojis, type EmojiCategoryId } from "../lib/emojiCatalog";

const CATEGORY_LABEL_KEYS: Record<EmojiCategoryId, DictKey> = {
  smileys: "composer.emojiCatSmileys",
  gestures: "composer.emojiCatGestures",
  animals: "composer.emojiCatAnimals",
  food: "composer.emojiCatFood",
  activities: "composer.emojiCatActivities",
  travel: "composer.emojiCatTravel",
  objects: "composer.emojiCatObjects",
  symbols: "composer.emojiCatSymbols",
};

export function EmojiPicker({
  t,
  onPick,
  onClose,
}: {
  t: Translator;
  onPick: (emoji: string) => void;
  onClose: () => void;
}) {
  const [activeCategory, setActiveCategory] = useState<EmojiCategoryId>(EMOJI_CATEGORIES[0].id);
  const [query, setQuery] = useState("");

  const trimmedQuery = query.trim();
  const searchResults = useMemo(() => searchEmojis(trimmedQuery), [trimmedQuery]);
  const browsing = trimmedQuery.length === 0;
  const activeEntries = useMemo(
    () => EMOJI_CATEGORIES.find((cat) => cat.id === activeCategory)?.emojis ?? [],
    [activeCategory],
  );
  const shownEntries = browsing ? activeEntries : searchResults;

  return (
    <div
      className="composer-emoji-menu"
      role="dialog"
      aria-label={t("composer.emojiPickerTitle")}
    >
      <div className="composer-emoji-menu__head">
        <span className="composer-emoji-menu__title">{t("composer.emojiPickerTitle")}</span>
        <button
          type="button"
          className="composer-emoji-menu__close"
          onClick={onClose}
          aria-label={t("common.close")}
        >
          <X size={14} aria-hidden="true" />
        </button>
      </div>
      <div className="composer-emoji-menu__search">
        <Search size={13} aria-hidden="true" />
        <input
          type="text"
          className="composer-emoji-menu__search-input"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("composer.emojiSearch")}
          aria-label={t("composer.emojiSearch")}
          spellCheck={false}
        />
      </div>
      {browsing && (
        <div className="composer-emoji-menu__frequent" aria-label={t("composer.emojiCatFrequent")}>
          {FREQUENT_EMOJIS.map((char) => (
            <button
              key={`freq-${char}`}
              type="button"
              className="composer-emoji-menu__cell"
              onClick={() => onPick(char)}
              aria-label={`${t("composer.emojiPickerTitle")} ${char}`}
            >
              {char}
            </button>
          ))}
        </div>
      )}
      {browsing && (
        <div className="composer-emoji-menu__tabs" role="tablist" aria-label={t("composer.emojiPickerTitle")}>
          {EMOJI_CATEGORIES.map((cat) => (
            <button
              key={cat.id}
              type="button"
              role="tab"
              aria-selected={cat.id === activeCategory}
              className={`composer-emoji-menu__tab${cat.id === activeCategory ? " composer-emoji-menu__tab--active" : ""}`}
              onClick={() => setActiveCategory(cat.id)}
            >
              {t(CATEGORY_LABEL_KEYS[cat.id])}
            </button>
          ))}
        </div>
      )}
      {shownEntries.length === 0 ? (
        <div className="composer-emoji-menu__empty">{t("composer.emojiEmpty")}</div>
      ) : (
        <div className="composer-emoji-menu__grid" role="listbox" aria-label={t("composer.emojiPickerTitle")}>
          {shownEntries.map((entry) => (
            <button
              key={entry.char}
              type="button"
              role="option"
              aria-selected={false}
              className="composer-emoji-menu__cell"
              title={entry.keywords}
              onClick={() => onPick(entry.char)}
            >
              {entry.char}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
