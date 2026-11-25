// surfaceAdmission —— tl1802 消息渲染通道归一化单一准入闸
//
// tl1801 盘点（docs/reasonix/report/消息渲染通道盘点-20261125.md）定性：5 写入者
// × 8 世代键互认矩阵非对角 9 格空白 = 双绘/重入/漏渲染同族 7 件修复（无一共享
// 判定函数）复发的结构性根因。本模块把散在 reducer 七 case 与渲染入口的准入
// 判定收拢为单一入口（评审共识 20261125）：不加层、不加 action、不落盘、不新增
// 后端字段；判定全部读既有键（K3 seq 水位线 / K4 revision / K5 行身份 / K7
// inboxItemId），渲染动作（折叠过滤、并集保留）留在原 case，闸只收「判定」。
//
// 行身份 SSOT：唯一真相源 = per-tab State.items 表面数据位本身（盘点报告 §1
// 「全部汇入唯一表面数据位」）。renderedTurnRows / renderedItemIds 以
// 「items 派生 + 引用级 WeakMap memo」承载（surfaceRowFacts）：items 数组不可变
// （每次变更产生新数组），故 memo 命中即 O(1)、失手即一次 O(n) 派生且结果与
// 现状全表扫描逐字节一致——不可能漂移（无需在任何行创建/移除点维护索引），
// 语义等价性由构造保证。行身份唯一断言门禁（acceptance 3）：
// src/__tests__/surface-admission-uniqueness.test.ts 守卫「装配层行 id 集合
// 派生只允许出现在本模块」。
//
// W1×W2 交叉窗收口依据（评审共识）：reducer 单线程同步落位天然原子——
// dispatchTo 串行应用 action（useController dispatchTo：reducer(prev, a) 后
// 同步 states.set），不存在两写入者并发读改写窗口，无需加锁；闸判定始终读到
// 已完全落位的前序状态。
import type { Item, State } from "./useController";

// ── 行身份派生（SSOT 访问器）─────────────────────────────────────────────────

export interface SurfaceRowFacts {
  /** turnId → 已上屏 `a:<turnId>:*` 行数（tl1739r2 turn-id 去重判定的 O(1) 形态）。 */
  readonly renderedTurnRows: ReadonlyMap<string, number>;
  /** 全部已上屏行 id（renderedItemIds SSOT 访问结果）。 */
  readonly renderedItemIds: ReadonlySet<string>;
  /** notice 行携带的 inboxItemId（K7 幂等：guidance_bubble / steer 消费侧）。 */
  readonly noticeInboxItemIds: ReadonlySet<string>;
  /** user 行携带的 inboxItemId（K7 幂等：user_input 物化，580/723 判定）。 */
  readonly userInboxItemIds: ReadonlySet<string>;
  /** user 行携带的 submissionId（K8 幂等：W5 乐观双发判定）。 */
  readonly userSubmissionIds: ReadonlySet<string>;
}

const rowFactsCache = new WeakMap<readonly Item[], SurfaceRowFacts>();

// items 数组不可变 ⇒ 以数组引用为 key 的 memo 命中即 O(1)、失手即一次派生。
// 派生结果与「逐行扫描 items」逐字节一致（同一事实源），不存在第二真相源。
export function surfaceRowFacts(items: readonly Item[]): SurfaceRowFacts {
  const cached = rowFactsCache.get(items);
  if (cached) return cached;
  const renderedTurnRows = new Map<string, number>();
  const renderedItemIds = new Set<string>();
  const noticeInboxItemIds = new Set<string>();
  const userInboxItemIds = new Set<string>();
  const userSubmissionIds = new Set<string>();
  for (const item of items) {
    if (item.id) renderedItemIds.add(item.id);
    const turnId = turnRowIdOf(item);
    if (turnId) renderedTurnRows.set(turnId, (renderedTurnRows.get(turnId) ?? 0) + 1);
    if (item.kind === "notice") {
      if (item.inboxItemId) noticeInboxItemIds.add(item.inboxItemId);
    } else if (item.kind === "user") {
      if (item.inboxItemId) userInboxItemIds.add(item.inboxItemId);
      if (item.submissionId) userSubmissionIds.add(item.submissionId);
    }
  }
  const facts: SurfaceRowFacts = { renderedTurnRows, renderedItemIds, noticeInboxItemIds, userInboxItemIds, userSubmissionIds };
  rowFactsCache.set(items, facts);
  return facts;
}

