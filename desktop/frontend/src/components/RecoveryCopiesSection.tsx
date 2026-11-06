import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { app, type ConsolidationReport, type RecoveryCopyGroupView, type RecoveryChainPreview, type RecoveryChainSet, type RecoveryChainView, type SessionVersionGroupView, type SessionRepairRecordView, type SessionHealthReportView } from "../lib/bridge";
import { useT, type Translator } from "../lib/i18n";
import { formatRecoveryActivateError } from "../lib/recoveryActivateError";
// tl1415d9: 会话行展开态的持久化记忆(重挂载/采纳换主线后仍保持——用户二轮裁决)。
import {
  readExpandedSessionKeys,
  sessionExpandKey,
  toggleExpandedSessionKey,
} from "../lib/recoveryExpandedMemory";
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
 *
 *  tl1415b 二轮形态（20261106，验收基准 = docs/drafts/版本面板三表合一-
 *  demo-20261106.html v5）：全页一张表——「类型」列（会话/主线/副本/版本）
 *  替代一切分组横条与小表；候选链段移除（与副本同名冗余），其数据（消息·
 *  轮次/尾部预览）并入副本行；两组列表——待处理默认展开、已合入默认折叠
 *  （组头为表内可折叠分组行+计数徽章）；预览=尾部消息（尾部徽章+结尾优先
 *  弹窗）；会话级操作全部沉底横排，修复记录等次要内容进「更多▾」弹窗。
 *
 *  tl1415b-2 三轮返修（20261106，用户装机实测四点打回，优先于 demo 原样）：
 *  ①行内尾部预览 3 行截断（原 2 行浪费空间），hover title 给行内没有的信息
 *  （路径/时间/读数/体积，不再复读行内文本）；②会话行恢复默认折叠（组段
 *  展开≠子行平铺，点会话行展开钮才见子行）；③主线行复选框=选定 HEAD（底
 *  部③由此可用），行内「合成」按钮删除——合成钮唯一、留底部，头合成形态
 *  由底部按会话形态自动路由；④版本行错排根治（.rc-chain 旧 flex 布局挂在
 *  tr 上拆掉单元格栅格，CSS 收敛为表格行安全形态）。
 *
 *  tl1415c 三轮打回返修（20261106，用户裁定）：主线标识统一到权威源——Go
 *  侧 ListRecoveryChains 的 Selected 改读 recoveryAuthorityMainPath（tab 当前
 *  路径→显式偏好，与切换闸同源，见 recovery_chains.go/recovery_lineage.go），
 *  本组件的行标/去重/删除闸全部经由 buildSessionChainModel 读这一个源；
 *  B2 同 path 去重——主线本体文件不再以副本行重复出现（tl1448 双行形态收敛
 *  为单行，其删除安全保证由函数级闸 preferredMainPathOf 继续承担）。
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
function sessionMeasured(group: RecoveryCopyGroupView): boolean {
  return group.mainMessages > 0 || group.mainTurns > 0 || group.copies.some((c) => c.scanned);
}

/** tl1415b: the session row's merged「消息数 · 轮次」cell (demo v5 shows one
 *  column carrying both readings as "291 · 37"). Unmeasured conversations name
 *  the state instead of inventing a pair of zeros. */
