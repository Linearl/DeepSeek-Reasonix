import { SettingsOptions } from "./SettingsOptions";
import { SettingsSelect } from "./SettingsSelect";
import { SessionEventsPanel, type RotationMode } from "./SessionEventsPanel";
import { SessionMigrationSection } from "./SessionMigrationSection";
import { DagMigrationSection } from "./DagMigrationSection";
import { PerfMemorySection } from "./PerfMemorySection";
import { isDockTabHidden, isLastRenderableVisibleTab, setDockTabHidden } from "../lib/dockTabs";
import { useRemoteStore } from "../store/remote";
import { reportFrontendLog } from "../lib/frontendLog";
import { useConfirmDialog } from "./ConfirmDialog";
import ForkFeaturesIntroDialog from "./ForkFeaturesIntroDialog";
import LabBulkToggleDialog from "./LabBulkToggleDialog";
import { useProviderT as useT } from "../lib/providerSettingsLocale";
import { QuickCommandsManagerDialog } from "./QuickCommandsManagerDialog";
import { lazy, memo, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, FlaskConical, Folder, FolderLock, Monitor, RefreshCw, Server, ShieldAlert, ShieldCheck, SlidersHorizontal, Sparkles, Terminal, Volume2, Zap } from "lucide-react";
import { app } from "../lib/bridge";
import { setSessionMonitorEnabled, setSessionMonitorOpen } from "../lib/sessionMonitor";
import { setAutoLoadOlderEnabled } from "../lib/autoLoadOlderPreference";
import { setCollabGuidanceMergeEnabled } from "../lib/collabGuidanceMergePreference";
import { setFeedbackEnabled, setFeedbackOpen } from "./FeedbackPanel";
import { fireMockCrashDrill } from "../lib/crashMock";
import { setCollabInboxOpen } from "./CollabInboxPanel";
import { setSplitViewEnabled } from "../lib/splitView";
import { type DictKey } from "../lib/i18n";
import { ErrorBoundary } from "./ErrorBoundary";
import { LabSectionCard, SettingsField } from "./SettingsForm";
import { SettingsOpenCodeGoUsageCard } from "./SettingsOpenCodeGoUsageCard";
import { EXPERIMENT_FEATURE_TIERS, LAB_WALL_PICKS, type TierFeatureId } from "../lib/experimentTiers";
import { findLabEntry, labEntries, labEntryBadgeTier, labEntryLightOn, resolveLabLayout } from "../lab/labLayout";
import labLayoutYaml from "../lab/lab-layout.yaml?raw";
import { LAB_GROUP_KEYS, type LabGroupKey, type LabLayoutEntry } from "../lab/labLayoutTypes";
import { LAB_PARAM_FIELDS } from "../lab/labParamFields";
import { LabWallOnContext } from "../lib/labWallOn";
import { TierBadge } from "./TierBadge";
import type { SettingsView } from "../lib/types";
import { chimeLocalAssetsEnabled, normalizeUpdateChimeTune, UPDATE_CHIME_TUNES } from "../lib/sound";
import { SettingsPageShell, type SectionProps } from "./SettingsPageShell";
import { LAB_PANE_IDS, type ExperimentFeatureId } from "../lab/labPaneRegistry";
import { normalizeDesktopLayoutStyle } from "../lib/desktopLayoutStyle";

const DEFAULT_AUTOPILOT_MAX_RUNTIME = "8h";

const HomePointerLabPane = lazy(() => import("./HomePointerLabPane").then((module) => ({ default: module.HomePointerLabPane })));

/** Task 173 UI fix: number input with local state so typing works before blur commits. */
function DailySendLimitInput({ value, disabled, onCommit }: { value: number; disabled?: boolean; onCommit: (v: number) => void }) {
  const [local, setLocal] = useState(String(value));
  useEffect(() => { setLocal(String(value)); }, [value]);
  return (
    <input
      type="number"
      min={0}
      value={local}
      disabled={disabled}
      onChange={(e) => setLocal(e.target.value)}
      onBlur={() => {
        const n = Math.max(0, Number(local) || 0);
        setLocal(String(n));
        if (n !== value) onCommit(n);
      }}
    />
  );
}

/** ExperimentalSection hosts opt-in capabilities behind one switch each (task 132).
 *
 *  Left rail lists one feature per row with an on/off dot; the right pane holds
 *  that feature's controls. With a switch off the original behaviour is untouched,
 *  so a capability can be compared rather than merely turned on.
 */

/** Conversation store modes (task 155), in upgrade order: read and write side of
 *  the v3/v4 pair move one stage at a time, never straight from v3 to v4. */
const SESSION_STORAGE_MODES = ["v3_only", "dual_write_read_v3", "dual_write_read_v4", "v4_only"] as const;
type SessionStorageMode = (typeof SESSION_STORAGE_MODES)[number];

/** tl1287: v4 support is retired — the card offers no v4 option any more, so the
 *  segmented control renders from this list alone. The full four-mode list above
 *  stays the parser for the STORED value: a user still parked on a v4 stage keeps
 *  their own stage/risk lines below (display only; the way back is the v4→v3
 *  migrator hosted in the same card, which the backend always allows). */
const SESSION_STORAGE_SELECTABLE_MODES = ["v3_only"] as const satisfies readonly SessionStorageMode[];

/** Narrow a wire value onto the typed mode list (unknown values read as v3). */
function asSessionStorageMode(value: string | undefined): SessionStorageMode {
  return SESSION_STORAGE_MODES.find((mode) => mode === value) ?? "v3_only";
}

/** 任务 775 — kind:param 参数卡通用渲染：无开关，一行一字段，控件形态按
 * field.input 分派（number/text/segment/checkbox），值读取与写入走
 * labParamFields 注册表。渲染规则（设计 §4.2）：布局里已登记的参数卡走这
 * 份数据渲染；未登记参数保持各硬编码分支兜底——渐进迁移，登记一张删一张。
 * number/text 键入中持草稿、onBlur 落盘（与原硬编码参数行同款手感，配置层
 * 自行钳制）；segment/checkbox 即点即存。行级徽章遵守 621 口径：卡头档位
 * 徽章只出现在首字段行（至多一次）。 */
