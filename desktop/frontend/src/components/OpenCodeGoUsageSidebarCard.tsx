import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { ACTIVE_MODEL_CHANGED_EVENT } from "../lib/activeModelChanged";
import { useI18n, type DictKey } from "../lib/i18n";
import {
  OPENCODE_GO_OFFICIAL_BASE,
  OPENCODE_GO_WINDOW_KEYS,
  resetCountdown,
  usageNoteText,
  type OpenCodeGoUsageResult,
} from "../lib/opencodeGoUsage";

// tl1487 四组 — OpenCode Go usage in the right-dock overview (PlanUsageCard
// 体系). 与 GLM Plan 卡同位同级、同一套 plan-usage 视觉。原 783 实验开关门
// （experimentalOpenCodeGoUsage）随「不提供开关、始终打开」裁决移除——卡与
// 轮询常在，仅剩 799 的 provider family 门（family 不对连请求都不发）。
//
// 任务 799 — 第二道显示门：provider 匹配，对齐 GLM 卡的任务 666 条件。显示
// 还要求当前 tab 的 CURRENT model 解析到 opencode-go family（Go 侧
// GetActivePlanFamily 的注册表判定，零 I/O）——GLM 地址下不渲染 OpenCode Go
// 卡（数据全 0% 的无意义占位），切到 opencode-go 地址时卡与轮询一起出现。
// family 与用量共用同一个 60s 轮询：family 不对时连用量查询都不发。
//
// 任务 872 — 热切换（ACTIVE_MODEL_CHANGED_EVENT）立即重查：epoch 递增让
// 在途的旧 provider family 查询作废（迟到答案不得复活旧卡），并立刻对新
// 模型重查——GLM↔OpenCode Go 来回切时本卡与 GLM 卡都不会同屏并存。
//
// fix-provider-poll-bound 刀② 按需查询（用户 2026-11-08 裁定）：family 答案
// 改为按事件刷新（挂载/换 tab/切模型/目录变更），已知非 opencode-go（含无
// 活跃 tab 的 family=""）时 60s tick 零调用——既不发用量请求，也不每 tick
// 重发 family 查询。Go 侧每次 family/用量查询都要解析全量 config（弃用键按
// 次 WARN），无相关活跃会话时的空转就是慢性重读源；事件面已覆盖 family 的
// 一切变化来源。刀① 失败有界由同主题后续 commit 落地。
//
// 数据面与 GLM 卡一致：60s 轮询（ensurePlanUsagePolling 同节奏）+ 手动刷新
// + 30s 倒计时 tick。任务 800 起本卡是明细的权威展示位：设置卡
// （SettingsOpenCodeGoUsageCard）只留开关管理位，卡内明细（三窗口/刷新/
// 降级提示）退役到这里；799 起另有 plan family 门（family 不对连请求都不发）。

const WINDOW_LABEL_KEY: Record<string, string> = {
  rolling: "settings.opencodeGoUsage.window.rolling",
  weekly: "settings.opencodeGoUsage.window.weekly",
  monthly: "settings.opencodeGoUsage.window.monthly",
};

const POLL_INTERVAL_MS = 60_000;
const COUNTDOWN_TICK_MS = 30_000;

export function OpenCodeGoUsageSidebarCard({ tabId }: { tabId?: string }) {
  const { t } = useI18n();
  // Task 799: the plan family of the tab's current provider, from
  // GetActivePlanFamily. undefined = not queried yet (in flight) —
  // the card renders nothing until the answer says opencode-go.
  const [family, setFamily] = useState<string | undefined>(undefined);
  const [usage, setUsage] = useState<OpenCodeGoUsageResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);
  // 任务 872: bumped on every hot model switch — the poll effect below tears
  // down (its cancelled flag drops in-flight answers for the outgoing model)
  // and re-queries the new model immediately.
  const [epoch, setEpoch] = useState(0);
  // 刀②：family 答案的内存门槛（事件刷新，tick 不重查——见 pollOnce）。
  const familyRef = useRef<string | undefined>(undefined);

  // 任务 872: forget the outgoing provider's family answer the moment the
  // switch lands (a late answer must not resurrect this card beside the
  // other provider's card) and re-query the new model right away.
  // 刀②：目录变更（设置页改默认模型/连接编辑）同样能翻转 family——同走
  // 这条「作废门槛 + 立即重查」的事件路径。
  useEffect(() => {
    const onQueryContextChanged = () => {
      familyRef.current = undefined;
      setFamily(undefined);
      setUsage(null);
      setEpoch((value) => value + 1);
    };
    window.addEventListener(ACTIVE_MODEL_CHANGED_EVENT, onQueryContextChanged);
    window.addEventListener("reasonix:model-catalog-changed", onQueryContextChanged);
    return () => {
      window.removeEventListener(ACTIVE_MODEL_CHANGED_EVENT, onQueryContextChanged);
      window.removeEventListener("reasonix:model-catalog-changed", onQueryContextChanged);
    };
  }, []);

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

  // Shared 60s poll with the 刀② on-demand gate. Per tick:
  //   family 已知非 opencode-go（含无活跃 tab 的 ""）→ 零调用零 config 读
  //     （family 只经事件变化——换 tab / 切模型 / 目录变更都会立即重查，
  //     见 onQueryContextChanged——tick 无需反复验证）；
  //   family 未定（新查询上下文首个 tick）→ 一次 family 查询定门，之后
  //     本上下文内不再重发；
  //   family = opencode-go → 照常查用量（tl1487「常开」= 功能不撤，活跃
  //     相关会话下 60s 节拍保持）。
  // A tabId change re-queries immediately so a tab switch never shows the
  // previous tab's provider card; an epoch bump (任务 872) does the same for a
  // hot model switch within the tab.
  useEffect(() => {
    let cancelled = false;
    // 新查询上下文（挂载/换 tab/epoch bump）：重置门槛。
    familyRef.current = undefined;
    const pollOnce = async () => {
      if (familyRef.current !== undefined && familyRef.current !== "opencode-go") return;
      if (familyRef.current === undefined) {
        try {
          const active = await app.GetActivePlanFamily(tabId ?? "");
          if (cancelled) return;
          familyRef.current = active;
          setFamily(active);
        } catch {
          // family 查询瞬时失败：保住旧门槛，下个 tick 重试
          // （它自身零网络 I/O，不计入用量调用）。
          return;
        }
        if (familyRef.current !== "opencode-go") {
          setUsage(null);
          return;
        }
      }
      await refresh();
    };
    void pollOnce();
    const poll = window.setInterval(() => void pollOnce(), POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(poll);
    };
  }, [tabId, refresh, epoch]);

  // Countdown lines re-render every 30s off the same fetched resetsAt values
  // (same cadence as the GLM plan card).
  const hasResets = (usage?.tiers ?? []).some((tier) => tier.resetsAt);
  useEffect(() => {
    if (!hasResets) return;
    const timer = window.setInterval(() => setTick((value) => value + 1), COUNTDOWN_TICK_MS);
    return () => window.clearInterval(timer);
  }, [hasResets]);

  if (family !== "opencode-go") return null;

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
