import { useSyncExternalStore } from "react";
import { ListTree, MessageSquareText } from "lucide-react";
import { onSurfaceViewChange, getSurfaceView, setSurfaceView, type SessionSurfaceView } from "../lib/trajectoryViewPreference";
import { useT } from "../lib/i18n";

// 任务 704 — 标题栏「转录 | 轨迹」分段控件（DSH 同款 view ring 的 fork 落点：
// topicbar children 插槽，spacer 与操作栈之间）。选择按会话持久化
//（trajectoryViewPreference，localStorage 按 tabId）。窄窗由 CSS 退化：
// 分段控件收成双图标（TrajectoryView.css 同族媒体查询在 styles.css）。

export function TopicbarSurfaceSwitch() {
  const t = useT();
  const view = useSyncExternalStore(onSurfaceViewChange, getSurfaceView, () => "transcript" as SessionSurfaceView);
  return (
    <div className="topicbar__view-switch" role="tablist" aria-label={t("trajectory.switchLabel")}>
      <button
        type="button"
        role="tab"
        aria-selected={view === "transcript"}
        className={`topicbar__view-btn${view === "transcript" ? " topicbar__view-btn--on" : ""}`}
        title={t("trajectory.view.transcript")}
        onClick={() => setSurfaceView("transcript")}
      >
        <MessageSquareText size={14} />
        <span className="topicbar__view-btn-text">{t("trajectory.view.transcript")}</span>
      </button>
      <button
        type="button"
        role="tab"
        aria-selected={view === "trajectory"}
        className={`topicbar__view-btn${view === "trajectory" ? " topicbar__view-btn--on" : ""}`}
        title={t("trajectory.view.trajectory")}
        onClick={() => setSurfaceView("trajectory")}
      >
        <ListTree size={14} />
        <span className="topicbar__view-btn-text">{t("trajectory.view.trajectory")}</span>
      </button>
    </div>
  );
}
