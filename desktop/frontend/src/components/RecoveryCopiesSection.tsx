import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { app, type ConsolidationReport, type RecoveryCopyGroupView, type RecoveryChainPreview, type RecoveryChainSet, type RecoveryChainView, type SessionVersionGroupView, type SessionRepairRecordView, type SessionHealthReportView } from "../lib/bridge";
import { useT, type Translator } from "../lib/i18n";
import { formatSessionBusyError } from "../lib/sessionBusyError";
import { useToast } from "../lib/toast";
import { useConfirmDialog } from "./ConfirmDialog";

/** 任务 1063：存储与路径页的两个版本分区合并成一个。
 *
 *  旧形态是两区并存：「查看并合并会话副本…」（recovery 副本文件扫描）+「工
 *  作阶段版本与修复（v3 事件日志）」（meta sidecar 的多 heads 会话）。同一
 *  份会话在两个入口里出现两次、读数却各说各话（副本区没有 v3 heads、版本
 *  区没有副本体积），用户要自己拼出全貌。合并后单一分区全景可见：每个会
 *  话一行，展开后该会话的所有分支（主线 + 副本文件 + v3 heads）都在同一
 *  张表里，每行读数含体积。
 *
 *  只动 UI 收口：两条数据链路（recovery 副本扫描 / v3 heads 重放）原样保
 *  留，各自负责各自的读数，本组件只负责把它们画到一起。原两区的全部操作
 *  （合并副本 / 候选链预览与选 HEAD / 检查健康 / 修复 / 切换版本）零丢失。
 */

