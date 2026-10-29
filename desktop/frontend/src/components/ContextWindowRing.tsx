import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import { app } from "../lib/bridge";
import { contextWindowPercentages } from "../lib/contextWindow";
import { contextReadoutSource, contextReadoutSourceKey } from "../lib/contextPanelUtils";
import { useI18n } from "../lib/i18n";
import type { DictKey } from "../locales/en";
import { formatMoneyLocalized } from "../lib/money";
import { appendRateBand, rateBandLabel } from "../lib/costRateBand";
import type { BalanceInfo, ContextInfo, ContextPanelInfo } from "../lib/types";
import { AnchoredPopover } from "./AnchoredPopover";
import { contextWindowStatus, formatCacheHitRate } from "../lib/contextPanelUtils";
import { compositionSegments, compositionThreeSections, isOpencodeGoProvider, requestContextOverview } from "../lib/contextGaugePopup";
import {
  OPENCODE_GO_OFFICIAL_BASE,
  OPENCODE_GO_WINDOW_KEYS,
  resetCountdown,
  usageNoteText,
  type OpenCodeGoUsageResult,
} from "../lib/opencodeGoUsage";

// tl1224 阶段二：明细视图勾选状态按 tabId 会话内保持（切走再切回不丢；
// 不跨应用重启持久化——显示偏好不值得进存储）。模块级 Map 即可：组件随
// tab 卸载重建，Map 与应用同生命周期。
const detailViewByTab = new Map<string, boolean>();

// tl1224 阶段二：能力目录注入的 64KB 警告线（与 Go 侧
// capability.DirectoryWarnBytes 同源的字面量；字节口径，非 token）。
const DIRECTORY_WARN_BYTES = 64 * 1024;

interface ContextWindowRingProps {
  turnMetrics?: { elapsed: string; tokens: string | null; tps: string | null };
  enabled?: boolean;
  context?: ContextInfo;
  tabId?: string;
  turnCost?: number;
  turnRateBand?: string;
  currency?: string;
  cacheHitTokens?: number;
  cacheMissTokens?: number;
  balance?: BalanceInfo;
}

const RING = 18;
const RING_R = (RING - 2) / 2;
const RING_C = 2 * Math.PI * RING_R;

function fmtCompact(n: number): string {
  if (n <= 0) return "0";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, "") + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, "") + "k";
  return String(Math.round(n));
}

function fmtDuration(ms: number, t: ReturnType<typeof useI18n>['t']): string {
  if (ms <= 0) return "-";
  const totalSeconds = Math.floor(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes <= 0) return t("context.durationSeconds", { seconds });
  return t("context.durationMinutesSeconds", { minutes, seconds });
}

// Task 442: the quota cards reuse the settings card's window labels — same
// wire keys (rolling/weekly/monthly), same subscription, one wording.
const QUOTA_WINDOW_LABEL: Record<string, DictKey> = {
  rolling: "settings.opencodeGoUsage.window.rolling",
  weekly: "settings.opencodeGoUsage.window.weekly",
  monthly: "settings.opencodeGoUsage.window.monthly",
};