// `a:<turnId>:<ordinal>` 由 ensureAssistant 唯一命名（assistantItems.ts，全仓
// grep 可证）；`a<seq>`（无 turnId 回退命名）不携带 turn 世代，不进 Map。
// 与现状 hasRenderedTurnRows 的前缀扫描等价：any-kind id 前缀匹配 ⟺ 解析出的
// turnId 相等（历史行 h*、乐观行 u*、notice n/s/g* 均不可能携带 `a:` 前缀）。
function turnRowIdOf(item: Item): string | undefined {
  const id = item.id;
  if (!id || !id.startsWith("a:")) return undefined;
  const end = id.lastIndexOf(":");
  return end > 2 ? id.slice(2, end) : undefined;
}

// ── 判定（七 case 单一入口）───────────────────────────────────────────────────

export type SurfaceVerdict = { admit: true } | { admit: false; reason: string };

// K3 seq 水位线（任务657）——reducer case "event" 顶部判定，语义逐字节沿现状。
export function judgeEventSeq(s: State, seq: number | undefined): SurfaceVerdict {
  if (typeof seq === "number" && seq > 0 && seq <= s.appliedEventSeq) return { admit: false, reason: "seq_watermark" };
  return { admit: true };
}

// K3 seq 水位线（任务657）——reducer case "stream_batch" 逐段判定；返回放行段
// （空数组 = 整批拒收）。seq-less 段照现状旁路（投影器设计内）。
export function judgeStreamSegments<S extends { seq?: number }>(s: State, segments: readonly S[]): S[] {
  return segments.filter((segment) => typeof segment.seq !== "number" || segment.seq <= 0 || segment.seq > s.appliedEventSeq);
}

// K4 revisionBand（historyRevisionIsOlder 原样内迁）——history_replace /
// history_rebase / history_prepend / history_page 顶部判定。
export function judgeHistoryRevision(s: State, revision: number | undefined): SurfaceVerdict {
  if (historyRevisionIsOlder(s.historyRevision, revision)) return { admit: false, reason: "revision_older" };
  return { admit: true };
}

export function historyRevisionIsOlder(current: number | undefined, incoming: number | undefined): boolean {
  return typeof current === "number" && current > 0
    && typeof incoming === "number" && incoming > 0
    && incoming < current;
}

// K7 inboxItemId 幂等（任务258 receipt/consume 家族收敛：guidance_bubble:3333、
// steer:2546）——O(1) 查表。
export function noticeBubbleWithItemIdRendered(s: State, inboxItemId: string | undefined): boolean {
  if (!inboxItemId) return false;
  return surfaceRowFacts(s.items).noticeInboxItemIds.has(inboxItemId);
}

// K7 user 行幂等（user_input 物化:2564，O(1) 查表）。
export function userInputRowWithItemIdRendered(s: State, inboxItemId: string | undefined): boolean {
  if (!inboxItemId) return false;
  return surfaceRowFacts(s.items).userInboxItemIds.has(inboxItemId);
}

// W4×W4 空白格收口（tl1802 新增判定，方案 §6.2 inbox 行「1712 判定扩展一行」）：
// 无 id 气泡的「尾邻孪生」折叠——紧邻上一行是同文 notice 行即拒（双发/重注入
// 窗口内气泡必然连续落位）。O(1) 尾查，不做全表面派生（追加路径每次 items
// 都是新引用，memo 永不命中，全表面派生=每追加一次 O(n)——bench 实测 51×
// 回退，故收窄）。漏检方向=现状行为（放行），不会造成漏渲染。P17 重注入不受
// 影响：replace 擦除气泡后尾行非同文气泡，判定放行——「表面重建即恢复」语义
// 保持。
export function guidanceBubbleDuplicateOnSurface(s: State, renderedText: string): boolean {
  const last = s.items[s.items.length - 1];
  return last !== undefined && last.kind === "notice" && last.text === renderedText;
}

// guidance_bubble 顶部统一判定（现状 K7 幂等 + 新增 W4 尾邻孪生折叠）。
export function judgeGuidanceBubble(s: State, inboxItemId: string | undefined, renderedText: string): SurfaceVerdict {
  if (noticeBubbleWithItemIdRendered(s, inboxItemId)) return { admit: false, reason: "inbox_item_idempotent" };
  if (!inboxItemId && guidanceBubbleDuplicateOnSurface(s, renderedText)) return { admit: false, reason: "bubble_consecutive_duplicate" };
  return { admit: true };
}

