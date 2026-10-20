// 任务 1023 阶段 0：未入档输入（已从检查点恢复）的前端整合面。
//
// 后端对账（UnfiledInputsForTab）严格只读；本模块只做三件事：
// 1. 按 tab 拉取对账结果（会话加载/切 tab 由组件重挂载触发）；
// 2. 「已处理」标记存 localStorage（unfiled:handled:v1），绝不回写
//    transcript 或事件日志——与阶段 0 只读口径一致；
// 3. 就地显示：把未入档条目按时间锚定插入 items 流（notice 形态），
//    由横幅面板承担复制/转草稿/重发等操作。

import { useCallback, useEffect, useRef, useState } from "react";
import { app } from "./bridge";
import { asArray } from "./array";
import type { Item } from "./useController";
import type { UnfiledInputView } from "./types";

const HANDLED_KEY = "unfiled:handled:v1";
const MAX_HANDLED_SESSIONS = 200;

function readHandledAll(): Record<string, number[]> {
  try {
    const raw = localStorage.getItem(HANDLED_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, number[]>;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writeHandledAll(all: Record<string, number[]>): void {
  try {
    // 上限裁剪：最旧的会话先淘汰，防 localStorage 无界增长。
    const keys = Object.keys(all);
    if (keys.length > MAX_HANDLED_SESSIONS) {
      for (const key of keys.slice(0, keys.length - MAX_HANDLED_SESSIONS)) delete all[key];
    }
    localStorage.setItem(HANDLED_KEY, JSON.stringify(all));
  } catch {
    // 存储失败退化为内存态：本次会话内不再提示即可。
  }
}

export function readHandledTurns(sessionKey: string): Set<number> {
  if (!sessionKey) return new Set();
  return new Set(readHandledAll()[sessionKey] ?? []);
}

export function markHandledTurns(sessionKey: string, turns: number[]): void {
  if (!sessionKey || turns.length === 0) return;
  const all = readHandledAll();
  const merged = new Set(all[sessionKey] ?? []);
  for (const turn of turns) merged.add(turn);
  all[sessionKey] = Array.from(merged).sort((a, b) => a - b);
  writeHandledAll(all);
}

export function useUnfiledInputs(tabId: string | undefined): {
  entries: UnfiledInputView[];
  loading: boolean;
  refresh: () => void;
} {
  const [entries, setEntries] = useState<UnfiledInputView[]>([]);
  const [loading, setLoading] = useState(false);
  const seqRef = useRef(0);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const refresh = useCallback(() => {
    if (!tabId || typeof app.UnfiledInputsForTab !== "function") {
      setEntries([]);
      return;
    }
    const seq = ++seqRef.current;
    setLoading(true);
    app
      .UnfiledInputsForTab(tabId)
      .then((result) => {
        if (!mountedRef.current || seq !== seqRef.current) return;
        setEntries(asArray<UnfiledInputView>(result).filter((entry) => entry && Number.isFinite(entry.turn)));
      })
      .catch(() => {
        if (!mountedRef.current || seq !== seqRef.current) return;
        setEntries([]);
      })
      .finally(() => {
        if (mountedRef.current && seq === seqRef.current) setLoading(false);
      });
  }, [tabId]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return { entries, loading, refresh };
}

// unfiledNoticeItem builds the in-transcript notice for one recovered input.
// Warn level keeps it outside the work-process fold (transcriptRows.ts), so the
// card stays visible at its anchored position.
export function unfiledNoticeItem(entry: UnfiledInputView, title: string): Item {
  return {
    kind: "notice",
    id: `unfiled:${entry.turn}`,
    level: "warn",
    code: "unfiled_input",
    title,
    text: entry.prompt,
  };
}

// withUnfiledNoticeItems interleaves recovered inputs into the transcript item
// stream, anchored by time: each card is inserted before the first item created
// after the input was submitted — exactly where the user message would have
// landed. Entries that cannot be anchored (no timestamps in the page) render at
// the end so they never disappear. Already-handled entries are skipped.
export function withUnfiledNoticeItems(
  items: Item[],
  entries: UnfiledInputView[],
  handledTurns: Set<number>,
  title: string,
): Item[] {
  const pending = entries.filter((entry) => !handledTurns.has(entry.turn));
  if (pending.length === 0) return items;
  const notices = pending
    .map((entry) => ({ entry, item: unfiledNoticeItem(entry, title) }))
    .sort((a, b) => (a.entry.time || 0) - (b.entry.time || 0));
  if (notices.length === 0) return items;

  const out: Item[] = [];
  let noticeIdx = 0;
  for (const item of items) {
    while (noticeIdx < notices.length) {
      const at = notices[noticeIdx].entry.time || 0;
      const created = (item as { createdAt?: number }).createdAt;
      if (at > 0 && typeof created === "number" && created > 0 && created >= at) {
        out.push(notices[noticeIdx].item);
        noticeIdx += 1;
        continue;
      }
      break;
    }
    out.push(item);
  }
  while (noticeIdx < notices.length) {
    out.push(notices[noticeIdx].item);
    noticeIdx += 1;
  }
  return out;
}

// countUnhandled is the banner number: only entries the user has not handled.
export function countUnhandled(entries: UnfiledInputView[], handledTurns: Set<number>): number {
  return entries.filter((entry) => !handledTurns.has(entry.turn)).length;
}
