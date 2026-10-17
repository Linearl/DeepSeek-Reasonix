// 任务 938：会话职责（sidecar purpose）的前端缓存。
//
// 树 payload 刻意不带 purpose（Go 侧每次树刷新每行读 sidecar 的代价不值得），
// hover/编辑弹窗按需拉取：每个会话路径至多一次网络往返，结果进模块级缓存；
// 保存成功后由调用方写缓存并通知——侧栏 hover 即时反映新职责，零刷新。
//
// 失败语义（587 同款）：拉取失败不缓存（下次 hover 重试），错误交给调用方
// 决定呈现；本模块只负责「成功的值」的存取与订阅。

import { app } from "./bridge";

const cache = new Map<string, string>();
const loaded = new Set<string>();
const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

/** 已缓存的职责；未加载过返回 undefined（区别于「加载过且为空」）。 */
export function cachedSessionPurpose(sessionPath: string): string | undefined {
  const key = sessionPath.trim();
  if (!key) return undefined;
  return cache.get(key);
}

/** 保存成功后写缓存（空串=清除登记）并通知订阅者即时重渲染。 */
export function setCachedSessionPurpose(sessionPath: string, purpose: string): void {
  const key = sessionPath.trim();
  if (!key) return;
  const next = purpose.trim();
  if (next) cache.set(key, next);
  else cache.delete(key);
  loaded.add(key);
  notify();
}

/** 拉取并缓存一个会话的职责；已加载过（含空值）直接兑现，不重复往返。 */
export async function loadSessionPurpose(sessionPath: string): Promise<string> {
  const key = sessionPath.trim();
  if (!key) return "";
  if (loaded.has(key)) return cache.get(key) ?? "";
  const purpose = (await app.GetSessionPurpose(key)).trim();
  if (purpose) cache.set(key, purpose);
  else cache.delete(key);
  loaded.add(key);
  notify();
  return purpose;
}

/** 订阅缓存变更（hover 卡/行 title 的即时刷新缝）。返回退订函数。 */
export function subscribeSessionPurposes(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
