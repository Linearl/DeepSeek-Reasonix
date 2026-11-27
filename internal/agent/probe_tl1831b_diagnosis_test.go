package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// probe_tl1831b_diagnosis_test.go — tl1831b 调研专用只读探针（tl1651 精神）。
// 不写任何 session/sidecar 数据；session 文件不存在时 Skip（包级测试在他机零红）。
//
// 目标：把 20261127 08:58 hash_mismatch（stored a3213691… vs current 59cadec5…）
// 的失配点钉到字节级——重算 coveredPrefixHash 对照日志读数、定位 pipeline 槽
// 2139 的消息内容、验证「历史 user turn 的注入块从有到无」假说。

const probeTL1831bSessionPath = `C:\Users\yinji\AppData\Roaming\reasonix\projects\c--users-yinji-appdata-roaming-reasonix-global-workspace\sessions\20261119-144104.761501900-mimo-token-plan-mimo-v2.6-flash.jsonl`

// 08:58 日志读数（desktop.log.1）。
const (
	probeStoredPrefixHash  = "a3213691611fad9dd86ed7e476431087" // sidecar projection_version=10 封存
	probeCurrentPrefixHash = "59cadec5836fc2b9a04502df345809a6" // 08:58 重算
	probeCoveredCount      = 2371
	probePipelineLen       = 2360
	probeDiffIndex         = 2139
	probeStoredSlotHash    = "fea9c498fbfb9534dd10674ba40d364e"
)

func TestProbeTL1831bMismatchLocalization(t *testing.T) {
	s, err := LoadSession(probeTL1831bSessionPath)
	if err != nil {
		t.Skipf("probe session unavailable on this machine: %v", err)
	}
	msgs := s.Messages
	t.Logf("canonical msgs: %d (08:58 log: 2582)", len(msgs))
	n := probeCoveredCount
	if n > len(msgs) {
		n = len(msgs)
	}

	cur := coveredPrefixHash(msgs, n)
	t.Logf("recomputed legacy coveredPrefixHash(msgs[:%d]) = %s | 08:58 current = %s | match(current) = %v",
		n, shortHash8(cur), shortHash8(probeCurrentPrefixHash), cur == probeCurrentPrefixHash)

	if s.normalizedDirty && len(s.rawMessages) > 0 {
		pre := coveredPrefixHash(s.rawMessages, n)
		t.Logf("recomputed legacy coveredPrefixHash(rawMessages pre-repair) = %s | 08:58 stored = %s | match(stored) = %v",
			shortHash8(pre), shortHash8(probeStoredPrefixHash), pre == probeStoredPrefixHash)
	} else {
		t.Logf("this load needed NO repair (normalizedDirty=false) — 08:58 load had load_repair_transcript=true")
	}

	fps := coveredMessageFingerprints(msgs, n)
	t.Logf("legacy per-message pipeline len: %d (08:58 log: %d)", len(fps), probePipelineLen)

	visible := provider.SanitizeToolPairing(modelInputMessages(msgs[:n]))
	t.Logf("sanitized pipeline len: %d; pre-sanitize model-visible len: %d", len(visible), func() int {
		return len(modelInputMessages(msgs[:n]))
	}())

	idx := probeDiffIndex
	if idx >= len(visible) {
		idx = len(visible) - 1
	}
	m := visible[idx]
	head := m.Content
	if len(head) > 300 {
		head = head[:300]
	}
	rawHead := m.RawContent
	if len(rawHead) > 200 {
		rawHead = rawHead[:200]
	}
	t.Logf("pipeline slot %d: role=%s origin=%s | content(%d chars) head=%q | raw(%d) head=%q | providerContent=%d chars | has <current-time>=%v has <context-state>=%v has 【上下文】=%v",
		idx, m.Role, m.Origin, len(m.Content), head, len(m.RawContent), rawHead,
		len(m.ProviderContent),
		strings.Contains(m.Content, "<current-time>"),
		strings.Contains(m.Content, "<context-state>"),
		strings.Contains(m.Content, "【上下文】"))

	// 全前缀扫描：user 槽里注入块形态分布（slot 级）。
	withTime, withState, withNew := 0, 0, 0
	for i := range visible {
		if visible[i].Role != provider.RoleUser {
			continue
		}
		if strings.Contains(visible[i].Content, "<current-time>") {
			withTime++
		}
		if strings.Contains(visible[i].Content, "<context-state>") {
			withState++
		}
		if strings.Contains(visible[i].Content, "【上下文】") {
			withNew++
		}
	}
	t.Logf("user slots in covered prefix: <current-time>=%d <context-state>=%d 【上下文】=%d",
		withTime, withState, withNew)

	// stripped 配方对照：剔除 transient 后整链是否稳定（对全历史重算一次，
	// 与「假如旧 sidecar 用 stripped 配方封存」的假想值无 stored 对照，但可
	// 观察剥离是否改变管线长度与指纹分布）。
	strippedFPs := strippedCoveredMessageFingerprints(msgs, n)
	strippedDiverge := 0
	for i := 0; i < len(fps) && i < len(strippedFPs); i++ {
		if fps[i] != strippedFPs[i] {
			strippedDiverge++
		}
	}
	t.Logf("stripped pipeline len: %d; slots where legacy≠stripped fingerprint: %d", len(strippedFPs), strippedDiverge)
}
