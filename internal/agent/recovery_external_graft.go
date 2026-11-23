package agent

// tl1787 件3/B：改写前备份（*.pre-repair-*）的历史合并回主线——「折叠/改写
// 掉的历史不因删除唯一化而永久丢失」的执行核。
//
// 复用 1.34 合并体系的既有原语（promote 前缀接回同一族，不造新机制）：
// 	prefixGapForMessages	——按轮次对齐找外部副本里主线缺失的独有前缀
// 	                          （SessionContentPrefixGap 的核心，前导 system
// 	                          裁剪后直接复用，见 GraftSessionExternalPrefix
// 	                          注释的实测修正）
// 	graftPrefixOntoLines	——逐字节只增不改地拼回主线头部（system 前导之后）
// 	compactSessionEventLog	——graft 后消息索引整体位移，事件日志折叠为一条
// 	                          replace 记录（promote-prefix-graft 同款理由标记）
// 与 promote 的差别只有一点：promote 先换身份再接回落败链前缀，这里主线
// 身份不动、只把外部转录（备份内 <stem>.jsonl）的独有历史接回来——备份树
// 永不作为会话加载的约束原样保留（备份只被当数据读，LoadSessionContentSnapshot
// 与 PreviewRecoveryChain 的只读面同一原语）。
//
// 纯本地数据操作，全程零 LLM 调用（tl1787 功能定稿 B 的判据）。折叠场景
// （ABCD→折叠→ED→EDFG）下：主线=折叠摘要+新消息，备份=折叠前全文，graft
// 把备份里主线不认识的轮次接回主线头部 → 恢复「旧史在前、新史在后」的可查
// 完整历史。
//
// 加锁与失败语义：主线转录持 SessionRemovalGuard（promote 同款——运行时
// 写入面与删除链在 guard 上互斥）；任一侧快照装载失败=拒绝（damaged log
// 不得产生合并决策，promote 同一 fail-closed 纪律原样继承）。

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/provider"
)

// GraftExternalPrefixReport 是一次外部前缀接回的读数：接回多少条、分叉点、
// 合并后主线条数——「合并后历史可查」的对屏核对数（tl1787 验收4）。
type GraftExternalPrefixReport struct {
	// Grafted 是实际接回主线的消息条数（0 = 主线已包含全部，无事可做）。
	Grafted int `json:"grafted"`
	// ForkIndex 是备份侧的分叉消息偏移（-1 = 备份与主线无任何交点——两条
	// 无关链，接回被拒绝；与 promote 的 HasForkPoint 语义一致）。
	ForkIndex int `json:"forkIndex"`
	// AfterMessages 是合并后主线的消息总数。
	AfterMessages int `json:"afterMessages"`
}

