// tl1350: host 协议消息折叠分类器。
//
// host 生成的协议消息（session-context 信封、压缩 briefing、reasoning-language
// 约束、feedback/回信 nudge、finalization nudge 等英文长段）走 notice 通道进
// transcript（desktop/history_host_guidance.go hostGuidanceRows → role=notice，
// live 侧 steer 事件同形态），此前以全宽 NoticeCard/SteerCard 逐字铺开，一条
// 就能挤掉整屏对话（用户 20261101 报）。后端 IsHostProtocolMessage 已把机器
// 契约类（压缩 briefing / pinned-context revision）整条滤出 transcript，但
// guidance 类必须可见（任务 172：host 插话不可静默）——所以修法是折叠而非
// 删除：默认一行摘要条（⚙ host context · 首行），点击展开全文。可见性契约
// 保留，占用压到一行/条。
//
// 锚点全部取自 host 独有标记（后端常量/注入文本首句的镜像），用户措辞或真实
// 用户 steer 不会命中；notice 文本可能带 "↪ " 前缀（hostGuidanceRows 统一加），
// 匹配前先剥掉。命中即整条进折叠条（任务书方案①+③）。

/** 供折叠条剥掉 hostGuidanceRows/live steer 统一加的 ↪ 前缀。 */
export function hostProtocolNoticeBody(text: string): string {
  const trimmed = text.trimStart();
  return trimmed.startsWith("↪ ") ? trimmed.slice(2) : trimmed;
}

/**
 * TransientUserBlockTags（internal/agent/preview.go）的前端镜像：这些块是
 * host 前置给 user 轮的机器上下文，正常已被 StripTransientUserBlocks 剥净；
 * 出现在 notice 文本里即原始块泄漏（旧会话历史/剥块遗漏），一律折叠。
 */
const HOST_BLOCK_TAGS: readonly string[] = [
  "response-language",
  "reasoning-language",
  "memory-update",
  "background-jobs",
  "active-goal",
  "autoresearch-runtime",
  "hook-context",
  "capability-route",
  "interrupted-turn-recovery",
  "execution-policy",
  "subagent-policy",
  "exec-speed-mode",
  "context-state",
  "current-time",
  "session-context",
];

/**
 * host 协议/引导消息的首句锚点（后端注入文本的稳定 lead，逐字镜像）：
 *  - FeedbackNudgeMarker（internal/agent/feedback_nudge.go，任务 172 T1/T2）
 *  - CollabReplyNudgeMarker（internal/agent/collab_reply_nudge.go，任务 530）
 *  - compactionInstruction 首行（internal/agent/compact.go）
 *  - planResearchNudgeMessage / readinessAdvisoryRetryMessage /
 *    emptyFinalRetryMessage / loop 纠偏 / todo 进度重定向首句
 *    （internal/agent/{plan_research_gate,agent,run_loop,goal_run_boundary}.go）
 */
const HOST_PROTOCOL_LEADS: readonly string[] = [
  "[Feedback inbox nudge",
  "[跨会话回信提醒",
  "Compact the preceding conversation prefix into a durable resume briefing",
  "Host note before the plan:",
  "Continue this task. The host could not settle ",
  "The previous assistant response finished without any visible answer text.",
  "Your previous reply was interrupted because it repeated the same text.",
  "Host progress reset: ",
  "Host progress redirect: ",
  "Your tool-call round limit (",
  "This task has reached its ",
];

// Lead match is anchored to line starts: the plain hostGuidanceRows row is
// `<lead>…` outright, and a partially-stripped carrier leaves the lead at the
// start of its own line — free user prose never starts a line with these.
const HOST_PROTOCOL_LEAD_RES: readonly RegExp[] = HOST_PROTOCOL_LEADS.map((lead) =>
  new RegExp(`(?:^|\\n)${lead.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`),
);
const HOST_BLOCK_TAG_RES: readonly RegExp[] = HOST_BLOCK_TAGS.map((tag) =>
  new RegExp(`<${tag}(?:[\\s>])`),
);

// isHostProtocolNotice reports whether a notice text is a host protocol or
// host-guidance message that should fold into the compact bar. Only the
// host writes these markers, so a real user steer never matches.
export function isHostProtocolNoticeText(text: string): boolean {
  const body = hostProtocolNoticeBody(text);
  if (!body) return false;
  for (const lead of HOST_PROTOCOL_LEAD_RES) {
    if (lead.test(body)) return true;
  }
  for (const tag of HOST_BLOCK_TAG_RES) {
    if (tag.test(body)) return true;
  }
  return false;
}
