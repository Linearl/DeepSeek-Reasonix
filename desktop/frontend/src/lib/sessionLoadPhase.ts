// 任务 560：会话加载分阶段文案的取数口。后端 historySliceTrace 把正在执行的
// 既有埋点阶段名（live-index-load / cold-eventlog / …）发布进内存注册表
// （desktop/history_slice_timing.go 的 historyLoadPhases，纯内存、零 IO），
// 这里按 tab 轮询读取并映射为加载横幅的主标题文案键。未知阶段或没有进行中
// 的读一律回落通用加载文案。
import { app } from "./bridge";

export type HistoryLoadPhaseKey =
  | "sessionRecovery.loadingIndex"
  | "sessionRecovery.loadingEvents"
  | "sessionRecovery.loadingHistory";

type PhaseLoader = (tabId: string) => Promise<string>;

const defaultPhaseLoader: PhaseLoader = (tabId) => app.HistoryLoadPhase(tabId);
let phaseLoader = defaultPhaseLoader;
let nowSource: () => number = () => Date.now();

/** 映射：后端既有阶段名 → 加载主标题文案键。未知阶段回落通用文案。 */
export function historyLoadPhaseKey(phase: string): HistoryLoadPhaseKey {
  if (phase === "live-index-load") return "sessionRecovery.loadingIndex";
  if (phase === "cold-eventlog") return "sessionRecovery.loadingEvents";
  return "sessionRecovery.loadingHistory";
}

/** 轮询指定 tab 正在执行的 history 读阶段名；空串=无进行中的读/阶段未知。 */
export function historyLoadPhase(tabId: string): Promise<string> {
  return phaseLoader(tabId);
}

/** 等待计时的时钟源，测试可注入。 */
export function sessionLoadNow(): number {
  return nowSource();
}

/** 测试注入：替换轮询来源/时钟；不传即恢复默认。 */
export function __setSessionLoadProbeForTest(probe?: { phaseLoader?: PhaseLoader; now?: () => number }): void {
  phaseLoader = probe?.phaseLoader ?? defaultPhaseLoader;
  nowSource = probe?.now ?? (() => Date.now());
}