function sessionMsgTurnsText(group: RecoveryCopyGroupView, t: Translator): string {
  if (sessionMeasured(group)) return `${group.mainMessages} · ${group.mainTurns}`;
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

/** fileStemOf is a transcript file's bare stem (basename without .jsonl) — the
 *  identity a panel row must fall back to when the path is not a recovery copy
 *  (tl1448: the scan-origin main file demoted to a 版本行 used to render the
 *  generic "副本" label, which is exactly the identity scramble behind the
 *  user's delete-the-wrong-line incident). */
function fileStemOf(path: string): string {
  const base = path.replaceAll("\\", "/").split("/").pop() ?? path;
  return base.replace(/\.jsonl$/i, "");
}

/** sameSessionPath compares two transcript paths as the same file. The copies
 *  scan and the chain enumerator are two different backend readers (tl1414's
 *  root cause was exactly a Windows case-folding mismatch between two such
 *  readers), so equality that decides "may this row delete/preview this file"
 *  folds case: a false positive only over-restricts a delete (safe), a false
 *  negative deletes the main line (tl1448 用户实测事故). */
function sameSessionPath(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
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

/** tl1415b: two groups only (demo v5)——待处理（有副本/候选需要处理的会话）
 *  与已合入（合并成功后自动进入）。The old three-phase split (unmerged /
 *  pending / merged) folded into one 待处理 group. 组迁移语义（口径④）：
 *  有副本但已无合并历史（headCount≤1）的会话留在待处理——清空合并历史把它
 *  移出已合入组，仍多副本时它（重新扫描后）回到待处理组。 */
type Phase = "todo" | "merged";

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

/** phaseOf sorts a conversation into the two demo groups by what a user would do
 *  about it. headCount is the merge-history reading (meta head mirror): a
 *  conversation with copies but no merge history has not been through the flow
 *  yet, whatever the coverage numbers say. */
function phaseOf(group: RecoveryCopyGroupView, headCount: number): Phase {
  if (copiesNeedingMeasurement(group).length > 0) return "todo";
  if (uniqueIn(group) > 0) return "todo";
  if (group.copies.length > 0 && headCount <= 1) return "todo";
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
 *  shows the shape of the problem without opening any transcript.
 *
 *  Measuring a copy means reading and comparing whole transcripts. The panel offers
 *  it on demand (the row scan button, step ① of the flow), and the merge performs
 *  it itself when needed - the preview is built from those counts, so the button
 *  can produce them rather than sending the user back to press Scan first.
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
      {/* 口径⑥: 结尾优先——尾部区置顶(上面),开头降为一行摘要收尾。 */}
      {!preview.degraded && preview.firstUserText?.trim() ? (
        <div className="rc-preview__start" title={preview.firstUserText}>
          {t("settings.recoveryCopiesPreviewStart")}：{preview.firstUserText}
        </div>
      ) : null}
    </div>
  );
}

/** SyntheticChainSet marks the enumeration-refused placeholder set so the row
 *  model can tell it apart from a backend degraded enumeration (tl1415c B1).
 *  The placeholder must never be promoted to the main row — a zero-count
 *  placeholder wearing the 主线 tag is exactly the type confusion the user
 *  flagged (slot ①). */
type SyntheticChainSet = RecoveryChainSet & { synthetic?: true };

/** degradedChainSet is the local stand-in for a conversation whose chain
 *  enumeration was refused (任务 1039r). The backend's own degradation (1019/1039)
 *  turns a replay refusal into a Degraded row, but a rejection that still escapes
 *  it - a directory read error, a non-refusal replay failure - used to leave the
 *  expanded area an empty table with a bare error line. Rendering one zero-count
 *  degraded row keeps the same face as the backend degradation: the row exists,
 *  carries no counts, and is never the recommended chain. tl1415c B1: the set
 *  carries `synthetic`, so the row renders in the 版本 area under the 降级副本
 *  type tag with the file stem as its identity — never as 主线. */
function degradedChainSet(mainPath: string): SyntheticChainSet {
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
  return { mainPath, mainLabel: "", chains: [row], longestPath: "", longestHead: "", newestPath: "", newestHead: "", synthetic: true };
}

/** One session's fully derived row model: the current main chain (tl1430b:
 *  Selected 是主线行的单一真相源), the chain bound to each copy file, and the
 *  leftover chains (in-log heads + extra heads of copy files) that render as
 *  版本 rows. Derived once per session render so every row shape below reads
 *  the same inventory.
 */
type SessionChainModel = {
  chains: RecoveryChainSet | undefined;
  loading: boolean;
  currentMainChain: RecoveryChainView | undefined;
  /** Candidate chains minus the current main. */
  candidates: RecoveryChainView[];
};

function buildSessionChainModel(
  group: RecoveryCopyGroupView,
  chains: RecoveryChainSet | SyntheticChainSet | undefined,
  loading: boolean,
): SessionChainModel {
  const chainRows = chains?.chains ?? [];
  // tl1415c B1(槽点①): 枚举被拒的合成占位集永远不升格为主线行——零计数
  // 占位行顶着「主线」类型标签正是用户指认的类型混淆(第3行=降级链行)。占
  // 位行走版本区降级形态:类型名「降级副本」+文件 stem 身份+原因横幅(挂
  // loadChains 的 errors)。后端真实降级枚举(非 synthetic)不受此限——降级
  // 读数的主线文件仍按 Selected/单主线回退升格,主线行带「已降级」徽标,
  // 那是诚实形态(主线行只有一行,不产生同 path 重复)。
  const synthetic = Boolean((chains as SyntheticChainSet | undefined)?.synthetic);
  const mainFileRows = synthetic ? [] : chainRows.filter((c) => c.path === group.mainPath);
  const selectedRow = chainRows.find((c) => c.selected);
  const currentMainChain =
    selectedRow ??
    (mainFileRows.length === 1 && chainRows.every((c) => !c.selected)
      ? mainFileRows[0]
      : mainFileRows.find((c) => Boolean(c.selected)));
  // 候选链与版本组 = 除当前主线外的全部链(其余 heads + 副本文件链)。当前主
  // 线升格为表首主线行,不再在组里重复出现。
  const candidates = currentMainChain ? chainRows.filter((c) => c !== currentMainChain) : [...chainRows];
  return { chains, loading, currentMainChain, candidates };
}

/** Each copy row binds to the first candidate chain on the same path (the copy
 *  file's own chain, whose 消息·轮次/尾部预览 merge into the copy row — 口径②:
 *  候选链段与副本同名冗余,移除段、数据并入行). Remaining chains on that path
 *  (a copy file with several heads) fall through to 版本 rows.
 *
 *  tl1448: the map is keyed by the copy's own path, not its array index — the
 *  row↔file mapping must read by path (行↔文件映射以 path 为唯一键), so a
 *  lookup can never drift when the copies array and the render disagree about
 *  ordering. The current-main file is not in the candidate pool (it was
 *  promoted to the main row), so its row binds through currentMainChain at the
 *  call site instead of staying chainless. */
function bindCopyChains(
  candidates: RecoveryChainView[],
  copies: RecoveryCopyGroupView["copies"],
): { byCopy: Map<string, RecoveryChainView>; leftover: RecoveryChainView[] } {
  const pool = [...candidates];
  const byCopy = new Map<string, RecoveryChainView>();
  for (const copy of copies) {
    const hit = pool.findIndex((c) => c.path === copy.path);
    if (hit >= 0) {
      byCopy.set(copy.path, pool[hit]);
      pool.splice(hit, 1);
    }
  }
  return { byCopy, leftover: pool };
}

export function RecoveryCopiesSection() {
  const t = useT();
  const { showToast } = useToast();
  const { confirm, dialog } = useConfirmDialog();
  const [open, setOpen] = useState(false);
  const [groups, setGroups] = useState<RecoveryCopyGroupView[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [scanning, setScanning] = useState<Set<string>>(new Set());
  // tl1415b: two group-collapse states (demo v5)——待处理默认展开、已合入默认
  // 折叠;组头是表内可折叠分组行,点击切换。
  const [todoOpen, setTodoOpen] = useState(true);
  const [mergedOpen, setMergedOpen] = useState(false);
  // tl1415b-2（用户二轮装机实测）：组段展开不再等于子行平铺——会话行恢复
  // 默认折叠（点开才见主线/副本/版本子行），否则几十个会话全平铺找会话不便。
  // tl1415d9（用户二轮裁决 20261107）：展开是用户状态，刷新不重置——默认折叠
  // 只是「开面板时」的行为。原 useState 实现红基线实测两条丢失路径：①组件
  // 重挂载（设置面板关开/tab 回切/边界重试）整体重置；②按完整 mainPath 记，
  // 采纳副本后主线换成副本文件（tl1430 语义：文件不搬、组 preferred path
  // 变），旧 path 永久失配。现持久化在 lib/recoveryExpandedMemory（模块级
  // Set + localStorage 写穿，tl1439 面板记忆同模式），键=目录+lineage stem
  // （剥 -recovery-<16hex> 后缀），跨采纳稳定。未动过的行开面板仍默认折叠
  // （裁决「默认收起语义不变」面）；load() 重取不清空——1146 钉的「原地反
  // 馈不因重取折行」同样适用于会话级展开（合成/合并后重取，展开着的会话不塌）。
  const [expanded, setExpanded] = useState<Set<string>>(() => readExpandedSessionKeys());
  const toggleExpanded = (path: string) =>
    setExpanded(() => toggleExpandedSessionKey(sessionExpandKey(path)));
  // 会话级动作的回执面（tl1415b 沉底后收在分区层，渲染在表格下方）：
  // 健康报告、修复回执、切换回执。行随重载折叠会丢行内状态（1064b 教训），
  // 分区层只有一份，不随行消失。
  const [healthReports, setHealthReports] = useState<{ label: string; report: SessionHealthReportView }[]>([]);
  const [repairNotes, setRepairNotes] = useState<string[]>([]);
  const [repairing, setRepairing] = useState(false);
  // tl1430:「切换到此版本」= 采纳该行的文件为新主线(adopt)。修复前的入口
  // 恒传 mainPath(点副本行请求的却是把主线自己设为激活)+scope 硬编码
  // global+topicId 空,后端校验链路对不上,点击成了静默 no-op。现在传被点
  // 行的真实 path;scope/topicId 留空由后端按 catalog filing 反查,副本行与
  // project 会话同一条通路。headId 恒空:in-log head 切换已随 tl1280 下线,
  // 后端对该形态显式报错,这里不再有机会走错分支。(tl1430b 在修切换生效位
  // ——本组件只挪按钮位置,activate 本体一字未动,衔接点见交付报告。)
  const [activating, setActivating] = useState<string | null>(null);
  const [switchedNote, setSwitchedNote] = useState("");
  const [deletingHead, setDeletingHead] = useState<string | null>(null);
  // 更多▾ 弹窗(口径⑨): 修复记录等次要内容进弹窗——选中会话的修复日志在
  // 弹窗打开时按需加载,关闭重开时刷新。
  const [moreOpen, setMoreOpen] = useState(false);
  const [records, setRecords] = useState<{ label: string; records: SessionRepairRecordView[] }[]>([]);
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
  // chains holds each conversation's candidate chains, keyed by main path.
  // Loading them replays every log for that conversation. tl1415b: rows are all
  // inline now (no per-row expansion), so the inventory loads for the groups
  // whose child rows are visible——待处理组展开时整组、已合入组展开时有内容
  // (副本或 heads)的会话。
  const [chains, setChains] = useState<Map<string, RecoveryChainSet>>(new Map());
  // versionGroups is the v3 events-log view of the same directories (任务 1011
  // 需求 E), fetched alongside the copies list so the two readings of one
  // conversation can be merged into the same row (任务 1063).
  const [versionGroups, setVersionGroups] = useState<Map<string, SessionVersionGroupView>>(new Map());
  // pickedChain names the branch the user chose to win, keyed by main path. Empty
  // means "let the engine take the fullest one". Keyed per conversation so a pick
  // in one group never leaks into another.
  const [pickedChain, setPickedChain] = useState<Record<string, string>>({});
  // 任务 1065：选 HEAD 反向合成——pickedChain（HEAD）+ 合成结果各自成线：
  // 完成后报合成前后读数（并集可核对）。
  // tl1415b-2：行内「合成」按钮删除后（用户裁决：合成钮唯一、留底部），进度
  // 徽章无处可挂——运行中反馈走底部状态行（setStatus 的「正在合成 N/M」），
  // synthesizing state 整个退役。
  const [synthesisOutcomes, setSynthesisOutcomes] = useState<SynthesisOutcome[]>([]);
  // 任务 1064：复选框批量选中——勾选的是**分支**（副本文件），不是会话行本身。
  // 会话行左侧的复选框仍是「勾选要合并的会话」，两套选择互不干扰：批量删除
  // 只看 branchesSelected。当前版本（主线转录本）永远不在候选里——它没有
  // 「分支」身份可删（1064 安全约束①的前端面，后端 DeleteRecoveryBranch 也
  // 会拒绝非副本路径）。
  const [branchesSelected, setBranchesSelected] = useState<Set<string>>(new Set());

  // tl1414：load() 返回本轮重取是否成功。横幅照旧由内部 catch 落（面板打开
  // 时可见），但删除后的重取失败必须让调用方能观察到——「已删除」回执配上
  // 一套纹丝不动的旧列表会被读成「删除没发生」（1146 的中间态教训同源），
  // toast 把重取失败也带出面板。返回值非抛错：本函数的既有调用方（useEffect
  // 的 void load() / 刷新按钮 / onReload）没有 catch，rethrow 会让 unhandled
  // rejection 打崩 harness 进程（组件注释里已有两处实测先例）。
  const load = useCallback(async (): Promise<boolean> => {
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
      // 实测「合成已生效，候选链区仍显示两条旧链」的根因。清空后仍可见的
      // 子行由下方 effect 立即重拉。
      setChains(new Map());
      setChainLoads(new Set());
      return true;
    } catch (e) {
      setGroups(null);
      setFailed(true);
      // 任务 1039：把后端原因带进横幅——「设置加载失败」单独出现时无法区分
      // 是 bridge 断、后端拒还是重放超限，排查只能靠猜。
      setFailedReason(e instanceof Error ? e.message : String(e));
      return false;
    }
  }, []);

  useEffect(() => {
    if (open) {
      void load();
    }
  }, [open, load]);

  // 任务 1085：v3-only 会话补行——groups（recovery 副本扫描）只含「有副本文件
  // 的会话」，v3 多 heads 会话若没有副本文件就不在其中，整行连同 1064 删除/
  // 1065 合成入口一起消失。这里把 versionGroups 中缺席的会话合成「无副本」的
  // 占位行（copies: []），使整行（含 v3 版本读数与 heads 子行的全部入口）照
  // 常渲染。mainBytes/mainModified 为 0/""——副本扫描链路从不读这类会话的
  // stat，不编造数字，行内照实显示「—」。
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
    const todo: RecoveryCopyGroupView[] = [];
    const merged: RecoveryCopyGroupView[] = [];
    for (const group of composedGroups) {
      const headCount = versionGroups.get(group.mainPath)?.headCount ?? 0;
      if (phaseOf(group, headCount) === "todo") todo.push(group);
      else merged.push(group);
    }
    return { todo, merged };
  }, [composedGroups, versionGroups]);

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
   *  tl1415 点3:合并表只认这一份链清单。枚举被拒时落 degradedChainSet(与原
   *  版本表的降级同语义:一行 Degraded + 原因横幅),而不是空集——空集会让
   *  版本行整块消失,降级读数无处显示。 */
  const loadChains = useCallback(async (mainPath: string) => {
    try {
      const set = await app.ListRecoveryChains(mainPath);
      // 任务 1039r2:后端 nil 切片会以 null 到达(全候选被非限额错误跳过时
      // chains 为 null)——落进 state 前归一成空数组,渲染树的 .length 读数
      // 才有定义(实测 chains:null 直通让分区横幅报 null.length)。
      const normalized = { ...set, chains: set.chains ?? [] };
      // tl1448 移植(b 类回植):chainsRef 是删除路径校验三闸的主线指针镜像
      // (preferredMainPathOf 读它),这里同步写穿——清理入口对「从未展开」的
      // 折叠会话补拉指针(见 cleanupNonMainCopies)后,同一函数体内立刻要读,
      // 等不到渲染提交后的 effect 同步。
      chainsRef.current = new Map(chainsRef.current).set(mainPath, normalized);
      setChains((current) => new Map(current).set(mainPath, normalized));
    } catch (err) {
      // 拉取失败落降级集:指针镜像与 state 同步写穿(降级行 path=扫描起点,
      // preferredMainPathOf 据它回落扫描起点——「无指针可读」的诚实形态)。
      const degraded = degradedChainSet(mainPath);
      chainsRef.current = new Map(chainsRef.current).set(mainPath, degraded);
      setChains((current) => new Map(current).set(mainPath, degraded));
      setErrors((current) => [...current, err instanceof Error ? err.message : String(err)]);
    }
  }, []);

  // Chain inventory is lazy: it replays every log for the conversation, so it
  // loads only for conversations whose child rows are on screen. tl1415b-2:
  // 子行随会话行折叠（点开才渲染）——只有「所在组段展开 **且** 会话行展开」
  // 的会话需要链清单（一次重放一份日志，折叠态不重放是实打实的性能收益，
  // 28 个已合入会话一次性全量重放是实测过的坑）。chainLoads guards repeats.
  const [chainLoads, setChainLoads] = useState<Set<string>>(new Set());
  const chainNeededPaths = useMemo(() => {
    const out: string[] = [];
    if (todoOpen) {
      for (const g of phased.todo) {
        // tl1415d9: 展开态键=目录+lineage stem(跨采纳稳定),不再按 mainPath 直记。
        if (expanded.has(sessionExpandKey(g.mainPath))) out.push(g.mainPath);
      }
    }
    if (mergedOpen) {
      for (const g of phased.merged) {
        if (!expanded.has(sessionExpandKey(g.mainPath))) continue;
        const headCount = versionGroups.get(g.mainPath)?.headCount ?? 0;
        if (g.copies.length > 0 || headCount > 1) out.push(g.mainPath);
      }
    }
    return out;
  }, [todoOpen, mergedOpen, phased, versionGroups, expanded]);
  useEffect(() => {
    for (const path of chainNeededPaths) {
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
  }, [chainNeededPaths, chains, chainLoads, loadChains]);

  /** togglePick previews a chain in a wide dialog and — on confirm — names it as
   *  the merge HEAD (pickedChain). Clicking the picked chain again un-picks it.
   *  The main chain is pickable too: it means keep the current main and archive
   *  the rest. Nothing is written on preview.
   *
   *  The tail-reveal state lives inside ChainPreviewBody: ConfirmDialog keeps
   *  the message element it was handed, so state owned out here was invisible
   *  to the rendered dialog. */
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

  // tl1430b 衔接点:activate 本体与 tl1430b 返修共用,本组件只把按钮摆上副本
  // 行/版本行(逻辑零改动)。tl1415c B3:拒绝文案走 formatRecoveryActivateError
  // ——already-main/热切被拒两类按后端机器码出三语人话(下一步说清),技术原
  // 文保留;同弹 toast(tl1414 反馈对称:点击失败不能只活在面板横幅里)。
  const activate = async (chain: RecoveryChainView) => {
    setActivating(chain.path);
    try {
      await app.SetActiveSessionVersion({ scope: "", topicId: "", path: chain.path, headId: "" });
      const label = recoveryShortName(chain.path) || chain.label || chain.path;
      setSwitchedNote(t("settings.sessionVersions.activated").replace("{name}", label));
      await load();
    } catch (e) {
      const detail = formatRecoveryActivateError(e);
      setErrors((current) => [...current, detail]);
      showToast(detail, "error");
    } finally {
      setActivating(null);
    }
  };

  /** 任务 1064：v3 版本块里的单版本删除。与父级的批量删除共用同一条确认与
   *  回收语义（后端 retire 标记：不选中就不可删，删后条目从列表消失、独有
   *  事件在下一次单写轮换前仍可通过重放找回）。 */
  const deleteHead = async (mainPath: string, headId: string) => {
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
    setDeletingHead(headId);
    try {
      await app.DeleteSessionVersionHead(mainPath, headId);
      // tl1414 反馈对称：retire 生效同弹 toast（outcome 行不存在于这条路径，
      // 无 toast 时成功零反馈，读作没点）。
      showToast(t("settings.recoveryCopiesVersionDeletedToast", { count: 1 }), "info");
      await load();
    } catch (e) {
      // tl1367-fix defect C: the busy family renders localized (holder block).
      const detail = formatSessionBusyError(e);
      // tl1414 反馈对称：失败同弹 toast（横幅只在面板打开时可见）。
      showToast(
        `${t("settings.recoveryCopiesVersionDeleteFailedToast", { count: 1 })}：${detail}`,
        "error",
      );
      setErrors((current) => [...current, detail]);
    } finally {
      setDeletingHead(null);
    }
  };

  const scanOne = async (mainPath: string): Promise<RecoveryCopyGroupView | null> => {
    setScanning((current) => new Set(current).add(mainPath));
    try {
      const fresh = await app.ScanRecoveryCopyGroup(mainPath);
      setGroups((current) => (current ?? []).map((g) => (g.mainPath === mainPath ? fresh : g)));
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

  // tl1415 点9: 清空合并历史——meta 头镜像复位为单链(账面重置,内容零触
  // 碰)。tl1415b: 沉底为会话级批量入口——作用于勾选会话中 headCount>1 的
  // 那些(不限组段:待处理组里带合并历史的会话同样可清),一次确认列清单,
  // 逐个调用。组迁移语义(口径④):清空后该会话移出已合入组(phaseOf 的
  // headCount 判定),仍多副本则回到待处理组。
  const clearMergeHistorySelected = async () => {
    const chosen = selectedGroups.filter(
      (g) => (versionGroups.get(g.mainPath)?.headCount ?? 0) > 1,
    );
    if (!chosen.length) return;
    const ok = await confirm({
      title: t("settings.recoveryCopiesClearHistoryTitle"),
      message: (
        <div>
          <p>{t("settings.recoveryCopiesClearHistoryBatchLead")}</p>
          <ul className="rc-versions__issues">
            {chosen.map((g) => (
              <li key={g.mainPath}>{g.mainLabel}</li>
            ))}
          </ul>
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
      let cleared = 0;
      const failures: string[] = [];
      for (const group of chosen) {
        try {
          await app.ClearSessionMergeHistory(group.mainPath);
          cleared += 1;
        } catch (err) {
          failures.push(`${group.mainLabel}: ${err instanceof Error ? err.message : String(err)}`);
        }
      }
      if (cleared > 0) showToast(t("settings.recoveryCopiesClearHistoryDone"), "info");
      if (failures.length > 0) setErrors((current) => [...current, ...failures]);
      await load();
    } finally {
      setBusy(false);
    }
  };

  // ── 会话级批处理:检查健康 / 修复 / 修复并瘦身(tl1415b 沉底) ──
  // 原先住在每行展开区(VersionRows),行折叠即丢状态;沉底后作用于勾选会话,
  // 回执收在分区层(表格下方 rc-versions__health 块),不随行消失。
  const inspectSelected = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;
    setErrors([]);
    for (const group of chosen) {
      try {
        const report = await app.InspectSessionHealthFor(group.mainPath);
        setHealthReports((current) => [
          ...current.filter((h) => h.label !== group.mainLabel),
          { label: group.mainLabel, report },
        ]);
      } catch (e) {
        setErrors((current) => [...current, e instanceof Error ? e.message : String(e)]);
      }
    }
  };

  const repairSelected = async (diet: boolean) => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;
    setRepairing(true);
    setRepairNotes([]);
    setErrors([]);
    try {
      for (const group of chosen) {
        let note = "";
        try {
          // 队尾体积列修复：修复前后文件读数对比——后端报告本身不带体积，这里在
          // 调用前后各读一次磁盘大小（os.Stat 的同一读数，与链表的体积列同源），
          // 瘦身类修复（diet）的收益才在面板上看得见；纯修复类动作体积不变时照实
          // 显示「无变化」，不编造数字。
          const beforeBytes = await fileSizeOf(group.mainPath);
          const report = await app.RepairSessionFor(group.mainPath, true, true, diet);
          const afterBytes = await fileSizeOf(group.mainPath);
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
          }
        } catch (e) {
          note = String(e);
        }
        setRepairNotes((current) => [...current, `${group.mainLabel} · ${note}`]);
      }
      await load();
    } finally {
      setRepairing(false);
    }
  };

  // tl1415 点8: 清理多余副本——tl1415b 改形为「清理非主线副本」:作用于勾选
  // 会话,把它们的全部非主线副本(孤立副本除外——主线已缺失的副本删除即真
  // 丢内容,只能被合并)逐份走 DeleteRecoveryBranch 的回收语义。二次确认列出
  // 会话与份数,结果给 toast。
  // tl1448 删除路径校验:「非主线」是这条入口的名字承诺,过滤必须认识主线
  // 指针——采纳后的主线文件仍在副本清单里,此前它照样入选(点一次清掉当前
  // 主线)。逐条发请求前再复核一次(最终闸)。
  // tl1448 移植(b 类回植,tl1415b-2 惰性加载形态):链清单只在会话行展开时
  // 惰性加载(本文件 chainNeededPaths effect),而清理入口在折叠态就可达——
  // 会话勾选框在会话行上,不展开也能勾。指针镜像缺席时 preferredMainPathOf
  // 回落扫描起点,采纳来的主线文件(Selected 已在副本文件上)就会作为普通
  // 副本混进清理目标集——tl1448 事故形态借惰性加载复活。与 synthesizeHeads
  // 的「入口不再要求该会话行展开过,链清单缺席时现拉」同一机制:过滤前把
  // 选中会话的链清单补齐(loadChains 同步写穿 chainsRef),过滤与最终闸读到
  // 同一份指针。
  const cleanupNonMainCopies = async () => {
    const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
    for (const group of chosen) {
      if (!chainsRef.current.has(group.mainPath)) {
        await loadChains(group.mainPath);
      }
    }
    const targets = chosen.flatMap((group) =>
      group.copies
        .filter((copy) => !copy.orphan && !sameSessionPath(copy.path, preferredMainPathOf(group)))
        .map((copy) => ({ group, copy })),
    );
    if (targets.length === 0) return;
    const perGroup = new Map<string, number>();
    for (const { group } of targets) {
      perGroup.set(group.mainLabel, (perGroup.get(group.mainLabel) ?? 0) + 1);
    }
    const ok = await confirm({
      title: t("settings.recoveryCopiesCleanupNonMainTitle"),
      message: (
        <div>
          <p>{t("settings.recoveryCopiesCleanupNonMainLead")}</p>
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
    setErrors([]);
    try {
      let archived = 0;
      const failures: string[] = [];
      for (const { group, copy } of targets) {
        // tl1448:最终闸——请求前以最新主线指针复核(确认框悬停期间指针可能
        // 已被采纳/合并改写)。
        if (sameSessionPath(copy.path, preferredMainPathOf(group))) {
          failures.push(`${copy.label}: ${t("settings.recoveryCopiesDeleteRefusedMain").replace("{name}", recoveryShortName(copy.path) || fileStemOf(copy.path))}`);
          continue;
        }
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
        showToast(t("settings.recoveryCopiesCleanupNonMainDone", { n: archived }), "info");
      }
      if (failures.length > 0) {
        setErrors((current) => [...current, ...failures]);
      }
      await load();
    } finally {
      setBusy(false);
      setStatus("");
    }
  };

  /** mergeSelected is the bottom step-③ entry — since tl1415b-2 the ONLY
   *  synthesis entry (用户裁决：合成钮唯一、留底部，行内钮删除). The checked
   *  conversations are routed by their shape:
   *  - copies present → the file-level copies merge below (unchanged engine);
   *  - no copies but several event-log heads（v3-only 形态，1146返修2 生效面）
   *    → head synthesis (synthesizeHeads, its own confirm chain);
   *  - neither → an honest blocked receipt instead of a silent no-op.
   *  A mixed shape (copies AND heads) takes the file merge first; once the
   *  copies are gone the next click routes to head synthesis — two clicks
   *  cover both, the 1065 capability survives the button's removal.
   *  Cancelling the file confirm aborts the whole run (no head synthesis after
   *  a cancel); cancelling one head synthesis skips that conversation and the
   *  batch moves on — per-session confirms stay independent. */
  const mergeSelected = async () => {
    // tl1415b-2: 读 composedGroups 而非 groups——v3-only 会话(无副本文件)只
    // 存在于合成视图(1085 补行),底部③路由要对它们生效(头合成分支)必须能
    // 看见;读 groups 会让勾选静默无效(1146r2 harness 实测抓到)。
    const chosen = composedGroups.filter((g) => selected.has(g.mainPath));
    if (!chosen.length) return;

    const headCountOf = (g: RecoveryCopyGroupView) => versionGroups.get(g.mainPath)?.headCount ?? 0;
    const headTargets = chosen.filter((g) => g.copies.length === 0 && headCountOf(g) > 1);
    const fileTargets = chosen.filter((g) => g.copies.length > 0);
    const idleTargets = chosen.filter((g) => g.copies.length === 0 && headCountOf(g) <= 1);
    // 无事可做的勾选会话先落 blocked 回执；后续文件级结果在其上追加（同一份
    // collected，不会被逐会话 setOutcomes 覆盖掉）。
    let collected: Outcome[] = idleTargets.map((g) => ({
      label: g.mainLabel,
      kind: "blocked" as const,
      detail: t("settings.recoveryCopiesIdleBlocked"),
    }));
    setOutcomes(collected);
    if (fileTargets.length > 0) {
      const fileResult = await mergeFileTargets(fileTargets, collected);
      if (fileResult === null) return;
      collected = fileResult;
    }
    for (const group of headTargets) {
      await synthesizeHeads(group.mainPath, group.mainLabel);
    }
  };

  /** mergeFileTargets is the file-level copies merge over the checked
   *  conversations (the pre-tl1415b-2 body of mergeSelected, extracted so the
   *  bottom button can route around it). Returns the accumulated outcomes, or
   *  null when the user cancelled the confirmation — a cancel ends the whole
   *  run instead of falling through to head synthesis. */
  const mergeFileTargets = async (
    fileTargets: RecoveryCopyGroupView[],
    collected: Outcome[],
  ): Promise<Outcome[] | null> => {
    setBusy(true);
    setErrors([]);
    try {
      // Measure anything unmeasured here rather than sending the user back to press
      // Scan. The preview is built out of these counts, so it cannot be shown first.
      const measured: RecoveryCopyGroupView[] = [];
      for (const group of fileTargets) {
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
      if (!ok) return null;

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
      return collected;
    } finally {
      setStatus("");
      setBusy(false);
    }
  };

  const scanningAll = scanning.size > 0;
  // tl1415b-2: 同 mergeSelected——勾选语义按合成视图算,v3-only 会话勾选后
  // 「清空合并历史」等底部入口才能识别它们。
  const selectedGroups = composedGroups.filter((g) => selected.has(g.mainPath));
  const needingCount = selectedGroups.reduce((n, g) => n + copiesNeedingMeasurement(g).length, 0);
  const anyMerged = outcomes.some((o) => o.kind === "merged" || o.kind === "forced");

  const toggleBranch = (path: string) =>
    setBranchesSelected((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  /** preferredMainPathOf resolves a conversation's current main FILE path — the
   *  Selected chain's file after an adoption (tl1430b), else the scan origin.
   *  tl1448 删除路径校验的判据：三个删除入口（单删/批量删/清理非主线副本）在
   *  触达后端前都以它复核删除对象——副本路径没有「选中」概念，后端
   *  DeleteRecoveryBranch 拒不了主线文件（recovery_branch_delete.go 1064 约束
   *  ①明文「前端负责禁用主线本身」），前端这道门就是唯一防线。
   *  读 chainsRef 而非闭包快照：删除确认框悬停期间列表可能已刷新（采纳/合并
   *  改写主线指针），「发请求前复核」必须看到最新链清单才是真复核。 */
  const chainsRef = useRef(chains);
  useEffect(() => {
    chainsRef.current = chains;
  }, [chains]);
  const preferredMainPathOf = (group: RecoveryCopyGroupView): string =>
    buildSessionChainModel(group, chainsRef.current.get(group.mainPath), false).currentMainChain?.path ?? group.mainPath;

  /** refuseMainDelete is the shared guard rejection: the panel never asks the
   *  backend to trash the conversation's current main file, however the request
   *  arrived (a stale checkbox from before an adoption, a same-stem row click
   *  mid-reload). Failure mode analysis: over-restriction only blocks a delete
   *  (safe), letting one through deletes the main line — the 20261106 incident. */
  const refuseMainDelete = (groupLabel: string, targetPath: string) => {
    const name = recoveryShortName(targetPath) || fileStemOf(targetPath);
    const detail = t("settings.recoveryCopiesDeleteRefusedMain").replace("{name}", name);
    setErrors((current) => [...current, `${groupLabel} · ${detail}`]);
    showToast(detail, "error");
  };

  /** deleteBranches archives the checked copy branches in one confirmation. The
   *  backend moves each into the recoverable .trash (no coverage proof required:
   *  the user has seen each branch's reading and decided), so the batch is one
   *  list of paths with per-path error collection - a single unreadable branch
   *  must not abort the rest. The outcomes reuse the panel's existing result
   *  vocabulary: "merged" reads as success-with-counts elsewhere, so a dedicated
   *  kind keeps the delete result honest ("deleted", not "merged"). */
  const deleteBranches = async () => {
    const chosen = (groups ?? [])
      .flatMap((g) => g.copies.map((c) => ({ path: c.path, group: g, label: `${g.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${g.copies.indexOf(c) + 1}` })))
      .filter((c) => branchesSelected.has(c.path));
    // tl1448 删除路径校验（批量面）：勾选可能产生于采纳之前、随刷新残留——
    // 勾选集中的当前主线文件在这里整批剔除，确认清单与后端请求都只见可删
    // 分支。全被剔除时无事可做（勾选清掉，不给空确认框）。
    const deletable = chosen.filter((c) => !sameSessionPath(c.path, preferredMainPathOf(c.group)));
    const skippedMainCount = chosen.length - deletable.length;
    if (!deletable.length) {
      if (skippedMainCount > 0) {
        const detail = t("settings.recoveryCopiesDeleteBatchMainSkipped").replace("{n}", String(skippedMainCount));
        setErrors((current) => [...current, detail]);
        showToast(detail, "error");
      }
      setBranchesSelected(new Set());
      return;
    }
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteBatchTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteBatchLead")}：{deletable.length}
          </p>
          <ul className="rc-versions__issues">
            {deletable.map((c) => (
              <li key={c.path}>{c.label}</li>
            ))}
          </ul>
          {skippedMainCount > 0 && (
            <p>
              {t("settings.recoveryCopiesDeleteBatchMainSkipped").replace("{n}", String(skippedMainCount))}
            </p>
          )}
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
    for (const [index, item] of deletable.entries()) {
      setStatus(`${t("settings.recoveryCopiesDeleting")} ${index + 1}/${deletable.length} · ${item.label}`);
      // 发请求前逐条复核（确认框悬停期间主线指针可能已改写）——最终闸。
      if (sameSessionPath(item.path, preferredMainPathOf(item.group))) {
        collected.push({ label: item.label, kind: "failed", detail: t("settings.recoveryCopiesDeleteRefusedMain").replace("{name}", recoveryShortName(item.path) || fileStemOf(item.path)) });
        setDeletedOutcomes([...collected]);
        continue;
      }
      try {
        await app.DeleteRecoveryBranch(item.path);
        collected.push({ label: item.label, kind: "deleted" });
      } catch (err) {
        // tl1367 口径：busy 族渲染本地化持有者文案（与单删/整对话删除同源）。
        collected.push({ label: item.label, kind: "failed", detail: formatSessionBusyError(err) });
      }
      setDeletedOutcomes([...collected]);
    }
    // tl1414 反馈对称：批量删除的结果同样不能只活在展开面板里。成功计数一
    // 条 toast；有失败时失败计数 + 首条原因一条 toast（其余原因在 outcome
    // 行，面板内可查），与 tl1401 合并 toast 的口径一致。
    const deletedCount = collected.filter((o) => o.kind === "deleted").length;
    const failedItems = collected.filter((o) => o.kind === "failed");
    if (deletedCount > 0) {
      showToast(t("settings.recoveryCopiesDeletedToast", { count: deletedCount }), "info");
    }
    if (failedItems.length > 0) {
      showToast(
        `${t("settings.recoveryCopiesDeleteFailedToast", { count: failedItems.length })}：${failedItems[0].detail ?? ""}`,
        "error",
      );
    }
    setBranchesSelected(new Set());
    // 任务 1146：与合成路径同理——列表重取完成后再释放 busy，删除期间底部
    // 保持加载指示，避免「已删除回执 + 旧列表」并存的静止中间态。
    // tl1414：load() 失败不吞——它内部 catch 横幅后**返回布尔**（从不抛错），
    // 这里按返回值补 toast。
    const reloaded = await load();
    if (!reloaded) {
      showToast(t("settings.loadFailed"), "error");
    }
    setBusy(false);
    setStatus("");
  };

  /** deleteBranchOne is the 任务 1064 single-copy delete. 语义原样:确认框 →
   *  DeleteRecoveryBranch(回收语义)→ 结果行 → 列表重取完成后才释放 busy(与
   *  批量删除同口径)。
   *  tl1448 删除路径校验:入口先以当前主线指针复核删除对象(删副本≠删主线
   *  ——渲染层的入口抑制之外,这里是函数级防线,防行刷新间隙/同 stem 行点击
   *  的残余路径);确认框明示删除对象文件(stem+完整路径),不再只说「副本 N」
   *  ——用户要能把确认框对回磁盘上那份文件(20261106 误删事故验收①)。 */
  const deleteBranchOne = async (group: RecoveryCopyGroupView, copyPath: string, index: number) => {
    if (sameSessionPath(copyPath, preferredMainPathOf(group))) {
      refuseMainDelete(group.mainLabel, copyPath);
      return;
    }
    const shortName = recoveryShortName(copyPath);
    const okToDelete = await confirm({
      title: t("settings.recoveryCopiesDeleteTitle"),
      message: (
        <div>
          <p>
            {t("settings.recoveryCopiesDeleteLead")}：{group.mainLabel} · {t("settings.recoveryCopiesCopy")} {index + 1}
            {shortName ? `（${shortName}）` : ""}
          </p>
          <p>
            {t("settings.recoveryCopiesDeleteTarget").replace("{path}", copyPath)}
          </p>
          <p className="rc-preview__warn">{t("settings.recoveryCopiesDeleteWarn")}</p>
        </div>
      ),
      confirmLabel: t("settings.recoveryCopiesDeleteConfirm"),
      cancelLabel: t("common.cancel"),
      tone: "danger",
    });
    if (!okToDelete) return;
    // 确认框悬停期间列表可能已刷新(采纳/合并改写主线指针)——发后端请求前
    // 以最新 state 复核一次,这是删除路径校验的第二道(最终)闸。
    if (sameSessionPath(copyPath, preferredMainPathOf(group))) {
      refuseMainDelete(group.mainLabel, copyPath);
      return;
    }
    setBusy(true);
    try {
      await app.DeleteRecoveryBranch(copyPath);
      setDeletedOutcomes((current) => [
        ...current,
        { label: `${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${index + 1}`, kind: "deleted" },
      ]);
      // tl1414 反馈对称（20261105 用户实测：点确认后静默无反应）：outcome 行
      // 只有展开面板才可见，成功同弹 toast——离开面板也在。
      showToast(t("settings.recoveryCopiesDeletedToast", { count: 1 }), "info");
    } catch (err) {
      // tl1367 口径：busy 族渲染本地化持有者文案，其余透传后端原文；失败
      // toast 带原因，不再只留展开式行。
      const detail = formatSessionBusyError(err);
      setDeletedOutcomes((current) => [
        ...current,
        {
          label: `${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${index + 1}`,
          kind: "failed",
          detail,
        },
      ]);
      showToast(
        `${t("settings.recoveryCopiesDeleteFailedToast", { count: 1 })}：${detail}`,
        "error",
      );
    } finally {
      // 任务 1146:列表重取完成后再释放 busy(与合成/批量删除路径同口径)。
      // tl1414：load() 从不抛错（内部 catch 落横幅后返回布尔），重取失败按
      // 返回值补 toast——列表不动会被读成「删除没发生」；setBusy(false) 恒
      // 执行，busy 卡死面板全按钮禁用。
      const reloaded = await load();
      if (!reloaded) {
        showToast(t("settings.loadFailed"), "error");
      }
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
    // tl1414 反馈对称：整对话删除与单删/批量删同族——结果不能只活在展开
    // 面板里。根因修复后（目录拼写折叠）该按钮才真正开始归档副本，成功/
    // 失败都给 toast；对话级删除的词汇是「对话」不是「分支」，单独成键。
    const conversationCount = collected.filter((o) => o.kind === "conversation").length;
    const failedConversations = collected.filter((o) => o.kind === "failed");
    if (conversationCount > 0) {
      showToast(t("settings.recoveryCopiesConversationDeletedToast", { count: conversationCount }), "info");
    }
    if (failedConversations.length > 0) {
      showToast(
        `${t("settings.recoveryCopiesConversationDeleteFailedToast", { count: failedConversations.length })}：${failedConversations[0].detail ?? ""}`,
        "error",
      );
    }
    setSelected(new Set());
    // 任务 1146 同口径：列表重取完成后再释放 busy，删除期间底部保持加载指示。
    // tl1414：load() 失败按返回值补 toast（同批量删口径）。
    const reloaded = await load();
    if (!reloaded) {
      showToast(t("settings.loadFailed"), "error");
    }
    setBusy(false);
    setStatus("");
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
   *  keep as the HEAD before pressing synthesize.
   *
   *  tl1415b-2: 行内按钮删除（用户裁决：合成钮唯一、留底部），本函数改为由
   *  底部③按会话形态路由调用（无副本但日志多 heads 的会话走这里，文件级
   *  合并照走原路）——两个引擎、两条确认链保留，只是入口合一。路由后入口
   *  不再要求该会话行展开过，链清单可能不在内存：缺席时现拉（拉不到落降级
   *  集，participants 判定照旧）。 */
  const synthesizeHeads = async (mainPath: string, groupLabel: string) => {
    const headId = pickedChain[mainPath] ?? "";
    let chainRows = chains.get(mainPath)?.chains;
    if (!chainRows) {
      try {
        const set = await app.ListRecoveryChains(mainPath);
        chainRows = set.chains ?? [];
        setChains((current) => new Map(current).set(mainPath, { ...set, chains: chainRows ?? [] }));
      } catch {
        chainRows = degradedChainSet(mainPath).chains;
        setChains((current) => new Map(current).set(mainPath, degradedChainSet(mainPath)));
      }
    }
    const participants = (chainRows ?? []).filter(
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
    setStatus(`${t("settings.recoveryCopiesSynthesizing")} 0/${participants.length} · ${groupLabel}`);
    try {
      // The backend call is one atomic synthesis; the progress line the panel
      // shows is the participant count it knew before the run (a real
      // per-branch progress would need the backend to stream, which 1.34's
      // merge never did either - the report arrives whole, then the panel
      // reloads and shows what changed).
      const report = await app.SynthesizeSessionHeads(mainPath, headId);
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
          // Named winners were consumed even on this no-op path; reset the pick.
        } else if (v3Only) {
          // 后端确认无事可做（无落败分支、无需切换）——保留诚实的「无可做」
          // 回执并指出现有出路（版本行的「切换到此版本」按钮，该入口真实存在）。
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

  // ── 行渲染(tl1415b 单表形态) ──
  // 任务 1123:same path+headId 的行会在链表里出现两次(主转录本体一行 + 它
  // 的某个 head 一行,字段读数相同)——两条标签各只打一次。
  const seenLongerPaths = new Set<string>();
  const seenNewerPaths = new Set<string>();
  const chainTagInfo = (c: RecoveryChainView, set: RecoveryChainSet | undefined) => {
    const chainKey = `${c.path}|${c.headId}`;
    const isLonger = !c.selected && set?.longestPath === c.path && set?.longestHead === c.headId && !seenLongerPaths.has(chainKey);
    const isNewer = !c.selected && set?.newestPath === c.path && set?.newestHead === c.headId && !seenNewerPaths.has(chainKey);
    if (isLonger) seenLongerPaths.add(chainKey);
    if (isNewer) seenNewerPaths.add(chainKey);
    return { isLonger, isNewer };
  };

  /** tailPreviewText is the inline tail-preview line's text (口径⑥: 预览=尾
   *  部消息;行内 sub2 摘要 + hover 全文). Degraded and empty states keep their
   *  tl1360 缺陷② distinction: a refused replay is a scan state, not "the
   *  branch has no preview text". */
  const tailPreviewText = (c: RecoveryChainView): string =>
    c.preview?.trim()
      ? c.preview
      : c.degraded
        ? t("settings.recoveryCopiesPreviewUnreadable")
        : t("settings.recoveryCopiesPreviewEmpty");

  /** previewTitle is the hover title for the inline tail-preview line
   *  (tl1415b-2 用户二轮实测：行内已显示尾部文本，title 复读同一句 = 无效
   *  hover）。title 必须比行内多给信息：类型｜路径｜更新时间｜消息·轮次｜
   *  体积｜尾部全文（行内 3 行截断由 CSS 管，title 永远给全文）。
   *  kindLabel 由调用方按行类型给（主线/副本 N·短名/版本 head）。 */
  const previewTitle = (c: RecoveryChainView, kindLabel: string): string =>
    [
      kindLabel,
      c.path,
      `${t("settings.recoveryCopiesColModified")} ${formatWhen(c.lastActivity)}`,
      `${t("settings.recoveryCopiesColMsgTurns")} ${c.messageCount} · ${c.turns}`,
      `${t("settings.recoveryCopiesColSize")} ${formatBytes(c.bytes)}`,
      tailPreviewText(c),
    ].join("｜");

  const renderSessionBlock = (group: RecoveryCopyGroupView, phase: Phase) => {
    const isScanning = scanning.has(group.mainPath);
    const headCount = versionGroups.get(group.mainPath)?.headCount ?? 0;
    const measured = copiesNeedingMeasurement(group).length === 0;
    const groupUnique = measured ? uniqueIn(group) : null;
    const model = buildSessionChainModel(group, chains.get(group.mainPath), chainLoads.has(group.mainPath) && !chains.has(group.mainPath));
    const { byCopy, leftover } = bindCopyChains(model.candidates, group.copies);
    // tl1448: 当前主线**文件**的路径(采纳后 Selected 指向副本文件;无采纳时即
    // 扫描起点)。B2 去重的锚点——副本清单里与它同 path 的行不再渲染(tl1415c
    // 验收③:同 path 不重复行),tl1448 的双行形态(主线行+副本行并存靠徽标区
    // 分)由用户 tl1415c 裁定收敛为单行。tl1448 的安全保证不受影响:主线文件
    // 不进任何删除/清理目标集(勾选残留由 deleteBranches/cleanup 的函数级闸
    // preferredMainPathOf 复核,入口层的渲染收回升格为「行不存在」)。
    const mainFilePath = model.currentMainChain?.path ?? group.mainPath;
    // tl1415c B2: 同 path 去重——主线行已升格收走当前主线文件,该文件不再以
    // 副本行重复出现。行的编号用去重后的可见序号(标签连续),「更长胜出」徽
    // 标仍按原始 copyIndex 对位(预测胜者按整份副本清单算,与渲染无关)。
    const visibleCopies = group.copies
      .map((copy, copyIndex) => ({ copy, copyIndex }))
      .filter(({ copy }) => !sameSessionPath(copy.path, mainFilePath));
    // tl1430 验收①:主线行换到采纳来的文件时亮短标识,让「主线行换了」立即可辨识。
    const mainChainShortName =
      model.currentMainChain && model.currentMainChain.path !== group.mainPath
        ? recoveryShortName(model.currentMainChain.path)
        : "";
    const mainMessages = model.currentMainChain ? model.currentMainChain.messageCount : group.mainMessages;
    const mainTurns = model.currentMainChain ? model.currentMainChain.turns : group.mainTurns;
    const mainModified = model.currentMainChain ? model.currentMainChain.lastActivity : group.mainModified;
    const mainBytes = model.currentMainChain ? model.currentMainChain.bytes : group.mainBytes;
    const winner = measured ? predictedWinner(group) : null;
    // tl1415b-2（点1 同族）：会话行 hover 也要比行内多给信息——label 之外补
    // 路径与更新时间（clip1 未截断时纯 label 的 hover 与可见文本相同，无效）。
    const sessionTitleText = [
      headCount > 1
        ? `${group.mainLabel}（${t("settings.recoveryCopiesVersionsBadge").replace("{count}", String(headCount))}）`
        : group.mainLabel,
      group.mainPath,
      `${t("settings.recoveryCopiesColModified")} ${formatWhen(group.mainModified)}`,
    ].join("｜");
    // tl1415b-2（点2）：会话行默认折叠，点名称格的展开钮才渲染子行。
    // tl1415d9: isOpen 读持久化记忆键(目录+lineage stem)——采纳换主线后同一
    // 会话的键不变,展开态跟会话走,不跟文件名走。
    const isOpen = expanded.has(sessionExpandKey(group.mainPath));

    return (
      <Fragment key={group.mainPath}>
        {/* ── 会话行(类型列=会话;勾选=参与底部合并;展开钮=子行开关;操作=①扫描。
            tl1415b-2: 行内「合成」按钮删除——合成钮唯一、留底部③(用户二轮实测:
            行内+底部重复),头合成形态由底部按会话形态自动路由) ── */}
        <tr className="rc-session rc-row--group">
          <td className="rc-detail__check">
            <input
              type="checkbox"
              checked={selected.has(group.mainPath)}
              disabled={busy}
              onChange={() => toggle(group.mainPath)}
              aria-label={group.mainLabel}
              title={t("settings.recoveryCopiesSelectHint")}
            />
          </td>
          <td className="rc-col-kind">
            <span className="rc-kindtag rc-kindtag--session">{t("settings.recoveryCopiesKindSession")}</span>
          </td>
          <td className="rc-col-name" title={sessionTitleText}>
            <button
              className="rc-session__toggle"
              type="button"
              aria-expanded={isOpen}
              title={sessionTitleText}
              onClick={() => toggleExpanded(group.mainPath)}
            >
              <span className="rc-caret">{isOpen ? "▾" : "▸"}</span>
              <span className="rc-clip1">{group.mainLabel}</span>
            </button>
          </td>
          <td>{formatWhen(group.mainModified)}</td>
          <td className="rc-num">{formatBytes(group.mainBytes)}</td>
          <td className="rc-num">
            {sessionMeasured(group) ? (
              sessionMsgTurnsText(group, t)
            ) : (
              <span className="rc-muted" title={t("settings.recoveryCopiesUnmeasuredHint")}>
                {sessionMsgTurnsText(group, t)}
              </span>
            )}
          </td>
          <td className="rc-num">×{group.copies.length}</td>
          <td className={`rc-num ${groupUnique ? "rc-unique" : "rc-muted"}`}>{groupUnique ?? "—"}</td>
          <td className="rc-col-ops">
            <div className="rc-ops">
              <button
                className="btn btn--secondary btn--small"
                type="button"
                title={
                  phase === "merged"
                    ? t("settings.recoveryCopiesRescanHint")
                    : t("settings.recoveryCopiesStepScanHint")
                }
                disabled={isScanning || scanningAll}
                onClick={() => void scanOne(group.mainPath)}
              >
                {isScanning ? "…" : t("settings.recoveryCopiesScan")}
              </button>
            </div>
          </td>
        </tr>

        {isOpen ? (
        <>
        {/* ── 主线行(类型列=主线;复选框=选定 HEAD(底部③落点);无切换钮——
            主线就是当前,只有预览)。tl1415b-2(点3):主线行可选中——勾选=以此
            主线为选定 HEAD,底部「以选定 HEAD 合成最长链」由此可用(此前只能
            走预览确认的隐藏路径,用户实测「不可选中⇒按钮不可用」) ── */}
        <tr className="rc-child rc-detail__row rc-detail__row--main">
          <td className="rc-detail__check">
            <input
              type="checkbox"
              checked={Boolean(model.currentMainChain) && pickedChain[group.mainPath] === model.currentMainChain?.path}
              disabled={busy || !model.currentMainChain}
              onChange={() => {
                const chain = model.currentMainChain;
                if (!chain) return;
                setPickedChain((current) => {
                  const next = { ...current };
                  if (next[group.mainPath] === chain.path) delete next[group.mainPath];
                  else next[group.mainPath] = chain.path;
                  return next;
                });
              }}
              aria-label={`${group.mainLabel} · ${t("settings.recoveryCopiesMain")}`}
              title={t("settings.recoveryCopiesMainPickHint")}
            />
          </td>
          <td className="rc-col-kind">
            <span className="rc-kindtag rc-kindtag--main">{t("settings.recoveryCopiesMain")}</span>
            {model.currentMainChain?.selected ? (
              <span className="rc-kindtag rc-kindtag--cur">{t("settings.sessionVersions.selected")}</span>
            ) : null}
          </td>
          <td
            className="rc-col-name"
            title={
              model.currentMainChain
                ? `${t("settings.recoveryCopiesMain")}${mainChainShortName ? `｜${model.currentMainChain.path}` : ""}｜${tailPreviewText(model.currentMainChain)}`
                : undefined
            }
          >
            <div className="rc-clip1">
              {t("settings.recoveryCopiesMain")}
              {mainChainShortName ? (
                <span className="rc-copy__shortname" title={model.currentMainChain?.path}>
                  {mainChainShortName}
                </span>
              ) : null}
              {winner?.isMain ? ` · ${t("settings.recoveryCopiesWins")}` : ""}
              {!group.mainExists ? ` · ${t("settings.recoveryCopiesOrphan")}` : ""}
              {/* 任务 1039r:降级行必须说自己是降级——否则零计数行与「空会话」
                  在面板上无法区分。悬停 title 给出完整原因。 */}
              {model.currentMainChain?.degraded && (
                <span className="sv-degraded" title={t("settings.sessionVersions.degradedHint")}>
                  {t("settings.sessionVersions.degraded")}
                </span>
              )}
            </div>
            {model.currentMainChain ? (
              <div className="rc-sub2 rc-clip3 rc-chain__preview" title={previewTitle(model.currentMainChain, t("settings.recoveryCopiesMain"))}>
                <span className="rc-tailtag">{t("settings.recoveryCopiesTailTag")}</span>
                {tailPreviewText(model.currentMainChain)}
              </div>
            ) : null}
          </td>
          <td>{formatWhen(mainModified)}</td>
          <td className="rc-num">{formatBytes(mainBytes)}</td>
          <td className="rc-num">{mainMessages || mainTurns ? `${mainMessages} · ${mainTurns}` : "—"}</td>
          <td className="rc-num rc-muted">—</td>
          <td className="rc-num rc-muted">—</td>
          <td className="rc-col-ops">
            <div className="rc-ops">
              <button
                className="btn btn--secondary btn--small"
                type="button"
                title={t("settings.recoveryCopiesStepMainPreviewHint")}
                disabled={busy || scanningAll || !model.currentMainChain}
                onClick={() => model.currentMainChain && void togglePick(group.mainPath, model.currentMainChain)}
              >
                {previewingPath === model.currentMainChain?.path
                  ? t("settings.recoveryCopiesPreviewBuildingShort")
                  : t("settings.recoveryCopiesPreviewBtn")}
              </button>
            </div>
          </td>
        </tr>

        {/* ── 副本行(类型列=副本;候选链数据并入——消息·轮次/尾部预览/更长·
            更新标签;操作=预览+②切换到此版本+删除)。tl1415c B2:与主线同
            path 的副本文件不进这里(主线行已收走,同 path 不重复行)——所以
            tl1448 的 isMainFile 徽标/入口收回整段退役,双行形态消失。 ── */}
        {visibleCopies.map(({ copy, copyIndex }, visibleIndex) => {
          const wins = winner ? !winner.isMain && winner.copyIndex === copyIndex : false;
          const chain = byCopy.get(copy.path);
          const tags = chain ? chainTagInfo(chain, model.chains) : { isLonger: false, isNewer: false };
          // tl1415 点12: 文件名短标识(recovery-<8 hex>)——用户要能把面板行
          // 对回磁盘上的 recovery-5df6de18 文件;悬停 title 给完整文件名。
          const shortName = recoveryShortName(copy.path);
          const copyTitle = chain
            ? `${t("settings.recoveryCopiesCopy")} ${visibleIndex + 1}${shortName ? ` · ${copy.path}` : ""}｜${tailPreviewText(chain)}`
            : shortName
              ? copy.path
              : undefined;
          return (
            <tr key={copy.path} className={`rc-child rc-row--copy${tags.isLonger ? " rc-chain--recommended" : ""}`}>
              <td className="rc-detail__check">
                {/* 任务 1064:副本行的复选框+删除入口。孤立副本(orphan,主线
                    已缺失)没有「另一条链可以兜底」的前提,删除即真正丢内容,
                    因此不提供删除入口——它只能被合并(作为新主线)而非删除。 */}
                {!copy.orphan && (
                  <input
                    type="checkbox"
                    checked={branchesSelected.has(copy.path)}
                    disabled={busy}
                    onChange={() => toggleBranch(copy.path)}
                    aria-label={`${group.mainLabel} · ${t("settings.recoveryCopiesCopy")} ${visibleIndex + 1}`}
                    title={t("settings.recoveryCopiesBranchCheckHint")}
                  />
                )}
              </td>
              <td className="rc-col-kind">
                {/* tl1415c B1(槽点①④): 链读数降级的行类型名改「降级副本」
                    ——降级身份在类型列可读,不再与正常副本同标签;悬停给原因。 */}
                <span
                  className="rc-kindtag rc-kindtag--copy"
                  title={chain?.degraded ? t("settings.sessionVersions.degradedHint") : undefined}
                >
                  {chain?.degraded ? t("settings.recoveryCopiesKindDegraded") : t("settings.recoveryCopiesCopy")}
                </span>
                {/* 选定 HEAD(合成③的落点)——demo v5 未画徽标,但「选了哪条」
                    必须在行上可辨识(1065 钉:预览确认后徽标落在该行)。 */}
                {chain && pickedChain[group.mainPath] === chain.path ? (
                  <span className="rc-chain__picked-badge">{t("settings.recoveryCopiesPickedBadge")}</span>
                ) : null}
              </td>
              <td className="rc-col-name" title={copyTitle}>
                <div className="rc-clip1">
                  {t("settings.recoveryCopiesCopy")} {visibleIndex + 1}
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
                  {tags.isLonger && (
                    <span className="rc-versions__tag">
                      {t("settings.sessionVersions.longerTag")
                        .replace("{messages}", String(chain?.messageCount ?? 0))
                        .replace("{turns}", String(chain?.turns ?? 0))}
                    </span>
                  )}
                  {tags.isNewer && (
                    <span className="rc-versions__tag">
                      {t("settings.sessionVersions.newerTag").replace("{date}", (chain?.lastActivity || "").slice(0, 10))}
                    </span>
                  )}
                </div>
                {chain ? (
                  <div className="rc-sub2 rc-clip3 rc-chain__preview" title={previewTitle(chain, `${t("settings.recoveryCopiesCopy")} ${visibleIndex + 1}${shortName ? ` · ${copy.path}` : ""}`)}>
                    <span className="rc-tailtag">{t("settings.recoveryCopiesTailTag")}</span>
                    {tailPreviewText(chain)}
                  </div>
                ) : null}
              </td>
              <td>{formatWhen(copy.modified)}</td>
              <td className="rc-num">{formatBytes(copy.bytes)}</td>
              <td className="rc-num">
                {copy.scanned ? (
                  `${copy.messages} · ${copy.turns}`
                ) : (
                  <span className="rc-muted" title={t("settings.recoveryCopiesUnmeasuredHint")}>
                    {t("settings.recoveryCopiesUnmeasured")}
                  </span>
                )}
              </td>
              <td className="rc-num rc-muted">—</td>
              <td className={`rc-num ${copy.unique > 0 ? "rc-unique" : "rc-muted"}`}>
                {copy.scanned ? copy.unique : "—"}
              </td>
              <td className="rc-col-ops">
                <div className="rc-ops">
                  <button
                    className="btn btn--secondary btn--small"
                    type="button"
                    title={t("settings.recoveryCopiesStepPreviewHint")}
                    disabled={busy || scanningAll || !chain}
                    onClick={() => chain && void togglePick(group.mainPath, chain)}
                  >
                    {previewingPath === chain?.path
                      ? t("settings.recoveryCopiesPreviewBuildingShort")
                      : pickedChain[group.mainPath] === chain?.path && chain
                        ? t("settings.recoveryCopiesUnpick")
                        : t("settings.recoveryCopiesPreviewBtn")}
                  </button>
                  {/* 口径⑤第②步:切换到此版本(主线行跟随/热切——tl1430 语义,
                      activate 本体未动)。当前主线行不提供切换(口径⑦);B2 去
                      重后主线本体文件连副本行都不会有,能点到切换的行必然不
                      是当前主线文件。 */}
                  <button
                    className="btn btn--secondary btn--small"
                    type="button"
                    title={
                      chain?.covered
                        ? t("settings.sessionVersionsActivateCoveredHint")
                        : t("settings.recoveryCopiesStepActivateHint")
                    }
                    disabled={busy || activating !== null || !chain || Boolean(chain?.covered)}
                    onClick={() => chain && void activate(chain)}
                  >
                    {activating === copy.path ? "…" : t("settings.sessionVersions.activate")}
                  </button>
                  {/* 任务 1064:非孤立副本行的删除入口(确认+回收语义;B2 后主
                      线本体文件不再有副本行,「删副本≠删主线」的渲染层防线升
                      格为行不存在,函数级闸见 deleteBranchOne 的指针复核)。 */}
                  {!copy.orphan && (
                    <button
                      className="btn btn--secondary btn--small rc-delete"
                      type="button"
                      title={t("settings.recoveryCopiesDeleteHint")}
                      disabled={busy}
                      onClick={() => void deleteBranchOne(group, copy.path, visibleIndex)}
                    >
                      {t("settings.recoveryCopiesDelete")}
                    </button>
                  )}
                </div>
              </td>
            </tr>
          );
        })}

        {/* 链清单重拉期间显示加载占位而不是整块消失(任务 1146 同款)。 */}
        {model.loading && (
          <tr className="rc-detail__loading">
            <td colSpan={9}>{t("settings.loading")}</td>
          </tr>
        )}

        {/* ── 版本行(v3 事件日志 heads + 未被副本行并走的链):预览/切换/删除。
            demo v5 数据未含该形态,类型列给「版本」——能力保留,衔接点见交付
            报告(tl1430b 在切换按钮区返修,activate 本体未动)。 ── */}
        {leftover.map((c) => {
          const tags = chainTagInfo(c, model.chains);
          const isPicked = pickedChain[group.mainPath] === c.path;
          return (
            <tr key={`${c.path}|${c.headId}|${leftover.indexOf(c)}`} className={`rc-child rc-chain${tags.isLonger ? " rc-chain--recommended" : ""}`}>
              <td className="rc-detail__check" />
              <td className="rc-col-kind">
                {/* tl1415c B1(槽点①④): 降级行类型名「降级副本」——枚举被拒
                    的占位行/后端降级行不再顶着「版本」标签;悬停给原因。 */}
                <span
                  className="rc-kindtag rc-kindtag--version"
                  title={c.degraded ? t("settings.sessionVersions.degradedHint") : undefined}
                >
                  {c.degraded ? t("settings.recoveryCopiesKindDegraded") : t("settings.recoveryCopiesKindVersion")}
                </span>
                {isPicked ? <span className="rc-chain__picked-badge">{t("settings.recoveryCopiesPickedBadge")}</span> : null}
              </td>
              <td
                className="rc-col-name"
                title={c.degraded ? t("settings.sessionVersions.degradedHint") : tailPreviewText(c)}
              >
                <div className="rc-clip1">
                  {/* tl1355:无 DAG 后行身份=文件(headId 恒空)——副本文件行显示
                      文件名短标识(recovery-<8 hex>);非副本文件链行显示 headId
                      前 8 位。tl1448:整文件行的路径就是扫描起点主文件时(采纳
                      后被取代的旧主线降级而来;降级兜底行同路径),行身份亮文件
                      stem——它此前落进泛化「副本」标签,正是用户把主线文件行当
                      副本删掉的身份错乱源头之一。tl1415c B1(槽点④): 降级行无
                      hash 的替代标识落到文件 stem 兜底——stem 恒存在,任何行都
                      不再以裸「副本」标签示人。 */}
                  {c.headId
                    ? c.headId.slice(0, 8)
                    : sameSessionPath(c.path, group.mainPath)
                      ? fileStemOf(c.path)
                      : recoveryShortName(c.path) || fileStemOf(c.path)}
                  {/* 任务 1039r:降级行必须说自己是降级。 */}
                  {c.degraded && (
                    <span className="sv-degraded" title={t("settings.sessionVersions.degradedHint")}>
                      {t("settings.sessionVersions.degraded")}
                    </span>
                  )}
                  {tags.isLonger && (
                    <span className="rc-versions__tag">
                      {t("settings.sessionVersions.longerTag")
                        .replace("{messages}", String(c.messageCount))
                        .replace("{turns}", String(c.turns))}
                    </span>
                  )}
                  {tags.isNewer && (
                    <span className="rc-versions__tag">
                      {t("settings.sessionVersions.newerTag").replace("{date}", (c.lastActivity || "").slice(0, 10))}
                    </span>
                  )}
                </div>
                <div className="rc-sub2 rc-clip3 rc-chain__preview" title={previewTitle(c, c.headId ? c.headId.slice(0, 8) : recoveryShortName(c.path) || t("settings.recoveryCopiesKindVersion"))}>
                  <span className="rc-tailtag">{t("settings.recoveryCopiesTailTag")}</span>
                  {tailPreviewText(c)}
                </div>
              </td>
              <td>{formatWhen(c.lastActivity)}</td>
              <td className="rc-num">{formatBytes(c.bytes)}</td>
              <td className="rc-num">{c.messageCount || c.turns ? `${c.messageCount} · ${c.turns}` : "—"}</td>
              <td className="rc-num rc-muted">—</td>
              <td className="rc-num rc-muted">—</td>
              <td className="rc-col-ops">
                <div className="rc-ops">
                  <button
                    className="btn btn--secondary btn--small"
                    type="button"
                    title={t("settings.recoveryCopiesPreviewBtnHint")}
                    disabled={busy || scanningAll}
                    onClick={() => void togglePick(group.mainPath, c)}
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
                      title={c.covered ? t("settings.sessionVersionsActivateCoveredHint") : t("settings.sessionVersionsActivateHint")}
                      disabled={busy || activating !== null || c.covered}
                      onClick={() => void activate(c)}
                    >
                      {activating === c.path ? "…" : t("settings.sessionVersions.activate")}
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
                      disabled={busy || deletingHead !== null}
                      onClick={() => void deleteHead(group.mainPath, c.headId)}
                    >
                      {deletingHead === c.headId ? "…" : t("settings.recoveryCopiesDelete")}
                    </button>
                  )}
                </div>
              </td>
            </tr>
          );
        })}
        </>
        ) : null}
      </Fragment>
    );
  };

  const renderGroupRow = (
    key: string,
    title: string,
    hint: string,
    count: number,
    isOpen: boolean,
    onToggle: () => void,
  ) => (
    <tr key={key} className="rc-grouprow" onClick={onToggle}>
      <td colSpan={9}>
        <span className="rc-caret">{isOpen ? "▾" : "▸"}</span>
        {title} <span className="rc-grouprow__count">{count}</span>
        <span className="rc-grouprow__note">{hint}</span>
        <span className="rc-grouprow__tip">{isOpen ? t("settings.recoveryCopiesGroupCollapseTip") : t("settings.recoveryCopiesGroupExpandTip")}</span>
      </td>
    </tr>
  );

  /** renderSessionBlockPlain is the collapsed-form session row for merged
   *  conversations that carry nothing to read (no copies, single version): one
   *  row, demo-faithful, whose only action is the rescan that brings it back to
   *  待处理 when copies reappear. Kept separate from renderSessionBlock so the
   *  merged group's many ×0 rows never trigger chain replays. */
  const renderSessionBlockPlain = (group: RecoveryCopyGroupView) => {
    const isScanning = scanning.has(group.mainPath);
    // tl1415b-2（点1 同族）：纯行 hover 同样补路径与更新时间。
    const sessionTitleText = [
      group.mainLabel,
      group.mainPath,
      `${t("settings.recoveryCopiesColModified")} ${formatWhen(group.mainModified)}`,
    ].join("｜");
    return (
      <tr key={group.mainPath} className="rc-session rc-row--group">
        <td className="rc-detail__check">
          <input
            type="checkbox"
            checked={selected.has(group.mainPath)}
            disabled={busy}
            onChange={() => toggle(group.mainPath)}
            aria-label={group.mainLabel}
            title={t("settings.recoveryCopiesSelectHint")}
          />
        </td>
        <td className="rc-col-kind">
          <span className="rc-kindtag rc-kindtag--session">{t("settings.recoveryCopiesKindSession")}</span>
        </td>
        <td className="rc-col-name" title={sessionTitleText}>
          <div className="rc-clip1">{group.mainLabel}</div>
        </td>
        <td>—</td>
        <td className="rc-num">—</td>
        <td className="rc-num rc-muted">—</td>
        <td className="rc-num">×{group.copies.length}</td>
        <td className="rc-num rc-muted">—</td>
        <td className="rc-col-ops">
          <div className="rc-ops">
            <button
              className="btn btn--secondary btn--small"
              type="button"
              title={t("settings.recoveryCopiesRescanHint")}
              disabled={isScanning || scanningAll}
              onClick={() => void scanOne(group.mainPath)}
            >
              {isScanning ? "…" : t("settings.recoveryCopiesScan")}
            </button>
          </div>
        </td>
      </tr>
    );
  };

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

  // 更多▾ 打开时按需加载选中会话的修复记录(次要内容进弹窗——口径⑨)。
  // 只挂 moreOpen:会话勾选变化时弹窗通常已关,重开即重读。
  useEffect(() => {
    if (!moreOpen) return;
    let cancelled = false;
    void (async () => {
      const chosen = (groups ?? []).filter((g) => selected.has(g.mainPath));
      const loaded: { label: string; records: SessionRepairRecordView[] }[] = [];
      for (const group of chosen) {
        let recs: SessionRepairRecordView[] = [];
        try {
          recs = (await app.ListSessionRepairRecordsFor(group.mainPath)) ?? [];
        } catch {
          // 修复日志读不到不是面板失败——列表主体照常渲染。
        }
        loaded.push({ label: group.mainLabel, records: recs });
      }
      if (!cancelled) setRecords(loaded);
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [moreOpen]);

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
                        {/* tl1415b: 全页一张表——唯一表头(sticky+不透明背景,表头
                            下滑遮盖行内容),「类型」列替代一切分组横条与小表。 */}
                        <table className="rc-versions__table rc-maintable">
                          <thead>
                            <tr className="rc-maintable__head">
                              <th className="rc-detail__hcheck" aria-label={t("settings.recoveryCopiesColCopies")} />
                              <th className="rc-col-kind">{t("settings.recoveryCopiesColKind")}</th>
                              <th className="rc-col-name">{t("settings.recoveryCopiesColLine")}</th>
                              <th className="rc-col-when">{t("settings.recoveryCopiesColModified")}</th>
                              <th className="rc-num">{t("settings.recoveryCopiesColSize")}</th>
                              <th className="rc-num">{t("settings.recoveryCopiesColMsgTurns")}</th>
                              <th className="rc-num">{t("settings.recoveryCopiesColCopies")}</th>
                              <th className="rc-num">{t("settings.recoveryCopiesColUnique")}</th>
                              <th className="rc-col-ops">{t("settings.recoveryCopiesColOps")}</th>
                            </tr>
                          </thead>
                          <tbody>
                            {renderGroupRow(
                              "grouprow-todo",
                              t("settings.recoveryCopiesPhaseTodo"),
                              t("settings.recoveryCopiesPhaseTodoHint"),
                              phased.todo.length,
                              todoOpen,
                              () => setTodoOpen((v) => !v),
                            )}
                            {todoOpen && phased.todo.map((g) => renderSessionBlock(g, "todo"))}
                            {renderGroupRow(
                              "grouprow-merged",
                              t("settings.recoveryCopiesPhaseMerged"),
                              t("settings.recoveryCopiesPhaseMergedHint"),
                              phased.merged.length,
                              mergedOpen,
                              () => setMergedOpen((v) => !v),
                            )}
                            {mergedOpen &&
                              phased.merged.map((g) => {
                                const headCount = versionGroups.get(g.mainPath)?.headCount ?? 0;
                                // 已合入组展开时,只给仍带内容(副本或 heads)的会话
                                // 渲染子行——×0 且单版本的会话没有可读的子行,不触
                                // 发链重放(28 个已合入会话一次性全量重放是实测过
                                // 的性能坑)。
                                if (g.copies.length === 0 && headCount <= 1) {
                                  return renderSessionBlockPlain(g);
                                }
                                return renderSessionBlock(g, "merged");
                              })}
                          </tbody>
                        </table>
                      </>
                    )}

                    {/* 会话级动作的回执面(沉底后收在分区层,不随行消失):健康
                        报告 / 切换回执 / 修复回执。 */}
                    {healthReports.map((h) => (
                      <div className="rc-versions__health" key={h.label}>
                        <div>
                          {h.label} ·{" "}
                          {h.report.healthy ? (
                            t("settings.sessionVersions.healthy")
                          ) : (
                            <ul className="rc-versions__issues">
                              {(h.report.issues ?? []).map((issue) => (
                                <li key={issue.code}>
                                  <code>{issue.code}</code> {issue.detail}
                                  {issue.suggestedAction && <> → {issue.suggestedAction}</>}
                                </li>
                              ))}
                            </ul>
                          )}
                        </div>
                      </div>
                    ))}
                    {switchedNote && <div className="rc-versions__health">{switchedNote}</div>}
                    {repairNotes.map((note, i) => (
                      <div className="rc-versions__health" key={i}>
                        {note}
                      </div>
                    ))}

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
                {/* 口径⑤+⑨: 沉底横排按 demo v5 编排——[已选 N] ③合成 | 检查
                    健康·修复·瘦身·清理非主线副本·清空合并历史 | 删除所选·刷新
                    | 更多▾;扫描全部/扫描所选/整对话删除收进更多▾(次要入口)。 */}
                <span className="rc-foot__label">
                  {t("settings.recoveryCopiesSelectedLabel").replace("{count}", String(selected.size))}
                </span>
                <button
                  className="btn btn--small btn--primary"
                  type="button"
                  title={t("settings.recoveryCopiesStepMergeHint")}
                  disabled={busy || selected.size === 0}
                  onClick={() => void mergeSelected()}
                >
                  {t("settings.recoveryCopiesMergeFlowBtn")}
                </button>
                <div className="rc-foot__sep" />
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.sessionVersionsCheckHint")}
                  disabled={busy || repairing || selected.size === 0}
                  onClick={() => void inspectSelected()}
                >
                  {t("settings.sessionVersions.check")}
                </button>
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.sessionVersionsRepairHint")}
                  disabled={busy || repairing || selected.size === 0}
                  onClick={() => void repairSelected(false)}
                >
                  {t("settings.sessionVersions.repair")}
                </button>
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.sessionVersionsRepairDietHint")}
                  disabled={busy || repairing || selected.size === 0}
                  onClick={() => void repairSelected(true)}
                >
                  {t("settings.sessionVersions.repairDiet")}
                </button>
                {/* 口径⑩: 「清理非主线副本」——清空合并历史左边,二次确认,主线
                    保留;作用于勾选会话的全部非主线副本(孤立副本除外)。 */}
                <button
                  className="btn btn--secondary btn--small rc-delete"
                  type="button"
                  title={t("settings.recoveryCopiesCleanupNonMainHint")}
                  disabled={busy || selected.size === 0}
                  onClick={() => void cleanupNonMainCopies()}
                >
                  {t("settings.recoveryCopiesCleanupNonMainTitle")}
                </button>
                {/* tl1415 点9: 清空合并历史——版本徽标的账面读数(head meta 镜像)
                    在 tl1280 头重放退役后无人维护,合并完的会话留着陈旧「N 版本」。
                    沉底为批量入口,作用于勾选会话中 headCount>1 的那些;清空后该
                    会话移出已合入组(口径④)。 */}
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.recoveryCopiesClearHistoryHint")}
                  disabled={
                    busy ||
                    !selectedGroups.some(
                      (g) => (versionGroups.get(g.mainPath)?.headCount ?? 0) > 1,
                    )
                  }
                  onClick={() => void clearMergeHistorySelected()}
                >
                  {t("settings.recoveryCopiesClearHistory")}
                </button>
                <div className="rc-foot__spacer" />
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
                <button
                  className="btn btn--secondary btn--small"
                  type="button"
                  title={t("settings.recoveryCopiesRefreshHint")}
                  disabled={busy || scanningAll}
                  onClick={() => void load()}
                >
                  {t("settings.recoveryCopiesRefresh")}
                </button>
                {/* 口径⑨: 修复记录等次要内容进更多▾弹窗;次要入口(面板级全量
                    扫描/扫描所选/整对话删除)收纳于此。JSDOM 里 details 折叠不
                    影响子元素在 DOM 与可点击——harness 的全局按钮查找不受影
                    响。 */}
                <details
                  className="rc-more"
                  open={moreOpen}
                  onToggle={(e) => setMoreOpen((e.target as HTMLDetailsElement).open)}
                >
                  <summary>{t("settings.recoveryCopiesMoreBtn")}</summary>
                  <div className="rc-more__popover">
                    {records.length > 0 ? (
                      <>
                        <h4>{t("settings.sessionVersions.repairLog")}</h4>
                        <ul className="rc-versions__issues">
                          {records.map((r) => (
                            <li key={r.label}>
                              {r.label}
                              <ul>
                                {r.records.map((rec, i) => (
                                  <li key={i}>
                                    {formatWhen(rec.at)} {rec.before} → {rec.after}
                                    {rec.backupDir ? ` · ${rec.backupDir}` : ""}
                                  </li>
                                ))}
                              </ul>
                            </li>
                          ))}
                        </ul>
                      </>
                    ) : null}
                    <h4>{t("settings.recoveryCopiesSecondaryEntries")}</h4>
                    <div className="rc-ops" style={{ justifyContent: "flex-start" }}>
                      <button
                        className="btn btn--secondary btn--small"
                        type="button"
                        title={t("settings.recoveryCopiesScanAllHint")}
                        disabled={busy || scanningAll}
                        onClick={() => void scanAll()}
                      >
                        {t("settings.recoveryCopiesScanAll")}
                      </button>
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
                      {/* 任务 1368 线2：整对话级删除——归档选中会话的主文件（含
                          v3 版本与侧车）与全部副本到两条可恢复回收站。与 1064 的
                          「删除所选」（删副本分支）是两个粒度：这里勾的是会话行
                          （selected），一次清掉整个对话。 */}
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
                    </div>
                    <p className="rc-more__hint">{t("settings.recoveryCopiesSynthesizePickHint")}</p>
                  </div>
                </details>
                {busy ? (
                  <span className="rc-note">{status || t("settings.loading")}</span>
                ) : selectedGroups.length === 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesPickFirst")}</span>
                ) : needingCount > 0 ? (
                  <span className="rc-note">{t("settings.recoveryCopiesWillMeasure")}</span>
                ) : null}
                {/* 口径⑤: 三步流程提示线(demo v5 底部 hintline)。 */}
                <div className="rc-foot__hintline">
                  {t("settings.recoveryCopiesFlowHint")}
                </div>
              </div>
            </div>
          </div>,
          document.body,
        )}

      {dialog}
    </>
  );
}


