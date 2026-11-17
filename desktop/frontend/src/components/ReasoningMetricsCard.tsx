import { useI18n } from "../lib/i18n";
import {
  formatThinkingCharCount,
  formatThinkingSeconds,
  hasThinkingDuration,
  reasoningCharCount,
} from "../lib/reasoningMetrics";

// tl1671: 思考指标的悬浮卡片内容（耗时 + 字数两行）。
//
// 载体：既有 Tooltip 的 ReactNode 直渲（与 tl1582 停止按钮 hover 卡同一条路，
// 不新造弹层）。卡片本体是纯展示件——不点开思考内容也能看到两个数字。
//
// 文案与数字形态对齐上游 studio（esengine Studio）：studio 在思考折叠头旁
// 行内显示「思考 {secs} 秒 · {chars}」（i18n/msg/chat.ts 的
// chat.thought_chars_secs_s），秒保留一位小数、字数带「字」单位。本件按用户
// 拍板改成悬浮卡片，数字与单位照 studio 的口径。

export function ReasoningMetricsCard({
  reasoning,
  durationMs,
}: {
  /** 思考正文（实时流里是当前文本，落定后是完整文本）。 */
  reasoning: string | undefined;
  /** 思考耗时（毫秒）；缺值时只显示字数行。 */
  durationMs: number | undefined;
}) {
  const { t, locale } = useI18n();
  const chars = reasoningCharCount(reasoning);
  const timed = hasThinkingDuration(durationMs);
  return (
    <span className="tooltip__thoughtcard">
      <span className="tooltip__thoughtcard-row">
        <span className="tooltip__thoughtcard-label">{t("msg.thinkingDurationLabel")}</span>
        <span className="tooltip__thoughtcard-value">
          {timed
            ? t("msg.thinkingDurationValue", { s: formatThinkingSeconds(durationMs / 1000, locale) })
            : t("msg.thinkingDurationUnknown")}
        </span>
      </span>
      <span className="tooltip__thoughtcard-row">
        <span className="tooltip__thoughtcard-label">{t("msg.thinkingCharsLabel")}</span>
        <span className="tooltip__thoughtcard-value">
          {t("msg.thinkingCharsValue", { n: formatThinkingCharCount(chars, locale) })}
        </span>
      </span>
    </span>
  );
}
