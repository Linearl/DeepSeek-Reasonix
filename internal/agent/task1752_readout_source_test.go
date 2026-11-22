package agent

import (
	"testing"

	"reasonix/internal/provider"
)

// tl1752：实测/估算标签与读数数字同源收口。任务1025 的标签判定
// （admission.observedPrompt>0）与读数链脱节——observedPrompt 的唯一赋值在
// 超限错误恢复（context_recovery.go），正常轮 usage 校准只写内存 calibration
// 不写它，且 admission 不持久化。20261122 现场：重启后读数已走盘上锚（实测）
// 标签却卡「估算」；压缩后正常轮校准也不翻标签。本文件钉住：
// snapshot.ReadoutMeasured 恒等于 observedContextTokens 的 ok——即「主读数
// 是否锚（实测）算出」的那个布尔本身。
// ① 锚在=实测；② 冷启动无锚=估算；③ fold 装成清锚后回落估算、下一轮采样
// 重锚即翻实测；④ 重启回读盘上锚=实测；⑤ 重建（Estimated）usage 不翻实测；
// ⑥ 语义分离：正常轮采样不写 admission.ObservedPrompt（legacy 链保持超限
// 实测语义，旧前端回退链不被污染）。

// 钉①：live 锚在 → ReadoutMeasured=true 且 ProjectedTokens=锚读数（同源）。
func TestReadoutMeasuredTrueWithLiveAnchor(t *testing.T) {
	a := tl1570Agent(t, &failingSummaryProvider{}, tl1570TranscriptPath(t), "model-a")
	if a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("cold-start agent labeled its estimator readout as measured")
	}
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)

	snap := a.ContextMaintenanceSnapshot()
	if !snap.ReadoutMeasured {
		t.Fatalf("anchored readout not labeled measured")
	}
	want, ok := a.observedContextTokens(visible)
	if !ok {
		t.Fatalf("precondition: anchor rejected")
	}
	if snap.ProjectedTokens != want {
		t.Fatalf("ProjectedTokens=%d diverged from the anchor readout %d", snap.ProjectedTokens, want)
	}
}

// 钉②（验收3）：全新会话无锚 → ReadoutMeasured=false（冷启动首轮正确标估算）。
func TestReadoutMeasuredFalseOnColdStart(t *testing.T) {
	a := tl1570Agent(t, &failingSummaryProvider{}, tl1570TranscriptPath(t), "model-a")
	if a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("fresh session labeled the cold estimate as measured")
	}
}

// 钉③（验收2 机制面）：fold 装成清锚（tl1600）后标签随锚回落估算（新视图由
// 估算服务=诚实表达）；下一轮可信采样重锚后 1 采样内翻回实测。
func TestReadoutMeasuredClearsOnFoldInstallAndRearms(t *testing.T) {
	a := tl1570Agent(t, &failingSummaryProvider{}, tl1570TranscriptPath(t), "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)
	if !a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("precondition: anchored readout not measured")
	}

	a.clearContextBaseline() // fold install leg（Prepare→clear 链由 tl1600 测试钉住）
	if a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("cleared anchor still labeled measured")
	}

	tl1570StoreAnchor(a, 3100, 130, int64(charsOfMessages(a.modelVisibleMessages()))-10) // 下一轮可信采样
	if !a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("re-anchored readout did not flip back to measured within one sample")
	}
}

// 钉④（验收1 端到端重启腿）：进程 A 的可信采样落盘 → 进程 B（同转录同目标）
// 首轮请求前 snapshot 即 ReadoutMeasured=true——重启后不撞超限也标实测，
// 读数=盘上锚回读值。这是 20261122 现场①（1.2M+估算）的回归钉。
func TestReadoutMeasuredSurvivesRestart(t *testing.T) {
	path := tl1570TranscriptPath(t)
	a := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)

	b := tl1570Agent(t, &failingSummaryProvider{}, path, "model-a")
	snap := b.ContextMaintenanceSnapshot()
	if !snap.ReadoutMeasured {
		t.Fatalf("restarted agent labeled its persisted-anchor readout as estimated")
	}
	want, ok := b.observedContextTokens(b.modelVisibleMessages())
	if !ok {
		t.Fatalf("precondition: persisted anchor rejected on restart")
	}
	if snap.ProjectedTokens != want {
		t.Fatalf("restarted ProjectedTokens=%d diverged from the anchor readout %d", snap.ProjectedTokens, want)
	}
}

// 钉⑤：重建（Estimated）usage 永不落地锚（tl1570 钉③的同源面）——标签也不翻实测。
func TestReadoutMeasuredRejectsUntrustedUsage(t *testing.T) {
	a := tl1570Agent(t, &failingSummaryProvider{}, tl1570TranscriptPath(t), "model-a")
	a.sess.output.activeReqShape.Store(&requestCalibrationShape{requestChars: 1000, compactChars: 1000})
	a.storeContextBaseline(&provider.Usage{PromptTokens: 500, Estimated: true})
	if a.ContextMaintenanceSnapshot().ReadoutMeasured {
		t.Fatalf("estimated usage labeled the readout as measured")
	}
}

// 钉⑥（语义分离）：正常轮采样路径不写 admission.ObservedPrompt——旧前端
// legacy 链的「实测」语义仍是超限恢复实测（context_recovery），单源收口后
// 两条链互不污染。
func TestSamplingPathLeavesAdmissionObservedPromptAlone(t *testing.T) {
	a := tl1570Agent(t, &failingSummaryProvider{}, tl1570TranscriptPath(t), "model-a")
	visible := a.modelVisibleMessages()
	tl1570StoreAnchor(a, 3000, 120, int64(charsOfMessages(visible))-10)
	if a.lastAdmission().ObservedPrompt != 0 {
		t.Fatalf("sampling path wrote admission.ObservedPrompt — legacy semantics polluted")
	}
}
