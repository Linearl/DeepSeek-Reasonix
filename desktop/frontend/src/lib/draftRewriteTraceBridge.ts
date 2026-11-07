// tl1427（草稿优化对齐 zcode prompt-enhance ②轨迹可见）：增强调用轨迹桥。
//
// Composer（任意挂载点：App 主树 / decision footer / channel 面板）把一次
// 增强调用的收尾通报给 controller，controller 落成一条 trajectoryOnly notice
// item——只进轨迹台账、不进聊天行投影、不经后端事件（因此不进持久化
// transcript，与「不进 transcript」数据纪律一致；记录不含草稿正文与改写
// 结果，source 对齐 zcode modelTrajectory.source.promptEnhance）。
//
// sink 模式与 frontendDiagnosticBridge 同款：桥本体无 React 依赖，harness
// 可直接驱动；未注册 sink 时调用是空操作（开关关/测试外零行为）。
export type DraftRewriteTraceOutcome = "done" | "cancelled" | "failed";
type DraftRewriteTraceSink = (tabId: string, outcome: DraftRewriteTraceOutcome, elapsedMs: number) => void;

let sink: DraftRewriteTraceSink | undefined;

export function setDraftRewriteTraceSink(next: DraftRewriteTraceSink | undefined): void {
  sink = next;
}

export function traceDraftRewrite(tabId: string | undefined, outcome: DraftRewriteTraceOutcome, elapsedMs: number): void {
  if (!tabId) return;
  sink?.(tabId, outcome, elapsedMs);
}
