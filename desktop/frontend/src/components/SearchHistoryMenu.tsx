import { Clock, X } from "lucide-react";
import { useT } from "../lib/i18n";

// tl1282: 检索历史下拉——设置面板搜索框与侧栏「搜索项目或会话」共用同一份
// 历史（lib/searchHistory 的 LRU 存储），本组件只负责渲染：
//   - 容器 onMouseDown preventDefault：点击任意行不先 blur 输入框（blur 即收起
//     的父级逻辑不会被点击竞争吃掉，选中后焦点仍在输入框）；
//   - 单条删除（行尾 ×）与一键清空（标题行）；
//   - 纯展示组件，不持有状态：开合由宿主「聚焦且无查询词」控制，默认视图零改动。
export function SearchHistoryMenu(props: {
  entries: string[];
  onSelect: (term: string) => void;
  onRemove: (term: string) => void;
  onClear: () => void;
}) {
  const t = useT();
  return (
    <div
      className="search-history"
      aria-label={t("searchHistory.title")}
      onMouseDown={(event) => event.preventDefault()}
    >
      <div className="search-history__head">
        <span className="search-history__title">{t("searchHistory.title")}</span>
        <button type="button" className="search-history__clear" onClick={props.onClear}>
          {t("searchHistory.clear")}
        </button>
      </div>
      <ul className="search-history__list">
        {props.entries.map((term) => (
          <li key={term} className="search-history__row">
            <button type="button" className="search-history__term" onClick={() => props.onSelect(term)}>
              <Clock size={13} aria-hidden="true" />
              <span className="search-history__term-text">{term}</span>
            </button>
            <button
              type="button"
              className="search-history__remove"
              aria-label={`${t("searchHistory.remove")}: ${term}`}
              title={t("searchHistory.remove")}
              onClick={() => props.onRemove(term)}
            >
              <X size={12} aria-hidden="true" />
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
