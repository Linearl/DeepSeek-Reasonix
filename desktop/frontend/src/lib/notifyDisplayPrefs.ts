// tl1464：全局通知类型开关（设置 → 通用 → 通知）。四族收敛三类后的展示闸门：
//   A 操作反馈（toast info 档 + ask 后台 toast）→ actionFeedback
//   B 状态横幅（7 个 banner 组件，transcript 顶/输入区上方）→ statusBanners
//   C 诊断告警（rt-notice 三面）→ diagnosticAlerts
// 默认全开 = 默认行为零变化。开关只抑制「展示」，通知留档（notifyArchive）
// 照写——留档≠展示，通知栏仍可回看被关闭类的历史。错误提示不设开关
// （toast error/warn 档不在任何开关名下，用户修正1 的延伸：错误类不可被静默）。
//
// 存储：localStorage 单键（reasonix.notifyDisplayPrefs.v1），与 layoutPreferences
// 同模式的前端本地偏好——通知展示是纯前端语义，走 Go 配置等于每次切换一次 IPC，
// 无对应收益。B 类经 <html data-notify-banners="off"> + CSS 隐藏实现（banner 7
// 组件零改动）；A/C 类在各自展示通路读同偏好。

export type NotifyKindKey = "actionFeedback" | "statusBanners" | "diagnosticAlerts";

export interface NotifyDisplayPrefs {
  actionFeedback: boolean;
  statusBanners: boolean;
  diagnosticAlerts: boolean;
}

const STORAGE_KEY = "reasonix.notifyDisplayPrefs.v1";

const KIND_KEYS: NotifyKindKey[] = ["actionFeedback", "statusBanners", "diagnosticAlerts"];

const DEFAULTS: NotifyDisplayPrefs = {
  actionFeedback: true,
  statusBanners: true,
  diagnosticAlerts: true,
};

let cache: NotifyDisplayPrefs | null = null;

function readPrefs(): NotifyDisplayPrefs {
  const fallback = { ...DEFAULTS };
  if (typeof window === "undefined") return fallback;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return fallback;
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return fallback;
    const out = fallback;
    for (const key of KIND_KEYS) {
      const value = (parsed as Record<string, unknown>)[key];
      if (typeof value === "boolean") out[key] = value;
    }
    return out;
  } catch {
    return fallback;
  }
}

function ensureCache(): NotifyDisplayPrefs {
  if (!cache) cache = readPrefs();
  return cache;
}

function writePrefs(prefs: NotifyDisplayPrefs): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    // 私密模式/配额满：偏好写入失败不阻塞本次切换的会话内效果。
  }
}

// ── B 类的文档级开关：<html data-notify-banners="off">，CSS 据此隐藏 6 个族根
// 选择器（RemoteReclaimBanner 无独立类名钩子，banner 零改动验收下不覆盖——见
// 交付报告）。默认开启时不写属性（零痕迹，默认视图零改动）；只有关闭才落
// data-notify-banners="off"，开回即删除属性。
let docFlagApplied = false;

function applyDocumentFlag(prefs: NotifyDisplayPrefs): void {
  if (typeof document === "undefined") return;
  docFlagApplied = true;
  if (prefs.statusBanners) {
    delete document.documentElement.dataset.notifyBanners;
  } else {
    document.documentElement.dataset.notifyBanners = "off";
  }
}

function ensureDocumentFlag(): void {
  if (!docFlagApplied) applyDocumentFlag(ensureCache());
}

const changeListeners = new Set<(prefs: NotifyDisplayPrefs) => void>();

/** 同步读某类开关（rt-notice 面的 paint 前闸门用；React 侧走 useSyncExternalStore）。 */
export function isNotifyKindEnabled(kind: NotifyKindKey): boolean {
  ensureDocumentFlag();
  return ensureCache()[kind];
}

export function readNotifyDisplayPrefs(): NotifyDisplayPrefs {
  ensureDocumentFlag();
  return { ...ensureCache() };
}

/** 切换某类开关：写存储 + 刷 <html> 旗标 + 广播（设置页与展示面实时联动）。 */
export function setNotifyKindEnabled(kind: NotifyKindKey, value: boolean): void {
  const prefs = ensureCache();
  if (prefs[kind] === value) return;
  prefs[kind] = value;
  writePrefs(prefs);
  applyDocumentFlag(prefs);
  for (const listener of changeListeners) listener({ ...prefs });
}

/** 开关变化订阅（返回退订函数；useSyncExternalStore 的 subscribe 缝）。 */
export function subscribeNotifyDisplayPrefs(cb: (prefs: NotifyDisplayPrefs) => void): () => void {
  ensureDocumentFlag();
  changeListeners.add(cb);
  return () => {
    changeListeners.delete(cb);
  };
}

/** 测试缝：复位内存态（不清存储盘面；重挂场景先清 localStorage）。 */
export function resetNotifyDisplayPrefsForTest(): void {
  cache = null;
  docFlagApplied = false;
}
