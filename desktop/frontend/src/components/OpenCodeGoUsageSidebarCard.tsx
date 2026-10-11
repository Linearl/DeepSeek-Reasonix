import { useCallback, useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { labFlagEnabled, onLabFlagsChange } from "../lib/labFlags";
import { useI18n, type DictKey } from "../lib/i18n";
import {
  OPENCODE_GO_OFFICIAL_BASE,
  OPENCODE_GO_WINDOW_KEYS,
  resetCountdown,
  usageNoteText,
  type OpenCodeGoUsageResult,
} from "../lib/opencodeGoUsage";

// 任务 783 — OpenCode Go usage in the right-dock overview (PlanUsageCard
// 体系). 与 GLM Plan 卡同位同级、同一套 plan-usage 视觉（验收：右侧边栏显示
// 与 GLM 卡一致）；显示门 = 实验开关 experimentalOpenCodeGoUsage（163 前端门
// 延伸到边栏——关 = 不渲染、零网络请求，铁律 2）。
//
// 任务 799 — 第二道显示门：provider 匹配，对齐 GLM 卡的任务 666 条件。显示
// 还要求当前 tab 的 CURRENT model 解析到 opencode-go family（Go 侧
// GetActivePlanFamily 的注册表判定，零 I/O）——GLM 地址下不渲染 OpenCode Go
// 卡（数据全 0% 的无意义占位），切到 opencode-go 地址时卡与轮询一起出现。
// family 与用量共用同一个 60s 轮询：family 不对时连用量查询都不发。
//
// 数据面与 GLM 卡一致：60s 轮询（ensurePlanUsagePolling 同节奏）+ 手动刷新
// + 30s 倒计时 tick。设置详情卡（SettingsOpenCodeGoUsageCard）保留原样：
// 那边是开关管理位 + 手动查询，这里是持续显示。

const WINDOW_LABEL_KEY: Record<string, string> = {
  rolling: "settings.opencodeGoUsage.window.rolling",
  weekly: "settings.opencodeGoUsage.window.weekly",
  monthly: "settings.opencodeGoUsage.window.monthly",
};

const POLL_INTERVAL_MS = 60_000;
const COUNTDOWN_TICK_MS = 30_000;

export function OpenCodeGoUsageSidebarCard({ tabId }: { tabId?: string }) {
  const { t } = useI18n();
  const [enabled, setEnabled] = useState(() => labFlagEnabled("opencodeGoUsage"));
  // Task 799: the plan family of the tab's current provider, from
  // GetActivePlanFamily. undefined = not queried yet (lab off or in flight) —
  // the card renders nothing until the answer says opencode-go.
  const [family, setFamily] = useState<string | undefined>(undefined);
  const [usage, setUsage] = useState<OpenCodeGoUsageResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);

  // Lab switch gate — same module gate the settings save re-applies, so the
  // card flips live without a restart.
  useEffect(() => onLabFlagsChange(() => setEnabled(labFlagEnabled("opencodeGoUsage"))), []);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const result = await app.GetOpenCodeGoUsage(OPENCODE_GO_OFFICIAL_BASE);
      setUsage(result);
    } catch {
      setUsage({ tiers: [], note: "network" });
    } finally {
      setLoading(false);
    }
  }, []);

  // Shared 60s poll for both gates: the family lookup first (zero-I/O config
  // scan), and the usage request only fires when this card may actually show.
  // A tabId change re-queries immediately so a tab switch never shows the
  // previous tab's provider card; a transport failure keeps the last family
  // and retries on the next tick. While the lab switch is off: no family
  // query, no usage fetch, no timer, no DOM (163 zero-regression contract).
  useEffect(() => {
    if (!enabled) {
      setFamily(undefined);
      setUsage(null);
      return;
    }
    let cancelled = false;
    const pollOnce = async () => {
      try {
        const active = await app.GetActivePlanFamily(tabId ?? "");
        if (cancelled) return;
        setFamily(active);
        if (active !== "opencode-go") {
          setUsage(null);
          return;
        }
      } catch {
        // Keep the last family (and its card) through a transient failure;
        // the next tick retries.
      }
      await refresh();
    };
    void pollOnce();
    const poll = window.setInterval(() => void pollOnce(), POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(poll);
    };
  }, [enabled, tabId, refresh]);

  // Countdown lines re-render every 30s off the same fetched resetsAt values
  // (same cadence as the GLM plan card).
  const hasResets = (usage?.tiers ?? []).some((tier) => tier.resetsAt);
  useEffect(() => {
    if (!enabled || !hasResets) return;
    const timer = window.setInterval(() => setTick((value) => value + 1), COUNTDOWN_TICK_MS);
    return () => window.clearInterval(timer);
  }, [enabled, hasResets]);

  if (!enabled || family !== "opencode-go") return null;

  // Same payload normalization as the settings detail card (Go nil slice can
  // marshal as null — fall to the note/empty branch instead of throwing).
  const tiers = Array.isArray(usage?.tiers) ? usage.tiers : [];
  const windows = OPENCODE_GO_WINDOW_KEYS
    .map((key) => tiers.find((tier) => tier.window === key))
    .filter((tier): tier is NonNullable<typeof tier> => Boolean(tier));
  const noteText = usage ? usageNoteText(usage.note, t) : "";

  return (
    <section className="context-panel__section plan-usage" data-testid="opencode-go-usage-sidebar">
      <header className="context-panel__section-head">
        <h3>{t("settings.opencodeGoUsage")}</h3>
        <button
          type="button"
          className="plan-usage__refresh"
          disabled={loading}
          aria-label={t("settings.opencodeGoUsage.refresh")}
          title={t("settings.opencodeGoUsage.refresh")}
          onClick={() => void refresh()}
        >
          <RefreshCw size={13} data-tick={tick} />
        </button>
      </header>
      {noteText && windows.length === 0 ? (
        <p className="plan-usage__note">{noteText}</p>
      ) : windows.length > 0 ? (
        <div className="plan-usage__rows">
          {windows.map((tier) => {
            // Three known windows carry literal keys; an unknown wire key
            // falls back to a literal so the Translator's key type stays
            // satisfied (same rule as the settings detail card).
            const labelKey = (WINDOW_LABEL_KEY[tier.window] ?? "settings.opencodeGoUsage.window.monthly") as DictKey;
            return (
              <div key={tier.window} className="plan-usage__row" data-window={tier.window}>
                <div className="plan-usage__row-head">
                  <span className="plan-usage__window">{t(labelKey)}</span>
                  <strong>{Math.round(tier.percent ?? 0)}%</strong>
                </div>
                <div className="plan-usage__track" aria-hidden="true">
                  <span className="plan-usage__fill" style={{ width: `${Math.max(0, Math.min(100, tier.percent ?? 0))}%` }} />
                </div>
                <span className="plan-usage__reset">
                  {/* tick participates so the interval re-render refreshes this line */}
                  {tier.resetsAt ? `${t("settings.opencodeGoUsage.resetsIn")} ${resetCountdown(tier.resetsAt) || "—"}` : ""}
                </span>
              </div>
            );
          })}
        </div>
      ) : (
        <p className="plan-usage__note">{t("settings.opencodeGoUsage.note.empty")}</p>
      )}
    </section>
  );
}
