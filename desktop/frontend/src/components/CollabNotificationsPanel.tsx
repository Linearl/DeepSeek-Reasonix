// tl1458：协作中心『通知』视图 —— 历史通知留档的展示面。
//
// 数据面 = lib/notifyArchive.ts（四族接入在数据侧完成，展示通路零拦截）；
// 本组件只读留档：时间降序列表 + 懒加载（首屏 20 条 + 滚动近底 +20，对齐
// tl1230 信件列表的方案）+ 按级筛选（error/warn/info 分档，图标/颜色与
// rt-notice 四档语义同族：error=! danger 色 / warn=⚠ 黄 / info=ℹ 中性）+
// 清空按钮（项目统一 ConfirmDialog，danger 声调二次确认）。
//
// 家族形态：与 CollabInboxPanel 的 embedded 视图一致——头部由 CollabHubPanel
// 壳承担，本组件只渲染列表体；列表滚动容器 = 面板根本体（overflow:auto）。

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  clearNotifications,
  listNotifications,
  onNotifyArchiveChange,
  type NotifyArchiveEntry,
  type NotifyArchiveLevel,
} from "../lib/notifyArchive";
import { useT } from "../lib/i18n";
import { useConfirmDialog } from "./ConfirmDialog";

/** 懒加载水位：首屏 20 条，滚动近底增量 +20（tl1230 信件列表同方案同数）。 */
export const collabNotificationsFirstPageSize = 20;

const LEVELS: NotifyArchiveLevel[] = ["error", "warn", "info"];

/** 级别徽章字形（与 toast / rt-notice 的语言中立字形同族）。 */
const LEVEL_ICONS: Record<NotifyArchiveLevel, string> = {
  error: "!",
  warn: "⚠",
  info: "ℹ",
};

function formatTime(ms: number): string {
  return new Date(ms).toLocaleString();
}

export function CollabNotificationsPanel() {
  const t = useT();
  const { confirm, dialog: confirmDialog } = useConfirmDialog();
  const [entries, setEntries] = useState<NotifyArchiveEntry[]>(() => listNotifications());
  const [pageSize, setPageSize] = useState(collabNotificationsFirstPageSize);
  const [levelFilter, setLevelFilter] = useState<"all" | NotifyArchiveLevel>("all");
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // 新通知落档 / 清空 → 订阅缝推全量（内存态镜像，零 IO）。页水位保持不变：
  // 正在浏览第 3 页时来新通知不把视图拽回首屏。
  useEffect(() => onNotifyArchiveChange((next) => setEntries(next)), []);

  const filtered = useMemo(
    () => (levelFilter === "all" ? entries : entries.filter((e) => e.level === levelFilter)),
    [entries, levelFilter],
  );
  const visible = useMemo(() => filtered.slice(0, pageSize), [filtered, pageSize]);
  const truncated = filtered.length > visible.length;

  // 级别筛选切换回首屏页（新查询从头翻页，水位沿用旧值会把新筛选直接放到
  // 第 N 页的语义）。
  const switchFilter = useCallback((next: "all" | NotifyArchiveLevel) => {
    setLevelFilter(next);
    setPageSize(collabNotificationsFirstPageSize);
  }, []);

  const growPage = useCallback(() => {
    // 到底自停（同值 setPageSize bail-out，滚动连发不会成环）。
    setPageSize((size) => (size >= filtered.length ? size : size + collabNotificationsFirstPageSize));
  }, [filtered.length]);

  const onPanelScroll = useCallback(() => {
    if (!truncated) return;
    const el = scrollRef.current;
    if (!el) return;
    // 距底 64px 内视为「想看更多」（信件列表同水位；滚动容器 = 面板根本体）。
    if (el.scrollTop + el.clientHeight < el.scrollHeight - 64) return;
    growPage();
  }, [growPage, truncated]);

  // 截断态但内容不足以滚动（大屏 + 矮列表）时自动续载一页——否则「永远到不
  // 了底」。无布局度量（clientHeight=0，含测试 harness）不介入，jsdom 下该
  // 效应恒惰性、由测试显式驱动滚动（信件列表同先例）。
  useEffect(() => {
    if (!truncated) return;
    const el = scrollRef.current;
    if (!el || el.clientHeight <= 0) return;
    if (el.scrollHeight > el.clientHeight) return;
    growPage();
  }, [filtered, truncated, growPage]);

  // 清空全部（危险操作，不可撤销；项目统一 ConfirmDialog danger 声调）。
  const clearAll = async () => {
    const total = entries.length;
    const ok = await confirm({
      title: t("collabNotif.clearConfirmTitle"),
      message: (
        <>
          {t("collabNotif.clearConfirmBody", { n: total })}
          <span className="collab-notif-panel__confirmnote">{t("collabNotif.clearConfirmNote")}</span>
        </>
      ),
      confirmLabel: t("collabNotif.clear"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!ok) return;
    clearNotifications();
  };

  return (
    <div className="collab-notif-panel" role="region" aria-label={t("collabNotif.title")} ref={scrollRef} onScroll={onPanelScroll}>
      {confirmDialog}
      {/* 工具条：按级筛选（分档=筛选 + 图标/颜色同族）+ 清空（右缘 danger）。 */}
      <div className="collab-notif-panel__toolbar">
        <div className="collab-notif-panel__levels" role="group" aria-label={t("collabNotif.levelGroup")}>
          <button
            type="button"
            className={`collab-notif-panel__level${levelFilter === "all" ? " collab-notif-panel__level--on" : ""}`}
            onClick={() => switchFilter("all")}
          >
            {t("collabNotif.level.all")}
          </button>
          {LEVELS.map((lv) => (
            <button
              key={lv}
              type="button"
              data-level={lv}
              className={`collab-notif-panel__level${levelFilter === lv ? " collab-notif-panel__level--on" : ""}`}
              onClick={() => switchFilter(lv)}
            >
              <span className="collab-notif-panel__levelicon" aria-hidden="true">{LEVEL_ICONS[lv]}</span>
              {t(`collabNotif.level.${lv}` as "collabNotif.level.error")}
            </button>
          ))}
        </div>
        <button
          type="button"
          className="btn btn--danger btn--small collab-notif-panel__clear"
          title={t("collabNotif.clearHint")}
          disabled={entries.length === 0}
          onClick={() => void clearAll()}
        >
          {t("collabNotif.clear")}
        </button>
      </div>

      {filtered.length === 0 ? (
        <div className="collab-notif-panel__empty">{t("collabNotif.empty")}</div>
      ) : (
        <ul className="collab-notif-panel__list">
          {visible.map((entry) => (
            <li key={entry.id} className="collab-notif-panel__row" data-level={entry.level}>
              <span className="collab-notif-panel__icon" aria-hidden="true">{LEVEL_ICONS[entry.level]}</span>
              <div className="collab-notif-panel__main">
                <div className="collab-notif-panel__text">{entry.text}</div>
                {entry.detail && <div className="collab-notif-panel__detail">{entry.detail}</div>}
              </div>
              <span className="collab-notif-panel__time">{formatTime(entry.at)}</span>
            </li>
          ))}
        </ul>
      )}
      {truncated && (
        <div className="collab-notif-panel__more">{t("collabNotif.more", { n: filtered.length - visible.length })}</div>
      )}
    </div>
  );
}
