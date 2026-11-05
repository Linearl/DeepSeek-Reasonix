import type { RemoteTabRefView } from "../lib/types";
import type { ShortcutPlatform } from "../lib/keyboardShortcuts";
import type { TopicShortcutEntry } from "../lib/topicShortcuts";
import type { ProjectTreeVariant } from "../lib/projectTreeTopic";

type ProjectTreeImTopicSource = {
  platform?: string;
  label: string;
  title?: string;
  remoteId?: string;
};

export interface ProjectTreeProps {
  activeScope?: string;
  activeWorkspaceRoot?: string;
  activeTopicId?: string;
  activeSessionPath?: string;
  activeRemote?: RemoteTabRefView;
  imTopicSources?: Record<string, ProjectTreeImTopicSource>;
  variant?: ProjectTreeVariant;
  onOpenTopic: (scope: string, workspaceRoot: string, topicId: string, sessionPath?: string) => Promise<void> | void;
  onAddProject: (path?: string) => Promise<void>;
  onCreateTopic?: (scope: string, workspaceRoot: string) => Promise<void> | void;
  onCreateIsolatedWorktree?: (workspaceRoot: string) => Promise<void> | void;
  onRenameTopic?: (topicId: string, title: string) => Promise<void> | void;
  onTopicsChanged?: () => Promise<void> | void;
  refreshSignal?: number;
  /** tl1413b: rename receipt from entries that live outside the tree (top-bar
   * title editor, history panel). The tree paints it immediately — the same
   * optimistic paint the sidebar's own rename entry does in commitRenameTopic —
   * because the refreshSignal-driven refresh() only reloads pinned shells
   * (reloadProjectTreeTopics with no options reloads nothing), so a plain
   * refetch would leave expanded-folder rows stale until the changed-v2
   * round-trip. Monotonic seq; a lower/equal seq is ignored. */
  topicTitlePatch?: { topicId: string; title: string; seq: number };
  timeFilter: "all" | "10" | "20" | "1h" | "3h" | "5h" | "1d";
  onTimeFilterChange: (filter: "all" | "10" | "20" | "1h" | "3h" | "5h" | "1d") => void;
  searchExpanded?: boolean;
  searchFocusSignal?: number;
  showShortcutBadges?: boolean;
  shortcutPlatform?: ShortcutPlatform;
  onVisibleTopicsChange?: (topics: TopicShortcutEntry[]) => void;
  /** tl 1103 会话右键「复制 ▸」的「日志路径」取值（应用根 logs/desktop，与
   * topicbar 复制菜单同一装配层来源）；缺省时该项禁用。 */
  logDirectory?: string;
}
