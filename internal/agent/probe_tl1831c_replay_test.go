package agent

import (
	"testing"

	"reasonix/internal/provider"
)

// probe_tl1831c_replay_test.go — tl1831c 阶段②验收探针（tl1651 精神，只读）。
// 不写任何 session/sidecar 数据；session 文件不存在时 Skip（包级测试在他机零红）。
//
// 把 20261127 08:58 hash_mismatch 现场（stored a3213691… vs current 59cadec5…，
// covered_count=2371，pipeline=2360，diff 槽 2139）在真会话上重放一遍：
// 以现盘字节为封存侧（模拟 v10 封存的 corpus 形态：长度 2360 < covered 2371），
// 对 21:07 冲突 preserved #0（pipeline 槽 2139 的 tool 消息）施加 repair 家族
// 字节差，然后验证 tl1785 stale keep 在修复后的双重门下命中——旧门在该算术下
// 直接 return nil（2360≠2371 + role 直读 canonical）。
//
// 全程只读：真实 canonical 从不落盘，重放构造只存在于内存副本。

const probeTL1831cSessionPath = `C:\Users\yinji\AppData\Roaming\reasonix\projects\c--users-yinji-appdata-roaming-reasonix-global-workspace\sessions\20261119-144104.761501900-mimo-token-plan-mimo-v2.6-flash.jsonl`

// 08:58 日志读数（desktop.log.1）。
const (
	probeTL1831cCurrentPrefixHash = "59cadec5836fc2b9a04502df345809a6"
	probeTL1831cCoveredCount      = 2371
	probeTL1831cPipelineLen       = 2360
	probeTL1831cDiffIndex         = 2139
)

func TestProbeTL1831cStaleKeepReplay(t *testing.T) {
	s, err := LoadSession(probeTL1831cSessionPath)
	if err != nil {
		t.Skipf("probe session unavailable on this machine: %v", err)
	}
	msgs := s.Messages
	t.Logf("canonical msgs: %d", len(msgs))
	n := probeTL1831cCoveredCount
	if n > len(msgs) {
		n = len(msgs)
	}

	// 08:58 读数复现：current hash 与管线长度。
	cur := coveredPrefixHash(msgs, n)
	t.Logf("coveredPrefixHash(msgs[:%d]) = %s | 08:58 current = %s | match = %v",
		n, shortHash8(cur), shortHash8(probeTL1831cCurrentPrefixHash), cur == probeTL1831cCurrentPrefixHash)
	pipeline := coveredMessageFingerprints(msgs, n)
	t.Logf("pipeline len = %d | 08:58 = %d | covered = %d (旧门死 clause: len(pipeline) != covered_count)",
		len(pipeline), probeTL1831cPipelineLen, probeTL1831cCoveredCount)
	if len(pipeline) != probeTL1831cPipelineLen {
		t.Fatalf("pipeline len = %d, want %d (08:58 reading drifted — re-pin before trusting the replay)", len(pipeline), probeTL1831cPipelineLen)
	}

	// 槽 2139 身份：tool 槽（preserved #0），canonical 直读给出另一坐标。
	visible := sanitizedPipeline(msgs, n)
	canonIdx := pipelineCanonicalIndexes(msgs, n)
	slot := probeTL1831cDiffIndex
	if slot >= len(visible) {
		t.Fatalf("pipeline slot %d out of range (len %d)", probeTL1831cDiffIndex, len(visible))
	}
	t.Logf("pipeline slot %d: role=%s | canonical index=%d (canonical msgs[%d].Role=%s — 旧 role 门的失真读数)",
		slot, visible[slot].Role, canonIdx[slot], canonIdx[slot], func() string {
			ci := canonIdx[slot]
			if ci < 0 || ci >= len(msgs) {
				return "?"
			}
			return string(msgs[ci].Role)
		}())

	// 重放：以现盘字节为封存侧（与 v10 corpus 同形态：长度 2360），
	// 对 preserved #0 tool 槽施加 repair 家族字节差（截断参数修复形态）。
	replay := append([]provider.Message(nil), msgs...)
	for i := range replay {
		replay[i].ToolCalls = append([]provider.ToolCall(nil), replay[i].ToolCalls...)
	}
	ci := canonIdx[slot]
	if ci < 0 || ci >= len(replay) {
		t.Fatalf("canonical index of pipeline slot %d = %d, out of range", slot, ci)
	}
	if len(replay[ci].ToolCalls) > 0 {
		replay[ci].ToolCalls[0].Arguments = replay[ci].ToolCalls[0].Arguments + " "
	} else {
		replay[ci].Content += "\n(normalized)"
	}
	seal := CompactionState{Projection: ContextProjection{
		CoveredCount:         probeTL1831cCoveredCount,
		CoveredPrefixHash:    coveredPrefixHash(msgs, n),
		CoveredMessageHashes: coveredMessageFingerprints(msgs, n),
	}}
	v := projectionRepairDriftKeepVerdict(seal, replay)
	if len(v.keep) == 0 {
		t.Fatalf("stale keep did NOT fire on the 08:58 replay (refusal=%q) — 旧门死 clause 复活或 role 门仍失真", v.refusal)
	}
	if v.keep[0] != probeTL1831cDiffIndex {
		t.Fatalf("first keep slot = %d, want %d", v.keep[0], probeTL1831cDiffIndex)
	}
	t.Logf("stale keep FIRED on the 08:58 replay: slots=%v roles=%v canonical=%v refusal=%q → 零 drop（旧门在本算术下 return nil：pipeline %d ≠ covered %d）",
		v.keep, v.keepRoles, v.keepCanonIdx, v.refusal, probeTL1831cPipelineLen, probeTL1831cCoveredCount)

	// 反向钉：同一封存侧，user 槽真实篡改 → content 类拒绝（安全钉不松）。
	tamperIdx := -1
	for i := range visible {
		if visible[i].Role == provider.RoleUser {
			tamperIdx = i
			break
		}
	}
	if tamperIdx >= 0 {
		tci := canonIdx[tamperIdx]
		tampered := append([]provider.Message(nil), msgs...)
		tampered[tci].Content = tampered[tci].Content + "\n[rewritten by hand]"
		tv := projectionRepairDriftKeepVerdict(seal, tampered)
		if tv.refusal != "content" || len(tv.keep) != 0 {
			t.Fatalf("user-slot tamper: refusal=%q keep=%v, want content/empty (安全钉)", tv.refusal, tv.keep)
		}
		t.Logf("user-slot tamper still refused: slot=%d canonical=%d class=%s", tamperIdx, tci, tv.refusal)
	}
}