// GraftSessionExternalPrefix 把 externalTranscript（改写前备份内的主线转录）
// 里 canonical 主线缺失的独有前缀接回 canonical 头部。只增不改：主线既有行
// 逐字节保留；接回后事件日志折叠为一条 replace 记录、listing 元数据
// （preview/轮次）经 UpdateSessionMeta 刷新——清单面与内容面同步成真。
//
// 前导裁剪（tl1787 实测修正）：gap 计算前把两侧快照的前导 system 消息裁掉
// （trimLeadingSystemMessages）——身份前导不是历史。不裁的真实形态（
// 20261123 诊断）：turnsOf 把前导 sys 组当首个轮次，宿主与备份的 sys 指纹
// 相同 → 首轮即判 present → gap 恒空（forkIndex=0），折叠历史永远接不回。
// graftPrefixOntoLines 本就把 system 行留在主线头部（插入点在其后），裁剪
// 与接回执行核的既有假设一致。
func GraftSessionExternalPrefix(mainPath, externalTranscript string) (GraftExternalPrefixReport, error) {
	report := GraftExternalPrefixReport{ForkIndex: -1}
	canonical, canonicalOK := LoadSessionContentSnapshot(mainPath)
	copied, copiedOK := LoadSessionContentSnapshot(externalTranscript)
	if !canonicalOK || !copiedOK {
		return report, fmt.Errorf("content snapshot unavailable; the merge decision needs both sides readable: %s", mainPath)
	}
	gap := prefixGapForMessages(
		trimLeadingSystemMessages(canonical.messages),
		trimLeadingSystemMessages(copied.messages),
	)
	if !gap.HasForkPoint() {
		// 备份与主线无任何交点：两条无关链，把整份备份接进主线上是破坏性的
		// （promote 的 HasForkPoint 判据同语义）。
		return report, fmt.Errorf("the backup shares no history with this session; merging unrelated chains is refused")
	}
	if len(gap.Messages) == 0 {
		// 主线已包含备份全部轮次：无事可做（幂等出口，merged=false）。
		report.ForkIndex = gap.ForkIndex
		if snap, snapOK := LoadSessionContentSnapshot(mainPath); snapOK {
			report.AfterMessages = snap.Len()
		}
		return report, nil
	}

	guard, err := TryAcquireSessionRemovalGuard(mainPath)
	if err != nil {
		return report, err
	}
	defer guard.Release()

	legacyMeta, _, err := LoadBranchMeta(mainPath)
	if err != nil {
		return report, err
	}

	raw, err := os.ReadFile(mainPath)
	if err != nil {
		return report, err
	}
	mergedLines, err := graftPrefixOntoLines(splitTranscriptLines(raw), gap.Messages)
	if err != nil {
		return report, err
	}
	if err := atomicWriteFileContext(context.Background(), mainPath, "external-prefix-graft", "transcript.external-prefix-graft", joinTranscriptLines(mergedLines), 0o600, true); err != nil {
		return report, err
	}
	mergedMsgs, err := decodeTranscriptMessages(mergedLines)
	if err != nil {
		return report, err
	}
	digest, _, err := digestAndSizeSessionMessages(mergedMsgs)
	if err != nil {
		return report, err
	}
	// 事件日志按消息索引记条目，graft 让全部既有索引位移——折叠为一条
	// replace 记录既是修正也是既有机制（promote-prefix-graft 同款）。
	if err := compactSessionEventLog(mainPath, mergedMsgs, digest, legacyMeta.Revision, "external-prefix-graft"); err != nil {
		return report, err
	}
	// listing 元数据与内容同步：graft 把更老的轮次接回开头，首条用户消息
	// 与轮次计数都变了——preview/turns 经 UpdateSessionMeta 刷新（listing
	// 面、picker 面不再陈旧；model 空串=保留原值）。
	if err := UpdateSessionMeta(mainPath, "", externalGraftPreview(mergedMsgs), len(turnsOf(mergedMsgs)), true); err != nil {
		// 元数据刷新失败不回滚内容（内容已落盘且自洽），如实上抛让调用方
		// 决定呈现；与 promote「catalog 刷新失败只记 skipped」同一口径。
		slog.Warn("graft: listing meta refresh failed", "main", mainPath, "err", err)
		return GraftExternalPrefixReport{Grafted: len(gap.Messages), ForkIndex: gap.ForkIndex, AfterMessages: len(mergedMsgs)}, nil
	}
	slog.Info("graft: external backup prefix merged onto the main line",
		"main", mainPath, "messages", len(gap.Messages), "forkIndex", gap.ForkIndex)
	return GraftExternalPrefixReport{Grafted: len(gap.Messages), ForkIndex: gap.ForkIndex, AfterMessages: len(mergedMsgs)}, nil
}

// trimLeadingSystemMessages 裁掉快照开头的 system 消息（身份前导）：gap 对
// 齐、known 集合都只在「历史」面上计算。与 leadingSystemLineCount（行形态，
// graftPrefixOntoLines 的插入点）同一语义的消息形态。
func trimLeadingSystemMessages(msgs []provider.Message) []provider.Message {
	i := 0
	for i < len(msgs) && msgs[i].Role == provider.RoleSystem {
		i++
	}
	return msgs[i:]
}

// externalGraftPreview 取合并后内容的首条用户消息做 listing 预览（graft 把
// 更老的轮次接回开头，对话开场白随之变化）；取数走 UserMessageText（显示面
// 同一原语：RawContent 优先、回退 wrapper 剥离）。找不到用户消息时回空串
// （UpdateSessionMeta 原样落空——与零消息会话的既有形态一致）。
func externalGraftPreview(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role == provider.RoleUser {
			if text := strings.TrimSpace(UserMessageText(m)); text != "" {
				return text
			}
		}
	}
	return ""
}
