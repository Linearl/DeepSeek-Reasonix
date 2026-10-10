// 任务 704 — 会话面视图选择（转录 | 轨迹），按会话持久化（DSH 同款）。
//
// 形制沿用 sessionExperience.ts 的模块 store + useSyncExternalStore：
// localStorage 按 tabId（会话标签身份）存档，切走再切回保持选择；缺省永远是
// "transcript"——铁律 2 之下，未开启实验室开关或无存档的会话一字不差地走转录。

import { useSyncExternalStore } from "react";

export type SessionSurfaceView = "transcript" | "trajectory";

const KEY_PREFIX = "reasonix-surface-view:";
const EVENT = "reasonix:surface-view";

let currentTabId = "";
let current: SessionSurfaceView = "transcript";
const listeners = new Set<() => void>();

function normalize(value: unknown): SessionSurfaceView {
  return value === "trajectory" ? "trajectory" : "transcript";
}

function storageKey(tabId: string): string {
  return KEY_PREFIX + tabId;
}

function emit(): void {
  for (const listener of listeners) listener();
  if (typeof window !== "undefined") {
    window.dispatchEvent(new CustomEvent(EVENT, { detail: { tabId: currentTabId, view: current } }));
  }
}

function readStored(tabId: string): SessionSurfaceView {
  if (typeof localStorage === "undefined" || tabId === "") return "transcript";
  try {
    return normalize(localStorage.getItem(storageKey(tabId)));
  } catch {
    return "transcript";
  }
}

/**
 * Point the store at a session tab (AppRuntimeView calls this on every
 * activeTabId change). The selection re-reads from storage so switching back
 * to a tab restores its persisted view. An empty id resets to transcript.
 */
export function setSurfaceViewTab(tabId: string): void {
  if (tabId === currentTabId) return;
  currentTabId = tabId;
  current = readStored(tabId);
  emit();
}

export function getSurfaceView(): SessionSurfaceView {
  return current;
}

export function setSurfaceView(next: SessionSurfaceView): void {
  const normalized = normalize(next);
  if (normalized === current) return;
  current = normalized;
  if (typeof localStorage !== "undefined" && currentTabId !== "") {
    try {
      localStorage.setItem(storageKey(currentTabId), normalized);
    } catch {
      // Storage unavailable (privacy mode): the choice stays session-local.
    }
  }
  emit();
}

export function onSurfaceViewChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useSurfaceView(): SessionSurfaceView {
  return useSyncExternalStore(onSurfaceViewChange, getSurfaceView, () => "transcript");
}

/** Test hook: reset module state between cases. */
export function resetSurfaceViewForTests(): void {
  currentTabId = "";
  current = "transcript";
  emit();
}
