import { formatActionError } from "./actionErrors";
import type { ProjectTreeRefresh } from "./projectTreeArchive";
import type { ToastContextValue } from "./toast";
import type { Translator } from "./i18n";
import type { SessionCatalogBindings } from "./types";

// tl1363（用户 20261102 拍板）：手动「重载会话列表」的调用链。后端一次对账
// （清零绑定影子条目 + 同步注册投影 + 重挂活会话 + 发 reason="metadata" v2
// 事件），随后本文件夹做一次权威全量重拉（full=true replace 合并丢弃驻留残行，
// 无需重启/折叠展开）。后端失败不阻断重拉：残留行仍可见且错误可见，重载语义
// 保持可重试；清出残留时 info toast 报数。
export async function runProjectTreeReload({
  bindings,
  scope,
  workspaceRoot,
  folderKey,
  refresh,
  showToast,
  t,
}: {
  bindings: Pick<SessionCatalogBindings, "ReloadProjectTreeSessions">;
  scope: string;
  workspaceRoot: string;
  /** The clicked folder's tree key; empty = reload every folder. */
  folderKey: string;
  refresh: ProjectTreeRefresh;
  showToast: ToastContextValue["showToast"];
  t: Translator;
}): Promise<void> {
  let removed = 0;
  try {
    const result = await bindings.ReloadProjectTreeSessions?.(scope, workspaceRoot);
    removed = result?.removedTopicIds?.length ?? 0;
  } catch (err) {
    showToast(formatActionError("action.sessionListReload", err), "error");
  }
  await refresh({
    reloadTopicKeys: folderKey ? [folderKey] : undefined,
    reloadAllTopics: !folderKey,
  });
  if (removed > 0) showToast(t("projectTree.reloadSessionsDone", { n: removed }), "info", { durationMs: 4000 });
}