// steer 消费侧（case "steer":2546 同机制收敛）——只做 K7 幂等（无 id 直通照现状）。
export function judgeSteerNoticeArrival(s: State, inboxItemId: string | undefined): SurfaceVerdict {
  if (noticeBubbleWithItemIdRendered(s, inboxItemId)) return { admit: false, reason: "inbox_item_idempotent" };
  return { admit: true };
}

// W5×W5 空白格收口（tl1802 新增最小门，方案 §6.2 optimistic 行的拆解落地形态）：
// K8 submissionId 幂等——同一 submission 的重复派发（同 submissionId 再 append）
// 拒收；不同 submissionId 的同文连续提交是合法行为（tl1712 族钉「composer rows
// render optimistically as typed」明文钉死 580 case 5），不拒。
// 拆解说明（对方案字面「pendingSubmissionId 活跃时拒绝同文重复 append」的偏差）：
// 字面形态与 tl1712 族钉直接冲突（两条不同 submissionId 的同文乐观行都未确认时，
// 族钉要求两行都渲染），故按「真双发」形态收窄为 submissionId 幂等——submissionId
// 内嵌 (seq, sessionGen, epoch)，新提交必得新 id；同一 id 只可能来自同一次 send
// 的重复派发（idempotency 面），拒绝它不会吞掉任何合法新 turn。
export function judgeOptimisticUser(s: State, submissionId: string | undefined): SurfaceVerdict {
  if (submissionId && surfaceRowFacts(s.items).userSubmissionIds.has(submissionId)) {
    return { admit: false, reason: "optimistic_duplicate_submission" };
  }
  return { admit: true };
}

// ── tl1739r2 渲染入口 turn-id 去重（自 assistantItems.ts 内迁，实现走 O(1) 查表）──

// tl1739r2: the surface's rendered-row fingerprint for one backend turn. The
// live path names every assistant segment `a:<turnId>:<ordinal>` (ensureAssistant),
// so "this turn already rendered here" is decidable from the row facts alone.
export function hasRenderedTurnRows(items: readonly Item[], turnId: string): boolean {
  if (!turnId) return false;
  return surfaceRowFacts(items).renderedTurnRows.has(turnId);
}

// tl1739r2: render-entry turn-id dedupe for the reconcile replay channel
// (reconcile 重放不重复 append). A replayed event whose turn is NOT the
// surface's active turn but already has rendered rows is a display-layer
// double-write: the seq watermark usually adjudicates it, but a restarted
// ledger (backend_status ledgerRestarted → appliedEventSeq reset, the tl1739
// tightening) re-arms small seqs and the whole settled turn re-pours onto the
// intact surface — the 20261120/20261121/20261123 duplicate-render recurrence
// (数据层无重复、display 层双写). The active-turn back-fill (fence-swallowed
// deltas, tl1444/tl1739 main shape) is preserved: equal activeTurnId passes.
export function replayDuplicatesRenderedTurn(
  state: Pick<State, "items" | "activeTurnId"> | undefined,
  turnId: string | undefined,
): boolean {
  if (!state || !turnId) return false;
  if (state.activeTurnId === turnId) return false;
  return hasRenderedTurnRows(state.items, turnId);
}

// ── tl1712 host-announced 同文折叠（判定内迁；过滤动作留在 history 两 case）────

// 页面行携带的 user 同文集合（每次 rebase/prepend 一次性派生，冷路径维持原位
// 原频次，方案 §6.4）。
export function pageUserTextSet(pageItems: readonly Item[]): Set<string> {
  const texts = new Set<string>();
  for (const item of pageItems) {
    if (item.kind === "user") texts.add(item.text);
  }
  return texts;
}

// tl1712 (form-2): live announce 行（id `u<seq>`，hostAnnounced 标记）与其已
// 持久化页面行是同一条消息——两个 id 体系永不碰撞，按同文折叠。composer 行
// 无标记，打字重复不受影响（判定收敛，语义与 useController 原 hostTwin 一致）。
export function isHostAnnouncedTwin(item: Item, pageUserTexts: ReadonlySet<string>): boolean {
  return item.kind === "user" && Boolean(item.hostAnnounced) && pageUserTexts.has(item.text);
}

// ── 行 id 集合派生（W2 装配层消费者共用）────────────────────────────────────

// 稳定行 id 集合派生的单一实现（task316 并集判定、retainedLiveTail 等 W2
// 消费者从本模块 import；行身份唯一断言门禁守卫「装配层不得再有本地派生」）。
export function stableItemIdSet(items: readonly { id?: string }[]): Set<string> {
  const ids = new Set<string>();
  for (const item of items) if (item.id) ids.add(item.id);
  return ids;
}
