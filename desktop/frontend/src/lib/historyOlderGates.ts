// 任务 448（384 收尾）：更早历史请求的组件层策略，四个入口共用一份。
//
// 为什么收成一个模块：滚动请求、自动填充、"加载更早"按钮、问题跳转原本各自
// 抄了一份闸，于是各自漂移——三处仍在 `running` 时拒绝（controller 层的这条
// 拒绝已由任务 384 移除），两处在 `olderHistoryError` 置位时拒绝（zcode 的
// 教训：只读可重发的查询失败是"留给下次触发重试"，不是把 UI 锁死）。一份谓词、
// 测一次，是防止四份拷贝再次分叉的最小结构（任务 255 两层永久不一致是同族病）。
//
// 只有两个状态有资格闸请求，且与 controller 层口径一致：更早页是否还存在
// （hasOlder）、是否已有一页在途（loading）。`running` 与 `olderHistoryError`
// 是展示态，不是策略：会话跑着也允许向上补一页（头部前插与尾部追加天然不相交，
// 碰撞交给闸下的 revision/digest 指纹与去重），失败也不改判下次。

export type OlderHistoryRequestState = {
  /**
   * controller 层的唯一硬闸（任务 384 A′）。调用方看不见它时省略（问题跳转），
   * 此时按"假定还有更早页"处理——真没有也由 controller 自己的闸回答。
   */
  hasOlderHistory?: boolean;
  loadingOlderHistory: boolean;
};

/**
 * 能否发起一次"加载更早"。
 *
 * 注意它**不**接受 `running` / `olderHistoryError`：这两项若重新进来就是把
 * 任务 384 与 zcode 借鉴项（B2/B3）一起退回，`task448` 测试会当场红。
 */
export function canRequestOlderHistory(state: OlderHistoryRequestState): boolean {
  if (state.loadingOlderHistory) return false;
  return state.hasOlderHistory !== false;
}

/**
 * 预取半径（借 zcode timelineScrollAnchor：`max(64, 2 × 视口高)`）：读者还差
 * 两个视口到顶就把更早一页拉起来，抵达时已在本地。64px 保底，兼作视口测高为
 * 0（卸载/测高期间）时的退化值——退化到旧行为而不是不触发。
 */
export function olderHistoryTriggerPx(viewportHeight: number): number {
  if (!Number.isFinite(viewportHeight) || viewportHeight <= 0) return 64;
  return Math.max(64, viewportHeight * 2);
}
