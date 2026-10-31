package main

import (
	"os"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// tl1281 冷读解码门：非活跃会话的大转录不在后台/冷路径做全量解码。
//
// tl1279 实测 literalStore 313.86MB（四时点 88.62→205→88.62→313.86），-peek
// 归因 100% 来自会话消息解码；tl1280 清单里的 4 个 schema-2 大会话（65.88 /
// 32.53 / 172.99 / 103.94 MB）正是其来源。它们的 events 日志是 schema-2
// 封存态（tl1227 弃读），冷读兜底走 checkpoint 全量解码；self-heal 的
// read-model repair 又是第二次全量解码+全量重写。骨架 tab（任务 619②）本无
// 控制器，唯一的全量解码来源就是这些冷读兜底——门即收口于此。
//
// 门的形状：转录任一侧车（checkpoint / events 日志）达到
// agent.SessionTailThresholdBytes（32MiB，与首绘尾窗同一判据）即视为
// oversize，冷路径不再全量解码：
//   - 最新页从流式扫描（常数内存）的锚页服务，或冷尾窗页（C2 后封存
//     schema-2 的 LoadSessionTail 真截断）服务；
//   - 翻页 cursor 请求与字段查值回落为失败/未命中（tab 点击即构建，活路径
//     数秒内接管， 冷路径只服务骨架期与 idle 预取）；
//   - 三处 repair kick（read-model repair / cold self-heal repair / live
//     index rebuild）对 oversize 一律跳过，Debug 留痕。
//
// 用户体验面：小会话逐字节不变；大会话在骨架期首屏从「秒级全量解码」变为
// 「锚页/尾窗快读（内容可能落后最近一次保存一个周期，点击构建后由活路径
// 纠正）」。这是 836 报告同一取舍（首绘快读优先于全量 stall）在封存
// schema-2 上的延伸。

// historyColdArtifactBytes reports the transcript's on-disk size across both
// sidecars: max(checkpoint, event log). 0 when neither exists.
func historyColdArtifactBytes(sessionPath string) int64 {
	size := int64(0)
	if info, err := os.Stat(sessionPath); err == nil && !info.IsDir() {
		size = info.Size()
	}
	if info, err := os.Stat(store.SessionEventLog(sessionPath)); err == nil && !info.IsDir() && info.Size() > size {
		size = info.Size()
	}
	return size
}

// historyColdDecodeOversize reports whether the session's transcript is at or
// above the shared tail threshold, so cold paths must not full-decode it.
func historyColdDecodeOversize(sessionPath string) bool {
	return historyColdArtifactBytes(sessionPath) >= agent.SessionTailThresholdBytes()
}
