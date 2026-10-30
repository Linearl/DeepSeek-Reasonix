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

/** 一次可消费的预取：请求本体 + 该请求的发起时刻（epoch ms）。
 *  tl 1284：startedAt 跟着请求走（原来是一个模块级全局时间戳）——旧实现里
 *  时间戳只在「发起新预取」时覆写、消费/作废/关面板都不清。desktop.log
 *  20261030 两例 big dataReadyMs 拆开后是两种机制，本修各占一半：
 *  ① 23545ms（20:01 例）= 纯伪影：重开面板的 reload 没有活预取（TTL 过期
 *     现拉），起点却 peek 到 22s 前那次打开的全局时间戳；后端当时仅 316ms。
 *  ② 17555ms（22:41 例）= 起点正确、消费被饿：lab 切换的预取 0.36s 就回到
 *     渲染进程，但 promise 解析排在启动期 transcript hydrate 长任务风暴后面
 *     （同窗 boot leg 完成→109MB 会话 hydrate/reconcile），骨架 359ms 就画
 *     出来了，内容 17.2s 后才到——这部分是渲染线程调度问题，归启动线处理；
 *     本修让它「如实计量」：起点=该请求发起时刻，读数不再混入别人的窗口。
 *  绑到请求本体后，消费方计时与请求对齐，重开不再继承旧起点（①根治，
 *  ②不再虚增）。 */
export interface SettingsViewPrefetch {
  promise: Promise<SettingsView>;
  startedAt: number;
}

let cached: { at: number; prefetch: SettingsViewPrefetch } | null = null;

/** 在入口点击的同步帧发出 Settings 请求；TTL 内重复调用复用同一请求。 */
export function prefetchSettingsView(): void {
  if (cached && Date.now() - cached.at < SETTINGS_PREFETCH_TTL_MS) return;
  const at = Date.now();
  const promise = app.Settings();
  const prefetch: SettingsViewPrefetch = { startedAt: at, promise };
  // 失败即弃：下次点击重新发，不把拒绝态缓存留给面板消费。接管必须静默
  // （不 rethrow）——预取发出后面板可能永远不消费它（用户又点回工作区），
  // rethrow 会制造无人处理的 rejection；面板若已消费同一 promise，由面板
  // 自己的 try/catch 兜底失败态。
  promise.catch(() => {
    if (cached && cached.prefetch.promise === promise) cached = null;
  });
  cached = { at, prefetch };
}

/** 面板挂载时消费预取；无预取或已超 TTL 返回 null（面板自行 app.Settings()）。
 *  消费即取走（缓存清空），startedAt 供消费方把计时起点对齐到这次请求。 */
export function takeSettingsViewPrefetch(): SettingsViewPrefetch | null {
  const hit = cached && Date.now() - cached.at < SETTINGS_PREFETCH_TTL_MS ? cached.prefetch : null;
  cached = null;
  return hit;
}

/** tl 1179：写盘动作发起前作废留存预取。设置面板内切 tab 会经 openPage 钩子
 *  重新填缓存（写前快照，TTL 10s 内留存）；若它跨越一次写盘被后续入口复用
 *  （关面板 10s 内重开 → 挂载读吃到跨写旧快照），面板会显示旧态。写前作废
 *  让「预取只服务未被写盘打断的读」——重新挂载时现拉，宁慢勿旧。 */
export function invalidateSettingsViewPrefetch(): void {
  cached = null;
}
