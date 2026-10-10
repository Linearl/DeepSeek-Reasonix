import { app } from "./bridge";
import type { SettingsView } from "./types";

// 任务 739：设置数据请求的「点击即发」通道。
//
// 正常链路是 点击 → 页面切换 → ManagementSurface 挂载 → SettingsPanel 挂载 →
// reload() 里才发出 app.Settings()。主线程被 transcript 流渲染长任务饿死时
// （jank 档案 longTaskFrames 头号 (anonymous) index-*.js:2:496745，反查为
// useTranscriptKernel setScroller 簇），面板组件迟迟挂不上，数据请求就一直
// 发不出去——用户看到的就是「面板打开但永远加载中」。
//
// 本模块把请求提前到入口点击的同一同步帧：入口（侧栏/顶栏/命令面板/启动闸门）
// 都经由 appNavigation 的 openPage({kind:"settings"})，App 在该处挂钩子调用
// prefetchSettingsView()。请求在线上跑的同时 UI 再慢慢追赶；面板挂载时用
// takeSettingsViewPrefetch() 直接消费已就绪（或已发出）的请求结果。

/** 预取结果的有效期：超时未消费即作废，面板自行重新拉取，避免吃到旧快照。 */
export const SETTINGS_PREFETCH_TTL_MS = 10_000;

let cached: { at: number; promise: Promise<SettingsView> } | null = null;

/** 在入口点击的同步帧发出 Settings 请求；TTL 内重复调用复用同一请求。 */
export function prefetchSettingsView(): void {
  if (cached && Date.now() - cached.at < SETTINGS_PREFETCH_TTL_MS) return;
  const at = Date.now();
  const promise = app.Settings();
  // 失败即弃：下次点击重新发，不把拒绝态缓存留给面板消费。接管必须静默
  // （不 rethrow）——预取发出后面板可能永远不消费它（用户又点回工作区），
  // rethrow 会制造无人处理的 rejection；面板若已消费同一 promise，由面板
  // 自己的 try/catch 兜底失败态。
  promise.catch(() => {
    if (cached && cached.promise === promise) cached = null;
  });
  cached = { at, promise };
}

/** 面板挂载时消费预取；无预取或已超 TTL 返回 null（面板自行 app.Settings()）。 */
export function takeSettingsViewPrefetch(): Promise<SettingsView> | null {
  const hit = cached && Date.now() - cached.at < SETTINGS_PREFETCH_TTL_MS ? cached.promise : null;
  cached = null;
  return hit;
}