function LabParamFieldsCard({ entry, s, busy, apply }: { entry: LabLayoutEntry; s: SettingsView; busy: boolean; apply: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const t = useT();
  // number/text 的键入草稿（键=字段 id）：只在字段键入过时覆盖快照读数。
  const [drafts, setDrafts] = useState<Record<string, string | number>>({});
  return (
    <>
      {(entry.fields ?? []).map((field, index) => {
        const binding = LAB_PARAM_FIELDS[field.id];
        // resolver 已核过引用；防御式跳过（无绑定字段不渲染，不出空行）。
        if (!binding) return null;
        const label = (
          <>
            {t(binding.labelKey ?? entry.labelKey)}
            {index === 0 && entry.tier ? <TierBadge tier={entry.tier} translator={t} /> : null}
          </>
        );
        if (binding.input === "checkbox") {
          return (
            <SettingsField key={field.id} label={label} hint={t(binding.hintKey)} icon={<Sparkles size={18} />}>
              <input
                type="checkbox"
                checked={Boolean(binding.read(s))}
                disabled={busy}
                onChange={(event) => void apply(() => binding.write(event.target.checked))}
              />
            </SettingsField>
          );
        }
        if (binding.input === "segment") {
          return (
            <SettingsField key={field.id} label={label} hint={t(binding.hintKey)} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {(binding.options ?? []).map((option) => (
                  <button
                    key={String(option.value)}
                    type="button"
                    className={`set-seg__btn${binding.read(s) === option.value ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => binding.write(option.value))}
                  >
                    {t(option.labelKey)}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          );
        }
        // number / text：草稿态 + onBlur 落盘。number 键入中间态（如 "-"）出
        // NaN 时不落盘，保留草稿等继续键入（原硬编码行会直接把 NaN 送桥，
        // 这里收口）。
        const current = drafts[field.id] ?? binding.read(s);
        return (
          <SettingsField key={field.id} label={label} hint={t(binding.hintKey)} icon={<Sparkles size={18} />}>
            <input
              type={binding.input}
              min={binding.min}
              max={binding.max}
              value={current}
              disabled={busy}
              onChange={(event) => setDrafts((prev) => ({
                ...prev,
                [field.id]: binding.input === "number" ? Number(event.target.value) : event.target.value,
              }))}
              onBlur={() => {
                if (binding.input === "number" && typeof current === "number" && Number.isNaN(current)) return;
                void apply(async () => { await binding.write(current); });
              }}
            />
          </SettingsField>
        );
      })}
    </>
  );
}

/** tl 1097 — 日志管理 pane（可观测性组 kind:param 卡专用渲染）。
 *
 * 通用 LabParamFieldsCard 只能渲染「一行一字段」，无法承载本卡多出来的三样：
 *   ① 区块说明固定行（「全量日志保留最近约 2 天……」）；
 *   ② 「当前占用」实时统计行（需调 AlertLogUsage 桥）；
 *   ③ 「打开日志目录」按钮 + 「永久+无限制」警示行（需要跨两个字段联动判断，
 *      通用渲染器的每字段独立渲染做不到）。
 * 两个 segment 档位的读写仍走 labParamFields 注册表（值语义单源），本组件只
 * 负责「把注册表里的 segment 字段渲成按钮行 + 叠加这三样」。 */
function LogManagementLabPane({ s, busy, apply }: { s: SettingsView; busy: boolean; apply: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const t = useT();
  const [usage, setUsage] = useState<{ bytes: number; files: number } | null>(null);
  useEffect(() => {
    let alive = true;
    void app.AlertLogUsage().then((u) => { if (alive) setUsage({ bytes: u.bytes, files: u.files }); }).catch(() => {});
    return () => { alive = false; };
  }, [s.desktopAlertLogRetentionDays, s.desktopAlertLogMaxTotalMB]);
  const retentionBinding = LAB_PARAM_FIELDS.alertLogRetentionDays;
  const sizeBinding = LAB_PARAM_FIELDS.alertLogMaxTotalMB;
  // 语义③：「永久（-1）+ 无限制（-1）」= 真无限增长 ⇒ 必须显示警示行；只选其
  // 一「无限」时不显示（另一个维度仍在兜底）。
  const foreverUnlimited = (s.desktopAlertLogRetentionDays ?? 0) === -1 && (s.desktopAlertLogMaxTotalMB ?? 0) === -1;
  const usageMB = usage ? (usage.bytes / (1024 * 1024)) : 0;
  const usageText = usage
    ? t("settings.logManagement.usage", { size: usageMB.toFixed(1), n: String(usage.files) })
    : "";
  return (
    <>
      <SettingsField label={t("settings.logManagement")} icon={<Activity size={18} />} stacked>
        <p className="settings-field__hint">{t("settings.logManagement.hint")}</p>
      </SettingsField>
      <SettingsField label={t(retentionBinding.labelKey ?? "settings.alertLogRetention.label")} hint={t(retentionBinding.hintKey)} icon={<Activity size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {(retentionBinding.options ?? []).map((option) => (
            <button
              key={String(option.value)}
              type="button"
              className={`set-seg__btn${retentionBinding.read(s) === option.value ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => void apply(() => retentionBinding.write(option.value))}
            >
              {t(option.labelKey)}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      <SettingsField label={t(sizeBinding.labelKey ?? "settings.alertLogMaxTotal.label")} hint={t(sizeBinding.hintKey)} icon={<Activity size={18} />}>
        <SettingsOptions layout="field" className="set-seg">
          {(sizeBinding.options ?? []).map((option) => (
            <button
              key={String(option.value)}
              type="button"
              className={`set-seg__btn${sizeBinding.read(s) === option.value ? " set-seg__btn--on" : ""}`}
              disabled={busy}
              onClick={() => void apply(() => sizeBinding.write(option.value))}
            >
              {t(option.labelKey)}
            </button>
          ))}
        </SettingsOptions>
      </SettingsField>
      {foreverUnlimited && (
        <p className="settings-field__hint-line experimental-lab__stable-warning" role="status">
          {t("settings.logManagement.foreverUnlimitedWarning")}
        </p>
      )}
      {usageText !== "" && (
        <p className="settings-field__hint" data-testid="alert-log-usage">{usageText}</p>
      )}
      <SettingsField label={t("settings.logManagement.openDirLabel")} icon={<Folder size={18} />} stacked>
        {/* tl 1180: `set-seg__btn` is a CHILD class of `.set-seg` (the segmented
            control's inner button). Used standalone it inherits no frame, border
            or background and reads as bare clickable text. Use the same standard
            secondary button every other action on this pane uses. */}
        <button
          type="button"
          className="btn btn--secondary btn--small"
          disabled={busy}
          onClick={() => void apply(async () => { await app.OpenLogDir(); })}
        >
          {t("settings.logManagement.openDir")}
        </button>
      </SettingsField>
    </>
  );
}

/** tl 1134 — rail 行 memo 化：行 props 全为纯值（feature 引用随 features useMemo
 * 稳定、active 布尔、onSelect 为 setState 引用稳定），selected 切换/他卡开合时
 * 未受影响的行不再重渲——63 行的 rail 在高频点击导航下避免全列表 reconciliation。
 * 灯状态刷新不受影响：settings 重载时 features 整表重算，行 props 随新引用更新。 */
const LabRailItem = memo(function LabRailItem({ feature, active, onSelect, translator }: {
  feature: {
    id: ExperimentFeatureId;
    label: string;
    on: boolean;
    kind: "feature" | "param" | "action";
    badgeTier: ReturnType<typeof labEntryBadgeTier>;
  };
  active: boolean;
  onSelect: (id: ExperimentFeatureId) => void;
  translator: ReturnType<typeof useT>;
}) {
  return (
    <button
      type="button"
      id={`lab-entry-${feature.id}`}
      className={`experimental-rail__item${active ? " experimental-rail__item--active" : ""}${feature.on || feature.kind === "param" || feature.kind === "action" ? "" : " experimental-rail__item--off"}`}
      aria-current={active ? "true" : undefined}
      onClick={() => onSelect(feature.id)}
    >
      {/* 任务 775：参数卡无开关——空心态点代替开/关状态点，
          且不参与 --off 变灰（参数卡不存在「关」态）。
          tl1227：动作卡（action）同语义，复用同一空心态点（零新增 CSS 类）。 */}
      <span className={`experimental-rail__dot${feature.on ? " experimental-rail__dot--on" : feature.kind === "param" || feature.kind === "action" ? " experimental-rail__dot--param" : ""}`} aria-hidden="true" />
      <span className="experimental-rail__label">{feature.label}</span>
      {/* 任务 774: the rail row (card head) renders exactly ONE
          aggregated badge — max(own, members) by tier strength;
          member badges stay on their in-card detail rows. */}
      {feature.badgeTier ? <TierBadge tier={feature.badgeTier} translator={translator} /> : null}
    </button>
  );
});

export function ExperimentalSection({ s, busy, apply, focusEntry }: SectionProps & { focusEntry?: ExperimentFeatureId | null }) {
  // 任务 1098（阶段 1 补打点）：实验室页专属的「首屏可交互」计时——上面那层
  // 的 first paint 只覆盖到「设置面板骨架出现」，这里再量一次从实验室入口被
  // 打开（reload 记下的 dataReady 基准）到实验室卡片（rail/分组树）真正画出
  // 来的耗时，覆盖 resolveLabLayout 解析 + 全部开关卡片渲染这段可能很重的
  // 前端工作。只在本实验室页每次挂载时上报一次，不随后续 state 抖动重复打。
  const labMountedAt = useRef<number | null>(null);
  const labPaintReported = useRef(false);
  if (labMountedAt.current === null) labMountedAt.current = Date.now();
  useEffect(() => {
    if (labPaintReported.current) return undefined;
    labPaintReported.current = true;
    const mountedAt = labMountedAt.current ?? Date.now();
    let cancelled = false;
    const raf = window.requestAnimationFrame(() => {
      if (cancelled) return;
      reportFrontendLog("lab", "experimental first paint", `mountToPaintMs=${Date.now() - mountedAt}`);
    });
    return () => {
      cancelled = true;
      window.cancelAnimationFrame(raf);
    };
  }, []);
  // Set when a boot-time setting is saved: apply() reloads the view, so the fact that a
  // restart is pending has to live outside the data being reloaded.
  const [restartNeeded, setRestartNeeded] = useState(false);
  // Task 259 (audit-2 minor c): the "keep at least one tab" guard counts only
  // tabs that can actually render, so it needs the live remote-host presence
  // and the configured layout.
  const remoteAvailable = useRemoteStore((state) => state.hosts.length > 0);
  const desktopLayoutStyle = normalizeDesktopLayoutStyle(s.desktopLayoutStyle);
  const [dreamTaskCreated, setDreamTaskCreated] = useState(false);
  // 任务 801：决策模式卡的本地草稿态（阈值数字输入与文件路径文本框），提交
  // 走 onBlur 保存；初始值随设置快照填充。
  const [decisionTaskCreated, setDecisionTaskCreated] = useState(false);
  // 阈值/路径的本地草稿随快照初始化（0 = 配置默认 5，框里显示 5）。
  const [decisionThresholdDraft, setDecisionThresholdDraft] = useState(String(s.decisionPatternThreshold || 5));
  const [decisionPathDraft, setDecisionPathDraft] = useState(s.decisionPatternFilePath ?? "");
  // Task 184: the sampler interval is a number the user can edit; the config layer
  // clamps it, so the box can hold an intermediate value while typing.
  const [perfInterval, setPerfInterval] = useState<number>(s.perfMonitorIntervalSeconds ?? 5);
  // Task 528: the heap-high trigger threshold is a number the user can edit
  // (MiB); the config layer clamps into 1024..131072. A 0 readback means the
  // built-in 6GB default, so the box starts there.
  const [perfHeapHighThreshold, setPerfHeapHighThreshold] = useState<number>(s.perfMonitorHeapHighThresholdMB ?? 6144);
  // Task 318.2: the fold-cooldown minutes only matter while the switch is on;
  // the config layer normalizes 0 → 10, so the box may hold an intermediate
  // value while typing.
  const [proactiveCooldownMinutes, setProactiveCooldownMinutes] = useState<number>(s.proactiveCompactCooldownMinutes ?? 10);
  // Task 204: the cross-session chain ceiling is a number the user can edit; the
  // config layer clamps it, so the box may hold an intermediate value while typing.
  const [hopLimit, setHopLimit] = useState<number>(s.sessionCollabHopLimit ?? 5);
  // 任务 775：308-O4/O3 两参数的本地草稿态随硬编码行一并退役——值读写改走
  // LabParamFieldsCard（组件内草稿态）+ labParamFields 注册表。
  // Task 19: the addressable roster is read on demand, not on every settings
  // load — a session only appears once it has registered a purpose.
  const [sessionCollabRoster, setSessionCollabRoster] = useState<Awaited<ReturnType<typeof app.ListAddressableSessions>>>([]);
  // 任务 722/724：布局数据驱动——分组树/成员徽章/排序/卡片灯引用全部来自
  // lab-layout.yaml（校验失败回退内置默认，安全网不白屏）。dev 模式改 yaml
  // 保存即热加载（vite ?raw 模块失效→刷新），无需重启。memo 化：解析一次，
  // 渲染表随 s 变化重算（性能附录②③——进实验室页不逐开关拉配置，读走
  // app.Settings() 单次快照，灯在本地谓词表上算）。
  const labLayoutResolved = useMemo(
    () => resolveLabLayout(labLayoutYaml, { allowedEntryIds: LAB_PANE_IDS }),
    [],
  );
  useEffect(() => {
    if (labLayoutResolved.source === "default" && labLayoutResolved.warnings.length > 0) {
      console.warn("[lab-layout]", ...labLayoutResolved.warnings);
    }
  }, [labLayoutResolved]);
  // Task 257: turning full access ON passes one danger confirmation first —
  // the same one-shot gate shape as Claude Code / MiMo's yolo mode. Turning
  // it OFF never asks.
  const { confirm: confirmFullAccess, dialog: fullAccessDialog } = useConfirmDialog();

// Task 561: the default selection follows the first rail entry (autopilot;
// the old default restartUpdate folded into the updateFeedback card).
const [selected, setSelected] = useState<ExperimentFeatureId>("autopilot");
  // 任务 775：当前选中条目的布局数据——kind:param 卡由此走通用数据渲染
  // （LabParamFieldsCard），feature 卡仍走各 selected===id 硬编码分支。
  const selectedEntry = useMemo(
    () => findLabEntry(labLayoutResolved.layout, selected),
    [labLayoutResolved, selected],
  );
  // 任务 764：随顶部筛选 chips 删除，labFilter 过滤态一并移除——rail 恒显
  // 全部组（组内条目仍受折叠控制），导航由分组目录 toc 承接。
  // Task 385a: the 回答风格 selector payload loads on demand — only while its
  // lab entry is open, and again after every save that can change the active
  // style or the discovered files (apply() reloads s, which re-fires this).
  // 任务 961：outputStyle 并入「交互与呈现」合并卡——按需加载的触发条件
  // 随迁卡 id（成员行在卡内，卡打开即加载，语义不变）。
  const [outputStyles, setOutputStyles] = useState<Awaited<ReturnType<typeof app.ListOutputStyles>> | null>(null);
  const [outputStylesError, setOutputStylesError] = useState<string | null>(null);
  useEffect(() => {
    if (selected !== "interactionPresentation") return undefined;
    let alive = true;
    void app.ListOutputStyles()
      .then((view) => {
        if (alive) {
          setOutputStyles(view);
          setOutputStylesError(null);
        }
      })
      .catch((e: unknown) => {
        if (!alive) return;
        setOutputStyles(null);
        setOutputStylesError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [selected, s.outputStyle, s.experimentalOutputStyleUI]);
  // Task 770: open the user-level output-styles directory. The binding picks
  // the highest-priority home convention dir that exists (or creates the
  // .reasonix fallback); the reveal rides the existing RevealPath binding —
  // explorer starts detached, so the click never blocks the UI (FeedbackPanel
  // 172 pattern).
  const openOutputStylesDir = async () => {
    try {
      const dir = await app.GetOutputStylesDir();
      if (dir) await app.RevealPath(dir);
    } catch (e) {
      reportFrontendLog("settings", "output style open dir failed", String(e));
    }
  };
  // 任务 790: resident ACP listener live state for its lab card.
  const [acpListenStatus, setACPListenStatus] = useState<Awaited<ReturnType<typeof app.ACPListenStatus>> | null>(null);
  // 任务 727：心跳会话轮换状态按需加载——只在安全/成本控制卡打开时读一次
  // heartbeat-rotation.json（单一事实源桥接，无第二开关）。
  const [hbRotation, setHbRotation] = useState<Awaited<ReturnType<typeof app.HeartbeatRotationStatus>> | null>(null);
  useEffect(() => {
    if (selected !== "safetyCostControl") return undefined;
    let alive = true;
    void app.HeartbeatRotationStatus()
      .then((view) => {
        if (alive) setHbRotation(view);
      })
      .catch(() => {
        if (alive) setHbRotation(null);
      });
    return () => {
      alive = false;
    };
  }, [selected]);
  useEffect(() => {
    if (selected !== "acpListen") return undefined;
    let alive = true;
    void app.ACPListenStatus()
      .then((st) => {
        if (alive) setACPListenStatus(st);
      })
      .catch(() => {
        if (alive) setACPListenStatus(null);
      });
    return () => {
      alive = false;
    };
  }, [selected, s.experimentalACPListen]);
  // Task 359: lab navigation — collapsible groups, intro banner (collapsed by
  // default), and the "disabled sink" display-order switch. The order switch
  // is a pure display preference (localStorage, no experimental_* chain — it
  // never touches switch semantics or persistence, task 359 / rule 2).
  const [introOpen, setIntroOpen] = useState(false);
  // 任务 1006：实验室快捷批量开关弹窗（范围×行为+重启两选项）。
  const [labBulkOpen, setLabBulkOpen] = useState(false);
  // 任务 764：搜索直达（focusEntry）时目标组直接展开挂载；无直达时维持
  // 359 契约——全部组默认折叠。
  const [expandedGroups, setExpandedGroups] = useState<ReadonlySet<LabGroupKey>>(() => {
    if (!focusEntry) return new Set<LabGroupKey>();
    const group = labEntries(labLayoutResolved.layout).find(({ entry }) => entry.id === focusEntry)?.group;
    return group ? new Set<LabGroupKey>([group]) : new Set<LabGroupKey>();
  });
  const [enabledFirst, setEnabledFirst] = useState<boolean>(
    () => {
      try { return globalThis.localStorage?.getItem("reasonix.lab.enabledFirst") === "1"; } catch { return false; }
    },
  );
  const toggleGroup = (key: LabGroupKey) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  };
  const jumpToGroup = (key: LabGroupKey) => {
    setExpandedGroups((prev) => (prev.has(key) ? prev : new Set(prev).add(key)));
    requestAnimationFrame(() => {
      document.getElementById(`lab-group-${key}`)?.scrollIntoView({ block: "start", behavior: "smooth" });
    });
  };
  // 任务 764：搜索直达——设置导航搜索命中实验室条目后点进来，预选该条目、
  // 确保所在组展开并把 rail 行滚入视野；右侧 pane 随 selected 同步出卡。
  // 无 focusEntry 时 effect 空转（默认选中仍是 autopilot，359 契约不变）。
  useEffect(() => {
    if (!focusEntry) return;
    setSelected(focusEntry);
    const group = labEntries(labLayoutResolved.layout).find(({ entry }) => entry.id === focusEntry)?.group;
    if (!group) return;
    setExpandedGroups((prev) => (prev.has(group) ? prev : new Set(prev).add(group)));
    requestAnimationFrame(() => {
      document.getElementById(`lab-entry-${focusEntry}`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    });
  }, [focusEntry, labLayoutResolved]);
  const setEnabledFirstPref = (on: boolean) => {
    setEnabledFirst(on);
    try { globalThis.localStorage?.setItem("reasonix.lab.enabledFirst", on ? "1" : "0"); } catch { /* private mode: session-only */ }
  };
  // 任务 1005：开发者模式——单项解锁，作用域仅稳定档（防呆不防刁；不做全局
  // dev mode）。默认关；开时稳定档开关解锁可关（卡内警示不保证兼容性和稳定
  // 性）。页面偏好存 localStorage（同 enabledFirst：零配置键、零 experimental_*
  // 链，纯前端 UI 门禁）。
  const [labDevMode, setLabDevMode] = useState<boolean>(
    () => {
      try { return globalThis.localStorage?.getItem("reasonix.lab.devMode") === "1"; } catch { return false; }
    },
  );
  const setLabDevModePref = (on: boolean) => {
    setLabDevMode(on);
    try { globalThis.localStorage?.setItem("reasonix.lab.devMode", on ? "1" : "0"); } catch { /* private mode: session-only */ }
  };
  // 稳定档判定/锁定（档位数据取自 562 注册表——Go labFeatureTiers 镜像）：
  // 锁定 = 稳定档 且 开发者模式关；解锁警示 = 稳定档 且 开发者模式开。
  const labStableLocked = (id: TierFeatureId) => EXPERIMENT_FEATURE_TIERS[id] === "stable" && !labDevMode;
  const labStableUnlocked = (id: TierFeatureId) => EXPERIMENT_FEATURE_TIERS[id] === "stable" && labDevMode;
  // 开发者模式 toggle 旁小字的 N：当前处于关态的稳定档开关数（从 settings
  // 状态直接算；批1 四项 = autopilot / sessionCollab / fullAccess / runtimeReuse）。
  const labStableOffCount = (
    [
      ["autopilot", Boolean(s.autopilot)],
      ["sessionCollab", Boolean(s.experimentalSessionCollab)],
      ["fullAccess", Boolean(s.experimentalFullAccess)],
      ["runtimeReuse", Boolean(s.experimentalRuntimeReuse)],
    ] as const
  ).filter(([id, on]) => EXPERIMENT_FEATURE_TIERS[id] === "stable" && !on).length;
  const t = useT();
  // Task 155: the configured store mode, normalized so every stage key stays a
  // literal (the dictionaries are typed by DictKey).
  const storageMode = asSessionStorageMode(s.sessionStorage);
  // 任务 805: a running migration locks the stage buttons (the stores must not
  // change sides mid-backfill); the section reports its phase up.
  const [migrationRunning, setMigrationRunning] = useState(false);
  // Task 140: the rail shows backend snapshot state; the sidebar buttons read
  // module state. Sync them whenever the settings view reloads so 「开」 without
  // a re-toggle still reveals the rail buttons.
  useEffect(() => {
    setSessionMonitorEnabled(Boolean(s.experimentalSessionMonitor));
    setFeedbackEnabled(Boolean(s.experimentalFeedback));
  }, [s.experimentalSessionMonitor, s.experimentalFeedback]);

  // A saved mode change only takes full effect after a restart, and the backend
  // knows that: the view carries the mode this process actually started with.
  useEffect(() => {
    if (s.sessionStorageRestartPending) setRestartNeeded(true);
  }, [s.sessionStorageRestartPending]);

  const reloadSessionCollabRoster = useCallback(async () => {
    try {
      const rows = await app.ListAddressableSessions();
      setSessionCollabRoster(Array.isArray(rows) ? rows : []);
    } catch {
      setSessionCollabRoster([]);
    }
  }, []);

  // 任务 722/724：组顺序即布局文档顺序（yaml groups 数组序 = rail 渲染序）。
  const labGroups = useMemo(
    () => labLayoutResolved.layout.groups.map((group) => ({ key: group.key, labelKey: group.labelKey })),
    [labLayoutResolved],
  );
  // Render table（任务 561 惯例延续）：从布局文档派生——每项自带灯读数
  // （labLightKeys 谓词）与卡内各自的 setter 接线（81/123 丢存铁律）。
  // 分组计数（626 单源）与 rail 徽章（labEntryBadgeTier）都吃同一份布局。
  const features = useMemo(
    () =>
      labEntries(labLayoutResolved.layout).map(({ entry, group }) => ({
        id: entry.id as ExperimentFeatureId,
        group,
        label: t(entry.labelKey),
        on: labEntryLightOn(entry, s),
        // 任务 774：卡头徽章聚合为单枚最高档（max(卡, members)，推荐>可选
        // >未稳定>已退役）；成员徽章只在卡内详情行可见，不再上卡头。
        badgeTier: labEntryBadgeTier(labLayoutResolved.layout, entry.id),
        // 任务 775：参数卡无开关——rail 呈现（空心态点/不参与 --off 变灰）
        // 与 pane 分派（通用参数渲染）都以 kind 为准。
        kind: entry.kind ?? ("feature" as const),
      })),
    [labLayoutResolved, s, t],
  );

  // 任务 626: single-source per-group totals — the rail group-header badges
  // read this ONE map (recomputed on every settings update, never hardcoded,
  // task 563 lesson). 任务 764：顶部筛选 chips（原第二读点）已按双入口去重
  // 裁决删除，组头徽章成为唯一读点——单源性质不变，只是读者少了一处。
  const labGroupTotals = useMemo(
    () =>
      Object.fromEntries(
        (LAB_GROUP_KEYS as readonly LabGroupKey[]).map((g) => [g, features.filter((f) => f.group === g).length]),
      ) as Record<LabGroupKey, number>,
    [features],
  );

  // 任务 562: pane-side tier badge — wrap a 表A feature's label with its tier
  // pill. `id` is typed as TierFeatureId, so a badge can never reference a
  // feature outside the 46-item register (preapproveManagedPaths, task-364
  // domain, deliberately shows no badge).
  // 任务 621 (用户口径 2026-10-08): a feature with several setting rows shows
  // its badge EXACTLY ONCE — on the master switch row; sibling switches and
  // dials (时长/间隔/阈值) never carry one. The task621 test pins the count
  // per id (labLabel("id") appears exactly once, or the usage card covers it).
  // 任务 856 ③：协作中心开关的前置依赖状态（LAB_FEATURE_DEPS 唯一消费方
  // ——sessionCollab 关 = collabHub 开关禁用 + 提示；关前置时下方联动关）。
  const sessionCollabOn = Boolean(s.experimentalSessionCollab);
  const labLabel = (id: TierFeatureId, label: string) => (
    <>
      {label}
      <TierBadge tier={EXPERIMENT_FEATURE_TIERS[id]} translator={t} />
    </>
  );

  // 任务 563: the picks wall's live states — read from the SAME `features`
  // render table the rail reads (recomputed on every settings update, never
  // hardcoded). The four wall picks folded into 561 merged cards read their
  // OWN keys, never the merged light: the merged `on` is an OR of siblings,
  // so it would leak a sibling's state into this member's card
  // (restartUpdate ≠ feedback, budgetControl ≠ cacheTuning, …).
  const labWallOnById = useMemo(() => {
    const map: Record<string, boolean> = {};
    for (const pick of LAB_WALL_PICKS) {
      const direct = features.find((f) => f.id === pick);
      if (direct) { map[pick] = direct.on; continue; }
      switch (pick) {
        case "budgetControl":
          map[pick] = Boolean(s.experimentalContextBudget) || Boolean(s.experimentalResearchBudget);
          break;
        case "compressOpt":
          map[pick] = Boolean(s.experimentalProactiveCompact) || Boolean(s.experimentalColdCacheCompact);
          break;
        case "restartUpdate":
          map[pick] = Boolean(s.experimentalRestartUpdate);
          break;
        case "subagentPanel":
          map[pick] = Boolean(s.experimentalSubagentPanel);
          break;
        case "messageMerge":
          // 任务 722：messageMerge 并入 safetyCostControl 卡后不再是 rail
          // 条目——墙灯读它自己的键（563 教训：成员永远读自己的键，不读卡灯）。
          map[pick] = (s.collabInboxMerge || "off") !== "off" || Boolean(s.collabGuidanceMerge);
          break;
        case "tabCompress":
          // 任务 742：自适应压缩并入 tabManagement 合并卡后不再是 rail
          // 条目——墙灯同 563 教训读它自己的键，不读合并卡灯。
          map[pick] = Boolean(s.experimentalTabCompress);
          break;
        case "promptHistoryPicker":
          // 任务 782：历史提问选择器并入 searchHistory 合并卡后不再是 rail
          // 条目——墙灯同 563 教训读它自己的键，不读合并卡灯。
          map[pick] = Boolean(s.experimentalPromptHistoryPicker);
          break;
        case "splitView":
          // 任务 782：分栏并入 viewLayout 合并卡后不再是 rail 条目——墙灯
          // 读它自己的键。
          map[pick] = Boolean(s.experimentalSplitView);
          break;
        case "selectionActions":
          // 任务 782：选区快捷操作并入 inputSelection 合并卡后不再是 rail
          // 条目——墙灯读它自己的键。
          map[pick] = Boolean(s.experimentalSelectionActions);
          break;
        default:
          map[pick] = false;
      }
    }
    return map;
  }, [features, s]);

  return (
    <LabWallOnContext.Provider value={labWallOnById}>
    <SettingsPageShell s={s} tab="experimental" busy={busy} apply={apply}>
      <div className="experimental-layout">
        <div className="experimental-lab">
          {/* Task 359 (Q1-A) upgraded by task 379 (0929): the entry now opens
              a LARGE modal with a card wall instead of the inline banner — the
              narrow-column form is retired. Button stays here (not in the
              panel), and every dialog control lives in the dialog layer, so
              the task-282 "pure display, no controls in the component"
              contract remains untouched. */}
          <button
            type="button"
            className={`experimental-lab__intro-toggle${introOpen ? " experimental-lab__intro-toggle--open" : ""}`}
            aria-expanded={introOpen}
            aria-haspopup="dialog"
            onClick={() => setIntroOpen((v) => !v)}
          >
            <span className="experimental-lab__intro-chevron" aria-hidden="true">{introOpen ? "▾" : "▸"}</span>
            <span>{t("settings.forkFeaturesIntro.title")}</span>
          </button>
          {introOpen ? <ForkFeaturesIntroDialog t={t} onClose={() => setIntroOpen(false)} /> : null}
          {/* Task 359 (③): display-order switch — enabled items first, disabled
              sunk to the end of each group. Display order only: no config,
              no semantics, no experimental_* chain; preference persists in
              localStorage (page preference), toggle restores source order. */}
          <div className="experimental-lab__order-row">
            <button
              type="button"
              role="switch"
              aria-checked={enabledFirst}
              className={`experimental-lab__order-toggle${enabledFirst ? " experimental-lab__order-toggle--on" : ""}`}
              onClick={() => setEnabledFirstPref(!enabledFirst)}
            >
              <span className="experimental-lab__order-dot" aria-hidden="true" />
              {/* Task 361: the label itself must show the CURRENT state — a
                  static caption read as "on" even when the switch was off.
                  Two keys (on/off), both languages, switched by aria state. */}
              <span>{t(enabledFirst ? "settings.lab.enabledFirst.on" : "settings.lab.enabledFirst.off")}</span>
            </button>
            {/* 任务 1006：快捷批量开关入口（与显示顺序开关同排）——点开
                范围×行为弹窗，确认后写配置并给重启两选项。 */}
            <button
              type="button"
              className="experimental-lab__bulk-toggle"
              aria-haspopup="dialog"
              onClick={() => setLabBulkOpen(true)}
            >
              {t("settings.labBulk.button")}
            </button>
          </div>
          {/* 任务 1005：开发者模式（作用域仅稳定档）——默认关，标签显示当前态
              （361 纪律，同上一行的 enabledFirst 开关）；旁侧小字只在开发者
              模式开且确有手动关闭的稳定能力时出现（只提示不弹窗，普通用户
              零打扰）。 */}
          <div className="experimental-lab__order-row">
            <button
              type="button"
              role="switch"
              aria-checked={labDevMode}
              className={`experimental-lab__order-toggle${labDevMode ? " experimental-lab__order-toggle--on" : ""}`}
              onClick={() => setLabDevModePref(!labDevMode)}
            >
              <span className="experimental-lab__order-dot" aria-hidden="true" />
              <span>{t(labDevMode ? "settings.lab.devMode.on" : "settings.lab.devMode.off")}</span>
            </button>
            {labDevMode && labStableOffCount > 0 ? (
              <span className="experimental-lab__devmode-hint">{t("settings.lab.devMode.disabledCount", { n: labStableOffCount })}</span>
            ) : null}
          </div>
          {/* 任务 764（用户 0325 裁决）：顶部带计数筛选 chips 已删除——它与
              下方 rail 内的分组目录 toc 视觉几乎相同（同款 999px 胶囊），目标
              层面重复（都是「去某个组」），且计数与 rail 组头徽章重复（626
              单源）。历史注（359→2331 行）：本页此前已删过一次双入口，速览
              卡引入的第二组同样按此口径去重。保留 toc（跳转+自动展开，导航
              能力更强）；组仍可经组标题折叠，聚焦能力不丢。 */}
          <nav className="experimental-rail" aria-label={t("settings.experimentalIntro")}>
            {/* Task 359 (Q2③): sticky group directory — jump scrolls the rail
                to the group and auto-expands it (user-annotated requirement). */}
            <div className="experimental-rail__toc" role="navigation" aria-label={t("settings.lab.tocLabel")}>
              {labGroups.map((g) => (
                <button
                  key={g.key}
                  type="button"
                  className="experimental-rail__toc-item"
                  onClick={() => jumpToGroup(g.key)}
                >
                  {t(g.labelKey)}
                </button>
              ))}
            </div>
            {labGroups.map((g) => {
              const filtered = features.filter((f) => f.group === g.key);
              if (filtered.length === 0) return null;
              // Task 359 (③): display order only — enabled items first when the
              // top switch is on, source order when off. Stable sort keeps the
              // config/persistence untouched (rule 2: off = zero behavior).
              const items = enabledFirst ? [...filtered].sort((a, b) => Number(b.on) - Number(a.on)) : filtered;
              const onCount = filtered.filter((f) => f.on).length;
              const expanded = expandedGroups.has(g.key);
              return (
                <div key={g.key} className={`experimental-lab__group${expanded ? " experimental-lab__group--open" : ""}`}>
                  <button
                    type="button"
                    id={`lab-group-${g.key}`}
                    className="experimental-lab__group-title experimental-lab__group-toggle"
                    aria-expanded={expanded}
                    onClick={() => toggleGroup(g.key)}
                  >
                    <span className="experimental-lab__group-arrow" aria-hidden="true">{expanded ? "▾" : "▸"}</span>
                    <span className="experimental-lab__group-name">{t(g.labelKey)}</span>
                    {/* 任务 626: total-items badge rides BEFORE the on-count
                        badge — totals share the chips' labGroupTotals source;
                        the on-count badge stays (task 359 contract + test). */}
                    <span className="experimental-lab__group-count experimental-lab__group-count--total">{t("settings.labGroup.itemCount", { n: labGroupTotals[g.key] })}</span>
                    <span className="experimental-lab__group-count">{t("settings.labGroup.onCount", { n: onCount })}</span>
                  </button>
                  {expanded ? items.map((feature) => (
                    <LabRailItem
                      key={feature.id}
                      feature={feature}
                      active={selected === feature.id}
                      onSelect={setSelected}
                      translator={t}
                    />
                  )) : null}
                </div>
              );
            })}
          </nav>
          {/* Task 359 (Q1-A): the intro section moved to the top banner above
              the chips — bottom entry removed to avoid a double entry point. */}
        </div>
        <div className="experimental-pane">
          {restartNeeded ? (
            <div className="banner settings-restart-banner" role="status">
              <span>{t("settings.restartRequired")}</span>
              <button
                type="button"
                className="btn btn--primary btn--sm settings-restart-banner__action"
                disabled={busy}
                onClick={() => void apply(() => app.RestartDesktop())}
              >
                {t("settings.restartNow")}
              </button>
            </div>
          ) : null}
          {fullAccessDialog}
          {/* 任务 1006：批量开关弹窗（portal 到 body，529 教训——rail 在滚动
              容器内，portal 才不被裁剪）。 */}
          {labBulkOpen ? <LabBulkToggleDialog t={t} onClose={() => setLabBulkOpen(false)} /> : null}
          {/* 任务 775：kind:param 参数卡通用 pane——布局里已登记的参数卡走
              这份数据渲染（字段形态按 field.input 分派，值读写走注册表）；
              未登记参数仍走下方各硬编码分支兜底（渐进迁移，登记一张删一张）。 */}
          {selectedEntry?.kind === "param" && selected !== "logManagement" && (
            <LabParamFieldsCard entry={selectedEntry} s={s} busy={busy} apply={apply} />
          )}
          {/* tl 1097：日志管理（可观测性组参数卡）——专用 pane，比通用
              参数渲染器多三样：区块说明固定行、「当前占用」实时统计行、
              「打开日志目录」按钮 + 「永久+无限制」警示行（通用渲染器
              无跨字段联动，无法承载 7.3 三条边界语义）。两个 segment
              档位本身仍走 labParamFields 注册表读写（值语义单源）。 */}
          {selected === "logManagement" && (
            <LogManagementLabPane s={s} busy={busy} apply={apply} />
          )}
          {/* tl1287：dagMigration 独立 pane 退役——动作卡转正进会话存储卡
              （sessionStore 分支内），不再占实验室 rail。 */}
          {selected === "fullAccess" && (
            <>
              <SettingsField label={labLabel("fullAccess", t("settings.fullAccess"))} hint={t("settings.fullAccessHint")} icon={<ShieldCheck size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalFullAccess) === on ? " set-seg__btn--on" : ""}`}
                      // 任务 1005：稳定档开关在开发者模式关时禁用（默认态只读）。
                      disabled={busy || labStableLocked("fullAccess")}
                      onClick={() => void (async () => {
                        // One-shot danger gate on the way in (task 257): the
                        // confirmation spells out that writes need no approval
                        // afterwards. Turning it off restores the old chain
                        // without asking.
                        if (on) {
                          const ok = await confirmFullAccess({
                            title: t("settings.fullAccessConfirmTitle"),
                            message: t("settings.fullAccessConfirmMessage"),
                            confirmLabel: t("settings.fullAccessConfirm"),
                            cancelLabel: t("common.cancel"),
                            tone: "danger",
                          });
                          if (!ok) return;
                        }
                        await apply(() => app.SetExperimentalFullAccess(on));
                        // Boot snapshot: the writable-root set and the bash spec
                        // are built at startup, so the flip lands on restart.
                        setRestartNeeded(true);
                      })()}
                    >
                      {t(on ? "settings.fullAccess.on" : "settings.fullAccess.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1005：稳定档禁用态两分支解释行（值开=默认开启说明 / 值关=
                  曾手动关闭说明）；开发者模式解锁态改显不保证兼容性/稳定性警示。 */}
              {labStableLocked("fullAccess") ? (
                <p className="settings-field__hint-line">{t(Boolean(s.experimentalFullAccess) ? "settings.lab.stable.lockedOn" : "settings.lab.stable.lockedOff")}</p>
              ) : labStableUnlocked("fullAccess") ? (
                <p className="settings-field__hint-line experimental-lab__stable-warning">{t("settings.lab.stable.devWarning")}</p>
              ) : null}
            </>
          )}
          {/* Task 561 M6 更新/反馈族: the restart-and-update family and the feedback
              family fold into ONE card; every switch below keeps its own
              setter and its own config key (81/123 lost-save rule; task
              427/318.5 merge precedent). */}
          {selected === "updateFeedback" && (
            <>
              {/* Task 670: the fork first-launch notice switch. Default ON per
                  user ruling (pure UI notice, 铁律 2 not applicable); a live
                  config flip, no restart. Re-enabling also clears the dialog's
                  「下次不提醒」 mute, so ON means the notice really comes back. */}
              <SettingsField label={t("settings.forkNotice")} hint={t("settings.forkNoticeHint")} icon={<RefreshCw size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.forkNotice) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetDesktopForkNotice(on))}
                    >
                      {t(on ? "settings.forkNotice.on" : "settings.forkNotice.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 971：restartUpdate 行迁至「工具优化」聚合页（toolOptimizations
                  pane）——键/setter 零变化，仅 UI 落点迁移。 */}
              <>
                {/*
                 * Task 381: the fast-switch staging directory. Empty = the
                 * historical default under the install root; the restart-and-
                 * update path names the exact directory in its error when the
                 * artifacts are missing, so a wrong path is never a silent
                 * failure. The reset button clears the override back to the
                 * default semantics. Rendered as a sibling block (not nested)
                 * so it gets its own full-width row.
                 */}
                <SettingsField label={t("settings.stagingDir")} hint={t("settings.stagingDirHint")} icon={<RefreshCw size={18} />}>
                  <div style={{ display: "flex", gap: 8, alignItems: "center", width: "100%" }}>
                    <input
                      key={`staging-${s.stagingDir ?? ""}`}
                      className="set-input"
                      type="text"
                      defaultValue={String(s.stagingDir ?? "")}
                      disabled={busy}
                      placeholder="<installRoot>/staging"
                      aria-label={t("settings.stagingDir")}
                      onBlur={(e) => void apply(() => app.SetStagingDir(e.target.value))}
                    />
                    <button
                      className="set-seg__btn"
                      type="button"
                      disabled={busy}
                      onClick={() => void apply(() => app.SetStagingDir(""))}
                    >
                      {t("settings.stagingDirReset")}
                    </button>
                  </div>
                </SettingsField>
              </>
              {/* Task 254: the agent-facing half of the restart-and-update lab. The
                  restart_update tool registers at boot only, so saving sets
                  restartNeeded — the same signal that raises the restart banner. */}
              <SettingsField label={t("settings.autonomousUpdate")} hint={t("settings.autonomousUpdateHint")} icon={<RefreshCw size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalAutonomousUpdate) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalAutonomousUpdate(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.autonomousUpdate.on" : "settings.autonomousUpdate.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 254 (user ruling): the auto-resume scope dial. Unlike the
                  tool toggle this takes effect live — execute reads it when it
                  fires and restore reads it when the new process boots — so no
                  restartNeeded here. */}
              <SettingsField label={t("settings.autonomousUpdateResume")} hint={t("settings.autonomousUpdateResumeHint")} icon={<RefreshCw size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {(["off", "goal_autopilot", "all"] as const).map((mode) => (
                    <button
                      key={mode}
                      className={`set-seg__btn${(s.autonomousUpdateResume ?? "goal_autopilot") === mode ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetAutonomousUpdateResume(mode))}
                    >
                      {t(`settings.autonomousUpdateResume.${mode}`)}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 277: update-complete chime. The one-shot gate lives in the
                  frontend (last-chimed version), so the flip applies immediately. */}
              <SettingsField label={t("settings.updateChime")} hint={t("settings.updateChimeHint")} icon={<Volume2 size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.updateChime) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetUpdateChime(on))}
                    >
                      {t(on ? "settings.updateChime.on" : "settings.updateChime.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 512: chime melody dial under the switch card. Mario lists
                  only in local builds — the Nintendo-owned asset is compiled out
                  of public builds, which also fall back to Nokia at playback time. */}
              <SettingsField label={t("settings.updateChimeTune")} hint={t("settings.updateChimeTuneHint")} icon={<Volume2 size={18} />}>
                <SettingsSelect
                  className="mem-select"
                  value={chimeLocalAssetsEnabled() ? normalizeUpdateChimeTune(s.updateChimeTune) : "nokia"}
                  disabled={busy}
                  onValueChange={(value) => void apply(() => app.SetUpdateChimeTune(value))}
                >
                  {(chimeLocalAssetsEnabled() ? UPDATE_CHIME_TUNES : ["nokia"] as const).map((tune) => (
                    <option key={tune} value={tune}>{t(`settings.updateChimeTune.${tune}` as DictKey)}</option>
                  ))}
                </SettingsSelect>
              </SettingsField>
              <>
                <SettingsField label={labLabel("feedback", t("settings.feedback"))} hint={t("settings.feedbackHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalFeedback) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          await app.SetExperimentalFeedback(on);
                          setFeedbackEnabled(on);
                        })}
                      >
                        {t(on ? "settings.feedback.on" : "settings.feedback.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* Task 172: the touchpoint sub-switch lives under the feedback
                    entry. The parent switch wins at runtime (config gate); the
                    agent reads the dial at boot, so a change raises the restart
                    banner instead of flipping behavior mid-session. */}
                <SettingsField label={t("settings.feedbackNudge")} hint={t("settings.feedbackNudgeHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalFeedbackNudge) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          await app.SetExperimentalFeedbackNudge(on);
                          setRestartNeeded(true);
                        })}
                      >
                        {t(on ? "settings.feedbackNudge.on" : "settings.feedbackNudge.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.feedbackOpen")} hint={t("settings.feedbackOpenHint")} icon={<Sparkles size={18} />}>
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    disabled={busy || !Boolean(s.experimentalFeedback)}
                    onClick={() => setFeedbackOpen(true)}
                  >
                    {t("settings.feedbackOpenAction")}
                  </button>
                </SettingsField>
              </>
            </>
          )}
                    {/* Task 318.5: one entry hosts both monitors. The two switches stay
              fully independent — enabling one never flips the other. */}
          {selected === "monitoring" && (
            <>
              <SettingsField label={labLabel("monitoring", t("settings.sessionMonitor"))} hint={t("settings.sessionMonitorHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSessionMonitor) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSessionMonitor(on);
                        setSessionMonitorEnabled(on);
                      })}
                    >
                      {t(on ? "settings.sessionMonitor.on" : "settings.sessionMonitor.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.sessionMonitorOpen")} hint={t("settings.sessionMonitorOpenHint")} icon={<Sparkles size={18} />}>
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  disabled={busy || !Boolean(s.experimentalSessionMonitor)}
                  onClick={() => setSessionMonitorOpen(true)}
                >
                  {t("settings.sessionMonitorOpenAction")}
                </button>
              </SettingsField>
              {/* 任务 621：perfMonitor 是 monitoring 特性的附属开关——徽章只挂
                  主控开关行（sessionMonitor），附属行不再重复挂。 */}
              <SettingsField label={t("settings.perfMonitor")} hint={t("settings.perfMonitorHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalPerfMonitor) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalPerfMonitor(on);
                      })}
                    >
                      {t(on ? "settings.perfMonitorMode.on" : "settings.perfMonitorMode.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.perfMonitor.interval")} hint={t("settings.perfMonitor.intervalHint")} icon={<Sparkles size={18} />}>
                <input
                  type="number"
                  min={1}
                  max={300}
                  value={perfInterval}
                  disabled={busy}
                  onChange={(event) => setPerfInterval(Number(event.target.value))}
                  onBlur={() => void apply(async () => {
                    await app.SetPerfMonitorIntervalSeconds(perfInterval);
                  })}
                />
              </SettingsField>
              <SettingsField label={t("settings.perfMonitor.heap")} hint={t("settings.perfMonitor.heapHint")} icon={<Sparkles size={18} />}>
                <button
                  type="button"
                  className="btn btn--secondary btn--sm"
                  disabled={busy}
                  onClick={() => void apply(async () => {
                    await app.SaveHeapProfile();
                  })}
                >
                  {t("settings.perfMonitor.heapAction")}
                </button>
              </SettingsField>
              {/* Task 501: threshold-triggered heap snapshot — an experimental
                  switch in the same lab section, off by default. */}
              <SettingsField label={t("settings.perfMonitor.heapHigh")} hint={t("settings.perfMonitor.heapHighHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalHeapHighProfile) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalHeapHighProfile(on);
                      })}
                    >
                      {t(on ? "settings.perfMonitorMode.on" : "settings.perfMonitorMode.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 528: the trigger threshold gets a real input (MiB). It only
                  matters while the 501 switch is on, so the box stays disabled
                  otherwise (zero behaviour when off); the config layer clamps
                  into 1024..131072, and a change needs a restart to arm. */}
              <SettingsField label={t("settings.perfMonitor.heapHighThreshold")} hint={t("settings.perfMonitor.heapHighThresholdHint")} icon={<Sparkles size={18} />}>
                <input
                  type="number"
                  min={1024}
                  max={131072}
                  step={1024}
                  value={perfHeapHighThreshold}
                  disabled={busy || !Boolean(s.experimentalHeapHighProfile)}
                  onChange={(event) => setPerfHeapHighThreshold(Number(event.target.value))}
                  onBlur={() => void apply(async () => {
                    await app.SetPerfMonitorHeapHighThresholdMB(perfHeapHighThreshold);
                  })}
                />
              </SettingsField>
              {/* Task 338: WS series + heap pie, read-only over the existing
                  samples (no new sampling cost; the switch stays authoritative). */}
              <PerfMemorySection busy={busy} apply={apply} />
            </>
          )}
          {/* Task 561 M7 会话存储族: storage-version + event-rotation fold into ONE
              card — each keeps its own setter and config key (81/123
              lost-save rule). */}
          {selected === "sessionStore" && (
            <>
              <SettingsField label={labLabel("sessionStorage", t("settings.sessionStorage"))} hint={t("settings.sessionStorageHint")} icon={<Sparkles size={18} />} stacked className="settings-field--wrapped">
                <SettingsOptions layout="field" className="set-seg">
                  {/* tl1287: v4 退役——卡内不再渲染任何 v4 档按钮（用户拍板
                      20261031）。仍停在 v4 档的用户由下方 stage/risk 行按已存
                      档位如实提示，回去路径是同卡的 v4→v3 迁移器（后端对降档
                      永远放行，点 v3 即降档，916 的「只许逐级升」仅约束升档）。 */}
                  {SESSION_STORAGE_SELECTABLE_MODES.map((mode) => (
                    <button
                      key={mode}
                      className={`set-seg__btn${storageMode === mode ? " set-seg__btn--on" : ""}`}
                      disabled={busy || migrationRunning}
                      onClick={() => void apply(async () => {
                        await app.SetSessionStorage(mode);
                      })}
                    >
                      {t(`settings.sessionStorage.mode.${mode}`)}
                      {/* 805: v3 is the fork's long-term recommendation — the
                          same pill the lab tiers use, no new copy or CSS. */}
                      <TierBadge tier="recommended" translator={t} />
                    </button>
                  ))}
                </SettingsOptions>
                {/* 916: 锁定原因贴着禁用档位渲染；下方 805 迁移区保持可达——
                    带 v4 数据的升级用户打开本卡仍看得到迁移引导。 */}
                <p className="settings-field__hint-line">{t("settings.sessionStorage.locked")}</p>
                <p className="settings-field__hint-line">{t(`settings.sessionStorage.stage.${storageMode}`)}</p>
                <p className="settings-field__hint-line">{t(`settings.sessionStorage.risk.${storageMode}`)}</p>
                {s.sessionStorageRestartPending ? (
                  <p className="settings-field__hint-line">
                    {t("settings.sessionStorage.restartPending", {
                      mode: t(`settings.sessionStorage.mode.${asSessionStorageMode(s.sessionStorageEffective)}`),
                    })}
                  </p>
                ) : null}
                {/* 任务 805: the migration routes (v4 backfill + the 773 v5
                    import) live in the same card, under the stage controls. */}
                <SessionMigrationSection t={t} onRunningChange={setMigrationRunning} />
                {/* tl1287③：DAG 迁移卡转正——自实验室 rail 摘除，动作卡改宿主
                    本卡（一次性收尾：迁完永久无事）。两步确认（扫描→执行）+
                    事件进度 + 逐会话读数原样保留；不再有任何实验室侧开关或
                    rail 入口。 */}
                <DagMigrationSection t={t} busy={busy} apply={apply} />
              </SettingsField>
              <SettingsField label={labLabel("eventsRotation", t("settings.eventsRotation"))} hint={t("settings.eventsRotationHint")} icon={<Sparkles size={18} />} stacked className="settings-field--wrapped">
                {/* The backend normalizes the stored mode to off|manual|auto
                    (config helper falls back to manual), so the cast cannot name
                    a value the locales lack. */}
                <SessionEventsPanel mode={(s.eventsAutoRotation ?? "manual") as RotationMode} busy={busy} apply={apply} />
              </SettingsField>
            </>
          )}
          {/* 任务 782：视图布局（合并卡，742 tabManagement 先例）——分栏 +
              轨迹视图条目级合并。两行各读各的键、各写各的 setter（81/123
              丢存铁律）；行级徽章经 labLabel 保留，rail 卡头聚合徽章取成员
              最高档（可选）。已存偏好零迁移：两键原样保留。 */}
          {selected === "viewLayout" && (
            <>
              <SettingsField label={labLabel("viewLayout", t("settings.viewLayout"))} hint={t("settings.viewLayoutHint")} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              <SettingsField label={labLabel("splitView", t("settings.splitView"))} hint={t("settings.splitViewHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSplitView) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSplitView(on);
                        setSplitViewEnabled(on);
                      })}
                    >
                      {t(on ? "settings.splitView.on" : "settings.splitView.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("trajectoryView", t("settings.trajectoryView"))} hint={t("settings.trajectoryViewHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalTrajectoryView) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalTrajectoryView(on);
                      })}
                    >
                      {t(on ? "settings.trajectoryView.on" : "settings.trajectoryView.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 826：左右边栏 hover 展开（视图布局卡成员，左右独立两键
                  两行——各读各键各写各 setter；300ms 防误触 + 离开 15s 延时
                  折叠，只回收 hover 自动展开的，手动展开不回收）。纯前端门：
                  保存即生效，免重启；关=不渲染热区不挂事件（铁律 2）。 */}
              <SettingsField label={labLabel("sidebarHoverLeft", t("settings.sidebarHoverLeft"))} hint={t("settings.sidebarHoverLeftHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSidebarHoverLeft) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSidebarHoverLeft(on);
                      })}
                    >
                      {t(on ? "settings.sidebarHoverLeft.on" : "settings.sidebarHoverLeft.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("sidebarHoverRight", t("settings.sidebarHoverRight"))} hint={t("settings.sidebarHoverRightHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSidebarHoverRight) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSidebarHoverRight(on);
                      })}
                    >
                      {t(on ? "settings.sidebarHoverRight.on" : "settings.sidebarHoverRight.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 940：composer 权限选择器形态二选一（视图布局卡成员行，
                  826 先例——读自己的键写自己的 setter）。关（默认）=现分段按钮组
                  零变化；开=zcode 同款下拉框省空间，档位语义同链路。纯前端门：
                  保存即生效，免重启（铁律 2）。 */}
              <SettingsField label={labLabel("approvalSelect", t("settings.approvalSelect"))} hint={t("settings.approvalSelectHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalApprovalSelect) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalApprovalSelect(on);
                      })}
                    >
                      {t(on ? "settings.approvalSelect.on" : "settings.approvalSelect.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 959 ①：对话表格按栏宽自动换行（视图布局卡成员行，
                  940 先例——读自己的键写自己的 setter）。关（默认）=宽表横滚
                  布局零变化；开=单元格随当前栏宽换行。纯前端门：保存即生效，
                  免重启（铁律 2）。 */}
              <SettingsField label={labLabel("tableWrap", t("settings.tableWrap"))} hint={t("settings.tableWrapHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalTableWrap) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalTableWrap(on);
                      })}
                    >
                      {t(on ? "settings.tableWrap.on" : "settings.tableWrap.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 959 ②：composer 发送目标选择器形态二选一（视图布局卡
                  成员行，940 同族——读自己的键写自己的 setter）。关（默认）=
                  三按钮组零变化；开=下拉框省空间，目标语义同链路。纯前端门：
                  保存即生效，免重启（铁律 2）。 */}
              <SettingsField label={labLabel("sendTargetSelect", t("settings.sendTargetSelect"))} hint={t("settings.sendTargetSelectHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSendTargetSelect) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSendTargetSelect(on);
                      })}
                    >
                      {t(on ? "settings.sendTargetSelect.on" : "settings.sendTargetSelect.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* tl 1165：流式滚动平滑跟随（视图布局卡成员行，940/959 同族——
                  读自己的键写自己的 setter）。DSH ScrollFollow 同款四条防线：
                  smooth 跟随+未完成目标不发新请求+手势取消+reduced-motion 退化。
                  关（默认）=每次流式尾部写入瞬移定位逐字节不变、不挂任何额外
                  事件；开=原生平滑 scrollTo。纯前端门：保存即生效，免重启。 */}
              <SettingsField label={labLabel("smoothScrollFollow", t("settings.smoothScrollFollow"))} hint={t("settings.smoothScrollFollowHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSmoothScrollFollow) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalSmoothScrollFollow(on))}
                    >
                      {t(on ? "settings.smoothScrollFollow.on" : "settings.smoothScrollFollow.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1024：composer 草稿优化（改写按钮）——发送左侧新增改写
                  入口，辅助模型改写草稿回填输入框，不自动发送。默认关=无按钮、
                  布局零变化；保存即换态免重启。 */}
              <SettingsField label={labLabel("draftRewrite", t("settings.draftRewrite"))} hint={t("settings.draftRewriteHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalDraftRewrite) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalDraftRewrite(on))}
                    >
                      {t(on ? "settings.draftRewrite.on" : "settings.draftRewrite.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {selected === "collabHub" && (
            <SettingsField
              label={labLabel("collabHub", t("settings.collabHub"))}
              hint={Boolean(s.experimentalSessionCollab) && !sessionCollabOn ? t("settings.collabHubHintDep") : t("settings.collabHubHint")}
              icon={<Sparkles size={18} />}
            >
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalCollabHub) === on ? " set-seg__btn--on" : ""}`}
                    // 任务 856 ③ 前置依赖（demo v2 交互稿）：跨会话协作关闭时
                    // 协作中心开关禁用 + 提示；依赖声明在 LAB_FEATURE_DEPS。
                    disabled={busy || (!on && Boolean(s.experimentalCollabHub) && !sessionCollabOn)}
                    title={!on || !sessionCollabOn ? t("settings.collabHubDepBlocked") : undefined}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalCollabHub(on);
                    })}
                  >
                    {t(on ? "settings.collabHub.on" : "settings.collabHub.off")}
                  </button>
                ))}
              </SettingsOptions>
              {!sessionCollabOn && (
                <div className="set-lab-dep-note" role="note">{t("settings.collabHubDepNote")}</div>
              )}
            </SettingsField>
          )}
          {selected === "todoSidebar" && (
            <>
              <SettingsField label={labLabel("todoSidebar", t("settings.todoSidebar"))} hint={t("settings.todoSidebarHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalTodoSidebar) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // Boot snapshot (task 259): the dock reads the flag once at
                        // startup, so saving raises the restart banner instead of
                        // flipping the layout mid-session.
                        await app.SetExperimentalTodoSidebar(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.todoSidebar.on" : "settings.todoSidebar.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField
                label={t("settings.dockTabVisibility")}
                hint={Boolean(s.experimentalTodoSidebar) ? t("settings.dockTabVisibilityHint") : t("settings.dockTabsDisabledHint")}
                icon={<Sparkles size={18} />}
              >
                <div className="set-gates">
                  {([
                    ["context", "rightDock.overview"],
                    ["files", "workspace.filesTab"],
                    ["changed", "workspace.changedTab"],
                    ["remote", "rightDock.remote"],
                    ["todos", "workspace.todosTab"],
                    ["artifacts", "workspace.artifactsTab"],
                    ["references", "workspace.referencesTab"],
                    // 任务620：子代理标签的显隐从「子代理族」卡移进这面墙，与其余
                    // 侧栏标签同处配置；它另有前置开关「子代理面板」——面板未开启
                    // 时此项置灰不可勾选（依赖联动），下方提示指认要开的开关。
                    ["subagents", "workspace.subagentsTab"],
                  ] as const).map(([tabId, labelKey]) => {
                    // Task 259: the visibility checkboxes live under the todo-sidebar
                    // switch; with it off they stay visible and readable but disabled,
                    // with a hint naming the switch to flip (task 253's lesson).
                    // 任务620：subagents 行还要过「子代理面板」这道前置——两道闸任
                    // 一关闭即置灰，提示行分别指认各自的开关。
                    const locked = busy
                      || !Boolean(s.experimentalTodoSidebar)
                      || (tabId === "subagents" && !Boolean(s.experimentalSubagentPanel));
                    const on = !isDockTabHidden(tabId);
                    // Keep at least one RENDERABLE tab visible (audit-2 minor c):
                    // gated-off tabs — remote with no hosts, overview in creation —
                    // must not satisfy the guard, or the user could hide everything
                    // the dock actually shows and end up with zero tabs.
                    const lastOn = isLastRenderableVisibleTab(tabId, {
                      todoSidebar: Boolean(s.experimentalTodoSidebar),
                      subagentsPanel: Boolean(s.experimentalSubagentPanel),
                      remoteAvailable,
                      creation: desktopLayoutStyle === "creation",
                    });
                    return (
                      <label key={tabId} className={`set-gates__item${locked ? " set-gates__item--locked" : ""}`}>
                        <input
                          type="checkbox"
                          checked={on}
                          disabled={locked || lastOn}
                          onChange={(event) => setDockTabHidden(tabId, !event.target.checked)}
                        />
                        <span className="set-gates__label">{t(labelKey)}</span>
                      </label>
                    );
                  })}
                  {!Boolean(s.experimentalTodoSidebar) && (
                    <div className="set-gates__hint">{t("settings.dockTabsDisabledHint")}</div>
                  )}
                  {!Boolean(s.experimentalSubagentPanel) && (
                    <div className="set-gates__hint">{t("settings.subagentPanelTabDisabledHint")}</div>
                  )}
                </div>
              </SettingsField>
            </>
          )}
          {/* Task 561 M4 子代理族: panel + detail + policy + tps fold into ONE card
              (the members used to sit in ui / automation / observability);
              every switch keeps its own setter (81/123 lost-save rule). */}
          {selected === "subagentSuite" && (
            <>
              <>
                <SettingsField label={labLabel("subagentPanel", t("settings.subagentPanel"))} hint={t("settings.subagentPanelHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalSubagentPanel) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          // Boot snapshot (task 495): the dock tab and the
                          // ended-card collapse read the flag once at startup,
                          // so saving raises the restart banner.
                          await app.SetExperimentalSubagentPanel(on);
                          setRestartNeeded(true);
                        })}
                      >
                        {t(on ? "settings.subagentPanel.on" : "settings.subagentPanel.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* 任务620：「子代理」标签显隐 checkbox 已移入「侧栏增强」卡的
                    「侧栏标签显示」墙（与其余侧栏标签同墙配置、同一套置灰联动），
                    此处不再重复渲染——面板开关本身保留在本卡首位。 */}
              </>
              <SettingsField label={labLabel("subagentDetail", t("settings.subagentDetail"))} hint={t("settings.subagentDetailHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSubagentDetail) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // 任务 507：纯前端门——设置保存后的 onChanged 会重放
                        // 快照（applyLabFlags），面板交互即时换态，无需重启。
                        await app.SetExperimentalSubagentDetail(on);
                      })}
                    >
                      {t(on ? "settings.subagentDetail.on" : "settings.subagentDetail.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("subagentPolicy", t("settings.subagentPolicy"))} hint={t("settings.subagentPolicyHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSubagentPolicy) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalSubagentPolicy(on))}
                    >
                      {t(on ? "settings.subagentPolicy.on" : "settings.subagentPolicy.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("subagentTps", t("settings.subagentTps"))} hint={t("settings.subagentTpsHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSubagentTps) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalSubagentTps(on))}
                    >
                      {t(on ? "settings.subagentTps.on" : "settings.subagentTps.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {selected === "sessionWall" && (
            <SettingsField label={labLabel("sessionWall", t("settings.sessionWall"))} hint={t("settings.sessionWallHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalSessionWall) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      // Boot snapshot (task 505): the palette entry and the
                      // wall read the flag once at startup, so saving raises
                      // the restart banner.
                      await app.SetExperimentalSessionWall(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.sessionWall.on" : "settings.sessionWall.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {/* 任务 686：工作流可视化面板——palette 入口开 DAG 流程图。纯前端
              门（685 引擎未落地，当前为 fixture 预览+演示回放），保存即
              生效免重启（sessionWall 的 boot-snapshot 语义不同）。 */}
          {selected === "workflowPanel" && (
            <SettingsField label={labLabel("workflowPanel", t("settings.workflowPanel"))} hint={t("settings.workflowPanelHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalWorkflowPanel) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      // Call-time gate（742/826 先例）：palette 条目与面板挂载
                      // 每次渲染现读 labFlags，保存后立即生效，无需重启。
                      await app.SetExperimentalWorkflowPanel(on);
                    })}
                  >
                    {t(on ? "settings.workflowPanel.on" : "settings.workflowPanel.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {/* tl 1132：启动与加载（合并卡，742/782 条目级合并先例）——首项=
              启动遮罩（DSH 式启动画面：logo+副文案+点动画，覆盖主区待核心
              UI 挂载完成后淡出）；为 tl 1133 后续启动优化开关预留成员位。
              默认关（用户 2026-10-24 steer：做成实验特性允许开关方便 A/B
              对比）=splash 不渲染、启动行为与引入前一致（铁律 2）；纯前端
              门，设置保存后的 onChanged 重放快照（applyLabFlags）即换态。 */}
          {selected === "startupLoading" && (
            <>
              <SettingsField label={labLabel("startupLoading", t("settings.startupLoading"))} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              <SettingsField label={labLabel("startupSplash", t("settings.startupSplash"))} hint={t("settings.startupSplashHint")} icon={<Monitor size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalStartupSplash) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // tl 1132：纯前端门——设置保存后的 onChanged 会重放
                        // 快照（applyLabFlags），开关即时生效，无需重启。
                        await app.SetExperimentalStartupSplash(on);
                      })}
                    >
                      {t(on ? "settings.startupSplash.on" : "settings.startupSplash.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {/* 任务 742：标签页管理（合并卡）——506 标签栏自适应压缩 + 651
              标签权限指示的条目级合并。两行各读各的键、各写各的 setter
              （81/123 丢存铁律）；行级徽章保留（621 每特性一枚），容器卡
              徽章挂在主标签上。已存偏好零迁移：两键原样保留。 */}
          {selected === "tabManagement" && (
            <>
              <SettingsField label={labLabel("tabManagement", t("settings.tabManagement"))} hint={t("settings.tabManagementHint")} icon={<Sparkles size={18} />} stacked>
                <p className="settings-field__hint-line">{t("settings.tabManagement.subHint")}</p>
              </SettingsField>
              <SettingsField label={labLabel("tabCompress", t("settings.tabCompress"))} hint={t("settings.tabCompressHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalTabCompress) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // 任务 506：纯前端门——设置保存后的 onChanged 会重放
                        // 快照（applyLabFlags），标签栏即时换档，无需重启。
                        await app.SetExperimentalTabCompress(on);
                      })}
                    >
                      {t(on ? "settings.tabCompress.on" : "settings.tabCompress.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("tabModeTint", t("settings.tabModeTint"))} hint={t("settings.tabModeTintHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {(["badge", "off", "background"] as const).map((mode) => (
                    <button
                      key={mode}
                      className={`set-seg__btn${(s.tabPermissionIndicator ?? "badge") === mode ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // 任务 651：纯前端门——设置保存后的 onChanged 会重放
                        // 快照（applyTabPermissionIndicator），标签栏即时换档，
                        // 无需重启。
                        await app.SetTabPermissionIndicator(mode);
                      })}
                    >
                      {t(`settings.tabModeTint.${mode}`)}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {/* 任务 742：心跳任务后台化（默认关=742 前行为逐位不变）。开启后
              心跳触发不进标签栏——Go 侧复用 264 detached 语义，提交成功后
              park；下一次运行经 prefer-live 打开自动提升回条。 */}
          {/* 任务 961：智能自动化（合并卡，742/782 条目级合并先例）——梦境整理 +
              决策模式提取 + 心跳任务后台化三散条并卡。卡内三行各读各的键、
              各写各的 setter；行级徽章经 labLabel 保留，rail 卡头聚合徽章取
              成员最高档（可选）。已存偏好零迁移：三键原样保留。
              任务 1099（首期试点）：卡内子项按功能族聚合成 LabSectionCard
              容器——梦境族 / 决策模式族 / 后台与续跑族三张分组卡，卡片边框
              即族边界（替代细分割线的弱分组）。纯视觉分组：各成员行仍读
              自己的键、写自己的 setter，徽章语义不变（621 成员徽章留行、
              774 rail 聚合徽章不动）。 */}
          {selected === "smartAutomation" && (
            <>
              <SettingsField label={labLabel("smartAutomation", t("settings.smartAutomation"))} hint={t("settings.smartAutomationHint")} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              {/* 族 1：梦境——整理开关 + 创建自动任务（任务书 1.3 红框 1）。 */}
              <LabSectionCard title={t("settings.lab.family.dream")} hint={t("settings.lab.family.dreamHint")}>
                <SettingsField label={labLabel("dream", t("settings.dream"))} hint={t("settings.dreamHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalDream) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalDream(on))}
                      >
                        {t(on ? "settings.dream.on" : "settings.dream.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.dreamCreateTask")} hint={t("settings.dreamCreateTaskHint")} icon={<Sparkles size={18} />}>
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    disabled={busy || !Boolean(s.experimentalDream)}
                    onClick={() => void apply(async () => {
                      const created = await app.CreateDreamHeartbeatTask();
                      if (created) setDreamTaskCreated(true);
                    })}
                  >
                    {t("settings.dreamCreateTaskAction")}
                  </button>
                </SettingsField>
                {dreamTaskCreated ? (
                  <div className="banner settings-restart-banner" role="status">
                    <span>{t("settings.dreamCreateTaskDone")}</span>
                  </div>
                ) : null}
              </LabSectionCard>
              {/* 族 2：决策模式——提取开关 + 创建自动任务 + 阈值 + 文件位置 +
                  跳过人工批准（任务书 1.3 红框 2）。 */}
              <LabSectionCard title={t("settings.lab.family.decisionPatterns")} hint={t("settings.lab.family.decisionPatternsHint")}>
                <SettingsField label={labLabel("decisionPatterns", t("settings.decisionPatterns"))} hint={t("settings.decisionPatternsHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalDecisionPatterns) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalDecisionPatterns(on))}
                      >
                        {t(on ? "settings.decisionPatterns.on" : "settings.decisionPatterns.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.decisionPatternsCreateTask")} hint={t("settings.decisionPatternsCreateTaskHint")} icon={<Sparkles size={18} />}>
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    disabled={busy || !Boolean(s.experimentalDecisionPatterns)}
                    onClick={() => void apply(async () => {
                      const created = await app.CreateDecisionPatternHeartbeatTask();
                      if (created) setDecisionTaskCreated(true);
                    })}
                  >
                    {t("settings.decisionPatternsCreateTaskAction")}
                  </button>
                </SettingsField>
                {decisionTaskCreated ? (
                  <div className="banner settings-restart-banner" role="status">
                    <span>{t("settings.decisionPatternsCreateTaskDone")}</span>
                  </div>
                ) : null}
                <SettingsField label={t("settings.decisionPatternsThreshold")} hint={t("settings.decisionPatternsThresholdHint")} icon={<Sparkles size={18} />}>
                  <input
                    type="number"
                    min={1}
                    max={500}
                    className="settings-input"
                    value={decisionThresholdDraft}
                    disabled={busy}
                    onChange={(e) => setDecisionThresholdDraft(e.target.value)}
                    onBlur={() => void apply(async () => {
                      const n = Number.parseInt(decisionThresholdDraft, 10);
                      if (Number.isFinite(n) && n >= 1 && n <= 500 && n !== s.decisionPatternThreshold) {
                        await app.SetDecisionPatternThreshold(n);
                      }
                    })}
                  />
                </SettingsField>
                <SettingsField label={t("settings.decisionPatternsFilePath")} hint={t("settings.decisionPatternsFilePathHint")} icon={<Sparkles size={18} />} stacked className="settings-field--wrapped">
                  <input
                    type="text"
                    className="settings-input"
                    placeholder={t("settings.decisionPatternsFilePathPlaceholder")}
                    value={decisionPathDraft}
                    disabled={busy}
                    onChange={(e) => setDecisionPathDraft(e.target.value)}
                    onBlur={() => void apply(async () => {
                      const v = decisionPathDraft.trim();
                      if (v !== s.decisionPatternFilePath) {
                        await app.SetDecisionPatternFilePath(v);
                      }
                    })}
                  />
                </SettingsField>
                <SettingsField label={t("settings.decisionPatternsAutoApprove")} hint={t("settings.decisionPatternsAutoApproveHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.decisionPatternAutoApprove) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetDecisionPatternAutoApprove(on))}
                      >
                        {t(on ? "settings.decisionPatterns.on" : "settings.decisionPatterns.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
              </LabSectionCard>
              {/* 族 3：后台与续跑——心跳后台化（961）+ 续轮提醒（1069）+
                  Standard 流续跑总闸（1070）：三条均为「后台/续跑」主题，且都不
                  属于用户框定的两族，聚成第三张卡保持全部子项有明确族归属。 */}
              <LabSectionCard title={t("settings.lab.family.backgroundContinuation")}>
                <SettingsField label={labLabel("heartbeatBackground", t("settings.heartbeatBackground"))} hint={t("settings.heartbeatBackgroundHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalHeartbeatBackground) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          // Call-time gate（244 B1 S4 先例）：引擎每次运行现读
                          // 配置，保存后下一次调度即生效，无需重启。
                          await app.SetExperimentalHeartbeatBackground(on);
                        })}
                      >
                        {t(on ? "settings.heartbeatBackground.on" : "settings.heartbeatBackground.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge，
                    117 首落地，本件补设置页入口）。Boot snapshot 读闸：保存后
                    需重启生效（决策模式提取/固化同款先例）。 */}
                <SettingsField label={labLabel("stalledIntentNudge", t("settings.stalledIntentNudge"))} hint={t("settings.stalledIntentNudgeHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.stalledIntentNudge) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          await app.SetStalledIntentNudge(on);
                        })}
                      >
                        {t(on ? "settings.stalledIntentNudge.on" : "settings.stalledIntentNudge.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* 任务 1070：Standard 流续跑总闸——普通对话续跑通道开闸
                    （todo 续跑/stalled-intent 修复/executor-handoff nudge）。默认
                    关=普通对话永不自动续跑（现状一致）；开启后仍有界上限与各
                    边界排除。保存即换态，下一次 run 生效（免重启）。 */}
                <SettingsField label={labLabel("standardContinuation", t("settings.standardContinuation"))} hint={t("settings.standardContinuationHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.standardContinuation) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          await app.SetStandardContinuation(on);
                        })}
                      >
                        {t(on ? "settings.standardContinuation.on" : "settings.standardContinuation.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
              </LabSectionCard>
            </>
          )}
          {/* 任务 782：检索与历史（合并卡，742 先例）——历史提问选择器 +
              搜索历史提问 + 自动加载更早消息条目级合并。三行各读各的键、
              各写各的 setter；行级徽章经 labLabel 保留，rail 卡头聚合徽章
              取成员最高档（推荐）。已存偏好零迁移：三键原样保留。 */}
          {selected === "searchHistory" && (
            <>
              <SettingsField label={labLabel("searchHistory", t("settings.searchHistory"))} hint={t("settings.searchHistoryHint")} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              <SettingsField label={labLabel("promptHistoryPicker", t("settings.promptHistoryPicker"))} hint={t("settings.promptHistoryPickerHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalPromptHistoryPicker) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        // Boot snapshot (task 261): the composer reads the flag
                        // once at startup; saving raises the restart banner.
                        await app.SetExperimentalPromptHistoryPicker(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.promptHistoryPicker.on" : "settings.promptHistoryPicker.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("questionSearch", t("settings.questionSearch"))} hint={t("settings.questionSearchHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalQuestionSearch) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalQuestionSearch(on))}
                    >
                      {t(on ? "settings.questionSearch.on" : "settings.questionSearch.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("autoLoadOlder", t("settings.autoLoadOlder"))} hint={t("settings.autoLoadOlderHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalAutoLoadOlder) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalAutoLoadOlder(on);
                        // The transcript reads this flag from a live store, so the switch
                        // reaches already-open tabs without a restart; mirror it here for
                        // the same reason the settings reload alone cannot.
                        setAutoLoadOlderEnabled(on);
                      })}
                    >
                      {t(on ? "settings.autoLoadOlder.on" : "settings.autoLoadOlder.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
                    {selected === "localServer" && (
            <SettingsField label={labLabel("localServer", t("settings.localServer"))} hint={t("settings.localServerHint")} icon={<Server size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalLocalServer) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalLocalServer(on))}
                  >
                    {t(on ? "settings.localServer.on" : "settings.localServer.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {selected === "homePointer" && (
            /* 任务 846: change-data-home wizard. The switch is the lab gate
               (ships off); the wizard body renders only while it is on. */
            <>
              <SettingsField label={labLabel("homePointer", t("settings.homePointer"))} hint={t("settings.homePointer.hint")} icon={<Sparkles size={18} />} stacked className="settings-field--wrapped">
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalHomePointer) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalHomePointer(on))}
                    >
                      {t(on ? "settings.homePointer.on" : "settings.homePointer.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <Suspense fallback={<div className="empty" />}><HomePointerLabPane enabled={Boolean(s.experimentalHomePointer)} /></Suspense>
            </>
          )}
          {selected === "jsonlZstd" && (
            /* tl-1174: cold-session transcript zstd archive. Storage
               optimization, optional tier, default off; the idle sweep reads
               the switch per tick so a flip applies without a restart. */
            <SettingsField label={labLabel("jsonlZstd", t("settings.jsonlZstd"))} hint={t("settings.jsonlZstd.hint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalJsonlZstd) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalJsonlZstd(on))}
                  >
                    {t(on ? "settings.jsonlZstd.on" : "settings.jsonlZstd.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {selected === "pinnedIndex" && (
            /* tl1193b: plan-E pinned standing-tier index mode. Memory-context
               group, unstable tier, default off; boot resolves the saved
               switch into the memory gate before the first Load, so a flip
               applies on restart. Off = legacy full-body injection unchanged. */
            <SettingsField label={labLabel("pinnedIndex", t("settings.pinnedIndex"))} hint={t("settings.pinnedIndex.hint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalPinnedIndex) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalPinnedIndex(on))}
                  >
                    {t(on ? "settings.pinnedIndex.on" : "settings.pinnedIndex.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {selected === "pathRules" && (
            /* Task 564: retired (task 551 paradigm, UI side) — the structured
               path-scope evaluation never wired into the runtime (zero
               consumers; production keeps confine + allow_write + write
               approval). Read-only display like the M2 modelCapabilityFilter
               block: stored value shows, no setter, config key still renders
               (nothing silently dropped on save). */
            <SettingsField label={labLabel("pathRules", t("settings.pathRules"))} hint={t("settings.pathRules.retired")} icon={<FolderLock size={18} />} stacked>
              <p className="settings-field__hint-line">
                {t("settings.pathRules.value", { value: t(Boolean(s.experimentalPathRules) ? "settings.pathRules.on" : "settings.pathRules.off") })}
              </p>
            </SettingsField>
          )}
                    {/* 任务 961：traceAsState 独立 pane 退役——并入「交互与呈现」合并卡
              （interactionPresentation，卡内成员行承载）。 */}
          {selected === "orphanHandling" && (
            <>
              <SettingsField label={labLabel("orphanHandling", t("settings.orphanHandling"))} hint={t("settings.orphanHandlingHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalOrphanHandling) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalOrphanHandling(on);
                      })}
                    >
                      {t(on ? "settings.orphanHandling.on" : "settings.orphanHandling.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}

                    {selected === "runtimeReuse" && (
            <>
              <SettingsField label={labLabel("runtimeReuse", t("settings.runtimeReuse"))} hint={t("settings.runtimeReuseHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalRuntimeReuse) === on ? " set-seg__btn--on" : ""}`}
                      // 任务 1005：稳定档开关在开发者模式关时禁用（默认态只读）。
                      disabled={busy || labStableLocked("runtimeReuse")}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalRuntimeReuse(on);
                      })}
                    >
                      {t(on ? "settings.runtimeReuse.on" : "settings.runtimeReuse.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1005：稳定档禁用态两分支解释行；开发者模式解锁态改显警示。 */}
              {labStableLocked("runtimeReuse") ? (
                <p className="settings-field__hint-line">{t(Boolean(s.experimentalRuntimeReuse) ? "settings.lab.stable.lockedOn" : "settings.lab.stable.lockedOff")}</p>
              ) : labStableUnlocked("runtimeReuse") ? (
                <p className="settings-field__hint-line experimental-lab__stable-warning">{t("settings.lab.stable.devWarning")}</p>
              ) : null}
            </>
          )}
          {/* 任务 517:「安全 / 成本控制」— one switch for all three task-244
              B-group guards (this absorbs the 561 M1 autonomousRunGuard card
              and the standalone eventWaitRecheck entry; the config side is a
              key-level merge into experimental_safety_cost_control, task 449
              precedent). The three member rows are read-only STATE rows —
              每个子项状态可见（随总开关同开同关），不可单独写。 */}
          {/* 任务 517+722:「安全 / 成本控制」——总开关保留（键
              experimental_safety_cost_control 不变），三个 B 组守护从只读状态行
              升级为细粒度子开关（722 点6）：未拨动过=跟随总开关（nil，配置里
              不落盘，旧配置行为逐位不变），拨动后写显式子键、独立生效。
              任务 722 点5/点6：指定压缩模型与消息合并的行迁入本卡；任务 727：
              心跳会话轮换开关桥接 heartbeat-rotation.json 的 enabled。 */}
          {selected === "safetyCostControl" && (
            <>
              <SettingsField label={labLabel("safetyCostControl", t("settings.safetyCostControl"))} hint={t("settings.safetyCostControlHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSafetyCostControl) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSafetyCostControl(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.safetyCostControl.on" : "settings.safetyCostControl.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <p className="settings-field__hint-line">{t("settings.safetyCostControl.subHint")}</p>
              <SettingsField label={t("settings.autonomousIdleTerminate")} hint={t("settings.autonomousIdleTerminateHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${(s.safetyIdleTerminate ?? s.experimentalSafetyCostControl) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetSafetyIdleTerminate(on);
                      })}
                    >
                      {t(on ? "settings.autonomousIdleTerminate.on" : "settings.autonomousIdleTerminate.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.loopStreakNote")} hint={t("settings.loopStreakNoteHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${(s.safetyLoopStreakNote ?? s.experimentalSafetyCostControl) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetSafetyLoopStreakNote(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.loopStreakNote.on" : "settings.loopStreakNote.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.eventWaitRecheck")} hint={t("settings.eventWaitRecheckHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${(s.safetyEventWaitRecheck ?? s.experimentalSafetyCostControl) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetSafetyEventWaitRecheck(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.eventWaitRecheck.on" : "settings.eventWaitRecheck.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 727：心跳会话轮换——直接桥接 heartbeat-rotation.json 的
                  enabled 字段（缺文件=内置默认开；改后随引擎下一个调度周期生效）。 */}
              <SettingsField label={labLabel("heartbeatRotation", t("settings.heartbeatRotation"))} hint={t("settings.heartbeatRotationHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${(hbRotation?.enabled ?? true) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy || hbRotation == null}
                      onClick={() => void apply(async () => {
                        await app.SetHeartbeatRotationEnabled(on);
                        try {
                          setHbRotation(await app.HeartbeatRotationStatus());
                        } catch {
                          setHbRotation(null);
                        }
                      })}
                    >
                      {t(on ? "settings.heartbeatRotation.on" : "settings.heartbeatRotation.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 722 点5：指定压缩模型（707 交付项）自上下文治理卡迁入；
                  配置键 experimental_compact_model / compact_model 不变。 */}
              <SettingsField label={labLabel("compactModel", t("settings.compactModel"))} hint={t("settings.compactModelHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalCompactModel) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalCompactModel(on))}
                    >
                      {t(on ? "settings.compactModel.on" : "settings.compactModel.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {!Boolean(s.experimentalCompactModel) && (
                <p className="settings-field__hint-line">{t("settings.compactModel.inactiveHint")}</p>
              )}
              {/* 任务 722 点6：消息合并自独立入口并入本卡（键 collab_inbox_merge /
                  collab_guidance_merge 不变）。徽章只在主控开关行（621 口径）。 */}
              <SettingsField label={labLabel("messageMerge", t("settings.collabInboxMerge"))} hint={t("settings.collabInboxMergeHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {(["off", "same_sender", "all"] as const).map((mode) => (
                    <button
                      key={mode}
                      className={`set-seg__btn${(s.collabInboxMerge || "off") === mode ? "set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetCollabInboxMerge(mode);
                      })}
                    >
                      {t(`settings.collabInboxMerge.${mode}`)}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.collabGuidanceMerge")} hint={t("settings.collabGuidanceMergeHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.collabGuidanceMerge) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetCollabGuidanceMerge(on);
                        setCollabGuidanceMergeEnabled(on);
                      })}
                    >
                      {t(on ? "settings.collabGuidanceMerge.on" : "settings.collabGuidanceMerge.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 895：sentinel 穿透封堵实验开关（sentinel_penetration_guard）——
                  单开关条目入「安全 / 成本控制」卡（517/722 合并卡内追加行，
                  卡片结构/成员/灯不动）。[sentinel] 用户全局段落盘，判定路径
                  每次调用读运行期开关，拨动即时生效、无需重启。 */}
              <SettingsField label={t("settings.sentinelPenetrationGuard")} hint={t("settings.sentinelPenetrationGuardHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${(s.sentinelPenetrationGuard ?? true) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetSentinelPenetrationGuard(on))}
                    >
                      {t(on ? "settings.sentinelPenetrationGuard.on" : "settings.sentinelPenetrationGuard.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {selected === "sessionCollab" && (
            <>
              <SettingsField label={labLabel("sessionCollab", t("settings.sessionCollab"))} hint={t("settings.sessionCollabHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSessionCollab) === on ? " set-seg__btn--on" : ""}`}
                      // 任务 1005：稳定档开关在开发者模式关时禁用（默认态只读）。
                      disabled={busy || labStableLocked("sessionCollab")}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSessionCollab(on);
                        // 任务 856 ③（demo v2 交互稿）：前置关闭 → 协作中心联动
                        // 关闭——否则留下「值仍为开、开关却禁用」的不一致状态。
                        if (!on && Boolean(s.experimentalCollabHub)) {
                          await app.SetExperimentalCollabHub(false);
                        }
                        // The collaboration tools register when a session's
                        // controller is built, so an already-open session does
                        // not see them until it is rebuilt — surface that.
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.sessionCollab.on" : "settings.sessionCollab.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1005：稳定档禁用态两分支解释行；开发者模式解锁态改显警示。 */}
              {labStableLocked("sessionCollab") ? (
                <p className="settings-field__hint-line">{t(Boolean(s.experimentalSessionCollab) ? "settings.lab.stable.lockedOn" : "settings.lab.stable.lockedOff")}</p>
              ) : labStableUnlocked("sessionCollab") ? (
                <p className="settings-field__hint-line experimental-lab__stable-warning">{t("settings.lab.stable.devWarning")}</p>
              ) : null}
              <SettingsField label={t("settings.sessionCollab.hopLimit")} hint={t("settings.sessionCollab.hopLimitHint")} icon={<Sparkles size={18} />}>
                <input
                  type="number"
                  min={3}
                  max={1000}
                  value={hopLimit}
                  disabled={busy}
                  onChange={(event) => setHopLimit(Number(event.target.value))}
                  onBlur={() => void apply(async () => {
                    await app.SetSessionCollabHopLimit(hopLimit);
                  })}
                />
              </SettingsField>
              {/* Task 308-O4 / 308-O3（闲置释放、软内存上限）两参数行已于
                  任务 775 迁出：登记为存储组 kind:param 参数卡（数据驱动
                  渲染，走 LabParamFieldsCard；绑定在 labParamFields 注册表，
                  配置键/setter 零变化）。 */}
              {/* Task 264: background-woken sessions stay out of the tab bar.
                  A regular live panel setting (unlike the task-224
                  consumption switch) — delivery is unchanged, only where the
                  woken runtime lands. */}
              <SettingsField label={t("settings.sessionCollabBackground")} hint={t("settings.sessionCollabBackgroundHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.sessionCollabBackground) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetSessionCollabBackground(on))}
                    >
                      {t(on ? "settings.sessionCollabBackground.on" : "settings.sessionCollabBackground.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 173: the collaboration panel gates (one apply, cannot half-apply). */}
              <SettingsField label={t("settings.sessionCollabGates")} hint={t("settings.sessionCollabGatesHint")} icon={<Sparkles size={18} />}>
                <div className="set-gates">
                  {([
                    ["allowDelete", "sessionCollabAllowDelete", "settings.sessionCollabGates.allowDelete"],
                    ["allowRequireReply", "sessionCollabAllowRequireReply", "settings.sessionCollabGates.allowRequireReply"],
                    ["allowReadTail", "sessionCollabAllowReadTail", "settings.sessionCollabGates.allowReadTail"],
                    ["allowCreate", "sessionCollabAllowCreate", "settings.sessionCollabGates.allowCreate"],
                    ["allowSteer", "sessionCollabAllowSteer", "settings.sessionCollabGates.allowSteer"],
                  ] as const).map(([key, field, labelKey]) => {
                    const on = Boolean(s[field as keyof SettingsView] ?? false);
                    // Task 253: gates render as checkboxes, two per row. With
                    // the master switch off they stay visible and readable —
                    // only the checkbox is disabled — and one hint names the
                    // switch to flip (the grayed-out segment buttons were
                    // effectively invisible twice in a row).
                    const locked = busy || !Boolean(s.experimentalSessionCollab);
                    return (
                      <label key={key} className={`set-gates__item${locked ? " set-gates__item--locked" : ""}`}>
                        <input
                          type="checkbox"
                          checked={on}
                          disabled={locked}
                          onChange={() => void apply(async () => {
                            await app.SetSessionCollabGates(
                              key === "allowDelete" ? !on : Boolean(s.sessionCollabAllowDelete),
                              key === "allowRequireReply" ? !on : Boolean(s.sessionCollabAllowRequireReply),
                              key === "allowReadTail" ? !on : Boolean(s.sessionCollabAllowReadTail),
                              key === "allowCreate" ? !on : Boolean(s.sessionCollabAllowCreate),
                              key === "allowSteer" ? !on : Boolean(s.sessionCollabAllowSteer),
                              s.sessionCollabDailySendLimit ?? 0,
                            );
                          })}
                        />
                        <span className="set-gates__label">{t(labelKey)}</span>
                      </label>
                    );
                  })}
                  {!Boolean(s.experimentalSessionCollab) && (
                    <div className="set-gates__hint">{t("settings.sessionCollabGates.masterOffHint")}</div>
                  )}
                </div>
              </SettingsField>
              {/* Task 530: turn-closure reply reminder. One visible host
                  reminder per require_reply mail still unanswered at a clean
                  turn close; off (default) keeps turns untouched. */}
              <SettingsField label={t("settings.sessionCollabReplyNudge")} hint={t("settings.sessionCollabReplyNudgeHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.sessionCollabReplyNudge) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetSessionCollabReplyNudge(on))}
                    >
                      {t(on ? "settings.sessionCollab.on" : "settings.sessionCollab.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.sessionCollabDailySendLimit")} hint={t("settings.sessionCollabDailySendLimitHint")} icon={<Sparkles size={18} />}>
                <DailySendLimitInput
                  value={s.sessionCollabDailySendLimit ?? 0}
                  disabled={busy}
                  onCommit={(val) => apply(async () => {
                    await app.SetSessionCollabGates(
                      Boolean(s.sessionCollabAllowDelete),
                      Boolean(s.sessionCollabAllowRequireReply),
                      Boolean(s.sessionCollabAllowReadTail),
                      Boolean(s.sessionCollabAllowCreate),
                      Boolean(s.sessionCollabAllowSteer),
                      Math.max(0, val),
                    );
                  })}
                />
              </SettingsField>
              {/* 任务 722 点3：自动折叠跨会话消息并为跨会话协作子项（配置键
                  experimental_session_collab_auto_fold 不变，纯 UI 归位）。 */}
              <SettingsField label={labLabel("sessionCollabAutoFold", t("settings.sessionCollabAutoFold"))} hint={t("settings.sessionCollabAutoFoldHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSessionCollabAutoFold) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSessionCollabAutoFold(on);
                      })}
                    >
                      {t(on ? "settings.sessionCollabAutoFold.on" : "settings.sessionCollabAutoFold.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 309: mailbox defaults for talk_to_session. */}
              <SettingsField label={t("settings.sessionCollabMailDefaults")} hint={t("settings.sessionCollabMailDefaultsHint")} icon={<Sparkles size={18} />}>
                <div className="set-gates">
                  <label className="set-gates__item">
                    <input
                      type="checkbox"
                      checked={Boolean(s.sessionCollabMailIdempotentDefault)}
                      disabled={busy}
                      onChange={() => void apply(async () => {
                        await app.SetSessionCollabMailDefaults(
                          !Boolean(s.sessionCollabMailIdempotentDefault),
                          Boolean(s.sessionCollabMailReceiptDefault),
                          s.sessionCollabDefaultDelivery ?? "steer",
                        );
                      })}
                    />
                    <span className="set-gates__label">{t("settings.sessionCollabMailIdempotent")}</span>
                  </label>
                  <label className="set-gates__item">
                    <input
                      type="checkbox"
                      checked={Boolean(s.sessionCollabMailReceiptDefault)}
                      disabled={busy}
                      onChange={() => void apply(async () => {
                        await app.SetSessionCollabMailDefaults(
                          Boolean(s.sessionCollabMailIdempotentDefault),
                          !Boolean(s.sessionCollabMailReceiptDefault),
                          s.sessionCollabDefaultDelivery ?? "steer",
                        );
                      })}
                    />
                    <span className="set-gates__label">{t("settings.sessionCollabMailReceipt")}</span>
                  </label>
                  {/* 任务 722 点4：标签放左、下拉框放右（--wide 整行跨双列），
                      半列宽下「下拉框在左+标签在右」会把标签文本截断。 */}
                  <label className="set-gates__item set-gates__item--wide">
                    <span className="set-gates__label">{t("settings.sessionCollabDefaultDelivery")}</span>
                    <select
                      value={s.sessionCollabDefaultDelivery ?? "steer"}
                      disabled={busy}
                      onChange={(e) => void apply(async () => {
                        await app.SetSessionCollabMailDefaults(
                          Boolean(s.sessionCollabMailIdempotentDefault),
                          Boolean(s.sessionCollabMailReceiptDefault),
                          e.target.value,
                        );
                      })}
                    >
                      <option value="steer">{t("settings.sessionCollabDefaultDeliverySteer")}</option>
                      <option value="followup">{t("settings.sessionCollabDefaultDeliveryFollowup")}</option>
                    </select>
                  </label>
                </div>
              </SettingsField>
              {/* Task 320: the cross-session inbox panel entry — history query,
                  five-bucket views, retention and dismissals in one place. */}
              <SettingsField label={t("settings.collabInbox")} hint={t("settings.collabInboxHint")} icon={<Sparkles size={18} />}>
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  disabled={busy}
                  onClick={() => setCollabInboxOpen(true)}
                >
                  {t("settings.collabInbox.open")}
                </button>
              </SettingsField>
              {/* Task 225: cascade approval to the autopilot parent. */}
              <SettingsField label={t("settings.cascadeApproval")} hint={t("settings.cascadeApprovalHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalCascadeApproval) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalCascadeApproval(on);
                      })}
                    >
                      {t(on ? "settings.cascadeApproval.on" : "settings.cascadeApproval.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.sessionCollabRoster")} hint={t("settings.sessionCollabRosterHint")} icon={<Sparkles size={18} />}>
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  disabled={busy || !Boolean(s.experimentalSessionCollab)}
                  onClick={() => void reloadSessionCollabRoster()}
                >
                  {t("settings.sessionCollabRosterRefresh")}
                </button>
              </SettingsField>
              {sessionCollabRoster.length > 0 ? (
                <SettingsField label={t("settings.sessionCollabRosterRows")} hint={t("settings.sessionCollabRosterRowsHint")} icon={<Sparkles size={18} />} stacked>
                  <ul className="set-rules">
                    {sessionCollabRoster.map((row) => (
                      <li key={row.contactId}>
                        <code>{row.contactId}</code>
                        {row.purpose ? ` — ${row.purpose}` : ""}
                        {row.title ? ` (${row.title})` : ""}
                        {row.open ? ` · ${t("settings.sessionCollabOpen")}` : ""}
                      </li>
                    ))}
                  </ul>
                </SettingsField>
              ) : null}
            </>
          )}
          {selected === "optimisticParallel" && (
            <>
            <SettingsField label={labLabel("optimisticParallel", t("settings.optimisticParallel"))} hint={t("settings.optimisticParallelHint")} icon={<Sparkles size={18} />}>
              {/* Task 650: the checkbox became a two-segment capsule control
                  (Off | On) matching the permission-tier switch style; the
                  binding still reads the field upright (On = optimistic). */}
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.sandbox?.optimisticWrite) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetOptimisticWrite(on))}
                  >
                    {t(on ? "settings.optimisticParallel.on" : "settings.optimisticParallel.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            {/* Task 575 → 815: the heavy-bash write guard joins the card as a
                family sibling row (researchBudget/coldCacheCompact precedent:
                companion rows read their own key, save through their own
                setter, carry no separate tier badge). Off keeps pre-575
                behavior byte-for-byte. */}
            <SettingsField label={t("settings.bashHeavyGuard")} hint={t("settings.bashHeavyGuardHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalBashHeavyGuard) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalBashHeavyGuard(on))}
                  >
                    {t(on ? "settings.bashHeavyGuard.on" : "settings.bashHeavyGuard.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            {/* tl-1166: the reasoning-only final repair joins the same card as
                a sibling row (bashHeavyGuard precedent: own key, own setter,
                no tier badge). Ships ON — the readback is the resolved gate,
                so the toggle always writes an explicit value. Restart to
                apply (the gate rides the agent Options boot snapshot). */}
            <SettingsField label={t("settings.reasoningOnlyContinuation")} hint={t("settings.reasoningOnlyContinuationHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.reasoningOnlyContinuation) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalReasoningOnlyContinuation(on))}
                  >
                    {t(on ? "settings.reasoningOnlyContinuation.on" : "settings.reasoningOnlyContinuation.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            </>
          )}
          {selected === "autopilot" && (
            <>
              <SettingsField label={labLabel("autopilot", t("settings.autopilot"))} hint={t("settings.autopilotHint")} icon={<ShieldCheck size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.autopilot) === on ? " set-seg__btn--on" : ""}`}
                      // 任务 1005：稳定档开关在开发者模式关时禁用（默认态只读）。
                      disabled={busy || labStableLocked("autopilot")}
                      onClick={() => void apply(() => {
                        const runtime = String(s.autopilotMaxRuntime ?? "").trim();
                        return app.SetDesktopAutopilot(
                          on,
                          on && runtime === "" ? DEFAULT_AUTOPILOT_MAX_RUNTIME : runtime,
                          String(s.autopilotApprovalGrace ?? ""),
                        );
                      })}
                    >
                      {t(on ? "settings.autopilot.on" : "settings.autopilot.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1005：稳定档禁用态两分支解释行；开发者模式解锁态改显警示。 */}
              {labStableLocked("autopilot") ? (
                <p className="settings-field__hint-line">{t(Boolean(s.autopilot) ? "settings.lab.stable.lockedOn" : "settings.lab.stable.lockedOff")}</p>
              ) : labStableUnlocked("autopilot") ? (
                <p className="settings-field__hint-line experimental-lab__stable-warning">{t("settings.lab.stable.devWarning")}</p>
              ) : null}
              <SettingsField label={t("settings.autopilotMaxRuntime")} hint={t("settings.autopilotMaxRuntimeHint")} icon={<ShieldCheck size={18} />}>
                <input
                  className="set-input"
                  type="text"
                  defaultValue={String(s.autopilotMaxRuntime ?? "")}
                  disabled={busy}
                  placeholder="8h"
                  aria-label={t("settings.autopilotMaxRuntime")}
                  onBlur={(e) => void apply(() => app.SetDesktopAutopilot(Boolean(s.autopilot), e.target.value, String(s.autopilotApprovalGrace ?? "")))}
                />
              </SettingsField>
              <SettingsField label={t("settings.autopilotApprovalGrace")} hint={t("settings.autopilotApprovalGraceHint")} icon={<ShieldCheck size={18} />}>
                <input
                  className="set-input"
                  type="text"
                  defaultValue={String(s.autopilotApprovalGrace ?? "")}
                  disabled={busy}
                  placeholder="15s"
                  aria-label={t("settings.autopilotApprovalGrace")}
                  onBlur={(e) => void apply(() => app.SetDesktopAutopilot(Boolean(s.autopilot), String(s.autopilotMaxRuntime ?? ""), e.target.value))}
                />
              </SettingsField>
              {/* Task 477: the experimental ask-timeout sub-option. Off (the
                  default) keeps the terminal stop after the built-in 10-minute
                  wait; on answers a timed-out high-risk ask with a refusal
                  after the configured seconds and lets the run continue.
                  Attended sessions are never touched by either state. */}
              <SettingsField label={t("settings.autopilotAskTimeout")} hint={t("settings.autopilotAskTimeoutHint")} icon={<ShieldCheck size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalAutopilotAskTimeout) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetDesktopAutopilotAskTimeout(on, Number(s.autopilotAskWaitSeconds ?? 0)))}
                    >
                      {t(on ? "settings.autopilotAskTimeout.on" : "settings.autopilotAskTimeout.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.autopilotAskWaitSeconds")} hint={t("settings.autopilotAskWaitSecondsHint")} icon={<ShieldCheck size={18} />}>
                <input
                  className="set-input"
                  type="number"
                  min={1}
                  max={3600}
                  defaultValue={String(s.autopilotAskWaitSeconds ?? 15)}
                  disabled={busy || !Boolean(s.experimentalAutopilotAskTimeout)}
                  placeholder="15"
                  aria-label={t("settings.autopilotAskWaitSeconds")}
                  onBlur={(e) => void apply(() => app.SetDesktopAutopilotAskTimeout(Boolean(s.experimentalAutopilotAskTimeout), Number(e.target.value)))}
                />
              </SettingsField>
              {/* Task 544: the experimental ask auto-continue sub-option. Off
                  (the default) keeps the idle stop after an answered ask; on
                  resumes a turn that stopped right after the answer with one
                  host continuation turn carrying the recorded decision.
                  Independent of the ask-timeout pair above. */}
              <SettingsField label={t("settings.autopilotAskAutoContinue")} hint={t("settings.autopilotAskAutoContinueHint")} icon={<ShieldCheck size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalAutopilotAskAutoContinue) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetDesktopAutopilotAskAutoContinue(on))}
                    >
                      {t(on ? "settings.autopilotAskAutoContinue.on" : "settings.autopilotAskAutoContinue.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 1084：高风险操作授权清单（5 checkbox，默认全不勾）+ ask
                  轮次上限。授权两级由硬到软：①实验室勾选（最硬）②决策模式条目
                  （次硬）；记忆不算授权源。默认不勾 = 出厂五类全要问（保守）。
                  高风险五类判定可靠性分级：对外发送 高/数据迁移删除 高/破坏性
                  变更 中/新技术路线 低/付费额度 低；本机制靠模型理解规则判断动
                  作属哪类，非字符串硬匹配；后两类必然漏判误判，漏判兜底是轮次
                  上限+复盘。
                  任务 1124：五个授权开关从五个独立 SettingsField 聚合为一张
                  卡（.set-highrisk-authorize 清单块，231/527 preapprove 先例的
                  1084 版）——聚合的是**入口层组织**，五项的配置键（config
                  读写路径）、默认值（全不勾）、setter（五个独立 bridge 方法，
                  一次点击只写一项，不合并批量写）与说明文案（各带自己的 hint
                  行）逐字节不变。 */}
              <div className="set-highrisk-authorize">
                <div className="set-highrisk-authorize__head">
                  <div className="set-highrisk-authorize__title">{t("settings.highRiskAuthorize")}</div>
                  <div className="set-highrisk-authorize__hint">{t("settings.highRiskAuthorizeHint")}</div>
                </div>
                {([
                  { key: "authorizeOutboundSend", label: t("settings.authorizeOutboundSend"), hint: t("settings.authorizeOutboundSendHint"), checked: Boolean(s.authorizeOutboundSend) },
                  { key: "authorizeDataMigrationDelete", label: t("settings.authorizeDataMigrationDelete"), hint: t("settings.authorizeDataMigrationDeleteHint"), checked: Boolean(s.authorizeDataMigrationDelete) },
                  { key: "authorizeDestructiveChange", label: t("settings.authorizeDestructiveChange"), hint: t("settings.authorizeDestructiveChangeHint"), checked: Boolean(s.authorizeDestructiveChange) },
                  { key: "authorizeNewTechRoute", label: t("settings.authorizeNewTechRoute"), hint: t("settings.authorizeNewTechRouteHint"), checked: Boolean(s.authorizeNewTechRoute) },
                  { key: "authorizePaidQuotaAlloc", label: t("settings.authorizePaidQuotaAlloc"), hint: t("settings.authorizePaidQuotaAllocHint"), checked: Boolean(s.authorizePaidQuotaAlloc) },
                ]).map((row) => (
                  <label key={row.key} className="set-highrisk-authorize__row">
                    <input
                      type="checkbox"
                      checked={row.checked}
                      disabled={busy}
                      onChange={(e) => {
                        const next = e.target.checked;
                        // 五个 setter 语义零变化：一次点击只调用该项自己的
                        // 桥接写入器（与聚合前的独立 checkbox 行为一致）。
                        if (row.key === "authorizeOutboundSend") void apply(() => app.SetAuthorizeOutboundSend(next));
                        else if (row.key === "authorizeDataMigrationDelete") void apply(() => app.SetAuthorizeDataMigrationDelete(next));
                        else if (row.key === "authorizeDestructiveChange") void apply(() => app.SetAuthorizeDestructiveChange(next));
                        else if (row.key === "authorizeNewTechRoute") void apply(() => app.SetAuthorizeNewTechRoute(next));
                        else void apply(() => app.SetAuthorizePaidQuotaAlloc(next));
                      }}
                    />
                    <span className="set-highrisk-authorize__copy">
                      <span className="set-highrisk-authorize__label">{row.label}</span>
                      <span className="set-highrisk-authorize__rowhint">{row.hint}</span>
                    </span>
                  </label>
                ))}
              </div>
              <SettingsField label={t("settings.experimentalAskRoundLimit")} hint={t("settings.experimentalAskRoundLimitHint")} icon={<ShieldCheck size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalAskRoundLimit) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalAskRoundLimit(on))}
                    >
                      {t(on ? "settings.experimentalAskRoundLimit.on" : "settings.experimentalAskRoundLimit.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={t("settings.askRoundLimit")} hint={t("settings.askRoundLimitHint")} icon={<ShieldCheck size={18} />}>
                <input
                  className="set-input"
                  type="number"
                  min={0}
                  max={100}
                  defaultValue={String(s.askRoundLimit ?? 3)}
                  disabled={busy || !Boolean(s.experimentalAskRoundLimit)}
                  placeholder="3"
                  aria-label={t("settings.askRoundLimit")}
                  onBlur={(e) => void apply(() => app.SetAskRoundLimit(Number(e.target.value)))}
                />
              </SettingsField>
              {/* Task 364: managed-path pre-approval moved here from its own
                  misc entry — it only affects autopilot behaviour, so it lives
                  inside this card as a sub-block. Config keys, persistence and
                  the approval semantics are byte-identical (moved, not
                  rewritten); the switch + four category checkboxes + risk line
                  render as-is. Task 527: the checkbox block + risk line moved
                  OUT of the field's control column (they used to sit in the
                  same flex row as the switch, sized by the warning's
                  max-content width and spilling past the panel edge); they now
                  render below the field as a full-width block whose left edge
                  matches the hint text above. */}
              <div className="autopilot-preapprove-subblock">
                <SettingsField label={t("settings.preapproveManagedPaths")} hint={t("settings.preapproveManagedPathsHint")} icon={<ShieldAlert size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalPreapproveManagedPaths) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetPreapproveManagedPaths(
                          on,
                          Boolean(s.preapproveSkills),
                          Boolean(s.preapproveHooks),
                          Boolean(s.preapproveSessionStores),
                          Boolean(s.preapproveBashEscape),
                        ))}
                      >
                        {t(on ? "settings.preapproveManagedPaths.on" : "settings.preapproveManagedPaths.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* Task 231: with the master switch on, the four categories check
                    independently — one write carries all five values so a save can
                    never land half-applied. 任务461-P5 (user ruling): the block
                    reads top-down as checkbox rows FIRST (one category per line),
                    then the risk line on its own closing row — the warning no
                    longer sits on top of the checkboxes it describes. Task 527:
                    the block sits BELOW the field (not inside its control
                    column), so its left edge lines up with the hint text and its
                    width is the card content width instead of the warning's
                    max-content spill. */}
                {Boolean(s.experimentalPreapproveManagedPaths) && (
                  <div className="set-preapprove">
                    {([
                      { key: "preapproveSkills", label: t("settings.preapproveManagedPaths.skills"), checked: Boolean(s.preapproveSkills) },
                      { key: "preapproveHooks", label: t("settings.preapproveManagedPaths.hooks"), checked: Boolean(s.preapproveHooks) },
                      { key: "preapproveSessionStores", label: t("settings.preapproveManagedPaths.sessionStores"), checked: Boolean(s.preapproveSessionStores) },
                      { key: "preapproveBashEscape", label: t("settings.preapproveManagedPaths.bashEscape"), checked: Boolean(s.preapproveBashEscape) },
                    ]).map((row) => (
                      <label key={row.key} className="set-preapprove__row">
                        <input
                          type="checkbox"
                          checked={row.checked}
                          disabled={busy}
                          onChange={(event) => {
                            const next = {
                              skills: Boolean(s.preapproveSkills),
                              hooks: Boolean(s.preapproveHooks),
                              stores: Boolean(s.preapproveSessionStores),
                              bash: Boolean(s.preapproveBashEscape),
                            };
                            if (row.key === "preapproveSkills") next.skills = event.target.checked;
                            else if (row.key === "preapproveHooks") next.hooks = event.target.checked;
                            else if (row.key === "preapproveSessionStores") next.stores = event.target.checked;
                            else next.bash = event.target.checked;
                            void apply(() => app.SetPreapproveManagedPaths(
                              Boolean(s.experimentalPreapproveManagedPaths),
                              next.skills,
                              next.hooks,
                              next.stores,
                              next.bash,
                            ));
                          }}
                        />
                        <span>{row.label}</span>
                      </label>
                    ))}
                    <p className="set-preapprove__warning">{t("settings.preapproveManagedPaths.warning")}</p>
                  </div>
                )}
              </div>
            </>
          )}
          {/* ── Task 265 lab intake: 9 fork features + task 262 quick commands ── */}
          {/* Task 561 M2 模型策略族: highSpeedModel keeps its writable switch;
              modelCapabilityFilter is retired (task 551/564 domain) and
              renders read-only below — stored value displays, no setter. */}
          {selected === "modelStrategy" && (
            <>
              <SettingsField label={labLabel("highSpeedModel", t("settings.highSpeedModel"))} hint={t("settings.highSpeedModelLabHint")} icon={<Sparkles size={18} />}>
                {/* Task 318.1: the lane arms from the next boot (boot-snapshot
                    read in Go); the light above reflects the stored switch. */}
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalHighSpeedModel) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalHighSpeedModel(on);
                      })}
                    >
                      {t(on ? "settings.highSpeedModel.on" : "settings.highSpeedModel.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* 任务 722 点1：备用模型自跨会话协作卡迁入「提效-模型策略」
                  （键 experimental_fallback_model 不变；模型目标行仍在
                  「设置 → 模型 → 模型偏好」）。 */}
              <SettingsField label={t("settings.fallbackModelSwitch")} hint={t("settings.fallbackModelSwitchHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalFallbackModel) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalFallbackModel(on);
                      })}
                    >
                      {t(on ? "settings.fallbackModelSwitch.on" : "settings.fallbackModelSwitch.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 551/564: retired key — read-only display only (task 561
                  M2 keeps it visible without a setter; 564 owns the copy). */}
              {/* 任务 722 点2：已退役徽章移除（rail 成员表与卡内行都不再挂
                  「已退役」档位徽章）——行本体保留只读展示（564 口径）。 */}
              <SettingsField label={t("settings.modelCapabilityFilter")} hint={t("settings.modelCapabilityFilter.retired")} icon={<Sparkles size={18} />} stacked>
                <p className="settings-field__hint-line">
                  {t("settings.modelCapabilityFilter.value", { value: t(Boolean(s.experimentalModelCapabilityFilter) ? "settings.modelCapabilityFilter.on" : "settings.modelCapabilityFilter.off") })}
                </p>
              </SettingsField>
            </>
          )}
          {/* Task 427: proactive-cooldown + cold-cache compact merge into ONE
              storage card — two switches, each saving through its own setter
              (config keys and knob semantics unchanged). */}
          {/* Task 561 M3 压缩/预算族: the four context/cache governance cards
              (compactionParallel + budgetControl + compressOpt + cacheTuning)
              fold into ONE card — every switch and knob below keeps its own
              setter and config key (81/123 lost-save rule; task 427
              merge precedent). */}
          {selected === "contextGovernance" && (
            <>
              <SettingsField label={labLabel("compactionParallel", t("settings.compactionParallel"))} hint={t("settings.compactionParallelHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalCompactionParallel) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalCompactionParallel(on))}
                    >
                      {t(on ? "settings.compactionParallel.on" : "settings.compactionParallel.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <>
                <SettingsField label={labLabel("budgetControl", t("settings.contextBudget"))} hint={t("settings.contextBudgetHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalContextBudget) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalContextBudget(on))}
                      >
                        {t(on ? "settings.contextBudget.on" : "settings.contextBudget.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.contextBudgetCompress")} hint={t("settings.contextBudgetCompressHint")} icon={<Sparkles size={18} />}>
                  <span />
                </SettingsField>
                {/* 任务 621：researchBudget 是 budgetControl 的附属开关——徽章只在
                    主控开关行（contextBudget），附属行不重复挂。 */}
                <SettingsField label={t("settings.researchBudget")} hint={t("settings.researchBudgetHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalResearchBudget) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalResearchBudget(on))}
                      >
                        {t(on ? "settings.researchBudget.on" : "settings.researchBudget.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
              </>
              <>
                <SettingsField label={labLabel("compressOpt", t("settings.proactiveCompact"))} hint={t("settings.proactiveCompactHint")} icon={<Sparkles size={18} />}>
                  {/* Task 318.2: off (default) keeps the hard-coded 10-minute
                      fold cooldown byte-for-byte; on makes the minutes below
                      authoritative (live read on the next fold). */}
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalProactiveCompact) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(async () => {
                          await app.SetExperimentalProactiveCompact(on);
                        })}
                      >
                        {t(on ? "settings.proactiveCompact.on" : "settings.proactiveCompact.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.proactiveCompact.cooldown")} hint={t("settings.proactiveCompact.cooldownHint")} icon={<Sparkles size={18} />}>
                  <input
                    type="number"
                    min={1}
                    max={720}
                    value={proactiveCooldownMinutes}
                    disabled={busy || !Boolean(s.experimentalProactiveCompact)}
                    onChange={(event) => setProactiveCooldownMinutes(Number(event.target.value))}
                    onBlur={() => void apply(async () => {
                      await app.SetProactiveCompactCooldownMinutes(proactiveCooldownMinutes);
                    })}
                  />
                </SettingsField>
                {/* 任务 621：coldCacheCompact 是 compressOpt 的附属开关——徽章只在
                    主控开关行（proactiveCompact），附属行不重复挂。 */}
                <SettingsField label={t("settings.coldCacheCompact")} hint={t("settings.coldCacheCompactHint")} icon={<Sparkles size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalColdCacheCompact) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalColdCacheCompact(on))}
                      >
                        {t(on ? "settings.coldCacheCompact.on" : "settings.coldCacheCompact.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                <SettingsField label={t("settings.coldCacheCompact.minBytes")} hint={t("settings.coldCacheCompact.minBytesHint")} icon={<Sparkles size={18} />}>
                  <input
                    type="number"
                    min={1}
                    max={64384}
                    defaultValue={Math.round((s.coldCacheCompactMinBytes || 614400) / 1024)}
                    disabled={busy}
                    onBlur={(e) => {
                      const kb = Math.max(1, Math.min(64384, Math.floor(Number(e.target.value) || 600)));
                      e.target.value = String(kb);
                      void apply(() => app.SetColdCacheCompactMinBytes(kb * 1024));
                    }}
                  />
                  <span> KB</span>
                </SettingsField>
                <SettingsField label={t("settings.coldCacheCompact.idleHours")} hint={t("settings.coldCacheCompact.idleHoursHint")} icon={<Sparkles size={18} />}>
                  <input
                    type="number"
                    min={1}
                    max={168}
                    defaultValue={Math.max(1, Math.round((s.coldCacheCompactIdleMinutes || 300) / 60))}
                    disabled={busy}
                    onBlur={(e) => {
                      const h = Math.max(1, Math.min(168, Math.floor(Number(e.target.value) || 5)));
                      e.target.value = String(h);
                      void apply(() => app.SetColdCacheCompactIdleMinutes(h * 60));
                    }}
                  />
                  <span> h</span>
                </SettingsField>
              </>
              <>
                <SettingsField label={labLabel("cacheTuning", t("settings.cacheTuning"))} hint={t("settings.cacheTuningHint")} icon={<FolderLock size={18} />}>
                  <SettingsOptions layout="field" className="set-seg">
                    {[false, true].map((on) => (
                      <button
                        key={String(on)}
                        className={`set-seg__btn${Boolean(s.experimentalCacheTuning) === on ? " set-seg__btn--on" : ""}`}
                        disabled={busy}
                        onClick={() => void apply(() => app.SetExperimentalCacheTuning(on))}
                      >
                        {t(on ? "settings.cacheTuning.on" : "settings.cacheTuning.off")}
                      </button>
                    ))}
                  </SettingsOptions>
                </SettingsField>
                {/* Task 191: the memory-governance panel is the entry surface, so it stays
                    visible with the switch off (entry visible ≠ feature on). The controls
                    keep saving; the boot loader simply ignores the values (see
                    useDesktopPreferences) until the switch is turned on. */}
                {!Boolean(s.experimentalCacheTuning) && (
                  <p className="settings-field__hint-line">{t("settings.cacheTuning.inactiveHint")}</p>
                )}
                <>
                  <SettingsField label={t("settings.cacheTuning.tabs")} hint={t("settings.cacheTuning.tabsHint")}>
                      <input
                        type="number"
                        min={0}
                        max={64}
                        defaultValue={s.maxCachedTabs || 0}
                        disabled={busy}
                        onBlur={(e) => {
                          const v = Math.max(0, Math.min(64, Math.floor(Number(e.target.value) || 0)));
                          e.target.value = String(v);
                          void apply(() => app.SetTranscriptCacheTuning(v, s.historyBodyBudgetMb || 192, s.markdownBudgetMb || 256));
                        }}
                      />
                    </SettingsField>
                    <SettingsField label={t("settings.cacheTuning.body")} hint={t("settings.cacheTuning.bodyHint")}>
                      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                        <input
                          type="range"
                          min={32}
                          max={512}
                          step={16}
                          defaultValue={s.historyBodyBudgetMb || 192}
                          disabled={busy}
                          style={{ flex: 1 }}
                          onChange={(e) => { const n = e.target.parentElement!.querySelector("input[type=number]") as HTMLInputElement | null; if (n) n.value = e.target.value; }}
                          onMouseUp={(e) => {
                            const v = Math.max(32, Math.min(512, Math.floor(Number((e.target as HTMLInputElement).value))));
                            void apply(() => app.SetTranscriptCacheTuning(s.maxCachedTabs || 0, v, s.markdownBudgetMb || 256));
                          }}
                        />
                        <input
                          type="number"
                          min={32}
                          max={512}
                          defaultValue={s.historyBodyBudgetMb || 192}
                          disabled={busy}
                          style={{ width: 90 }}
                          onBlur={(e) => {
                            const v = Math.max(32, Math.min(512, Math.floor(Number(e.target.value) || 192)));
                            e.target.value = String(v);
                            void apply(() => app.SetTranscriptCacheTuning(s.maxCachedTabs || 0, v, s.markdownBudgetMb || 256));
                          }}
                        />
                        <span>MB</span>
                      </div>
                    </SettingsField>
                    <SettingsField label={t("settings.cacheTuning.markdown")} hint={t("settings.cacheTuning.markdownHint")}>
                      <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                        <input
                          type="range"
                          min={64}
                          max={2048}
                          step={64}
                          defaultValue={s.markdownBudgetMb || 256}
                          disabled={busy}
                          style={{ flex: 1 }}
                          onChange={(e) => { const n = e.target.parentElement!.querySelector("input[type=number]") as HTMLInputElement | null; if (n) n.value = e.target.value; }}
                          onMouseUp={(e) => {
                            const v = Math.max(64, Math.min(2048, Math.floor(Number((e.target as HTMLInputElement).value))));
                            void apply(() => app.SetTranscriptCacheTuning(s.maxCachedTabs || 0, s.historyBodyBudgetMb || 192, v));
                          }}
                        />
                        <input
                          type="number"
                          min={64}
                          max={2048}
                          defaultValue={s.markdownBudgetMb || 256}
                          disabled={busy}
                          style={{ width: 90 }}
                          onBlur={(e) => {
                            const v = Math.max(64, Math.min(2048, Math.floor(Number(e.target.value) || 256)));
                            e.target.value = String(v);
                            void apply(() => app.SetTranscriptCacheTuning(s.maxCachedTabs || 0, s.historyBodyBudgetMb || 192, v));
                          }}
                        />
                        <span>MB</span>
                      </div>
                    </SettingsField>

                    {/* Task 347: same-domain switches move in from misc. The
                        resident exemption is NOT the tabs pool limit above:
                        pool = total resident LRU line, this = active/running
                        exemption quota (two parameters, two hints). */}
                    <SettingsField label={t("settings.activeTabResident")} hint={t("settings.activeTabResidentHint")}>
                      <SettingsOptions layout="field" className="set-seg">
                        {[false, true].map((on) => (
                          <button
                            key={String(on)}
                            className={`set-seg__btn${Boolean(s.experimentalActiveTabResident) === on ? " set-seg__btn--on" : ""}`}
                            disabled={busy}
                            onClick={() => void apply(() => app.SetExperimentalActiveTabResident(on))}
                          >
                            {t(on ? "settings.activeTabResident.on" : "settings.activeTabResident.off")}
                          </button>
                        ))}
                      </SettingsOptions>
                    </SettingsField>
                    {/* Task 347: tunable replayed-graph cache LRU (task 196fix2
                        contract: 1..16, write pushes immediately, the view
                        reports the effective value so 0-in-file shows 3). */}
                    <SettingsField label={t("settings.cacheTuning.dagCache")} hint={t("settings.cacheTuning.dagCacheHint")}>
                      <input
                        type="number"
                        min={1}
                        max={16}
                        defaultValue={s.dagGraphCacheCapacity || 3}
                        disabled={busy}
                        onBlur={(e) => {
                          const v = Math.max(1, Math.min(16, Math.floor(Number(e.target.value) || 3)));
                          e.target.value = String(v);
                          void apply(() => app.SetDagGraphCacheCapacity(v));
                        }}
                      />
                    </SettingsField>
                    <p style={{ opacity: 0.7, fontSize: 12 }}>{t("settings.cacheTuning.restartHint")}</p>
                  </>
              </>
            </>
          )}
          {/* 任务 783：用量监测（可观测性组合并卡，742 先例）——GLM Plan
              用量（已上线只读展示行）+ OpenCode Go 用量（开关行）。任务 800
              两处布局清理：大项介绍从左列窄位（Tooltip 单行截断）扩展到右侧
              大项介绍位（多行完整展示）；GLM 行同步去重——同一句 hint 原先
              左列 + 右段渲染两遍，现只保留右侧介绍位一份。OpenCode Go 行改
              标准「说明左/开关右」成员行，卡内明细退役（783 起侧栏卡是权威
              展示位，799 起按 plan family 门控；查询与 163 零请求门全在侧栏）。
              coding plan preset 家族未来按需加成员行（布局 members 即扩展点）。 */}
          {selected === "usageMonitoring" && (
            <>
              <SettingsField label={labLabel("usageMonitoring", t("settings.usageMonitoring"))} icon={<Activity size={18} />} stacked>
                <p className="settings-field__hint">{t("settings.usageMonitoringHint")}</p>
              </SettingsField>
              <SettingsField label={labLabel("glmPlanUsage", t("settings.glmPlanUsage"))} icon={<Activity size={18} />} stacked>
                <p className="settings-field__hint">{t("settings.glmPlanUsageHint")}</p>
              </SettingsField>
              <ErrorBoundary>
                <SettingsOpenCodeGoUsageCard
                  enabled={Boolean(s.experimentalOpenCodeGoUsage)}
                  busy={busy}
                  onToggle={(on) => void apply(() => app.SetExperimentalOpenCodeGoUsage(on))}
                />
              </ErrorBoundary>
            </>
          )}
          {/* 任务 782：输入与选区（合并卡，742 先例）——草稿持久化 + 选区
              快捷操作条目级合并。两行各读各的键、各写各的 setter；行级徽章
              经 labLabel 保留，rail 卡头聚合徽章取成员最高档（可选）。已存
              偏好零迁移：两键原样保留。 */}
          {selected === "inputSelection" && (
            <>
              <SettingsField label={labLabel("inputSelection", t("settings.inputSelection"))} hint={t("settings.inputSelectionHint")} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              <SettingsField label={labLabel("draftPersistence", t("settings.draftPersistence"))} hint={t("settings.draftPersistenceHint")} icon={<Sparkles size={18} />}>
                {/* Task 318.3: off (default) = runtime-memory-only drafts
                    (pre-#9580 contract); the settings refresh feeds the live
                    gate in composerDraftPersistence. */}
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalComposerDraft) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalComposerDraft(on);
                      })}
                    >
                      {t(on ? "settings.draftPersistence.on" : "settings.draftPersistence.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("selectionActions", t("settings.selectionActions"))} hint={t("settings.selectionActionsHint")} icon={<Sparkles size={18} />}>
                {/* Task 369: off (default) = the selection menu is exactly the
                    pre-369 surface (fork rule 2). */}
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalSelectionActions) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalSelectionActions(on);
                      })}
                    >
                      {t(on ? "settings.selectionActions.on" : "settings.selectionActions.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
            </>
          )}
                    {selected === "completionSummary" && (
            <SettingsField label={labLabel("completionSummary", t("settings.completionSummary"))} hint={t("settings.completionSummaryHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalCompletionSummary) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalCompletionSummary(on))}
                  >
                    {t(on ? "settings.completionSummary.on" : "settings.completionSummary.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {/* 任务 961：交互与呈现（合并卡，742/782 条目级合并先例）——快捷指令 +
              轨迹状态 + 回答风格三散条并卡。卡内三行各读各的键、各写各的
              setter；行级徽章经 labLabel 保留，rail 卡头聚合徽章取成员最高档
              （推荐）。已存偏好零迁移：三键原样保留。 */}
          {selected === "interactionPresentation" && (
            <>
              <SettingsField label={labLabel("interactionPresentation", t("settings.interactionPresentation"))} hint={t("settings.interactionPresentationHint")} icon={<Sparkles size={18} />} stacked>{null}</SettingsField>
              <SettingsField label={labLabel("quickCommands", t("settings.quickCommands"))} hint={t("settings.quickCommandsLabHint")} icon={<Zap size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalQuickCommands) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalQuickCommands(on))}
                    >
                      {t(on ? "settings.quickCommandsLab.on" : "settings.quickCommandsLab.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {Boolean(s.experimentalQuickCommands) && (
                <QuickCommandsManager s={s} busy={busy} apply={apply} />
              )}
              <SettingsField label={labLabel("traceAsState", t("settings.traceAsState"))} hint={t("settings.traceAsStateHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalTraceAsState) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalTraceAsState(on))}
                    >
                      {t(on ? "settings.traceAsState.on" : "settings.traceAsState.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              <SettingsField label={labLabel("outputStyle", t("settings.outputStyle"))} hint={t("settings.outputStyleLabHint")} icon={<Sparkles size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalOutputStyleUI) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(() => app.SetExperimentalOutputStyleUI(on))}
                    >
                      {t(on ? "settings.outputStyle.on" : "settings.outputStyle.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {Boolean(s.experimentalOutputStyleUI) && (
                <>
                  <SettingsField label={t("settings.outputStyle.selector")} hint={t("settings.outputStyle.selectorHint")} icon={<SlidersHorizontal size={18} />}>
                    <SettingsSelect
                      value={s.outputStyle ?? ""}
                      onValueChange={(next) => void apply(() => app.SetOutputStyle(next))}
                      searchPlaceholder={t("settings.outputStyle.searchPlaceholder")}
                      options={[
                        { value: "", label: t("settings.outputStyle.default"), hint: t("settings.outputStyle.defaultHint") },
                        ...(outputStyles?.options ?? []).map((opt) => ({
                          value: opt.name,
                          label: opt.name,
                          hint: [
                            opt.builtin ? t("settings.outputStyle.builtin") : t("settings.outputStyle.custom"),
                            opt.description,
                          ].filter(Boolean).join(" — "),
                        })),
                      ]}
                    />
                    <button
                      type="button"
                      className="btn btn--secondary btn--small"
                      onClick={() => void openOutputStylesDir()}
                    >
                      {t("settings.outputStyle.openDir")}
                    </button>
                  </SettingsField>
                  {outputStylesError && (
                    <div className="banner banner--error" role="alert">{outputStylesError}</div>
                  )}
                  {outputStyles && outputStyles.issues.length > 0 && (
                    // A style file that did not load must be visible here —
                    // the loader itself skips it silently (task 385a).
                    <div className="banner banner--error" role="alert">
                      <div>{t("settings.outputStyle.issues")}</div>
                      {outputStyles.issues.map((issue) => (
                        <div key={issue.path}>{issue.name}: {issue.reason}</div>
                      ))}
                    </div>
                  )}
                </>
              )}
            </>
          )}
          {/* Task 561 M5 开发调试族: CDP endpoint + lifecycle noise triage fold into
              ONE card — both are developer-facing diagnostics; each switch
              keeps its own setter (81/123 lost-save rule). */}
          {selected === "devDebug" && (
            <>
              {/* Task 342: WebView2 CDP debug endpoint. The WebView2 browser
                  environment is created once at startup, so a flip needs a
                  restart — same banner pattern as the full-access boot snapshot. */}
              <SettingsField label={labLabel("cdpDebugPort", t("settings.cdpDebugPort"))} hint={t("settings.cdpDebugPortHint")} icon={<Terminal size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalCDPDebugPort) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalCDPDebugPort(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.cdpDebugPort.on" : "settings.cdpDebugPort.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 377: crash-report lifecycle noise triage. Startup
                  diagnostics read the gate once per process, so the flip lands on
                  restart — same boot-snapshot rule as the CDP endpoint above. */}
              <SettingsField label={labLabel("lifecycleNoiseGate", t("settings.lifecycleNoiseGate"))} hint={t("settings.lifecycleNoiseGateHint")} icon={<Volume2 size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  {[false, true].map((on) => (
                    <button
                      key={String(on)}
                      className={`set-seg__btn${Boolean(s.experimentalLifecycleNoiseGate) === on ? " set-seg__btn--on" : ""}`}
                      disabled={busy}
                      onClick={() => void apply(async () => {
                        await app.SetExperimentalLifecycleNoiseGate(on);
                        setRestartNeeded(true);
                      })}
                    >
                      {t(on ? "settings.lifecycleNoiseGate.on" : "settings.lifecycleNoiseGate.off")}
                    </button>
                  ))}
                </SettingsOptions>
              </SettingsField>
              {/* Task 642: mock crash drill. Not a flag — a one-shot action that
                  synthesizes a clearly marked test report and opens the real
                  crash overlay, so 617/618 (report channel, pending queue,
                  one-click analysis) are verifiable end to end without a real
                  crash. The overlay itself carries the anti-misreport banner. */}
              <SettingsField label={t("settings.mockCrash")} hint={t("settings.mockCrashHint")} icon={<FlaskConical size={18} />}>
                <SettingsOptions layout="field" className="set-seg">
                  <button
                    className="set-seg__btn"
                    disabled={busy}
                    onClick={() => fireMockCrashDrill()}
                  >
                    {t("settings.mockCrashRun")}
                  </button>
                </SettingsOptions>
              </SettingsField>
            </>
          )}
          {selected === "acpListen" && (
            // 任务 790: resident ACP listener. The listener arms at desktop
            // boot, so a flip needs a restart — same banner pattern as the
            // CDP endpoint. When on, the card doubles as the 状态灯: bound
            // loopback address while running, bind failure, or the
            // await-restart note. 127.0.0.1 is pinned in the host; the port
            // (acp_listen_port, default 8790) is a config key, not a UI field.
            <SettingsField
              label={labLabel("acpListen", t("settings.acpListen"))}
              hint={
                acpListenStatus && acpListenStatus.err
                  ? `${t("settings.acpListen.notRunning")} ${acpListenStatus.err}`
                  : acpListenStatus && acpListenStatus.running
                    ? `${t("settings.acpListen.running")} ${acpListenStatus.addr}`
                    : Boolean(s.experimentalACPListen) && acpListenStatus && !acpListenStatus.running
                      ? t("settings.acpListen.awaitRestart")
                      : t("settings.acpListenHint")
              }
              icon={<Server size={18} />}
            >
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalACPListen) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalACPListen(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.acpListen.on" : "settings.acpListen.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
          )}
          {/* 任务 961：outputStyle 独立 pane 退役——并入「交互与呈现」合并卡
              （interactionPresentation，卡内成员行承载；Task 385a 门控+选择器
              语义不变，仅落点迁卡）。 */}
          {selected === "toolOptimizations" && (
            /* 任务 971：工具优化聚合页——工具表相关开关单页管理。
               顶部常驻 rev2 警告（定稿原文）+ 三生效点机制说明 + 四配套；
               下方 readBack / multi_edit 直注 / restart_update 三开关 + 证据门
               成员行 + session_hub 预留键位。任务 603 readBack 语义不变；任务
               943 两开关独立语义保持（合并入页不回并，各持各键各 setter）。 */
            <>
            {/* 任务 971：顶部常驻警告（rev2 定稿全文，双向语义保留）。 */}
            <div className="banner banner--warning" role="alert" data-testid="tool-opt-hub-warning">
              {t("settings.toolOptHub.warning")}
            </div>
            {/* 任务 971：三生效点机制说明 + 四配套（pending 态/compact 立即生效/
                换装 notice/纯 schema 范围）。 */}
            <p className="approval-mode-hint">{t("settings.toolOptHub.effect")}</p>
            <p className="approval-mode-hint">{t("settings.toolOptHub.pending")}</p>
            <p className="approval-mode-hint">{t("settings.toolOptHub.notice")}</p>
            <p className="approval-mode-hint">{t("settings.toolOptHub.scope")}</p>
            <SettingsField label={labLabel("toolOptimizations", t("settings.toolOptimizations"))} hint={t("settings.toolOptimizationsHint")} icon={<Sparkles size={18} />} stacked>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${(s.experimentalToolOptimizations ?? true) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalToolOptimizations(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.toolOptimizations.on" : "settings.toolOptimizations.off")}
                  </button>
                ))}
              </SettingsOptions>
              <p className="approval-mode-hint">{t("settings.toolOptimizations.risk")}</p>
            </SettingsField>
            {/* 任务 789 → 815 登记窗口：编辑证据门宽松模式落卡成员行（Go 侧
                开关 789 先行，前端 yaml/experimentTiers 登记已在；本行补齐
                设置页入口+labLabel 徽章面，tier 镜像 Go 侧 unstable）。 */}
            <SettingsField label={labLabel("editEvidenceRelaxed", t("settings.editEvidenceRelaxed"))} hint={t("settings.editEvidenceRelaxedHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalEditEvidenceRelaxed) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalEditEvidenceRelaxed(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.editEvidenceRelaxed.on" : "settings.editEvidenceRelaxed.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            {/* 任务 943 multi_edit 直注（971 聚合入页为成员行，独立 pane 退役）。
                显示走与 Go 侧同一条回落链（键缺省跟随旧族键，族键也缺省按面板层
                默认开）：存量族键开的用户拆分前后显示不跳变。键/setter 零变化。 */}
            <SettingsField label={labLabel("multiEditInjection", t("settings.multiEditInjection"))} hint={t("settings.multiEditInjectionHint")} icon={<Sparkles size={18} />} stacked>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${(s.experimentalMultiEditInjection ?? (s.experimentalToolOptimizations ?? true)) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalMultiEditInjection(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.multiEditInjection.on" : "settings.multiEditInjection.off")}
                  </button>
                ))}
              </SettingsOptions>
              <p className="approval-mode-hint">{t("settings.multiEditInjection.risk")}</p>
            </SettingsField>
            {/* 任务 971：restart_update 自界面组「更新与反馈」卡迁入（键/setter
                零变化，仅 UI 落点迁移）。 */}
            <SettingsField label={labLabel("restartUpdate", t("settings.restartUpdate"))} hint={t("settings.restartUpdateHint")} icon={<RefreshCw size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalRestartUpdate) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(() => app.SetExperimentalRestartUpdate(on))}
                  >
                    {t(on ? "settings.restartUpdate.on" : "settings.restartUpdate.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            {/* 任务 979：能力组披露+session_hub 聚合件真开关——替换 971 的
                占位键位（954/955 已合入 149491604，占位从未挂接）。一键覆盖
                953（use_capability 组行/组卡/体积闸/call_hint）、954（session_hub
                L0 聚合件注册+可见面）、969（常驻目录组归属）；boot 快照，重启
                （或新会话）生效。默认关=零行为（铁律 2），九件协作工具直挂不变。 */}
            <SettingsField label={labLabel("capabilityGroups", t("settings.capabilityGroups"))} hint={t("settings.capabilityGroupsHint")} icon={<Sparkles size={18} />} stacked>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalCapabilityGroups) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalCapabilityGroups(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.capabilityGroups.on" : "settings.capabilityGroups.off")}
                  </button>
                ))}
              </SettingsOptions>
              <p className="approval-mode-hint">{t("settings.capabilityGroups.risk")}</p>
            </SettingsField>
            {/* tl-1218：multi_edit 三条增强（①错误详情增强 ②证据门连坐收窄
                ③跳过失败项模式）——三个独立开关的卡内成员行，与 multi_edit
                直注成组成员行。boot 快照，重启生效；默认关=与基线逐字节一致
                （铁律 2）。 */}
            <SettingsField label={labLabel("multiEditErrorDetail", t("settings.multiEditErrorDetail"))} hint={t("settings.multiEditErrorDetailHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalMultiEditErrorDetail) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalMultiEditErrorDetail(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.multiEditErrorDetail.on" : "settings.multiEditErrorDetail.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            <SettingsField label={labLabel("multiEditEvidenceNarrow", t("settings.multiEditEvidenceNarrow"))} hint={t("settings.multiEditEvidenceNarrowHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalMultiEditEvidenceNarrow) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalMultiEditEvidenceNarrow(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.multiEditEvidenceNarrow.on" : "settings.multiEditEvidenceNarrow.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            <SettingsField label={labLabel("multiEditSkipFailed", t("settings.multiEditSkipFailed"))} hint={t("settings.multiEditSkipFailedHint")} icon={<Sparkles size={18} />}>
              <SettingsOptions layout="field" className="set-seg">
                {[false, true].map((on) => (
                  <button
                    key={String(on)}
                    className={`set-seg__btn${Boolean(s.experimentalMultiEditSkipFailed) === on ? " set-seg__btn--on" : ""}`}
                    disabled={busy}
                    onClick={() => void apply(async () => {
                      await app.SetExperimentalMultiEditSkipFailed(on);
                      setRestartNeeded(true);
                    })}
                  >
                    {t(on ? "settings.multiEditSkipFailed.on" : "settings.multiEditSkipFailed.off")}
                  </button>
                ))}
              </SettingsOptions>
            </SettingsField>
            </>
          )}
        </div>
      </div>
    </SettingsPageShell>
    </LabWallOnContext.Provider>
  );
}

/** Task 262: the quick-commands manager as a self-contained surface — the lab
 *  pane hosts the entry button, the dialog carries search, per-row enable and
 *  inline editing, and an editor form reused for both create and edit (262-4).
 *  Entry lives in the lab (efficiency) while experimentalQuickCommands is on. */
function QuickCommandsManager({ s, busy, apply }: { s: SettingsView; busy: boolean; apply: (task: () => Promise<unknown>) => Promise<unknown> }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const entries = s.quickCommands ?? [];
  return (
    <div className="settings-quick-commands">
      <button
        type="button"
        className="btn btn--secondary btn--small"
        disabled={busy}
        onClick={() => setOpen(true)}
      >
        {t("settings.quickCommandsManage")}{entries.length > 0 ? ` (${entries.length})` : ""}
      </button>
      {/* Task 721: dialog body moved to QuickCommandsManagerDialog — the single
          implementation also mounted by the composer picker's bottom entry. */}
      {open && (
        <QuickCommandsManagerDialog
          entries={entries}
          busy={busy}
          save={(next) => apply(() => app.SetQuickCommands(next))}
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  );
}