/** bytes → a short human size. */
function formatBytes(n: number): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function formatWhen(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** tl1415 点10: the summary row's turns column shows the main's measured turns
 *  once a scan exists; before that it names the state instead of a bare "—"
 *  (the column used to render the message count under a turns header, which is
 *  exactly the "5396 · 215" confusion the user flagged). Zero turns after a
 *  scan is a real reading (tool-only transcripts have none) and shows as 0. */
function groupTurnsText(group: RecoveryCopyGroupView, t: Translator): string {
  if (group.mainMessages > 0 || group.mainTurns > 0) return String(group.mainTurns);
  return group.copies.length > 0 ? t("settings.recoveryCopiesUnmeasured") : "—";
}

/** tl1415 点12: the recovery filename's short signature — "recovery-<8 hex>",
 *  the part that maps a panel row back to the file the user sees on disk
 *  (recovery-5df6de18…). Empty for names that are not recovery copies, so
 *  callers can fall back to the generic label. */
function recoveryShortName(path: string): string {
  const m = /-recovery-([0-9a-fA-F]{16})\.jsonl$/i.exec(path);
  return m ? `recovery-${m[1].slice(0, 8).toLowerCase()}` : "";
}

/** fileSizeOf reads a transcript's on-disk size via the bridge's chain listing
 *  (the same reading the volume column shows), returning 0 when the file cannot
 *  be stat'd — a repair receipt that cannot compare sizes says so rather than
 *  inventing a delta. 任务 队尾体积列修复。 */
async function fileSizeOf(path: string): Promise<number> {
  try {
    const set = await app.ListRecoveryChains(path);
    const main = (set.chains ?? []).find((c) => c.isMain);
    return main?.bytes ?? 0;
  } catch {
    return 0;
  }
}

/** volumeDeltaText renders the repair receipt's before→after size comparison.
 *  Both readings missing (0/0) means the file could not be stat'd either time;
 *  the receipt then omits the volume line instead of showing "0 → 0". */
function volumeDeltaText(before: number, after: number, t: Translator): string {
  if (!before && !after) return "";
  if (before === after) {
    return ` · ${t("settings.sessionVersions.volumeUnchanged")}`;
  }
  return ` · ${t("settings.sessionVersions.volumeDelta")
    .replace("{before}", formatBytes(before))
    .replace("{after}", formatBytes(after))}`;
}

type Phase = "pending" | "unmerged" | "merged";

/** copiesNeedingMeasurement is what stands between "not measured" and "mergeable".
 *
 *  An orphan copy has no canonical transcript to be compared against, so it can
 *  never be measured and must not hold a merge back.
 */
function copiesNeedingMeasurement(group: RecoveryCopyGroupView) {
  return group.copies.filter((copy) => !copy.orphan && !copy.scanned);
}

/** uniqueIn is the count that decides whether merging is worth anything: events a
 *  copy holds that the canonical transcript does not. */
function uniqueIn(group: RecoveryCopyGroupView): number {
  return group.copies.reduce((n, c) => n + (c.orphan ? 0 : c.unique), 0);
}

/** Winner describes which transcript a merge would end up with.
 *
 *  The backend promotes the fullest loadable candidate, whether that is the main
 *  transcript or one of the copies, so the panel can say in advance which one wins
 *  instead of surprising the user afterwards. The turns of a copy are only known
 *  once it has been measured, so a group that has not been measured has no winner
 *  to name yet.
 */
type Winner = { isMain: boolean; turns: number; copyIndex: number };

function predictedWinner(group: RecoveryCopyGroupView): Winner | null {
  if (copiesNeedingMeasurement(group).length > 0) return null;
  let best: Winner = { isMain: true, turns: group.mainMessages, copyIndex: -1 };
  group.copies.forEach((copy, index) => {
    if (copy.orphan) return;
    if (copy.messages > best.turns) best = { isMain: false, turns: copy.messages, copyIndex: index };
  });
  return best;
}

/** phaseOf sorts a conversation by what a user would do about it. */
function phaseOf(group: RecoveryCopyGroupView): Phase {
  if (copiesNeedingMeasurement(group).length > 0) return "pending";
  if (uniqueIn(group) > 0) return "unmerged";
  return "merged";
}

/** Outcome is one conversation's result. Kept per conversation because a count
 *  cannot say which conversation was refused, nor why. */
type Outcome = {
  label: string;
  kind: "merged" | "forced" | "blocked" | "failed";
  before?: number;
  after?: number;
  trashed?: number;
  notCovered?: number;
  /** Copies the backend refused to load, so their events were never considered. */
  unloadable?: number;
  /** Losing-chain turns grafted onto the new main instead of being archived. */
  prefixed?: number;
  reasons?: string[];
  detail?: string;
};

/** DeleteOutcome is one branch's delete result (任务 1064). Kept separate from
 *  Outcome because "deleted" is not a merge shape - the panel's merge vocabulary
 *  ("merged"/"forced"/"blocked") would misreport a delete as a content move.
 *  任务 1368：kind 增加 "conversation"——整对话级删除（主文件+全部副本）的
 *  回执；detail 携带 locale 化的完整读数文案（主线去向+副本归档数）。 */
type DeleteOutcome = {
  label: string;
  kind: "deleted" | "conversation" | "failed";
  detail?: string;
};

/** SynthesisOutcome is one synthesis run's record (任务 1065): what went in
 *  (participants + their unique counts) and what the new main reads as afterwards,
 *  so "总条数≈并集" is checkable on screen rather than asserted.
 *
 *  任务 1146 返修：kind 增加 "v3only"——纯 v3 头会话（无副本文件）的合成在
 *  文件层无事可做（其余版本住在同一份事件日志里），此前它因 report.items
 *  非空而照走 "synthesized" 成功回执，用户看到「合成已生效……落败分支还在
 *  回收站里」却面对一套纹丝不动的列表（用户 20261025 复测现场）。v3only 是
 *  诚实的「无可做」回执：不算成功（不触发生效横幅），也不算失败（不是错误）。
 *
 *  任务 1146 返修2（用户 20261025 裁决）：kind 增加 "v3synthesized"——返修后
 *  v3-only 形态的合成真实生效：主线切到选定 head（switchedTo）+ 落败 heads
 *  走 1064 retire 标记清理（retiredHeads）。横幅如实报两件事各自发生与否，
 *  不再沿用文件级成功文案（「落败分支在回收站里」对 retire 语义是失实的：
 *  head 清理不走文件回收站）。 */
type SynthesisOutcome = {
  label: string;
  kind: "synthesized" | "failed" | "v3only" | "v3synthesized";
  participants?: number;
  uniqueTotal?: number;
  afterMessages?: number;
  afterTurns?: number;
  prefixed?: number;
  trashed?: number;
  switched?: boolean;
  retired?: number;
  detail?: string;
};

/** RecoveryCopiesSection is the one versions-and-copies panel on the storage page.
 *
 *  Opening it is one directory read per session folder, so it appears at once and
 *  shows the shape of the problem without opening any transcript. Conversations
 *  start collapsed and are grouped by what they need.
 *
 *  Measuring a copy means reading and comparing whole transcripts. The panel offers
 *  it on demand, and the merge performs it itself when needed - the preview is built
 *  from those counts, so the button can produce them rather than sending the user
 *  back to press Scan first.
 *
 *  This is a promotion, not a splice: the fullest candidate becomes the canonical
 *  transcript and the rest are folded into the recoverable trash. So the preview
 *  names the winner in advance - including when that winner is a copy - because
 *  "which transcript will I be left with" is the question the merge decides.
 *
 *  Merging asks twice because the backend has two modes. A plain merge refuses when
 *  the fullest copy and the main transcript each hold turns the other lacks
 *  (typical after a main-side compaction). That refusal is reported, and the user is
 *  then asked whether the copy should win. Neither mode runs without its own
 *  confirmation.
 */

/** ChainPreviewBody is the whole preview dialog body as its own component, so
 *  the tail-reveal state lives inside the rendered dialog. The body used to be
 *  JSX built in the outer component; ConfirmDialog keeps the element it was
 *  handed, so "load more" updated outer state the frozen tree never saw and the
 *  tail never grew. As a real child component its own state re-renders it. */
function ChainPreviewBody({ preview }: { preview: RecoveryChainPreview }) {
  const t = useT();
  // The tail opens revealed. The dialog exists to judge a branch by how it ends,
  // and a body that renders nothing until a click reads as "nothing to preview" -
  // worst on branches whose tail is only a page long, where the click revealed
  // everything at once anyway. One page shows up front, further clicks append a
  // page each, and the counter states how much of the tail is on screen.
  const [showTail, setShowTail] = useState(true);
  const [tailChars, setTailChars] = useState(3000);
  // degraded previews carry no overlap numbers at all (shared/unique stay 0 as
  // placeholders), so the "you would drop N unique messages" warning must not
  // fire for them - it read as a factual "0 unique" which is neither true nor
  // computable until the merge normalizes the copy.
  const dropping =
    !preview.degraded && preview.sharedWithMain < preview.messageCount && !preview.isMain;
  const all = preview.tailLines ?? [];
  const total = all.reduce((n, line) => n + line.length, 0);
  const shown = showTail ? Math.min(total, tailChars) : Math.min(total, 3000);
  const lines: string[] = [];
  let spent = 0;
  for (let i = all.length - 1; i >= 0 && spent < shown; i--) {
    lines.unshift(all[i]);
    spent += all[i].length;
  }
  const remaining = total - spent;
  return (
    <div className="rc-preview">
      <div className="rc-preview__stats">
        <span>
          {t("settings.recoveryCopiesColMessages")} {preview.messageCount}
        </span>
        <span>
          {preview.turns} {t("settings.recoveryCopiesColTurns")}
        </span>
        {!preview.isMain && !preview.degraded ? (
          <>
            <span>
              {t("settings.recoveryCopiesPreviewShared")} {preview.sharedWithMain}
            </span>
            <span>
              {t("settings.recoveryCopiesPreviewUnique")} {preview.uniqueToChain}
            </span>
          </>
        ) : null}
      </div>
      {preview.degraded ? (
        <div className="rc-preview__warn">{t("settings.recoveryCopiesPreviewDegraded")}</div>
      ) : null}
      {/* The trailing messages, revealed on demand: zero-latency because they
          arrive with the summary, and enough of the ending to recognize the
          scene without replaying the whole log. */}
      {total > 0 ? (
        <>
          <button
            className="btn btn--small"
            type="button"
            onClick={() => {
              // Load more appends a page; once nothing is left the same button
              // collapses instead, so it never looks like a dead "load more".
              if (!showTail) setShowTail(true);
              else if (remaining > 0) setTailChars((n) => n + 3000);
              else setShowTail(false);
            }}
          >
            {!showTail
              ? t("settings.recoveryCopiesPreviewTailShow")
              : remaining > 0
                ? `${t("settings.recoveryCopiesPreviewTailMore")} (${remaining})`
                : t("settings.recoveryCopiesPreviewTailHide")}
          </button>
          {showTail ? (
            <span className="rc-preview__tail-count">
              {t("settings.recoveryCopiesPreviewTailCount")
                .replace("{shown}", String(spent))
                .replace("{total}", String(total))}
            </span>
          ) : null}
          {showTail ? (
            <div className="rc-preview__section rc-preview__section--tail">
              <div className="rc-preview__label">{t("settings.recoveryCopiesPreviewEnd")}</div>
              {lines.map((line, i) => (
                <div className="rc-preview__line" key={i}>
                  {line}
                </div>
              ))}
            </div>
          ) : null}
        </>
      ) : null}
      {dropping ? (
        <div className="rc-preview__warn">
          {t("settings.recoveryCopiesPreviewDropWarn").replace("{n}", String(preview.uniqueToChain))}
        </div>
      ) : null}
    </div>
  );
}

/** degradedChainSet is the local stand-in for a conversation whose chain
 *  enumeration was refused (任务 1039r). The backend's own degradation (1019/1039)
 *  turns a replay refusal into a Degraded row, but a rejection that still escapes
 *  it - a directory read error, a non-refusal replay failure - used to leave the
 *  expanded area an empty table with a bare error line. Rendering one zero-count
 *  degraded row keeps the same face as the backend degradation: the row exists,
 *  carries no counts, and is never the recommended chain.
 */
function degradedChainSet(mainPath: string): RecoveryChainSet {
  const row: RecoveryChainView = {
    path: mainPath,
    headId: "",
    label: "",
    isMain: true,
    selected: false,
    covered: false,
    messageCount: 0,
    turns: 0,
    lastActivity: "",
    bytes: 0,
    preview: "",
    degraded: true,
  };
  return { mainPath, mainLabel: "", chains: [row], longestPath: "", longestHead: "", newestPath: "", newestHead: "" };
}

/** VersionRows is the merged detail table of one conversation's expanded row
 *  (tl1415 点3: 副本列表 + 候选链 + 版本表三块合一).
 *
 *  任务 1063 把副本块与 v3 版本块并排放进展开区时,二者各自渲染一张表;用户
 *  实测同一会话的信息被三张表说三遍(候选链行还裸拼「5396 · 215 交互轮次」
 *  没有表头)。本组件现在把三者收进一张表:主线行(当前主线链或主线读数)、
 *  副本组行(recovery 文件)、候选链与版本组行(事件日志 heads),每条分支两行
 *  ——主行放核心读数,次行放操作与预览摘要——全部操作入口零丢失。
 *
 *  候选链与版本表的数据源本来就是同一个调用(ListRecoveryChains 的链清单),
 *  是真合并;副本是另一条链路(副本文件扫描),视觉并入同一张表+分组行,不
 *  强行同质化。链清单由外层的惰性缓存传入(外层 loadChains),本组件不再自
 *  己重放一遍——原先两个组件各调一次 ListRecoveryChains,同一会话被重放两
 *  次;合并后一次加载喂整张表。
 *
 *  Local state remains for what only this row needs (health report, repair
 *  receipt, repair log), loaded on demand the first time the row expands. */
function VersionRows({
  group,
  chains,
  chainLoading,
  t,
  confirm,
  onReload,
  onError,
  repairNote,
  setRepairNote,
  dataVersion,
  busy,
  scanningAll,
  synthesizing,
  pickedChain,
  previewingPath,
  branchesSelected,
  onToggleBranch,
  onDeleteBranch,
  onPick,
  onSynthesize,
  headCount,
  onClearHistory,
}: {
  group: RecoveryCopyGroupView;
  /** 外层惰性缓存的一条链清单(loadChains 的落点)。undefined = 尚未加载完
   *  成;空数组 = 枚举被拒后的降级落点(外层已报错误横幅)。 */
  chains: RecoveryChainSet | undefined;
  chainLoading: boolean;
  t: Translator;
  confirm: ReturnType<typeof useConfirmDialog>["confirm"];
  onReload: () => void;
  onError: (message: string) => void;
  repairNote: string;
  setRepairNote: (note: string) => void;
  /** 任务 1146:load() 的代际号。行保持展开时组件不重挂载,本 effect 若只看
   *  mainPath 就永远不重拉——合成/切换/删除版本后列表已换代,版本表必须跟
   *  着重读一次,否则用户看到的是操作前的旧表(实测:合成已生效,版本表仍
   *  显示旧版本)。 */
  dataVersion: number;
  busy: boolean;
  scanningAll: boolean;
  /** 该会话的合成进度("0/N"),undefined = 不在合成中。 */
  synthesizing: string | undefined;
  /** 该会话已选定的 HEAD(外层 pickedChain),undefined = 未选。 */
  pickedChain: string | undefined;
  previewingPath: string | null;
  branchesSelected: Set<string>;
  onToggleBranch: (path: string) => void;
  onDeleteBranch: (copyPath: string, index: number) => void;
  onPick: (chain: RecoveryChainView) => void;
  onSynthesize: () => void;
  /** tl1415 点9: 该会话在 meta 头镜像里的版本计数(>1 才有「清空合并历史」
   *  可清)。0 = 版本清单里没有该会话。 */
  headCount: number;
  /** tl1415 点9: 清空合并历史(确认+调用+toast+重载都在外层)。 */
  onClearHistory: () => void;
}) {
  const mainPath = group.mainPath;
  const [health, setHealth] = useState<{ path: string; report: SessionHealthReportView | null }>({ path: "", report: null });
  const [repairing, setRepairing] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [records, setRecords] = useState<SessionRepairRecordView[]>([]);
  // 任务 1146:区分「换行」与「同行的数据刷新」。mainPath 变化才把读数打回
  // loading;dataVersion 触发的重拉保留旧读数渲染(stale-while-revalidate),
  // 新读数到达后原地替换——否则修复回执/健康检查结果会在每次刷新时闪失
  // (1064b 当初就是把回执提升到分区层来躲「行重载丢回执」的,这里不能再
  // 把它藏回 loading 里)。
  const prevMainRef = useRef(mainPath);

  // 本组件只剩修复日志一项自加载(chains 已由外层惰性缓存提供,原先在这里
  // 重复调用的 ListRecoveryChains 随三表合并一并去掉)。错误不反向触发重载:
  // 报错走 onError 的通道,避免「读被拒→报错→effect 重跑→再被拒」循环。
  useEffect(() => {
    const mainChanged = prevMainRef.current !== mainPath;
    prevMainRef.current = mainPath;
    if (mainChanged) {
      setHealth({ path: "", report: null });
      setRecords([]);
    }
    let cancelled = false;
    void (async () => {
      let recs: SessionRepairRecordView[] = [];
      try {
        recs = (await app.ListSessionRepairRecordsFor(mainPath)) ?? [];
      } catch {
        // 修复日志读不到不是展开区的失败——链读数才是主体,缺席时其余部分照常渲染。
      }
      if (!cancelled) setRecords(recs);
    })();
    return () => {
      cancelled = true;
    };
  }, [mainPath, dataVersion]);

  const inspect = async () => {
    try {
      const report = await app.InspectSessionHealthFor(mainPath);
      setHealth({ path: mainPath, report });
    } catch (e) {
      onError(String(e));
    }
  };

  const repair = async (diet: boolean) => {
    setRepairing(true);
    setRepairNote("");
    let note = "";
    let receiptWritten = false;
    try {
      // 队尾体积列修复：修复前后文件读数对比——后端报告本身不带体积，这里在
      // 调用前后各读一次磁盘大小（os.Stat 的同一读数，与链表的体积列同源），
      // 瘦身类修复（diet）的收益才在面板上看得见；纯修复类动作体积不变时照实
      // 显示「无变化」，不编造数字。
      const beforeBytes = await fileSizeOf(mainPath);
      const report = await app.RepairSessionFor(mainPath, true, true, diet);
      const afterBytes = await fileSizeOf(mainPath);
      if (!report.repaired) {
        note = t("settings.sessionVersions.noRepairNeeded");
      } else {
        // 动作名标注 done/skipped（后端 SelfHealAction.Status 就是 done|skipped|
        // failed）：一行回执说清「哪些做了、哪些没做」，而不是只报一串动作名。
        const actionsText = report.actions
          .map((a) => `${a.action}(${a.status})`)
          .join("+");
        const volumeText = volumeDeltaText(beforeBytes, afterBytes, t);
        note = `${t("settings.sessionVersions.repaired")
          .replace("{actions}", actionsText)
          .replace("{backup}", report.backupDir || "—")}${volumeText}`;
        // 任务 1116 件 1：hydrate 回填了检查点但 diet 没跑、投影仍超窗 ⇒ 回执
        // 不能停在「修复完成」。追加显式告警与下一步（勾 Diet 再跑一次），与
        // 后端 report.warning / repairlog / notice 三方一致。
        if (report.warning) {
          note += ` — ${t("settings.sessionVersions.repairStillOverWindow")}`;
        }
        setRepairNote(note);
        receiptWritten = true;
        onReload();
      }
      const fresh = await app.InspectSessionHealthFor(mainPath).catch(() => null);
      if (fresh) setHealth({ path: mainPath, report: fresh });
    } catch (e) {
      note = String(e);
    } finally {
      setRepairing(false);
      if (note && !receiptWritten) {
        // The success path wrote its receipt above (same tick as the repair);
        // the no-repair-needed and error paths fall through here.
        setRepairNote(note);
      }
    }
  };

  const activate = async (headId: string) => {
    try {
      await app.SetActiveSessionVersion({ scope: "global", topicId: "", path: mainPath, headId });
      onReload();
    } catch (e) {
      onError(String(e));
    }
  };

  /** 任务 1064：v3 版本块里的单版本删除。与父级的批量删除共用同一条确认与
   *  回收语义（后端 retire 标记：不选中就不可删，删后条目从列表消失、独有
   *  事件在下一次单写轮换前仍可通过重放找回）。 */
  const deleteHead = async (headId: string) => {
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteHeadTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteHeadLead")}
            ：{headId ? headId.slice(0, 8) : "main"}
          </p>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesDeleteHeadWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesDeleteConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToDelete) return;
    setDeleting(headId);
    try {
      await app.DeleteSessionVersionHead(mainPath, headId);
      onReload();
    } catch (e) {
      // tl1367-fix defect C: the busy family renders localized (holder block).
      onError(formatSessionBusyError(e));
    } finally {
      setDeleting(null);
    }
  };

  // ── 合并表行数据(tl1415 点3) ──
  const chainRows = chains?.chains ?? [];
  // 任务 1146 返修2(协调线 2120):「当前主线」只标当前激活的那条链——主转录
  // 文件内、后端标记为当前选中的 head(RecoveryChainView.Selected,与版本表
  // 「当前选中」同源)才算;无任何 Selected 读数(降级行/schema-1 整文件行)
  // 时退回旧行为——单行场景不回归。
  const mainFileRows = chainRows.filter((c) => c.path === mainPath);
  const selectedMainExists = mainFileRows.some((c) => c.selected);
  const currentMainChain = chainRows.find(
    (c) => c.path === mainPath && (selectedMainExists ? Boolean(c.selected) : mainFileRows.length === 1),
  );
  // 候选链与版本组 = 除当前主线外的全部链(其余 heads + 副本文件链)。当前主
  // 线升格为表首主线行,不再在组里重复出现——原先它在候选链块和版本表各出现
  // 一次,同一事实被渲染两遍。
  const candidateChains = currentMainChain ? chainRows.filter((c) => c !== currentMainChain) : chainRows;
  // 主线行读数来源:有当前主线链时用链读数(含轮次),未加载时用主线 stat 读数
  // (副本扫描链路,零重放;tl1415 点10 起扫描带实测轮次 mainTurns,不再是
  // 恒 0 的「—」)。
  const mainMessages = currentMainChain ? currentMainChain.messageCount : group.mainMessages;
  const mainTurns = currentMainChain ? currentMainChain.turns : group.mainTurns;
  const mainModified = currentMainChain ? currentMainChain.lastActivity : group.mainModified;
  const mainBytes = currentMainChain ? currentMainChain.bytes : group.mainBytes;
  // 副本行的「将成为主线」判定(与外层概要行同一预测)。
  const measured = copiesNeedingMeasurement(group).length === 0;
  const winner = measured ? predictedWinner(group) : null;
  // 任务 1123:same path+headId 的行会在链表里出现两次(主转录本体一行 + 它
  // 的某个 head 一行,字段读数相同)——两条标签各只打一次。
  const seenLongerPaths = new Set<string>();
  const seenNewerPaths = new Set<string>();

  return (
    <div className="rc-versions rc-detail">
      <table className="rc-versions__table">
        {/* tl1415 点1+4:合并表自带语义化表头——「条数」「交互轮次」进列名,
            候选链行的裸数字拼列(「5396 · 215 交互轮次 …」)废止。 */}
        <thead>
          <tr>
            <th className="rc-detail__hcheck" aria-label={t("settings.recoveryCopiesColCopies")} />
            <th>{t("settings.recoveryCopiesColLine")}</th>
            <th className="rc-num">{t("settings.sessionVersions.messages")}</th>
            <th className="rc-num">{t("settings.recoveryCopiesColTurns")}</th>
            <th>{t("settings.recoveryCopiesColModified")}</th>
            <th className="rc-num">{t("settings.recoveryCopiesColSize")}</th>
            <th className="rc-num">{t("settings.recoveryCopiesColUnique")}</th>
          </tr>
        </thead>
        <tbody>
          {/* ── 主线行:当前主线链升格为表首(挂 rc-chain,候选链计数的口径
              与原候选链块一致),chains 未加载时以主线 stat 读数先行渲染。 ── */}
          <tr
            className={`rc-detail__row${currentMainChain ? " rc-chain" : ""} rc-detail__row--main${
              winner?.isMain ? " rc-row--winner" : ""
            }`}
          >
            <td className="rc-detail__check" />
            <td className="rc-label">
              {t("settings.recoveryCopiesMain")}
              {winner?.isMain ? ` · ${t("settings.recoveryCopiesWins")}` : ""}
              {!group.mainExists ? ` · ${t("settings.recoveryCopiesOrphan")}` : ""}
              {/* 任务 1039r:降级行必须说自己是降级——否则零计数行与「空会话」
                  在面板上无法区分。悬停 title 给出完整原因。 */}
              {currentMainChain?.degraded && (
                <span className="sv-degraded" title={t("settings.sessionVersions.degradedHint")}>
                  {t("settings.sessionVersions.degraded")}
                </span>
              )}
              {currentMainChain?.selected && (
                <span className="rc-versions__tag">{t("settings.sessionVersions.selected")}</span>
              )}
            </td>
            <td className="rc-num">{mainMessages || "—"}</td>
            <td className="rc-num">{mainTurns || "—"}</td>
            <td>{formatWhen(mainModified)}</td>
            <td className="rc-num">{formatBytes(mainBytes)}</td>
            <td className="rc-num rc-muted">—</td>
          </tr>

          {/* ── 副本组(recovery 文件链路):异源数据视觉并入,分组行保住数据
              源的可辨识性——副本是独立文件,候选链与版本是同一份事件日志的
              读数,两者不强行同质化。 ── */}
          {group.copies.length > 0 && (
            <tr className="rc-detail__group">
              <td colSpan={7}>{t("settings.recoveryCopiesGroupCopies")}</td>
            </tr>
          )}
          {group.copies.map((copy, index) => {
            const wins = winner ? !winner.isMain && winner.copyIndex === index : false;
            // tl1415 点12: 文件名短标识(recovery-<8 hex>)——用户要能把面板行
            // 对回磁盘上的 recovery-5df6de18 文件;悬停 title 给完整文件名。
            const shortName = recoveryShortName(copy.path);
            return (
              <Fragment key={copy.path}>
                <tr className={`rc-detail__row rc-row--copy${wins ? " rc-row--winner" : ""}`}>
                  <td className="rc-detail__check" />
                  <td className="rc-label">
                    {t("settings.recoveryCopiesCopy")} {index + 1}
                    {shortName ? (
                      <span className="rc-copy__shortname" title={copy.path}>
                        {shortName}
                      </span>
                    ) : null}
                    {copy.orphan ? ` · ${t("settings.recoveryCopiesOrphan")}` : ""}
                    {wins ? ` · ${t("settings.recoveryCopiesWins")}` : ""}
                    {/* 任务 1063:副本行的「独有」徽标——分级展示的核心:副本
                        不是又一个版本,它可能是主线没有的唯一一份工作。 */}
                    {copy.scanned && copy.unique > 0 ? (
                      <span className="rc-copy__unique-badge">
                        {t("settings.recoveryCopiesUniqueBadge").replace("{n}", String(copy.unique))}
                      </span>
                    ) : null}
                  </td>
                  <td className="rc-num">{copy.scanned ? copy.messages : "—"}</td>
                  {/* tl1415 点10: 轮次随扫描实测(与条数同一轮重放)。未扫描的
                      副本给「未测量」状态而不是裸「—」;扫描后的 0 是真读数
                      (纯工具转录),照实显示 0。 */}
                  <td className="rc-num">
                    {copy.scanned ? (
                      copy.turns
                    ) : (
                      <span className="rc-muted" title={t("settings.recoveryCopiesUnmeasuredHint")}>
                        {t("settings.recoveryCopiesUnmeasured")}
                      </span>
                    )}
                  </td>
                  <td>{formatWhen(copy.modified)}</td>
                  <td className="rc-num">{formatBytes(copy.bytes)}</td>
                  <td className={copy.unique > 0 ? "rc-unique rc-num" : "rc-muted rc-num"}>
                    {copy.scanned ? copy.unique : "—"}
                  </td>
                </tr>
                <tr className="rc-detail__ops">
                  <td colSpan={7} className="rc-detail__ops-cell">
                    {/* 任务 1064:副本行的复选框+删除入口。孤立副本(orphan,主线
                        已缺失)没有「另一条链可以兜底」的前提,删除即真正丢内容,
                        因此不提供删除入口——它只能被合并(作为新主线)而非删除。 */}
                    {!copy.orphan && (
                      <>
                        <input
                          type="checkbox"
                          checked={branchesSelected.has(copy.path)}
                          disabled={busy}
                          onChange={() => onToggleBranch(copy.path)}
                          aria-label={`${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${index + 1}`}
                        />
                        <button
                          className="btn btn--secondary btn--small rc-delete"
                          type="button"
                          title={t("settings.recoveryCopiesDeleteHint")}
                          disabled={busy}
                          onClick={() => onDeleteBranch(copy.path, index)}
                        >
                          {t("settings.recoveryCopiesDelete")}
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              </Fragment>
            );
          })}

          {/* ── 候选链与版本组(v3 事件日志链路):除当前主线外的全部链。 ── */}
          {(chainLoading || candidateChains.length > 0) && (
            <tr className="rc-detail__group">
              <td colSpan={7}>{t("settings.recoveryCopiesGroupChains")}</td>
            </tr>
          )}
          {/* 任务 1146:链清单重拉期间显示加载占位而不是整块消失——合成/删除
              后的自动刷新会清空链缓存再重读,占位让「正在刷新」可见,链区不
              闪烁。空集(拉取被拒时的降级落点,外层错误横幅已另行报告)不渲染
              占位,不把它误报成「加载中」。 */}
          {chainLoading && (
            <tr className="rc-detail__loading">
              <td colSpan={7}>{t("settings.loading")}</td>
            </tr>
          )}
          {candidateChains.map((c, chainIndex) => {
            // 任务 1123(原 1065 的「建议(更长且更新)」拆成两条独立标签):
            // 「更长」按轮次优先(后端 longestPath/longestHead,已按轮次排序,
            // 全降级时为空 ⇒ 不标)、「更新」按最后活动时间独立计算(后端
            // newestPath/newestHead,同样跳过降级行)。长/新是两个独立事实,
            // 一条链可能只占其一(也可能都占),因此各标各的理由、各带各的
            // 实际值。当前选中行不打标签——它是合成的保留对象。
            const chainKey = `${c.path}|${c.headId}`;
            const seenLonger = seenLongerPaths.has(chainKey);
            const seenNewer = seenNewerPaths.has(chainKey);
            const isLonger = !c.selected && chains?.longestPath === c.path && chains?.longestHead === c.headId && !seenLonger;
            const isNewer = !c.selected && chains?.newestPath === c.path && chains?.newestHead === c.headId && !seenNewer;
            if (isLonger) seenLongerPaths.add(chainKey);
            if (isNewer) seenNewerPaths.add(chainKey);
            const isPicked = pickedChain === c.path;
            return (
              <Fragment key={`${c.path}|${c.headId}|${chainIndex}`}>
                <tr
                  className={`rc-detail__row rc-chain${isLonger ? " rc-chain--recommended" : ""}${isPicked ? " rc-chain--picked" : ""}`}
                >
                  <td className="rc-detail__check" />
                  <td className="rc-label">
                    {/* tl1355:无 DAG 后行身份=文件(headId 恒空)——副本行显示
                        文件名短标识(recovery-<8 hex>,tl1415 点12:用户要能把
                        面板行对回磁盘文件);非副本文件链行显示 headId 前 8 位。 */}
                    {c.headId ? c.headId.slice(0, 8) : recoveryShortName(c.path) || t("settings.recoveryCopiesCopy")}
                    {/* 任务 1039r:降级行必须说自己是降级。 */}
                    {c.degraded && (
                      <span className="sv-degraded" title={t("settings.sessionVersions.degradedHint")}>
                        {t("settings.sessionVersions.degraded")}
                      </span>
                    )}
                    {isLonger && (
                      <span className="rc-versions__tag">
                        {t("settings.sessionVersions.longerTag")
                          .replace("{messages}", String(c.messageCount))
                          .replace("{turns}", String(c.turns))}
                      </span>
                    )}
                    {isNewer && (
                      <span className="rc-versions__tag">
                        {t("settings.sessionVersions.newerTag").replace("{date}", (c.lastActivity || "").slice(0, 10))}
                      </span>
                    )}
                    {isPicked && <span className="rc-chain__picked-badge">{t("settings.recoveryCopiesPickedBadge")}</span>}
                  </td>
                  <td className="rc-num">{c.messageCount}</td>
                  <td className="rc-num">{c.turns}</td>
                  <td>{formatWhen(c.lastActivity)}</td>
                  <td className="rc-num">{formatBytes(c.bytes)}</td>
                  <td className="rc-num rc-muted">—</td>
                </tr>
                <tr className="rc-detail__ops">
                  <td colSpan={7} className="rc-detail__ops-cell">
                    {/* The head's own preview text: what this chain would leave the
                        conversation looking like, which is the thing a user needs in
                        order to choose. Without it the list showed numbers only.
                        tl1415 点11: 降级行(预算外/解析拒)的预览不可得是**扫描状态**,
                        不冒充「该分支无预览文本」(数据缺席)——两种空态分开说。 */}
                    <span className="rc-chain__preview rc-chain__name" title={c.preview || undefined}>
                      {c.preview?.trim()
                        ? c.preview
                        : c.degraded
                          ? t("settings.recoveryCopiesPreviewUnreadable")
                          : t("settings.recoveryCopiesPreviewEmpty")}
                    </span>
                    <button
                      className="btn btn--secondary btn--small rc-chain__preview-btn"
                      type="button"
                      title={t("settings.recoveryCopiesPreviewBtnHint")}
                      disabled={busy || scanningAll}
                      onClick={() => onPick(c)}
                    >
                      {previewingPath === c.path
                        ? t("settings.recoveryCopiesPreviewBuildingShort")
                        : isPicked
                          ? t("settings.recoveryCopiesUnpick")
                          : t("settings.recoveryCopiesPreviewBtn")}
                    </button>
                    {!c.selected && (
                      <button
                        className="btn btn--secondary btn--small"
                        type="button"
                        title={t("settings.sessionVersionsActivateHint")}
                        disabled={repairing}
                        onClick={() => void activate(c.headId)}
                      >
                        {t("settings.sessionVersions.activate")}
                      </button>
                    )}
                    {/* 任务 1064:非当前版本行加删除入口——当前选中版本不可选不
                        可删(后端也拒绝),删除走确认+回收语义。降级行没有 head
                        身份可言,不提供删除。 */}
                    {!c.selected && !c.degraded && c.headId && (
                      <button
                        className="btn btn--secondary btn--small rc-delete"
                        type="button"
                        title={t("settings.recoveryCopiesDeleteHeadHint")}
                        disabled={repairing || deleting !== null}
                        onClick={() => void deleteHead(c.headId)}
                      >
                        {deleting === c.headId ? "…" : t("settings.recoveryCopiesDelete")}
                      </button>
                    )}
                  </td>
                </tr>
              </Fragment>
            );
          })}
        </tbody>
      </table>
      <div className="rc-versions__actions">
        {/* 任务 1065:合成入口与进度放表下操作区——它需要用户先在链行里选定
            HEAD(行内预览+选中徽标),行内按钮只负责「把这一行设为 HEAD 并预
            览」,一键合成是整表级操作(1.34 的「选 HEAD → 反向合成最长链」语
            义:合成针对整条会话,不是单行)。 */}
        <button
          className="btn btn--secondary btn--small"
          type="button"
          title={t("settings.recoveryCopiesSynthesizeBtnHint")}
          disabled={busy || scanningAll || Boolean(synthesizing)}
          onClick={onSynthesize}
        >
          {synthesizing
            ? `${t("settings.recoveryCopiesSynthesizing")} ${synthesizing}`
            : t("settings.recoveryCopiesSynthesizeBtn")}
        </button>
        {pickedChain ? (
          <span className="rc-chains__synth-hint">{t("settings.recoveryCopiesSynthesizeHeadHint")}</span>
        ) : (
          <span className="rc-chains__synth-hint">{t("settings.recoveryCopiesSynthesizePickHint")}</span>
        )}
        <button className="btn btn--secondary btn--small" type="button" title={t("settings.sessionVersionsCheckHint")} onClick={() => void inspect()}>{t("settings.sessionVersions.check")}</button>
        <button className="btn btn--secondary btn--small" type="button" title={t("settings.sessionVersionsRepairHint")} disabled={repairing} onClick={() => void repair(false)}>{t("settings.sessionVersions.repair")}</button>
        <button className="btn btn--secondary btn--small" type="button" title={t("settings.sessionVersionsRepairDietHint")} disabled={repairing} onClick={() => void repair(true)}>{t("settings.sessionVersions.repairDiet")}</button>
        {/* tl1415 点9: 清空合并历史——版本徽标的账面读数(head meta 镜像)在
            tl1280 头重放退役后无人维护,合并完的会话留着陈旧「N 版本」。展开
            后才可见,只清账面不动内容。 */}
        {headCount > 1 && (
          <button
            className="btn btn--secondary btn--small"
            type="button"
            title={t("settings.recoveryCopiesClearHistoryHint")}
            disabled={busy}
            onClick={onClearHistory}
          >
            {t("settings.recoveryCopiesClearHistory")}
          </button>
        )}
      </div>
      {health.path === mainPath && health.report && (
        <div className="rc-versions__health">
          {health.report.healthy ? (
            <div>{t("settings.sessionVersions.healthy")}</div>
          ) : (
            <ul className="rc-versions__issues">
              {(health.report.issues ?? []).map((issue) => (
                <li key={issue.code}>
                  <code>{issue.code}</code> {issue.detail}
                  {issue.suggestedAction && <> → {issue.suggestedAction}</>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {repairNote && <div className="rc-versions__health">{repairNote}</div>}
      {records.length > 0 && (
        <div className="rc-versions__health">
          <div>{t("settings.sessionVersions.repairLog")}</div>
          <ul className="rc-versions__issues">
            {records.map((r, i) => (
              <li key={i}>
                {formatWhen(r.at)} {r.before} → {r.after}
                {r.backupDir ? ` · ${r.backupDir}` : ""}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

export function RecoveryCopiesSection() {
  const t = useT();
  const { showToast } = useToast();
  const { confirm, dialog } = useConfirmDialog();
  const [open, setOpen] = useState(false);
  const [groups, setGroups] = useState<RecoveryCopyGroupView[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [scanning, setScanning] = useState<Set<string>>(new Set());
  // 队尾体积列修复回执：放在分区层（而非行内组件状态）——行随重载折叠时其本
  // 身的状态会随子树卸载而丢，回执读不到是实测根因；分区层只有一份，切换会话
  // 时由行的展开动作清空。
  const [repairNote, setRepairNote] = useState("");
  const [busy, setBusy] = useState(false);
  // previewingPath names the chain whose preview is being built. Without it the
  // only feedback was every button greying out, which reads as a dead click
  // until the dialog finally appears.
  const [previewingPath, setPreviewingPath] = useState<string | null>(null);
  const [status, setStatus] = useState("");
  const [outcomes, setOutcomes] = useState<Outcome[]>([]);
  // 任务 1064：删除结果单独留一份——「已删除 N 条」与合并结果的词汇不同
  // （merged/forced 会把删除误报成内容搬迁），两条结果线各自成列。
  const [deletedOutcomes, setDeletedOutcomes] = useState<DeleteOutcome[]>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const [failed, setFailed] = useState(false);
  // 任务 1039：失败原因单独留一份——只显示「设置加载失败」时用户与日志都
  // 无从判断是哪条链路断了（实测 49MB events 日志重放被拒）。
  const [failedReason, setFailedReason] = useState("");
  // chains holds each expanded conversation's candidate chains, keyed by main path.
  // Loading them replays every log for that conversation, so it happens when a row
  // is opened rather than for the whole list at once.
  const [chains, setChains] = useState<Map<string, RecoveryChainSet>>(new Map());
  // versionGroups is the v3 events-log view of the same directories (任务 1011
  // 需求 E), fetched alongside the copies list so the two readings of one
  // conversation can be merged into the same row (任务 1063).
  const [versionGroups, setVersionGroups] = useState<Map<string, SessionVersionGroupView>>(new Map());
  // 任务 1146：dataVersion 是全量刷新的代际号——load() 每成功一轮就自增。
  // VersionRows 的加载 effect 只看 mainPath 的话，行保持展开时（本件起 load()
  // 不再折叠行）切换/删除版本后的重拉没有触发条件；把这个号传进去，列表数据
  // 每换一代，版本表就跟着重读一次。
  const [dataVersion, setDataVersion] = useState(0);
  // pickedChain names the branch the user chose to win, keyed by main path. Empty
  // means "let the engine take the fullest one". Keyed per conversation so a pick
  // in one group never leaks into another.
  const [pickedChain, setPickedChain] = useState<Record<string, string>>({});
  const pickedChainCount = Object.values(pickedChain).filter(Boolean).length;
  // 任务 1065：选 HEAD 反向合成——pickedChain（HEAD）+ 合成结果/进度各自成
  // 线：合成中报「正在合成第 N/M 条」，完成后报合成前后读数（并集可核对）。
  const [synthesizing, setSynthesizing] = useState<Record<string, string>>({});
  const [synthesisOutcomes, setSynthesisOutcomes] = useState<SynthesisOutcome[]>([]);
  // Tail reveal: the summary carries ~12k chars of trailing messages; the first
  // Clicking a chain previews it in a wide dialog - sizes, how it starts and
  // ends, and what picking it keeps or drops against the main - and only a
  // confirm inside that dialog names it as the winner. Clicking the picked
  // chain again un-picks it. The main chain is pickable too: it means keep the
  // current main and archive the rest. Nothing is written on preview.
  // The tail-reveal state lives inside ChainPreviewBody: ConfirmDialog keeps
  // the message element it was handed, so state owned out here was invisible
  // to the rendered dialog.
  const togglePick = async (mainPath: string, chain: RecoveryChainView) => {
    if (pickedChain[mainPath] === chain.path) {
      setPickedChain((current) => {
        const next = { ...current };
        delete next[mainPath];
        return next;
      });
      return;
    }
    let preview: RecoveryChainPreview;
    setBusy(true);
    setPreviewingPath(chain.path);
    setStatus(t("settings.recoveryCopiesPreviewBuilding"));
    try {
      preview = await app.PreviewRecoveryChain(mainPath, chain.path);
    } catch (err) {
      setBusy(false);
      setPreviewingPath(null);
      setStatus("");
      // Surface preview failures where the click happened - burying them in the
      // page-level error list is why "some branches do nothing on click" read as
      // a dead button instead of a diagnosable failure.
      const message = err instanceof Error ? err.message : String(err);
      await confirm({
        title: t("settings.recoveryCopiesPreviewFailedTitle"),
        message: (
          <div>
            <p>{t("settings.recoveryCopiesPreviewFailedLead")}</p>
            <p className="rc-outcome__reasons">{message}</p>
          </div>
        ),
        confirmLabel: t("common.close"),
        cancelLabel: t("common.cancel"),
        tone: "danger",
      });
      return;
    }
    setBusy(false);
    setPreviewingPath(null);
    setStatus("");
    const ok = await confirm({
      title: t("settings.recoveryCopiesPreviewTitle"),
      wide: true,
      message: <ChainPreviewBody preview={preview} />,
      confirmLabel: preview.isMain
        ? t("settings.recoveryCopiesPickKeepMain")
        : t("settings.recoveryCopiesPickConfirm"),
      cancelLabel: t("common.cancel"),
    });
    if (ok) {
      setPickedChain((current) => ({ ...current, [mainPath]: chain.path }));
    }
  };

  const load = useCallback(async () => {
    setFailed(false);
    setFailedReason("");
    try {
      // 任务 1063：两个列表合并拉取——v3 版本列表（meta sidecar 读数，零重放）
      // 与副本扫描并存，合并成同一行。任何一侧失败都不拖垮另一侧：副本列表
      // 是本分区的主体，v3 读数是叠加的可见性，拿不到时行内只缺版本徽标。
      //
      // 两个调用刻意**串行**（不是 Promise.all）：两个 reject 的 Promise 在同一
      // 事件循环 tick 里被同一个 try/catch 消费时，Node 的 unhandledRejection
      // 判定会先于 catch 链接成立——第二个 rejection 在 catch 附着前就已被标记，
      // 进程级 handler 收到它（实测 harness 场景：ListRecoveryCopyGroups
      // reject 时整个测试进程被这条「已处理」的 rejection 打崩）。串行 await
      // 让每个 rejection 在下一个调用发出前就被消费，消除这个窗口。
      //
      // v3 列表用 typeof 守卫而不是 .catch：bridge 的 dev mock 没实现这个方法，
      // 直接调用会抛「not a function」（同步 throw，不是 Promise rejection），
      // .catch 拦不住它——旧 harness 的 Partial stub 正是这个形态。
      const list = await app.ListRecoveryCopyGroups();
      const versions =
        typeof app.ListSessionVersionGroups === "function"
          ? await app.ListSessionVersionGroups().catch(() => [] as SessionVersionGroupView[])
          : ([] as SessionVersionGroupView[]);
      setGroups(list);
      setVersionGroups(new Map((versions ?? []).map((g) => [g.mainPath, g])));
      setSelected(new Set());
      // 任务 1146：候选链缓存一并作废。合成/删除/合并之后链清单已经变了，但
      // 这份 Map 只在行展开时写入：load() 不清它，惰性加载 effect 的
      // chains.has(path) 守卫就会命中合成前的旧缓存而跳过重拉——这正是用户
      // 实测「合成已生效，候选链区仍显示两条旧链」的根因。清空后仍展开的行
      // 由下方 effect 立即重拉，折叠中的行在重新展开时重拉。
      setChains(new Map());
      setChainLoads(new Set());
      setDataVersion((v) => v + 1);
    } catch (e) {
      setGroups(null);
      setFailed(true);
      // 任务 1039：把后端原因带进横幅——「设置加载失败」单独出现时无法区分
      // 是 bridge 断、后端拒还是重放超限，排查只能靠猜。
      setFailedReason(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    if (open) {
      // 任务 1146：默认折叠是「打开面板」的语义（Collapsed by default: the
      // summary line answers "which conversation has copies", and the per-copy
      // detail only matters once one is chosen），不是每次数据刷新的副作用。
      // 放在这里而非 load()：面板内合成/删除/合并/手动刷新触发的 load() 保持
      // 行展开，候选链与版本表在原地重拉，用户不再需要重新展开行或重开面板
      // 才能看到操作后的状态。
      setExpanded(new Set());
      void load();
    }
  }, [open, load]);

  // 任务 1085：v3-only 会话补行——groups（recovery 副本扫描）只含「有副本文件
  // 的会话」，v3 多 heads 会话若没有副本文件就不在其中，整行连同 1064 删除/
  // 1065 合成入口一起消失。这里把 versionGroups 中缺席的会话合成「无副本」的
  // 占位行（copies: []），使整行（含 v3 版本徽标与展开后的链表块/VersionRows
  // 的全部入口）照常渲染。mainBytes/mainModified 为 0/""——副本扫描链路从不
  // 读这类会话的 stat，不编造数字，行内照实显示「—」。
  const composedGroups = useMemo(() => {
    const list = groups ?? [];
    const seen = new Set(list.map((g) => g.mainPath));
    const extras: RecoveryCopyGroupView[] = [];
    for (const [mainPath, versionGroup] of versionGroups) {
      if (seen.has(mainPath)) continue;
      extras.push({
        mainPath,
        mainLabel: versionGroup.mainLabel,
        directory: versionGroup.directory,
        mainExists: true,
        mainMessages: 0,
        mainTurns: 0,
        mainBytes: 0,
        mainModified: "",
        copies: [],
      });
    }
    return extras.length > 0 ? [...list, ...extras] : list;
  }, [groups, versionGroups]);

  const phased = useMemo(() => {
    const pending: RecoveryCopyGroupView[] = [];
    const unmerged: RecoveryCopyGroupView[] = [];
    const merged: RecoveryCopyGroupView[] = [];
    for (const group of composedGroups) {
      const phase = phaseOf(group);
      if (phase === "pending") pending.push(group);
      else if (phase === "unmerged") unmerged.push(group);
      else merged.push(group);
    }
    return { pending, unmerged, merged };
  }, [composedGroups]);

  /** redundantCopies is what the tl1415 点8 one-click cleanup targets: measured,
   *  non-orphan copies whose every event the main transcript already holds
   *  (unique = 0) — the residue a synthesis leaves behind once the branches
   *  have been folded into a single long chain. Unmeasured or divergent copies
   *  never qualify, and the button disappears entirely when nothing does. */
  const redundantCopies = useMemo(
    () =>
      composedGroups.flatMap((group) =>
        group.copies
          .filter((copy) => !copy.orphan && copy.scanned && copy.unique === 0)
          .map((copy) => ({ group, copy })),
      ),
    [composedGroups],
  );

  const toggle = (path: string) =>
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  /** loadChains replays one conversation's logs to list its candidate chains. A
   *  merge promotes the chain with the most messages, so the list is what tells the
   *  user which transcript they are about to be left with. Failure is recorded as
   *  a loaded-but-empty inventory so the effect below does not retry in a loop;
   *  the error banner carries the reason.
   *
   *  tl1415 点3:展开区合并表只认这一份链清单(原 VersionRows 自己重放的第二
   *  份已随合并删除)。枚举被拒时落 degradedChainSet(与原版本表的降级同语
   *  义:一行 Degraded + 原因横幅),而不是空集——空集会让「候选链与版本」组
   *  整组消失,降级读数无处显示。 */
  const loadChains = useCallback(async (mainPath: string) => {
    try {
      const set = await app.ListRecoveryChains(mainPath);
      // 任务 1039r2:后端 nil 切片会以 null 到达(全候选被非限额错误跳过时
      // chains 为 null)——落进 state 前归一成空数组,渲染树的 .length 读数
      // 才有定义(实测 chains:null 直通让分区横幅报 null.length)。
      setChains((current) => new Map(current).set(mainPath, { ...set, chains: set.chains ?? [] }));
    } catch (err) {
      setChains((current) => new Map(current).set(mainPath, degradedChainSet(mainPath)));
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
    }
  }, []);

  // Chain inventory is lazy: it replays every log for the conversation, so it
  // loads when a row becomes expanded - however that happened. Scanning
  // expands rows programmatically (scanOne), and before this effect only the
  // manual toggle loaded the inventory, which is why scanned rows showed no
  // candidate chains at all. One effect keeps "expanded" and "chains loaded"
  // in step for both paths; chainLoads guards against concurrent repeats.
  const [chainLoads, setChainLoads] = useState<Set<string>>(new Set());
  useEffect(() => {
    for (const path of expanded) {
      if (chains.has(path) || chainLoads.has(path)) continue;
      setChainLoads((current) => new Set(current).add(path));
      void loadChains(path).finally(() => {
        setChainLoads((current) => {
          const next = new Set(current);
          next.delete(path);
          return next;
        });
      });
    }
  }, [expanded, chains, chainLoads, loadChains]);

  const toggleExpanded = (path: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  };

  /** tl1415 点9: 已合入默认折叠——合并/合成把一个会话收成单一主线后,它的
   *  展开区使命已完成,行折回概要一行(点击可再展开);面板重开时所有行本就
   *  默认折叠,这里是操作后的即时回落。 */
  const collapseRow = (path: string) => {
    setExpanded((current) => {
      const next = new Set(current);
      next.delete(path);
      return next;
    });
  };

  /** tl1415 点9: 清空合并历史——meta 头镜像复位为单链(账面重置,内容零触
   *  碰)。确认、调用、toast、重载收在这一处,VersionRows 只拿回调。 */
  const clearMergeHistory = async (mainPath: string, label: string) => {
    const ok = await confirm({
      title: t("settings.recoveryCopiesClearHistoryTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesClearHistoryLead")}：{label}
          </p>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesClearHistoryNote")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesClearHistoryConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      await app.ClearSessionMergeHistory(mainPath);
      showToast(t("settings.recoveryCopiesClearHistoryDone"), "info");
      await load();
    } catch (err) {
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
    } finally {
      setBusy(false);
    }
  };

  /** scanOne measures a conversation and returns the fresh row, so callers that
   *  need the numbers immediately do not have to wait for a re-render. */
  const scanOne = async (mainPath: string): Promise<RecoveryCopyGroupView | null> => {
    setScanning((current) => new Set(current).add(mainPath));
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
      // Scanning is the moment the detail becomes worth reading, so open the row.
      setExpanded((current) => new Set(current).add(mainPath));
      return fresh;
    } catch (err) {
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
      return null;
    } finally {
      setScanning((current) => {
        const next = new Set(current);
        next.delete(mainPath);
        return next;
      });
    }
  };

  const scanAll = async () => {
    setErrors([]);
    // 任务 1085：只扫真有副本的行——v3-only 占位行（copies 为空）没有可测的
    // 副本文件，ScanRecoveryCopyGroup 对它只会空跑一轮目录扫描。
    // tl1355 协调线输入③：多会话测量是串行循环，逐个报「第 N / 共 M 个」——
    // 复用合并/合成进度同一状态行，不新增 UI 面。
    const targets = composedGroups.filter((g) => g.copies.length > 0);
    for (const [index, group] of targets.entries()) {
      setStatus(`${t("settings.recoveryCopiesMeasuring")} ${index + 1}/${targets.length} · ${group.mainLabel}`);
      await scanOne(group.mainPath);
    }
    setStatus("");
  };

  // Scan only the rows the user has checked. On a project with dozens of
  // conversation groups the full sweep replays every log of every session; the
  // checked subset is the one the user is actually deciding about.
  const scanSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;
    setErrors([]);
    for (const [index, group] of chosen.entries()) {
      setStatus(`${t("settings.recoveryCopiesMeasuring")} ${index + 1}/${chosen.length} · ${group.mainLabel}`);
      await scanOne(group.mainPath);
    }
    setStatus("");
  };

  const winnerText = (group: RecoveryCopyGroupView) => {
    const winner = predictedWinner(group);
    if (!winner) return "—";
    return winner.isMain
      ? `${t("settings.recoveryCopiesWinnerMain")} ${winner.turns}`
      : `${t("settings.recoveryCopiesCopy")} ${winner.copyIndex + 1} ${winner.turns}`;
  };

  // tl1415 点8: 清理多余副本——把主线已完整覆盖(unique=0)的副本逐份走
  // DeleteRecoveryBranch 的回收语义(可恢复 .trash;打开中的副本后端拒绝,
  // 跳过并在结果里说明)。确认框列出会话与份数,结果给 toast。
  const [cleaningUp, setCleaningUp] = useState(false);
  const cleanupRedundantCopies = async () => {
    const targets = redundantCopies;
    if (targets.length === 0) return;
    const perGroup = new Map<string, number>();
    for (const { group } of targets) {
      perGroup.set(group.mainLabel, (perGroup.get(group.mainLabel) ?? 0) + 1);
    }
    const ok = await confirm({
      title: t("settings.recoveryCopiesCleanupTitle"),
      message: (
        <div>
          <p>{t("settings.recoveryCopiesCleanupLead")}</p>
          <ul className="rc-versions__issues">
            {Array.from(perGroup.entries()).map(([label, count]) => (
              <li key={label}>
                {label} · ×{count}
              </li>
            ))}
          </ul>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesCleanupNote")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesCleanupConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    setCleaningUp(true);
    setErrors([]);
    try {
      let archived = 0;
      const failures: string[] = [];
      for (const { copy } of targets) {
        try {
          await app.DeleteRecoveryBranch(copy.path);
          archived += 1;
        } catch (err) {
          failures.push(`${copy.label}: ${err instanceof Error ? err.message : String(err)}`);
        }
      }
      if (failures.length === targets.length) {
        showToast(t("settings.recoveryCopiesCleanupFailed"), "error");
      } else {
        showToast(t("settings.recoveryCopiesCleanupDone", { n: archived }), "info");
      }
      if (failures.length > 0) {
        setErrors((current) => [...current, ...failures]);
      }
      await load();
    } finally {
      setBusy(false);
      setCleaningUp(false);
      setStatus("");
    }
  };

  const mergeSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;

    setBusy(true);
    setErrors([]);
    setOutcomes([]);
    try {
      // Measure anything unmeasured here rather than sending the user back to press
      // Scan. The preview is built out of these counts, so it cannot be shown first.
      const measured: RecoveryCopyGroupView[] = [];
      for (const group of chosen) {
        if (copiesNeedingMeasurement(group).length > 0) {
          setStatus(`${t("settings.recoveryCopiesMeasuring")} ${group.mainLabel}`);
          const fresh = await scanOne(group.mainPath);
          measured.push(fresh ?? group);
        } else {
          measured.push(group);
        }
      }
      setStatus("");

      // The preview states the change, not just the inputs: which transcript is
      // left after the merge, its turn count against the current main's, and how
      // much unique work would be folded away.
      const totalUnique = measured.reduce((n, g) => n + uniqueIn(g), 0);
      const totalCopies = measured.reduce((n, g) => n + g.copies.length, 0);
      const ok = await confirm({
        title: t("settings.recoveryCopiesMerge"),
        message: (
          <div>
            <p>
              {t("settings.recoveryCopiesPreviewLead")}：{measured.length} ·{" "}
              {t("settings.recoveryCopiesPreviewCopies")} {totalCopies} ·{" "}
              {t("settings.recoveryCopiesUnique")} {totalUnique}
            </p>
            <table className="rc-preview-table">
              <thead>
                <tr>
                  <th>{t("settings.recoveryCopiesColLine")}</th>
                  <th>{t("settings.recoveryCopiesColTurns")}</th>
                  <th>{t("settings.recoveryCopiesColWinner")}</th>
                  <th>{t("settings.recoveryCopiesColUnique")}</th>
                </tr>
              </thead>
              <tbody>
                {measured.map((g) => (
                  <tr key={g.mainPath}>
                    <td>{g.mainLabel}</td>
                    <td>{g.mainMessages || "—"}</td>
                    <td>{winnerText(g)}</td>
                    <td>{uniqueIn(g)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="rc-preview__warn">{t("settings.recoveryCopiesPreviewWarn")}</p>
          </div>
        ),
        confirmLabel: t("settings.recoveryCopiesMerge"),
        cancelLabel: t("common.cancel"),
        tone: "danger",
      });
      if (!ok) return;

      const collected: Outcome[] = [];
      // Report per conversation as it finishes. A merge over a dozen copies takes long
      // enough that a silent run reads as "nothing happened", and the per-conversation
      // result is what tells the user whether it worked.
      for (const [index, group] of measured.entries()) {
        setStatus(
          `${t("settings.recoveryCopiesMerging")} ${index + 1}/${measured.length} · ${group.mainLabel}`,
        );
        let report: ConsolidationReport;
        try {
          // Task 203: refuse to merge a conversation two tabs are actively
          // writing — the merge would race both writers and bury the fork the
          // user is trying to resolve. Block this group and move on; the
          // outcome row explains why.
          if (await app.IsConversationDualOpen(group.mainPath)) {
            collected.push({
              label: group.mainLabel,
              kind: "blocked",
              detail: t("settings.recoveryCopiesDualOpen"),
            });
            setOutcomes([...collected]);
            continue;
          }
          report = await app.ConsolidateSessionRecoveryCopies(group.mainPath, pickedChain[group.mainPath] ?? "");
        } catch (err) {
          collected.push({
            label: group.mainLabel,
            kind: "failed",
            detail: err instanceof Error ? err.message : String(err),
          });
          setOutcomes([...collected]);
          continue;
        }

        if (report.blockedByDivergence) {
          // The backend refuses when the main and the fullest copy each hold turns
          // the other lacks. Ask before letting the copy win, and say what that
          // costs: the current main is archived whole and stays recoverable.
          setBusy(false);
          const winner = predictedWinner(group);
          const forceOk = await confirm({
            title: t("settings.recoveryCopiesForceTitle"),
            message: (
              <div>
                <p>
                  {t("settings.recoveryCopiesForceLead")}：{group.mainLabel}
                </p>
                <p>
                  {t("settings.recoveryCopiesForceTurns")} {group.mainMessages || "—"} →{" "}
                  {winner?.turns || "—"}
                </p>
                <p className="rc-preview__warn">{t("settings.recoveryCopiesForceWarn")}</p>
              </div>
            ),
            confirmLabel: t("settings.recoveryCopiesForceConfirm"),
            cancelLabel: t("common.cancel"),
            tone: "danger",
          });
          setBusy(true);
          if (!forceOk) {
            collected.push({ label: group.mainLabel, kind: "blocked", notCovered: uniqueIn(group) });
            setOutcomes([...collected]);
            continue;
          }
          try {
            const forced = await app.ForceConsolidateSessionRecoveryCopies(group.mainPath, pickedChain[group.mainPath] ?? "");
            // tl1415 点9: 已合入默认折叠——合并成功,展开区使命完成,行折回。
            collapseRow(group.mainPath);
            collected.push({
              label: group.mainLabel,
              kind: "forced",
              before: forced.mainMessageCount,
              after: forced.winnerMessageCount,
              trashed: forced.trashed?.length ?? 0,
              unloadable: forced.skippedUnloadable?.length ?? 0,
              prefixed: forced.prefixed ?? 0,
            });
          } catch (err) {
            collected.push({
              label: group.mainLabel,
              kind: "failed",
              detail: err instanceof Error ? err.message : String(err),
            });
          }
          setOutcomes([...collected]);
          continue;
        }

        // tl1415 点9: 已合入默认折叠——合并成功,行折回概要一行(点击可再展开)。
        collapseRow(group.mainPath);
        collected.push({
          label: group.mainLabel,
          kind: "merged",
          before: report.mainMessageCount,
          after: report.winnerMessageCount,
          trashed: report.trashed?.length ?? 0,
          notCovered: report.notCoveredDetail?.filter((d) => d.unique > 0).length ?? 0,
          unloadable: report.skippedUnloadable?.length ?? 0,
          prefixed: report.prefixed ?? 0,
          reasons: report.notCoveredDetail?.map((d) => d.reason).filter((r): r is string => Boolean(r)),
        });
        setOutcomes([...collected]);
      }

      setOutcomes(collected);
      // tl1401 反馈对称：面板内的 outcome 行只有展开存储页才可见，用户实测
      // 「合成最长链后没有任何弹窗反馈」。成功给 toast（条数=合入主线的增量
      // 轮数，多组求和），失败同样给 toast——失败路径原本有行无弹窗，与成功
      // 静默是同一个「读作没发生」的问题。blocked 是明确的业务原因（双重打开
      // /用户取消强制），不算失败也不抢 toast。
      const mergedOutcomes = collected.filter((o) => o.kind === "merged" || o.kind === "forced");
      if (mergedOutcomes.length > 0) {
        const grafted = mergedOutcomes.reduce((sum, o) => sum + Math.max(0, (o.after ?? 0) - (o.before ?? 0)), 0);
        showToast(t("settings.recoveryCopiesMergedToast", { count: grafted }), "info");
      }
      const failedOutcomes = collected.filter((o) => o.kind === "failed");
      if (failedOutcomes.length > 0) {
        showToast(t("settings.recoveryCopiesFailedToast", { count: failedOutcomes.length }), "error");
      }
      setSelected(new Set());
      // Named winners were consumed by the merge just run; the picked branches
      // no longer exist as copies, so the next run starts neutral.
      setPickedChain({});
      await load();
    } finally {
      setStatus("");
      setBusy(false);
    }
  };

  const scanningAll = scanning.size > 0;
  const selectedGroups = (groups ?? []).filter((g) => selected.has(g.mainPath));
  const needingCount = selectedGroups.reduce((n, g) => n + copiesNeedingMeasurement(g).length, 0);
  const anyMerged = outcomes.some((o) => o.kind === "merged" || o.kind === "forced");

  // 任务 1064：复选框批量选中——勾选的是**分支**（副本文件），不是会话行本身。
  // 会话行左侧的复选框仍是「勾选要合并的会话」，两套选择互不干扰：批量删除
  // 只看 branchesSelected。当前版本（主线转录本）永远不在候选里——它没有
  // 「分支」身份可删（1064 安全约束①的前端面，后端 DeleteRecoveryBranch 也
  // 会拒绝非副本路径）。
  const [branchesSelected, setBranchesSelected] = useState<Set<string>>(new Set());
  const toggleBranch = (path: string) =>
    setBranchesSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  /** deleteBranches archives the checked copy branches in one confirmation. The
   *  backend moves each into the recoverable .trash (no coverage proof required:
   *  the user has seen each branch's reading and decided), so the batch is one
   *  list of paths with per-path error collection - a single unreadable branch
   *  must not abort the rest. The outcomes reuse the panel's existing result
   *  vocabulary: "merged" reads as success-with-counts elsewhere, so a dedicated
   *  kind keeps the delete result honest ("deleted", not "merged"). */
  const deleteBranches = async () => {
    const chosen = (groups ?? [])
      .flatMap((g) => g.copies.map((c) => ({ path: c.path, label: `${g.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${g.copies.indexOf(c) + 1}` })))
      .filter((c) => branchesSelected.has(c.path));
    if (!chosen.length) return;
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteBatchTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteBatchLead")}：{chosen.length}
          </p>
          <ul className="rc-versions__issues">
            {chosen.map((c) => (
              <li key={c.path}>{c.label}</li>
            ))}
          </ul>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesDeleteBatchWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesDeleteConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToDelete) return;
    setBusy(true);
    setErrors([]);
    const collected: DeleteOutcome[] = [];
    for (const [index, item] of chosen.entries()) {
      setStatus(`${t("settings.recoveryCopiesDeleting")} ${index + 1}/${chosen.length} · ${item.label}`);
      try {
        await app.DeleteRecoveryBranch(item.path);
        collected.push({ label: item.label, kind: "deleted" });
      } catch (err) {
        collected.push({ label: item.label, kind: "failed", detail: err instanceof Error ? err.message : String(err) });
      }
      setDeletedOutcomes([...collected]);
    }
    setBranchesSelected(new Set());
    // 任务 1146：与合成路径同理——列表重取完成后再释放 busy，删除期间底部
    // 保持加载指示，避免「已删除回执 + 旧列表」并存的静止中间态。
    try {
      await load();
    } finally {
      setBusy(false);
      setStatus("");
    }
  };

  /** deleteBranchOne is the 任务 1064 single-copy delete. tl1415 点3:抽出为
   *  具名函数——合并表把副本行的渲染移进了 VersionRows,删除动作统一走外层
   *  回调,不再内联在行 JSX 里。语义原样:确认框 → DeleteRecoveryBranch(回
   *  收语义)→ 结果行 → 列表重取完成后才释放 busy(与批量删除同口径)。 */
  const deleteBranchOne = async (group: RecoveryCopyGroupView, copyPath: string, index: number) => {
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteLead")}：{group.mainLabel} · {t("settings.recoveryCopiesCopy")} {index + 1}
          </p>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesDeleteWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesDeleteConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToDelete) return;
    setBusy(true);
    try {
      await app.DeleteRecoveryBranch(copyPath);
      setDeletedOutcomes((current) => [
        ...current,
        { label: `${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${index + 1}`, kind: "deleted" },
      ]);
    } catch (err) {
      setDeletedOutcomes((current) => [
        ...current,
        {
          label: `${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${index + 1}`,
          kind: "failed",
          detail: err instanceof Error ? err.message : String(err),
        },
      ]);
    } finally {
      // 任务 1146:列表重取完成后再释放 busy(与合成/批量删除路径同口径)。
      await load();
      setBusy(false);
    }
  };

  /** deleteConversations is the 1368 whole-conversation clear: each checked
   *  conversation (its main transcript, every version inside the event log,
   *  every recovery copy and all sidecars) moves into the two recoverable
   *  trashes in one backend call. The panel's contribution is the same as the
   *  other destructive actions here: one confirmation naming what leaves, a
   *  per-conversation outcome line, and a reload so the rows disappear from
   *  the panel (and the sidebar, via the backend's tree refresh) at once. */
  const deleteConversations = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteConversationTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteConversationLead")}：{chosen.length}
          </p>
          <ul className="rc-versions__issues">
            {chosen.map((g) => (
              <li key={g.mainPath}>
                {g.mainLabel}
                {g.copies.length > 0 ? ` · ×${g.copies.length}` : ""}
              </li>
            ))}
          </ul>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesDeleteConversationWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesDeleteConversation"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToDelete) return;
    setBusy(true);
    setErrors([]);
    const collected: DeleteOutcome[] = [];
    for (const [index, group] of chosen.entries()) {
      setStatus(`${t("settings.recoveryCopiesDeleting")} ${index + 1}/${chosen.length} · ${group.mainLabel}`);
      try {
        const report = await app.DeleteConversationWithCopies(group.mainPath);
        const template = report.mainTrashed
          ? t("settings.recoveryCopiesOutcomeConversationDeleted")
          : t("settings.recoveryCopiesOutcomeConversationCopiesOnly");
        collected.push({
          label: group.mainLabel,
          kind: "conversation",
          detail: template.replace("{copies}", String(report.copiesArchived)),
        });
      } catch (err) {
        collected.push({
          label: group.mainLabel,
          kind: "failed",
          // tl1367-fix defect C: the busy family renders localized (holder
          // block) instead of the raw English sentence.
          detail: formatSessionBusyError(err),
        });
      }
      setDeletedOutcomes([...collected]);
    }
    setSelected(new Set());
    // 任务 1146 同口径：列表重取完成后再释放 busy，删除期间底部保持加载指示。
    try {
      await load();
    } finally {
      setBusy(false);
      setStatus("");
    }
  };

  /** synthesizeHeads is the 1065 one-key operation: given the HEAD the user
   *  picked (or the current main when nothing is picked), fold every other
   *  branch's unique events into that one chain. The panel's contribution over
   *  the bare backend call is the progress line (participants read before the
   *  run) and the outcome record (participants / unique total / new main's
   *  readings), which is what makes "总条数≈并集" checkable on screen.
   *
   *  The 1.34 merge rules ride along untouched: the chain list already carries
   *  the turn-priority recommendation (轮次优先, backend longestHead) and each
   *  chain row offers the tail preview that lets a user judge which branch to
   *  keep as the HEAD before pressing synthesize. */
  const synthesizeHeads = async (mainPath: string, groupLabel: string) => {
    const headId = pickedChain[mainPath] ?? "";
    const participants = (chains.get(mainPath)?.chains ?? []).filter(
      (c) => !(c.path === mainPath && c.headId === (headId || "")),
    );
    if (participants.length === 0) {
      setSynthesisOutcomes((current) => [
        ...current,
        { label: groupLabel, kind: "failed", detail: t("settings.recoveryCopiesSynthesizeNoBranch") },
      ]);
      return;
    }
    const okToSynthesize = await confirm({
      title: t("settings.recoveryCopiesSynthesizeTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesSynthesizeLead")}：{groupLabel}
            {headId ? ` · ${headId.slice(0, 8)}` : ""}
          </p>
          <ul className="rc-versions__issues">
            {participants.map((c) => (
              <li key={`${c.path}#${c.headId}`}>
                {c.messageCount} · {c.turns} {t("settings.recoveryCopiesColTurns")} · {c.preview?.trim() || t("settings.recoveryCopiesPreviewEmpty")}
              </li>
            ))}
          </ul>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesSynthesizeWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesSynthesizeConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToSynthesize) return;
    setBusy(true);
    setSynthesizing((current) => ({ ...current, [mainPath]: `0/${participants.length}` }));
    setStatus(`${t("settings.recoveryCopiesSynthesizing")} 0/${participants.length} · ${groupLabel}`);
    try {
      // The backend call is one atomic synthesis; the progress line the panel
      // shows is the participant count it knew before the run (a real
      // per-branch progress would need the backend to stream, which 1.34's
      // merge never did either - the report arrives whole, then the panel
      // reloads and shows what changed).
      const report = await app.SynthesizeSessionHeads(mainPath, headId);
      setSynthesizing((current) => ({ ...current, [mainPath]: `${participants.length}/${participants.length}` }));
      // 任务 1117：report.items 为空（无副本、无其余 heads）说明这次调用其实
      // 没合成任何东西——此时必须走「失败」分支并展示 locale 化文案，而不是
      // 照常渲染「已合成」成功横幅。成功与失败的 outcome 互斥：走 failed 就
      // 不再落 synthesized 的读数横幅（否则会出现「红框报错 + 顶部成功横幅」
      // 并存的矛盾现场，20261024 fork开发-新8 实测）。
      if (report.items.length === 0) {
        // tl1402 反馈对称：失败同弹 toast（与成功 toast 对称），不再只留
        // 面板内 outcome 行——弹窗关掉后失败读作「没发生」。
        setSynthesisOutcomes((current) => [
          ...current,
          { label: groupLabel, kind: "failed", detail: t("settings.recoveryCopiesSynthesizeNoBranch") },
        ]);
        showToast(t("settings.recoveryCopiesSynthesizeNoBranch"), "error");
        // Named winners were consumed even on this no-op path; reset the pick.
        setPickedChain((current) => {
          const next = { ...current };
          delete next[mainPath];
          return next;
        });
        return;
      }
      // 任务 1146 返修：先判定这次合成有没有文件级效果。合成引擎对纯 v3 头
      // 会话（无副本文件）在文件层无事可做——其余版本住在主转录自己的事件日
      // 志里。判定：promoted 或 trashed 非空才算文件级生效。
      const fileEffective = report.promoted || report.trashed.length > 0;
      if (!fileEffective) {
        // 参与项全部是主转录自身的 v3 heads（path === report.mainPath）：
        // 纯 v3 头会话。
        const v3Only = report.items.every((item) => item.path === report.mainPath);
        // 任务 1146 返修2（用户 20261025 裁决）：v3-only 形态的合成现在真实
        // 生效——后端切换主线到选定 head（switchedTo）并按 1064 的 retire 标
        // 记清理落败 heads（retiredHeads）。横幅如实报两件事各自发生与否；
        // retire 不走文件回收站（隐藏 + 下次单写轮换裁掉、轮换前可重放找回），
        // 文件级成功文案的「落败分支已归档到回收站」在这里是失实表述，不用。
        const retiredCount = report.retiredHeads?.length ?? 0;
        if (v3Only && (report.switchedTo || retiredCount > 0)) {
          const detail = report.switchedTo
            ? retiredCount > 0
              ? t("settings.recoveryCopiesV3SynthesizedBoth").replace("{count}", String(retiredCount))
              : t("settings.recoveryCopiesV3SynthesizedSwitched")
            : t("settings.recoveryCopiesV3SynthesizedRetired").replace("{count}", String(retiredCount));
          setSynthesisOutcomes((current) => [
            ...current,
            { label: groupLabel, kind: "v3synthesized", switched: Boolean(report.switchedTo), retired: retiredCount, detail },
          ]);
          // tl1402：v3 形态的合成真实生效（切主线/清理落败 heads）——同弹
          // toast 让反馈离开面板也可见；文案直接用横幅的 locale 句（已三语）。
          showToast(detail, "info");
          // tl1415 点9: 已合入默认折叠——v3 合成真实生效,行折回概要一行。
          collapseRow(mainPath);
        } else if (v3Only) {
          // 后端确认无事可做（无落败分支、无需切换）——保留诚实的「无可做」
          // 回执并指出现有出路（版本表的「切换到此版本」按钮，该入口真实存在）。
          setSynthesisOutcomes((current) => [
            ...current,
            { label: groupLabel, kind: "v3only", detail: t("settings.recoveryCopiesSynthesizeNoCopyBranch") },
          ]);
        } else {
          // 有副本参与却没有文件级效果（全部被跳过/归档失败）：按失败
          // 处理，透传后端 skipped 明细供诊断（1039 先例：后端原文不
          // 加工直出），skipped 缺席时退回「没有可合成的分支」。
          const failureDetail =
            report.skipped.length > 0
              ? report.skipped.join("; ")
              : t("settings.recoveryCopiesSynthesizeNoBranch");
          setSynthesisOutcomes((current) => [
            ...current,
            {
              label: groupLabel,
              kind: "failed",
              detail: failureDetail,
            },
          ]);
          // tl1402 反馈对称：失败同弹 toast。
          showToast(failureDetail, "error");
        }
        // Named winners were consumed even on this no-op path; reset the pick.
        setPickedChain((current) => {
          const next = { ...current };
          delete next[mainPath];
          return next;
        });
        return;
      }
      const uniqueTotal = report.items.reduce((n, item) => n + item.unique, 0);
      setSynthesisOutcomes((current) => [
        ...current,
        {
          label: groupLabel,
          kind: "synthesized",
          participants: report.items.length,
          uniqueTotal,
          afterMessages: report.afterMessages,
          afterTurns: report.afterTurns,
          prefixed: report.prefixed,
          trashed: report.trashed.length,
        },
      ]);
      // tl1402（用户 20261105 实测：点「合成最长链」后没有任何弹窗反馈，数据
      // 其实已落盘）：成功必须有 toast——「已合成 N 条入主线」，N=参与分支并
      // 入主线的独有事件数（graft 零双份，与主线增量同量级）。outcome 行只
      // 有展开面板才可见，toast 让反馈离开面板也在。
      showToast(t("settings.recoveryCopiesMergedToast", { count: uniqueTotal }), "info");
      // tl1415 点9: 已合入默认折叠——文件级合成生效,行折回概要一行。
      collapseRow(mainPath);
      // Named winners were consumed; the next run starts neutral.
      setPickedChain((current) => {
        const next = { ...current };
        delete next[mainPath];
        return next;
      });
    } catch (err) {
      // 任务 1117：后端报错时只落一条「失败」outcome，不再同时渲染成功横幅。
      // 文案优先走 locale：errHeadSynthesisNoBranch 这类已知错误命中现有 key
      // settings.recoveryCopiesSynthesizeNoBranch（三语），其余错误保留后端
      // 原文（那些错误暂无现成 locale key，直译会引入新键面，交由后续统一收
      // 整，这里不做生造文案）。
      const rawMessage = err instanceof Error ? err.message : String(err);
      // tl1402 反馈对称：后端报错同弹 toast（已知错误命中三语文案，其余保留
      // 后端原文——1117 的口径不变）。
      const failureText = rawMessage.includes("only one chain")
        ? t("settings.recoveryCopiesSynthesizeNoBranch")
        : rawMessage;
      setSynthesisOutcomes((current) => [
        ...current,
        {
          label: groupLabel,
          kind: "failed",
          detail: failureText,
        },
      ]);
      showToast(failureText, "error");
    } finally {
      setSynthesizing((current) => {
        const next = { ...current };
        delete next[mainPath];
        return next;
      });
      // 任务 1146：busy 保持到列表重取完成。原先在这里先关 busy 再 await
      // load()，全量列表还在飞行时面板已呈「就绪」相——按钮可点、无加载
      // 指示，用户看到的就是成功横幅配一整套旧列表（旧链/旧版本表/旧分
      // 区），截图成了「合成已生效但列表不刷新」的现场。让 load() 完成后再
      // 释放，刷新期间底部显示加载中、按钮保持禁用，中间态不再以静止旧数
      // 据的样子出现。进度文案也在这里停更：合成已经结束，「正在合成 0/N」
      // 不能陪跑刷新段，rc-note 会落回通用加载指示。
      setStatus("");
      try {
        await load();
      } finally {
        setBusy(false);
        setStatus("");
      }
    }
  };

  const renderGroup = (group: RecoveryCopyGroupView) => {
    const isOpen = expanded.has(group.mainPath);
    const isScanning = scanning.has(group.mainPath);
    const measured = copiesNeedingMeasurement(group).length === 0;
    const groupUnique = measured ? uniqueIn(group) : null;
    // 任务 1063：同一会话的 v3 版本读数叠在副本行上——多 heads 的会话在
    // 合并前的两个入口里各说各话，现在版本徽标直接跟在行内，展开行才不需
    // 要用户自己去另一区对号入座。
    const versions = versionGroups.get(group.mainPath);
    return (
      <div className="rc-group" key={group.mainPath}>
        <div className="rc-row rc-row--group">
          <input
            type="checkbox"
            checked={selected.has(group.mainPath)}
            disabled={busy}
            onChange={() => toggle(group.mainPath)}
            aria-label={group.mainLabel}
            title={t("settings.recoveryCopiesSelectHint")}
          />
          <button
            className="rc-toggle"
            type="button"
            aria-expanded={isOpen}
            onClick={() => toggleExpanded(group.mainPath)}
          >
            <span className="rc-caret">{isOpen ? "▾" : "▸"}</span>
            <span className="rc-label">{group.mainLabel}</span>
            {versions ? (
              <span
                className="rc-versions__badge"
                title={t("settings.recoveryCopiesVersionsBadgeHint").replace("{count}", String(versions.headCount))}
              >
                {t("settings.recoveryCopiesVersionsBadge").replace("{count}", String(versions.headCount))}
              </span>
            ) : null}
          </button>
          <span>{formatWhen(group.mainModified)}</span>
          <span>{formatBytes(group.mainBytes)}</span>
          {/* tl1415 点10: 该列列名是「交互轮次」——放实测轮次,不再拿消息数
              冒充;未测量时给状态文案而不是裸「—」。 */}
          <span
            className={group.mainMessages > 0 || group.mainTurns > 0 ? undefined : "rc-muted"}
            title={group.mainMessages > 0 || group.mainTurns > 0 ? undefined : t("settings.recoveryCopiesUnmeasuredHint")}
          >
            {groupTurnsText(group, t)}
          </span>
          <span>×{group.copies.length}</span>
          <span className={groupUnique ? "rc-unique" : "rc-muted"}>{groupUnique ?? "—"}</span>
          <span className="rc-action">
            <button
              className="btn btn--secondary btn--small"
              type="button"
              title={t("settings.recoveryCopiesScanHint")}
              disabled={isScanning || scanningAll}
              onClick={() => void scanOne(group.mainPath)}
            >
              {isScanning ? "…" : t("settings.recoveryCopiesScan")}
            </button>
          </span>
        </div>

        {isOpen && (
          /* tl1415 点3:展开区三表合一。原先的主线行、副本行、候选链块
             (rc-chains,无表头,行内裸拼「条数 · 交互轮次」)与版本表四块,
             收进 VersionRows 渲染的一张合并表(两行制:主行=核心读数,次行=
             操作+预览摘要);全部操作入口(勾选合并/删除副本/预览并选 HEAD/
             切换到此版本/删除版本/合成/检查健康/修复/修复并瘦身)零丢失。 */
          <VersionRows
            group={group}
            chains={chains.get(group.mainPath)}
            chainLoading={chainLoads.has(group.mainPath) && !chains.has(group.mainPath)}
            t={t}
            confirm={confirm}
            onReload={() => { void load(); }}
            onError={(message) => setErrors((current) => [...current, message])}
            repairNote={repairNote}
            setRepairNote={setRepairNote}
            dataVersion={dataVersion}
            busy={busy}
            scanningAll={scanning.size > 0}
            synthesizing={synthesizing[group.mainPath]}
            pickedChain={pickedChain[group.mainPath]}
            previewingPath={previewingPath}
            branchesSelected={branchesSelected}
            onToggleBranch={toggleBranch}
            onDeleteBranch={(copyPath, index) => { void deleteBranchOne(group, copyPath, index); }}
            onPick={(chain) => { void togglePick(group.mainPath, chain); }}
            onSynthesize={() => { void synthesizeHeads(group.mainPath, group.mainLabel); }}
            headCount={versions?.headCount ?? 0}
            onClearHistory={() => { void clearMergeHistory(group.mainPath, group.mainLabel); }}
          />
        )}
      </div>
    );
  };

  const renderPhase = (title: string, rows: RecoveryCopyGroupView[], hint: string) =>
    rows.length ? (
      <div className="rc-phase" key={title}>
        <div className="rc-phase__head">
          <span className="rc-phase__title">{title}</span>
          <span className="rc-phase__count">{rows.length}</span>
          <span className="rc-phase__hint">{hint}</span>
        </div>
        {rows.map(renderGroup)}
      </div>
    ) : null;

  const outcomeText = (outcome: Outcome) => {
    switch (outcome.kind) {
      case "merged":
        return `${t("settings.recoveryCopiesOutcomeMerged")} ${outcome.before} → ${outcome.after}`;
      case "forced":
        return `${t("settings.recoveryCopiesOutcomeForced")} ${outcome.before} → ${outcome.after}`;
      case "blocked":
        return t("settings.recoveryCopiesOutcomeBlocked");
      default:
        return outcome.detail ?? "";
    }
  };

  return (
    <>
      <section className="settings-section">
        <div className="settings-section__head">
          <div>
            <div className="settings-section__title">{t("settings.recoveryCopiesTitle")}</div>
            <div className="settings-section__desc">{t("settings.recoveryCopiesHint")}</div>
          </div>
        </div>
        <div className="settings-section__body">
          <button className="btn btn--secondary btn--small" type="button" onClick={() => setOpen(true)}>
            {t("settings.recoveryCopiesOpen")}
          </button>
        </div>
      </section>

      {open &&
        createPortal(
          <div className="modal-backdrop rc-backdrop" onClick={() => setOpen(false)}>
            <div
              className="modal rc-modal"
              role="dialog"
              aria-modal="true"
              onClick={(event) => event.stopPropagation()}
            >
              <div className="rc-modal__head">
                <span className="rc-modal__title">{t("settings.recoveryCopiesTitle")}</span>
                <button className="btn btn--small" type="button" onClick={() => setOpen(false)}>
                  {t("common.close")}
                </button>
              </div>

              <div className="rc-modal__body">
                {failed ? (
                  <div className="banner banner--error" role="alert">
                    <span>{t("settings.loadFailed")}</span>
                    {/* 任务 1039：横幅带上后端原因——否则「设置加载失败」是
                        死话，用户与日志都定位不到是哪一步断了。 */}
                    {failedReason && <span className="rc-failed-reason">{failedReason}</span>}
                    <button className="btn btn--secondary btn--small" type="button" onClick={() => void load()}>
                      {t("common.retry")}
                    </button>
                  </div>
                ) : groups === null ? (
                  <div className="empty">{t("settings.loading")}</div>
                ) : (
                  <>
                    {/* 任务 1368 线2：结果回执区（合并/删除/合成的 outcome）从
                        「列表非空」分支提升到与空态平级——整对话删除后列表必然
                        变空，回执若还挂在非空分支里就会被空态吞掉（用户删完只
                        看到「没有发现副本」，删除结果无从确认）。列表状态管列表，
                        回执状态管回执，互不吞并。 */}
                    {composedGroups.length === 0 ? (
                      /* 任务 1085：空态文案区分——「真没副本文件」与「有副本但 v3
                         无 heads」是两种情况，一句「没有发现 recovery 副本」盖住
                         后者会让用户以为面板坏了。 */
                      <div className="empty">
                        {groups.length === 0
                          ? t("settings.recoveryCopiesEmpty")
                          : t("settings.recoveryCopiesEmptyNoHeads")}
                      </div>
                    ) : (
                      <>
                        {errors.length > 0 && (
                      <div className="banner banner--error" role="alert">
                        <ul className="rc-errors">
                          {/* key 用下标不用文案本身：同一句话重复出现（如两行同时
                              遇到同一个重放拒绝）时，按文案取 key 会让 React 报
                              重复 key 警告并把两行折叠成一行。 */}
                          {errors.map((message, index) => (
                            <li key={index}>{message}</li>
                          ))}
                        </ul>
                      </div>
                    )}
                    <div className="rc-table">
                      <div className="rc-row rc-row--head">
                        <span />
                        <span>{t("settings.recoveryCopiesColLine")}</span>
                        <span>{t("settings.recoveryCopiesColModified")}</span>
                        <span>{t("settings.recoveryCopiesColSize")}</span>
                        <span>{t("settings.recoveryCopiesColTurns")}</span>
                        <span>{t("settings.recoveryCopiesColCopies")}</span>
                        <span>{t("settings.recoveryCopiesColUnique")}</span>
                        <span />
                      </div>
                      {renderPhase(
                        t("settings.recoveryCopiesPhaseUnmerged"),
                        phased.unmerged,
                        t("settings.recoveryCopiesPhaseUnmergedHint"),
                      )}
                      {renderPhase(
                        t("settings.recoveryCopiesPhasePending"),
                        phased.pending,
                        t("settings.recoveryCopiesPhasePendingHint"),
                      )}
                      {renderPhase(
                        t("settings.recoveryCopiesPhaseMerged"),
                        phased.merged,
                        t("settings.recoveryCopiesPhaseMergedHint"),
                      )}
                      {/* tl1415 点8: 清理多余副本——一键把主线已完整覆盖的副本
                          (unique=0)逐份送回收站。无副本/未测量/未合成(没有
                          命中的副本)时整块不渲染。 */}
                      {redundantCopies.length > 0 && (
                        <div className="rc-cleanup">
                          <button
                            className="btn btn--secondary btn--small rc-delete"
                            type="button"
                            title={t("settings.recoveryCopiesCleanupHint")}
                            disabled={busy || cleaningUp}
                            onClick={() => void cleanupRedundantCopies()}
                          >
                            {cleaningUp
                              ? "…"
                              : `${t("settings.recoveryCopiesCleanup")} (${redundantCopies.length})`}
                          </button>
                          <span className="rc-note">{t("settings.recoveryCopiesCleanupHintShort")}</span>
                        </div>
                      )}
                    </div>
                      </>
                    )}
                    {outcomes.length > 0 && (
                      <div className="rc-result">
                        <ul className="rc-outcomes">
                          {outcomes.map((outcome) => (
                            <li
                              key={outcome.label}
                              className={
                                outcome.kind === "failed"
                                  ? "rc-outcome--failed"
                                  : outcome.kind === "blocked"
                                    ? "rc-outcome--blocked"
                                    : "rc-outcome--ok"
                              }
                            >
                              <span className="rc-outcome__label">{outcome.label}</span>
                              <span>{outcomeText(outcome)}</span>
                              {outcome.trashed ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesArchived")} {outcome.trashed}
                                </span>
                              ) : null}
                              {outcome.notCovered ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesLeftBehind")} {outcome.notCovered}
                                </span>
                              ) : null}
                              {outcome.unloadable ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesUnloadable")} {outcome.unloadable}
                                </span>
                              ) : null}
                              {outcome.prefixed ? (
                                <span className="rc-muted">
                                  {t("settings.recoveryCopiesPrefixed")} {outcome.prefixed}
                                </span>
                              ) : null}
                              {outcome.reasons?.length ? (
                                <div className="rc-outcome__reasons">
                                  {outcome.reasons.map((r, i) => (
                                    <div key={i}>{r}</div>
                                  ))}
                                </div>
                              ) : null}
                            </li>
                          ))}
                        </ul>
                        {anyMerged ? (
                          <p className="rc-result__hint">{t("settings.recoveryCopiesReopen")}</p>
                        ) : null}
                      </div>
                    )}
                    {deletedOutcomes.length > 0 && (
                      <div className="rc-result">
                        <ul className="rc-outcomes">
                          {deletedOutcomes.map((outcome) => (
                            <li
                              key={outcome.label}
                              className={outcome.kind === "failed" ? "rc-outcome--failed" : "rc-outcome--ok"}
                            >
                              <span className="rc-outcome__label">{outcome.label}</span>
                              <span>
                                {outcome.kind === "deleted"
                                  ? t("settings.recoveryCopiesOutcomeDeleted")
                                  : outcome.detail ?? ""}
                              </span>
                            </li>
                          ))}
                        </ul>
                        <p className="rc-result__hint">{t("settings.recoveryCopiesDeleteReopenHint")}</p>
                      </div>
                    )}
                    {synthesisOutcomes.length > 0 && (
                      <div className="rc-result">
                        <ul className="rc-outcomes">
                    {synthesisOutcomes.map((outcome) => (
                      <li
                        key={outcome.label}
                        className={
                          outcome.kind === "failed"
                            ? "rc-outcome--failed"
                            : outcome.kind === "v3only"
                              ? // 任务 1146 返修：「无可做」回执走中性样式——绿
                                // 色成功面会把无事发生读成「已成功」。
                                "rc-outcome--blocked"
                              : "rc-outcome--ok"
                        }
                      >
                        <span className="rc-outcome__label">{outcome.label}</span>
                        <span>
                          {outcome.kind === "synthesized"
                            ? `${t("settings.recoveryCopiesOutcomeSynthesized")} ${outcome.participants} · ${t("settings.recoveryCopiesUnique")} ${outcome.uniqueTotal} → ${outcome.afterMessages} · ${outcome.afterTurns} ${t("settings.recoveryCopiesColTurns")}${outcome.prefixed ? ` · ${t("settings.recoveryCopiesSynthesizePrefixed")} ${outcome.prefixed}` : ""}${outcome.trashed ? ` · ${t("settings.recoveryCopiesArchived")} ${outcome.trashed}` : ""}`
                            : outcome.detail ?? ""}
                        </span>
                      </li>
                    ))}
                        </ul>
                        {/* 任务 1117：「合成已生效」提示只在真的有合成成功时显示
                            ——全部失败的 outcome 列表配上这句成功文案会自相矛盾
                            （红框报错 + 顶部「合成已生效」并存的现场根因之一）。 */}
                        {synthesisOutcomes.some((outcome) => outcome.kind === "synthesized") ? (
                          <p className="rc-result__hint">{t("settings.recoveryCopiesSynthesizeReopenHint")}</p>
                        ) : null}
                        {/* 任务 1146 返修2：v3-only 生效合成走专属下一步提示——
                            head 清理走 retire 标记（隐藏 + 下次单写轮换裁掉），
                            文件级文案的「落败分支还在回收站里」在这里失实。 */}
                        {synthesisOutcomes.some((outcome) => outcome.kind === "v3synthesized") ? (
                          <p className="rc-result__hint">{t("settings.recoveryCopiesV3SynthesizedHint")}</p>
                        ) : null}
                      </div>
                    )}
                  </>
                )}
              </div>

              <div className="rc-modal__foot">
                <button
                  className="btn btn--small btn--primary"
                  type="button"
                  title={t("settings.recoveryCopiesMergeHint")}
                  disabled={busy || selected.size === 0}
                  onClick={() => void mergeSelected()}
                >
                  {pickedChainCount > 0
                    ? `${t("settings.recoveryCopiesMergePicked")} (${pickedChainCount})`
                    : `${t("settings.recoveryCopiesMerge")} (${selected.size})`}
                </button>
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.recoveryCopiesScanAllHint")}
                  disabled={busy || scanningAll}
                  onClick={() => void scanAll()}
                >
                  {t("settings.recoveryCopiesScanAll")}
                </button>
                {/* 任务 1064：批量删除所选分支——勾选的是副本行左侧的复选框
                    （branchesSelected），与会话行的合并勾选互不干扰。 */}
                {branchesSelected.size > 0 ? (
                  <button
                    className="btn btn--secondary btn--small rc-delete"
                    type="button"
                    title={t("settings.recoveryCopiesDeleteSelectedHint")}
                    disabled={busy}
                    onClick={() => void deleteBranches()}
                  >
                    {`${t("settings.recoveryCopiesDeleteSelected")} (${branchesSelected.size})`}
                  </button>
                ) : null}
                {selected.size > 0 ? (
                  <button
                    className="btn btn--secondary btn--small"
                    type="button"
                    title={t("settings.recoveryCopiesScanSelectedHint")}
                    disabled={busy || scanningAll}
                    onClick={() => void scanSelected()}
                  >
                    {`${t("settings.recoveryCopiesScanSelected")} (${selected.size})`}
                  </button>
                ) : null}
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.recoveryCopiesRefreshHint")}
                  disabled={busy || scanningAll}
                  onClick={() => void load()}
                >
                  {t("settings.recoveryCopiesRefresh")}
                </button>
                {/* 任务 1368 线2：整对话级删除——放在「刷新」右边，归档选中会话
                    的主文件（含 v3 版本与侧车）与全部副本到两条可恢复回收站。
                    与 1064 的「删除所选」（删副本分支）是两个粒度：这里勾的是
                    会话行（selected），一次清掉整个对话。 */}
                <button
                  className="btn btn--secondary btn--small rc-delete"
                  type="button"
                  title={t("settings.recoveryCopiesDeleteConversationHint")}
                  disabled={busy || scanningAll || selected.size === 0}
                  onClick={() => void deleteConversations()}
                >
                  {selected.size > 0
                    ? `${t("settings.recoveryCopiesDeleteConversation")} (${selected.size})`
                    : t("settings.recoveryCopiesDeleteConversation")}
                </button>
                {busy ? (
                  <span className="rc-note">{status || t("settings.loading")}</span>
                ) : selectedGroups.length === 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesPickFirst")}</span>
                ) : needingCount > 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesWillMeasure")}</span>
                ) : null}
              </div>
            </div>
          </div>,
          document.body,
        )}

      {dialog}
    </>
  );
}
