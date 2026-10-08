import { useMemo } from "react";
import { ClipboardList } from "lucide-react";
import { useRuntimeSession } from "../lib/useRuntimeState";
import { useI18n } from "../lib/i18n";

// 任务408 异步决策点回访：回归汇总标记。用户回到会话时，运行态里的
// 持久待批卡片数直接变成一个可见入口（"N 项待你决定"）；点击补发该会话
// 的审批/提问卡片（既有 ReplayPendingPrompts 通道），逐个批完即断点续跑。
// 开关关闭时后端恒报 0，本组件不渲染——纯读取面，不新增任何轮询。

const COPY = {
  en: "{n} pending your decision",
  zh: "{n} 项待你决定",
  "zh-TW": "{n} 項待你決定",
} as const;

export function PendingCardsBadge({
  tabId,
  onClick,
}: {
  tabId?: string;
  onClick?: () => void;
}) {
  const { locale } = useI18n();
  const runtime = useRuntimeSession(tabId);
  const count = runtime.known ? runtime.state?.pendingCards ?? 0 : 0;
  const label = useMemo(
    () => (count > 0 ? COPY[locale].replace("{n}", String(count)) : ""),
    [count, locale],
  );
  if (count <= 0 || !label) return null;
  return (
    <button
      type="button"
      className="pending-cards-badge"
      title={label}
      onClick={onClick}
    >
      <ClipboardList size={13} aria-hidden="true" />
      <span>{label}</span>
    </button>
  );
}
