import { purgeTrashBatch, type TrashBatchProgress, type TrashFailure } from "../lib/trashOperations";
import { useCallback, useEffect, useRef, useState } from "react";
import { RotateCw } from "lucide-react";
import { app } from "../lib/bridge";
import type { SessionMeta } from "../lib/types";
import { useT } from "../lib/i18n";
import { useManagementT } from "../lib/managementLocale";
import { useToast } from "../lib/toast";
import { reportFrontendLog } from "../lib/frontendLog";
import { trashFamilyRows } from "../lib/trashFamily";
import { ManagementPageShell } from "./ManagementPageShell";
import { HistoryPanel } from "./HistoryPanel";
import { useConfirmDialog } from "./ConfirmDialog";
import "./TrashPage.css";

const noop = () => {};
// tl1306：失败明细要带会话名与原因一起展示；name 在批次开始时快照，
// 刷新/本地过滤后仍可读。
type TrashFailureDetail = TrashFailure & { name: string };
export function TrashPage({ active, onBack, list, restore, purge }: {
  active: boolean; onBack: () => void; list: () => Promise<SessionMeta[]>;
  restore: (path: string) => Promise<void>; purge: (path: string) => Promise<void>;
}) {
  const t = useT(); const m = useManagementT();
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [failedPaths, setFailedPaths] = useState<string[]>([]);
  // tl1306：批量清空/恢复的进度条与失败原因明细。
  const [progress, setProgress] = useState<TrashBatchProgress | null>(null);
  const [failures, setFailures] = useState<TrashFailureDetail[]>([]);
  const busyRef = useRef(false); const seq = useRef(0);
  // tl1306：异步批次完成时人可能已离开本页（页面 hidden 不卸载），
  // 用 ref 读到最新 active 决定是否补全局 toast。
  const activeRef = useRef(active); activeRef.current = active;
  const toast = useToast();
  const { confirm, dialog, dismiss } = useConfirmDialog();
  useEffect(() => { if (!active) dismiss(); }, [active, dismiss]);
  const refresh = useCallback(async (afterMutation = false) => {
    const generation = ++seq.current;
    setLoading(true);
    try {
      const value = await list();
      if (generation !== seq.current) return;
      setSessions(value); setLoadFailed(false);
    } catch (err) {
      if (generation !== seq.current) return;
      setLoadFailed(true);
      if (afterMutation) setNotice(m("refreshedFailed"));
      // tl1449（症状④）：列表加载失败此前连 err 都丢弃——横幅关掉后零痕迹可查。
      reportFrontendLog("trash-page", "trash list load failed", err instanceof Error ? err.message : String(err), "error");
    } finally { if (generation === seq.current) setLoading(false); }
  }, [list, m]);
  useEffect(() => { if (active && !busyRef.current) void refresh(); }, [active, refresh]);
  useEffect(() => () => { seq.current++; }, []);
  const mutate = async (paths: string[], kind: "restore" | "purge") => {
    if (busyRef.current) return;
    const targets = [...new Set(paths)];
    // 名字快照在批次开始时取：成功项会从本地列表移除，失败项刷新后也可能
    // 消失；明细里的会话名不能依赖完成后的列表。
    const names = new Map(sessions.map((item) => [item.path, item.topicTitle || item.title || item.preview || t("history.emptySession")]));
    busyRef.current = true; setBusy(true); setLastKind(kind); ++seq.current; setFailedPaths([]); setFailures([]);
    // tl1306：批量才挂进度条——单条操作保持原有静默体验，无进度噪声。
    setProgress(targets.length > 1 ? { done: 0, total: targets.length, succeeded: 0, failed: 0 } : null);
    try {
      const result = await purgeTrashBatch(targets, kind === "restore" ? restore : purge, (event) => setProgress(event));
      const removed = new Set(result.succeeded);
      setSessions((current) => current.filter((item) => !removed.has(item.path)));
      setFailedPaths(result.failed);
      setFailures(result.failures.map((item) => ({ ...item, name: names.get(item.path) ?? item.path })));
      // tl1449（症状④）：清空/恢复批次失败此前只在页内展示明细——页面被关掉（或
      // 被 ManagementSurface 看门狗误杀卸载）后，desktop.log 与前端日志环零痕迹。
      if (result.failed.length > 0) reportFrontendLog("trash-page", `${kind} batch finished with failures`,
        `${result.failed.length}/${targets.length} failed — first: ${result.failures[0]?.path} (${result.failures[0]?.reason})`, "warn");
      const summary = targets.length > 1 ? m("batchResult", { success: result.succeeded.length, failed: result.failed.length })
        : result.failed.length ? m("operationFailed") : m(kind === "restore" ? "restored" : "deleted");
      setNotice(summary);
      // tl1306：大批量异步执行期间允许离开本页（页面 hidden 不卸载，批次继续跑）；
      // 完成时人不在页上就补一条全局 toast，回到页面后摘要与失败明细仍在。
      if (!activeRef.current) toast.showToast(summary, result.failed.length > 0 ? "warn" : "info", { durationMs: 6000 });
      await refresh(result.succeeded.length > 0);
    } finally { busyRef.current = false; setBusy(false); setProgress(null); }
  };
  const requestPurge = async (paths: string[], clear: boolean) => {
    if (busyRef.current) return;
    const snapshot = [...new Set(paths)];
    const name = sessions.find((item) => item.path === snapshot[0]);
    if (await confirm({ title: m(clear ? "clearTitle" : "purgeTitle"),
      message: clear ? m("clearDescription", { n: snapshot.length }) : m("purgeDescription", { name: name?.topicTitle || name?.title || name?.preview || t("history.emptySession") }),
      confirmLabel: m("confirm"), cancelLabel: m("cancel"), tone: "danger" })) await mutate(snapshot, "purge");
  };
  const [lastKind, setLastKind] = useState<"restore" | "purge">("purge");
  // tl1429d2：标题计数与默认视图一致——同族多桶合并后按族计（副本桶不再各占一行）。
  const familyCount = trashFamilyRows(sessions.filter((item) => !item.recoveryCopy)).length;
  return <ManagementPageShell active={active} onBack={onBack} title={`${t("history.trashTitle")} · ${familyCount}`}
    description={m("trashDescription")} actions={<><button className="btn btn--small" disabled={busy || loading || sessions.length === 0} onClick={() => { const paths = sessions.map((item) => item.path); setLastKind("restore"); void mutate(paths, "restore"); }}>{m("restoreAll")}{sessions.length > 0 ? ` (${sessions.length})` : ""}</button><button className="btn btn--small" disabled={busy || loading} onClick={() => void refresh()}><RotateCw size={14} />{m("refresh")}</button><button className="btn btn--small btn--danger history-clear" disabled={busy || !sessions.some((item) => !item.recoveryCopy)} onClick={() => void requestPurge(sessions.filter((item) => !item.recoveryCopy).map((item) => item.path), true)}>{t("history.clearTrash")}</button></>}>
    {loadFailed && <div className="management-notice" role="alert">{m("loadFailed")}<button className="btn btn--secondary btn--small" disabled={busy} onClick={() => void refresh()}>{m("retry")}</button></div>}
    {notice && <div className="management-notice" role="status">{notice}{failedPaths.length > 0 && <button className="btn btn--secondary btn--small" disabled={busy} onClick={() => void mutate(failedPaths, lastKind)}>{m("retryFailed")}</button>}</div>}
    {!busy && failures.length > 0 && <div className="management-notice trash-failure-detail" role="status">
      <span>{m("failureDetail")}</span>
      <ul>{failures.map((item) => <li key={item.path}>{item.name}: {item.reason}</li>)}</ul>
    </div>}
    {/* tl1306：批量清空/恢复进行中的进度条（x/N 已删）。批次结束即隐藏。 */}
    {busy && progress && progress.total > 1 && <div className="trash-batch-progress" role="status">
      <span className="trash-batch-progress__label">{m(lastKind === "restore" ? "restoreProgress" : "clearProgress", { done: progress.done, total: progress.total })}</span>
      <div className="trash-batch-progress__track" role="progressbar" aria-valuemin={0} aria-valuemax={progress.total} aria-valuenow={progress.done}>
        <div className="trash-batch-progress__fill" style={{ width: `${Math.round((progress.done / progress.total) * 100)}%` }} />
      </div>
    </div>}
    {loading && <div className="management-notice" role="status">{m("loading")}</div>}
    {/* tl1474：首屏加载显示骨架行（project-tree skeleton 同款 pulse 惯用法），
        加载不再是空白面板；刷新已有列表时不重放骨架，避免闪烁。 */}
    {loading && sessions.length === 0 && !loadFailed && (
      <div className="trash-page__skeleton" aria-hidden="true">
        {[0, 1, 2, 3, 4].map((row) => (
          <div key={row} className="trash-page__skeleton-row">
            <span className="trash-page__skeleton-bar" />
            <span className="trash-page__skeleton-bar trash-page__skeleton-bar--short" />
          </div>
        ))}
      </div>
    )}
    <div style={{ display: sessions.length === 0 && (loading || loadFailed) ? "none" : "contents" }}><HistoryPanel presentation="page" active={active} sessions={sessions} kind="trash" running={false} busy={busy}
      onClose={onBack} onResume={noop} onDelete={noop} onRename={noop}
      onPreview={async (path) => (await app.PreviewSession(path)) ?? []}
      onRestore={async (path) => { await mutate([path], "restore"); }}
      onPurge={async (path) => { await requestPurge([path], false); }}
      onPurgeAll={async (paths) => { await requestPurge(paths, true); }} /></div>
    {active && dialog}
  </ManagementPageShell>;
}