export function ContextWindowRing({ enabled = true, context, tabId, turnCost, turnRateBand, currency, cacheHitTokens, cacheMissTokens, balance, turnMetrics }: ContextWindowRingProps) {
  const { locale, t } = useI18n();
  const [open, setOpen] = useState(false);
  const [info, setInfo] = useState<ContextPanelInfo | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const loadingTabRef = useRef<string | null>(null);
  const requestSeq = useRef(0);
  const enterTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const used = context?.used ?? 0;
  const windowTokens = context?.window ?? 0;
  const usagePercentages = contextWindowPercentages(used, windowTokens);
  const rawUsagePct = usagePercentages.raw;
  const usagePct = usagePercentages.display;
  const compactRatio = context?.compactRatio && context.compactRatio > 0 ? context.compactRatio : 0.80;
  const compactPct = Math.round(compactRatio * 100);
  const status = contextWindowStatus(rawUsagePct, compactPct);

  const loadInfo = useCallback(() => {
    if (!enabled || !tabId) return;
    if (loadingTabRef.current === tabId) return;
    const requestTab = tabId;
    const seq = requestSeq.current + 1;
    requestSeq.current = seq;
    loadingTabRef.current = requestTab;
    app.ContextPanel(requestTab).then((next) => {
      if (requestSeq.current === seq) setInfo(next);
    }).catch(() => {}).finally(() => {
      if (requestSeq.current === seq) loadingTabRef.current = null;
    });
  }, [enabled, tabId]);

  // Reset when tabId changes so an older panel request cannot paint a new session.
  useEffect(() => {
    requestSeq.current += 1;
    loadingTabRef.current = null;
    setInfo(null);
    if (!enabled) setOpen(false);
  }, [enabled, tabId]);

  // Task 442: the quota cards are provider-conditional — shown whenever the
  // session's provider is opencode-go (a lab switch would make this a feature
  // flag; it is not one). One query per provider while the popup is open; the
  // settings card keeps owning refresh, this popup shows the snapshot it read
  // when it opened. A provider without an opencode-go half issues no call.
  const [quota, setQuota] = useState<OpenCodeGoUsageResult | null>(null);
  const quotaProviderRef = useRef<string | null>(null);
  const providerName = info?.providerName ?? "";
  const quotaEligible = isOpencodeGoProvider(providerName);

  useEffect(() => {
    if (!quotaEligible) {
      quotaProviderRef.current = null;
      setQuota(null);
      return;
    }
    if (!open || quotaProviderRef.current === providerName) return;
    quotaProviderRef.current = providerName;
    let cancelled = false;
    app.GetOpenCodeGoUsage(OPENCODE_GO_OFFICIAL_BASE)
      .then((result) => {
        if (!cancelled) setQuota(result);
      })
      .catch(() => {
        // Network/parse failures degrade to the same note the settings card
        // shows — the popup never throws out of a quota read.
        if (!cancelled) setQuota({ tiers: [], note: "failed" });
      });
    return () => {
      cancelled = true;
    };
  }, [open, providerName, quotaEligible]);

  // Task 442: the segmented bar reads the host's composition; absent when the
  // host predates the accessor, and then the whole block hides.
  const segments = compositionSegments(info?.composition);
  // tl1224 阶段二：协议三大段明细视图——勾选「明细」才启用；默认视图（平铺
  // 四项）零改动。目录块超 64KB 警告线时圆环与浮层着警告色（被动可见，无
  // 弹窗、无档位、无闩锁）。
  const [detailView, setDetailView] = useState(() => (tabId ? detailViewByTab.get(tabId) === true : false));
  useEffect(() => {
    setDetailView(tabId ? detailViewByTab.get(tabId) === true : false);
  }, [tabId]);
  const toggleDetailView = useCallback((checked: boolean) => {
    if (tabId) detailViewByTab.set(tabId, checked);
    setDetailView(checked);
  }, [tabId]);
  const sections = detailView ? compositionThreeSections(info?.composition) : [];
  const directoryBytes = info?.composition?.directoryBytes ?? 0;
  const overInjectLine = directoryBytes > DIRECTORY_WARN_BYTES;
  const injectPct = windowTokens > 0 ? Math.min(100, (DIRECTORY_WARN_BYTES / windowTokens) * 100) : 0;

  useEffect(() => () => {
    requestSeq.current += 1;
    if (enterTimer.current != null) clearTimeout(enterTimer.current);
    if (leaveTimer.current != null) clearTimeout(leaveTimer.current);
  }, []);

  const onEnter = useCallback(() => {
    if (enterTimer.current != null) clearTimeout(enterTimer.current);
    if (leaveTimer.current != null) clearTimeout(leaveTimer.current);
    loadInfo();
    enterTimer.current = setTimeout(() => setOpen(true), 200);
  }, [loadInfo]);

  const onLeave = useCallback(() => {
    if (enterTimer.current != null) clearTimeout(enterTimer.current);
    leaveTimer.current = setTimeout(() => setOpen(false), 120);
  }, []);

  const onPopoverEnter = useCallback(() => {
    if (leaveTimer.current != null) clearTimeout(leaveTimer.current);
  }, []);

  const onPopoverLeave = useCallback(() => {
    setOpen(false);
  }, []);

  if (!enabled) return null;

  const turnCacheHit = cacheHitTokens ?? info?.cacheHitTokens ?? 0;
  const turnCacheMiss = cacheMissTokens ?? info?.cacheMissTokens ?? 0;
  const turnCacheRate = formatCacheHitRate(turnCacheHit, turnCacheMiss);
  const compactTokens = windowTokens > 0 ? Math.round(windowTokens * compactRatio) : 0;
  const tokensToCompact = compactTokens > used ? compactTokens - used : 0;
  const ringOffset = RING_C * (1 - usagePct / 100);
  // 任务1025: 读数来源标记——「实测」=上次响应已校准；「估算」=新会话首轮/
  // 切 provider 首轮。与右栏容量卡同一权威值口径与标记。
  const observedPrompt = info?.contextBudget?.observedPrompt ?? context?.contextBudget?.observedPrompt ?? context?.maintenance?.contextBudget?.observedPrompt ?? 0;
  const readoutSource = contextReadoutSource(observedPrompt);
  const readoutSourceLabel = t(contextReadoutSourceKey(readoutSource));
  const readoutSourceTitle = t("context.readoutSourceTitle");
  const elapsed = info?.elapsedMs && info.elapsedMs > 0 ? fmtDuration(info.elapsedMs, t) : undefined;
  const quoteStatus = info?.sessionCostQuote?.displayStatus;
  const sessionCostBucketed = quoteStatus === "bucketed" || info?.sessionCostQuote?.aggregateMode === "currency_buckets";
  const sessionCostFallback = quoteStatus === "fallback_original";
  const sessionCostComplete = (sessionCostFallback || info?.sessionCostComplete !== false) && quoteStatus !== "unavailable";
  const sessionCostRaw = info?.sessionCostQuote?.selected
    ? Number(info.sessionCostQuote.selected.amount)
    : info?.sessionCost;
  const sessionCostCurrency = info?.sessionCostQuote?.selected?.currency || info?.sessionCurrency;
  const sessionCost =
    !sessionCostBucketed && sessionCostComplete && typeof sessionCostRaw === "number" && sessionCostRaw > 0
      ? `≈${formatMoneyLocalized(sessionCostRaw, sessionCostCurrency, { locale, empty: "dash" }).replace(/^≈/, "")}`
      : undefined;
  const sessionCostHint =
    info?.sessionBillingMode === "subscription_equivalent"
        ? "payg_equivalent"
        : sessionCostFallback
          ? "fallback_original"
        : sessionCost
          ? "estimated"
        : undefined;
  const turnCostLabel = appendRateBand(formatMoneyLocalized(turnCost, info?.sessionCurrency || currency, { locale, empty: "dash" }), turnRateBand, t);
  const sessionCostLabel = sessionCost ? appendRateBand(sessionCost, info?.sessionCostQuote?.rateBand, t) : undefined;
  const turnRateBandTitle = rateBandLabel(turnRateBand, t) ? t("billing.rateBand.tooltip") : undefined;
  const sessionRateBandTitle = rateBandLabel(info?.sessionCostQuote?.rateBand, t) ? t("billing.rateBand.tooltip") : undefined;

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className={`context-ring${open ? " context-ring--open" : ""} context-ring--${status.tone}${overInjectLine ? " context-ring--overinject" : ""}`}
        onMouseEnter={onEnter}
        onMouseLeave={onLeave}
        onFocus={onEnter}
        onBlur={onLeave}
        onClick={() => {
          if (enterTimer.current != null) clearTimeout(enterTimer.current);
          if (leaveTimer.current != null) clearTimeout(leaveTimer.current);
          loadInfo();
          setOpen(true);
        }}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={t("context.windowUsageSummary", { used: String(used), window: String(windowTokens), pct: rawUsagePct })}
      >
        <svg width={RING} height={RING} viewBox={`0 0 ${RING} ${RING}`} className="context-ring__svg">
          <circle className="context-ring__track" cx={RING / 2} cy={RING / 2} r={RING_R} fill="none" strokeWidth={2} />
          <circle
            className="context-ring__arc"
            cx={RING / 2} cy={RING / 2} r={RING_R}
            fill="none" strokeWidth={2}
            strokeLinecap="round"
            strokeDasharray={RING_C}
            strokeDashoffset={ringOffset}
            transform={`rotate(-90 ${RING / 2} ${RING / 2})`}
          />
        </svg>
        <span className="context-ring__percent">{rawUsagePct}%</span>
      </button>
      <AnchoredPopover
        open={open}
        anchorRef={triggerRef}
        onClose={() => {
          if (enterTimer.current != null) clearTimeout(enterTimer.current);
          setOpen(false);
        }}
        className={`context-ring-popover context-ring-popover--${status.tone}${overInjectLine ? " context-ring-popover--overinject" : ""} composer-menu-surface`}
        align="end"
        placement="auto"
      >
        <div className="context-ring-popover__inner" role="dialog" aria-label={t("context.windowUsageSummary", { used: String(used), window: String(windowTokens), pct: rawUsagePct })} onMouseEnter={onPopoverEnter} onMouseLeave={onPopoverLeave}>
          <div className="context-ring-popover__header">
            <span className="context-ring-popover__title">
              {fmtCompact(used)} / {fmtCompact(windowTokens)}
            </span>
            {/* 任务1025: 读数来源标记——「实测」/「估算」，与右栏容量卡同一口径。 */}
            <span className="context-ring-popover__readout-source" title={readoutSourceTitle}>
              {readoutSourceLabel}
            </span>
            <span className="context-ring-popover__pct">{rawUsagePct}%</span>
            {/* tl1239①: 明细开关移到头部行右端空白处（用户验收反馈：色条下方
                独立行不顺手）。仅在有构成数据时出现（与明细视图同进退）；
                勾选行为与会话内状态保持逻辑不变。 */}
            {(segments.length > 0 || sections.length > 0) && (
              <label
                className="context-composition__detail-toggle context-composition__detail-toggle--header"
                title={t("context.detailToggleTitle")}
              >
                <input
                  type="checkbox"
                  className="context-composition__detail-checkbox"
                  data-testid="composition-detail-toggle"
                  checked={detailView}
                  onChange={(event) => toggleDetailView(event.currentTarget.checked)}
                />
                <span>{t("context.detailToggle")}</span>
              </label>
            )}
          </div>
          <div className="context-ring-popover__gauge">
            <div className="context-ring-popover__bar">
              <span className="context-ring-popover__fill" style={{ width: `${usagePct}%` }} />
              <span className="context-ring-popover__mark context-ring-popover__mark--compact" style={{ left: `${compactPct}%` }} title={t("context.windowCompactThresholdTitle")} />
              <span className="context-ring-popover__mark context-ring-popover__mark--attention" style={{ left: `30%` }} />
              {/* tl1224 阶段二：64K 注入警告线刻度——复用距压缩刻度的视觉语言；
                  目录块字节（非 token）与 64KB 警告线比较，超线时圆环变色。 */}
              {directoryBytes > 0 && (
                <span
                  className={`context-ring-popover__mark context-ring-popover__mark--inject${overInjectLine ? " context-ring-popover__mark--inject-over" : ""}`}
                  style={{ left: `${injectPct}%` }}
                  title={t("context.injectMarkTitle", { size: fmtCompact(directoryBytes), line: "64k" })}
                />
              )}
            </div>
          </div>
          {/* tl1224 阶段二：明细开关原置于此（tl1239① 移至头部行右端）。
              US-A：默认视图（未勾选）= 现状平铺四项零改动；US-B：勾选后切协议
              三大段（横线分割+彩色子项）。无构成数据时整块隐藏。 */}
          {(segments.length > 0 || sections.length > 0) && (
            <div className="context-composition" role="group" aria-label={t("context.compositionTitle")}>
              {detailView ? (
                <div className="context-composition__sections" data-testid="composition-sections">
                  <div className="context-composition__bar" aria-hidden="true">
                    {sections.map((section) => (
                      <span
                        key={section.key}
                        className={`context-composition__seg context-composition__seg--section-${section.key}`}
                        style={{ width: `${section.share}%` }}
                      />
                    ))}
                  </div>
                  {sections.map((section) => (
                    <div className="context-composition__section" key={section.key}>
                      <div className="context-composition__section-head">
                        <span className={`context-composition__dot context-composition__dot--section-${section.key}`} aria-hidden="true" />
                        <span className="context-composition__section-name">{t(section.labelKey)}</span>
                        <span className="context-composition__tokens">{fmtCompact(section.tokens)}</span>
                        <span className="context-composition__share">{section.share.toFixed(1)}%</span>
                      </div>
                      <ul className="context-composition__section-items">
                        {section.items.map((item) => (
                          <li className="context-composition__section-item" key={item.key}>
                            <span className={`context-composition__dot context-composition__dot--item-${item.key}`} aria-hidden="true" />
                            <span className="context-composition__label">{t(item.labelKey)}</span>
                            <span className="context-composition__tokens">{fmtCompact(item.tokens)}</span>
                            <span className="context-composition__share">{item.share.toFixed(1)}%</span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ))}
                </div>
              ) : (
                <>
              <div className="context-composition__bar" aria-hidden="true">
                {segments.map((seg) => (
                  <span
                    key={seg.key}
                    className={`context-composition__seg context-composition__seg--${seg.key}`}
                    style={{ width: `${seg.share}%` }}
                  />
                ))}
              </div>
              <ul className="context-composition__legend">
                {segments.map((seg) => (
                  <li className="context-composition__legend-item" key={seg.key}>
                    <span className={`context-composition__dot context-composition__dot--${seg.key}`} aria-hidden="true" />
                    <span className="context-composition__label">{t(seg.labelKey)}</span>
                    <span className="context-composition__tokens">{fmtCompact(seg.tokens)}</span>
                    <span className="context-composition__share">{seg.share.toFixed(1)}%</span>
                  </li>
                ))}
              </ul>
                </>
              )}
            </div>
          )}
          <div className="context-ring-popover__rows">
            <div className="context-ring-popover__row">
              <span className="context-ring-popover__label">{t("context.windowCompactDistance")}</span>
              <span className="context-ring-popover__value">{fmtCompact(tokensToCompact)}</span>
            </div>
            {info?.requestCount != null && info.requestCount > 0 && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("context.requests")}</span>
                <span className="context-ring-popover__value">{info.requestCount}</span>
              </div>
            )}
            {elapsed && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("context.sessionTime")}</span>
                <span className="context-ring-popover__value">{elapsed}</span>
              </div>
            )}
            {turnMetrics && <div className="context-ring-popover__turn-metrics" title={t("composer.runStripEstimateHint")}>
              {[[t("context.turnTime"), turnMetrics.elapsed], [t("status.tpsLabel"), turnMetrics.tps], [t("status.turnTokensLabel"), turnMetrics.tokens]].map(([label, value]) => value && (
                <div className="context-ring-popover__row" key={label}>
                  <span className="context-ring-popover__label">{label}</span>
                  <span className="context-ring-popover__value">{value}</span>
                </div>
              ))}
            </div>}
            <div className="context-ring-popover__row">
              <span className="context-ring-popover__label">{t("status.cacheLabel")}</span>
              <span className="context-ring-popover__value">{turnCacheRate}</span>
            </div>
            {/* Task 442: the session-wide average sits beside the per-turn
                number — one is "this request", this one is "the session". */}
            <div className="context-ring-popover__row">
              <span className="context-ring-popover__label">{t("context.avgCacheHitRate")}</span>
              <span className="context-ring-popover__value">
                {formatCacheHitRate(info?.sessionCacheHitTokens ?? 0, info?.sessionCacheMissTokens ?? 0)}
              </span>
            </div>
            {turnCost != null && turnCost > 0 && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("status.turnCostLabel")}</span>
                <span className="context-ring-popover__value" title={turnRateBandTitle}>{turnCostLabel}</span>
              </div>
            )}
            {sessionCostLabel && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">
                  {sessionCostHint === "payg_equivalent"
                    ? t("context.sessionCostPaygEquivalent")
                    : sessionCostHint === "fallback_original"
                      ? t("context.sessionCostFallback")
                    : t("context.sessionCostEstimated")}
                </span>
                <span className="context-ring-popover__value" title={sessionRateBandTitle}>{sessionCostLabel}</span>
              </div>
            )}
            {!sessionCost && sessionCostBucketed && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("context.sessionCost")}</span>
                <span className="context-ring-popover__value">{t("context.sessionCostBucketed")}</span>
              </div>
            )}
            {!sessionCost && !sessionCostBucketed && info?.sessionCostComplete === false && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("context.sessionCostEstimated")}</span>
                <span className="context-ring-popover__value">—</span>
              </div>
            )}
            {balance?.available && balance.display && (
              <div className="context-ring-popover__row">
                <span className="context-ring-popover__label">{t("status.balanceLabel")}</span>
                <span className="context-ring-popover__value context-ring-popover__value--accent">{balance.display}</span>
              </div>
            )}
          </div>
          {/* Task 442: multi-channel quota cards. Only opencode-go has a
              quota endpoint today, and it shows only for its own providers —
              the card list grows when another channel gains a data source. */}
          {quotaEligible && quota && quota.tiers.length > 0 && (
            <div className="context-quota" role="group" aria-label={t("context.quotaTitle")}>
              <div className="context-quota__head">{t("context.quotaTitle")}</div>
              <div className="context-quota__cards">
                {OPENCODE_GO_WINDOW_KEYS.map((windowKey) => {
                  const tier = quota.tiers.find((candidate) => candidate.window === windowKey);
                  if (!tier) return null;
                  const used = tier.percent ?? null;
                  const left = used == null ? null : Math.max(0, Math.round(100 - used));
                  const labelKey: DictKey = QUOTA_WINDOW_LABEL[windowKey] ?? "settings.opencodeGoUsage.window.monthly";
                  return (
                    <div className="context-quota__card" key={windowKey} data-window={windowKey}>
                      <span className="context-quota__window">{t(labelKey)}</span>
                      <span className="context-quota__value">
                        {left == null ? "—" : `${t("context.quotaRemaining")} ${left}%`}
                      </span>
                      <span className="context-quota__resets">
                        {tier.resetsAt
                          ? `${t("settings.opencodeGoUsage.resetsIn")} ${resetCountdown(tier.resetsAt) || "—"}`
                          : ""}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
          {quotaEligible && quota && quota.tiers.length === 0 && quota.note && (
            <p className="context-quota__note">{usageNoteText(quota.note, t)}</p>
          )}
          <button
            type="button"
            className="context-ring-popover__more"
            onClick={() => {
              requestContextOverview();
              setOpen(false);
            }}
          >
            <span>{t("context.moreDetail")}</span>
            <ChevronRight size={12} aria-hidden="true" />
          </button>
        </div>
      </AnchoredPopover>
    </>
  );
}
