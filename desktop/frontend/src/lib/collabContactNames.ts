// 任务462: contact_id → 会话名 的显示层解析。
//
// 跨会话消息的投递文本只携带 contact_id（设计使然：会话名会被改名，接收方
// 必须能按 id 核对落点）。人类可读的名字由前端在「显示时」向
// ListAddressableSessions（任务141 的通讯录，覆盖全局/项目/已归档）解析，
// 不改投递文本本身 —— 模型读到的内容保持 id 不变。
//
// 一致性口径：
// - 同一轮查询返回全量名单，天然是「按 contact_id 批量解析」；已归档会话
//   也在名单内（Archived 行），因此归档会话的名字同样查得到。
// - 名单的 Title 在查询时点现读（用户改名优先），所以改名后最迟一个 TTL
//   窗口、或一次窗口聚焦/收件箱面板打开，就会收敛到新名字。
// - 查不到名字（本机名单里没有该 id，如已删除的会话）降级为截短 id，
//   绝不渲染空白；完整 id 始终保留在 hover（title 属性）里。

import { useEffect, useState } from "react";
import { app } from "./bridge";

/** ListAddressableSessions 行里本模块关心的字段（其余字段与这里无关）。 */
export type CollabContactNameRow = {
  contactId: string;
  title?: string;
};

type CollabContactDirectory = {
  ListAddressableSessions(): Promise<CollabContactNameRow[]>;
};

/** 名单缓存的生存期：改名同步延迟的上界（叠加窗口聚焦/面板打开触发）。 */
const NAME_TTL_MS = 15_000;

const titleByContact = new Map<string, string>();
let fetchedAt = 0;
let inflight: Promise<void> | null = null;
const listeners = new Set<() => void>();

let directory: CollabContactDirectory = app as CollabContactDirectory;

/** 测试注入口：替换名单来源（默认走 app bridge）。传 null 恢复默认。 */
export function setCollabContactDirectory(source: CollabContactDirectory | null): void {
  directory = source ?? (app as CollabContactDirectory);
}

/** 测试复位：清缓存、监听者与在途请求标记。 */
export function resetCollabContactNamesForTest(): void {
  titleByContact.clear();
  fetchedAt = 0;
  inflight = null;
  listeners.clear();
}

function notify(): void {
  for (const listener of listeners) listener();
}

/** 把一批名单行并入缓存（仅在有变化时通知订阅者）。
 * 收件箱面板每次打开都会拉全量名单，顺手喂进来，消息卡就能复用同一份。 */
export function rememberCollabContactNames(rows: CollabContactNameRow[] | undefined | null): void {
  if (!Array.isArray(rows)) return;
  let changed = false;
  for (const row of rows) {
    const id = (row?.contactId ?? "").trim();
    if (!id) continue;
    const title = (row?.title ?? "").trim();
    if (title && titleByContact.get(id) !== title) {
      titleByContact.set(id, title);
      changed = true;
    }
  }
  if (changed) notify();
}

/** 拉取名单（并发去重；TTL 内直接复用缓存）。
 * 失败时保留旧缓存，且本次时间戳照写 —— 用同一 TTL 挡住失败重试风暴；
 * 代价是失败后最多一个 TTL 窗口内显示降级短 id。 */
export function refreshCollabContactNames(options?: { force?: boolean }): Promise<void> {
  if (inflight) return inflight;
  if (!options?.force && fetchedAt > 0 && Date.now() - fetchedAt < NAME_TTL_MS) {
    return Promise.resolve();
  }
  inflight = directory
    .ListAddressableSessions()
    .then((rows) => {
      rememberCollabContactNames(rows);
      fetchedAt = Date.now();
    })
    .catch(() => {
      // 网关不可达/查询失败：保留旧缓存（可能是空），TTL 内不再重试。
      fetchedAt = Date.now();
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

/** 查缓存里的会话名；查不到返回空串（调用方决定降级形态）。 */
export function collabTitleFor(contactId: string): string {
  return titleByContact.get(contactId.trim()) ?? "";
}

/** 降级显示：长 id 截成「前 12 位…后 4 位」，短 id 原样，空 id 给占位。
 * 投递文本会把未登记写成「(未登记)」、目标未知写成「(未知)」——这些都比
 * 16 位短，会原样透出，正好是诚实的降级。 */
export function shortContactId(contactId: string): string {
  const id = contactId.trim();
  if (!id) return "(未知会话)";
  if (id.length <= 16) return id;
  return `${id.slice(0, 12)}…${id.slice(-4)}`;
}

/** 显示标签：缓存里有会话名用会话名，否则截短 id —— 绝不返回空串。 */
export function collabDisplayLabel(contactId: string): string {
  const id = contactId.trim();
  const title = id ? collabTitleFor(id) : "";
  return title || shortContactId(id);
}

/** 订阅 contact_id→会话名 缓存的 React hook。
 * 挂载时按 TTL 拉一次名单；窗口重新可见/聚焦时再补一次（仍受 TTL 门控）。
 * 名单变化通过订阅推送，已挂载的卡片会就地更新成新名字。 */
export function useCollabContactNames(): Map<string, string> {
  const [snapshot, setSnapshot] = useState(() => new Map(titleByContact));
  useEffect(() => {
    let alive = true;
    const update = () => {
      if (alive) setSnapshot(new Map(titleByContact));
    };
    listeners.add(update);
    void refreshCollabContactNames();
    const refreshOnFocus = () => {
      if (document.visibilityState !== "hidden") void refreshCollabContactNames();
    };
    document.addEventListener("visibilitychange", refreshOnFocus);
    window.addEventListener("focus", refreshOnFocus);
    return () => {
      alive = false;
      listeners.delete(update);
      document.removeEventListener("visibilitychange", refreshOnFocus);
      window.removeEventListener("focus", refreshOnFocus);
    };
  }, []);
  return snapshot;
}
