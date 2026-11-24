package agent

// tl1787 续作（tl1674 阶段二定案，用户 20261124 拍板）：pre-repair 分叉的
// **元信息拼接**——档案袋（sidecar）记录层。
//
// 概念：pre-repair 备份本质是分叉——备份快照=被换掉的旧链，改写后的主线=
// 新链续段，拼接即长链。合并是**纯元信息操作**：
//   - 拼接点登记 / 段边界：记在 <stem>.splices.json（store.SessionSpliceLedger）
//   - 校验值重算：档案袋自身的三条校验值（段哈希/拼接点前后边界指纹/备份
//     内容摘要）在登记时本地重算（零额度）——主线自己的 ContentDigest/
//     Turns/Revision 与 lineage 字段**原值保留**（投影内容一个字节不动，
//     那些键未失效）
//   - 投影零改动：本层绝不写转录、绝不写事件日志、绝不调模型
//
// 拼接判据（内容连续性，纯本地读数）——对裁掉前导 system 消息后的两侧：
//   1. 备份已含于主线（covered）：无事可登记；
//   2. **分叉长链**（主线形态）：两侧共享一条精确公共前缀（≥1 条消息）且
//      两侧各自有独有续段——丢失段=备份[k:]，拼接点=主线第 k 条（长链=
//      主线[:k] + 备份[k:] + 主线[k:]）。这是修复截断/折叠后「备份=旧链、
//      主线=新链续段」的真实形态；
//   3. **折叠续接**（改写锚点形态）：无前缀关系但桌面层提供了修复记录锚点
//      （repairlog 的 BackupDir 命中 + 时间线备份先于改写）——整条旧链接在
//      新链之前（拼接点=0）。锚点校验在调用方（desktop sweep），
//      本层只接受锚点参数并记入档案袋。
//
// 失败回退：档案袋写原子（tmp+rename），失败即无登记，主线零触碰（投影
// 无损的平凡推论）。重复登记拒绝（幂等：同一 backupDir 只记一次）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// PreRepairSpliceKind 是拼接形态令牌（档案袋记录与面板展示共用）。
type PreRepairSpliceKind string

const (
	// PreRepairSpliceDiverged 分叉长链：两侧共享精确公共前缀且各有独有续段
	// （修复截断/折叠后备份=旧链、主线=新链续段的真实形态）。
	PreRepairSpliceDiverged PreRepairSpliceKind = "diverged-long-chain"
	// PreRepairSpliceFoldContinuation 折叠续接：无前缀关系，凭修复记录锚点
	// +时间线登记整链拼接（拼接点=0）。
	PreRepairSpliceFoldContinuation PreRepairSpliceKind = "fold-continuation"
)

// PreRepairSplicePlan 是拼接判据的输出：可拼接时给出形态与段边界；不可拼
// 接时 Kind 为空、Reason 给人话原因（面板/自愈日志共用）。
type PreRepairSplicePlan struct {
	Kind PreRepairSpliceKind `json:"kind,omitempty"`
	// SharedPrefix 是两侧（裁掉前导 system 后）的精确公共前缀消息数 k。
	SharedPrefix int `json:"sharedPrefix,omitempty"`
	// SegmentStart/SegmentEnd 是备份侧丢失段的边界 [start,end)（trim 口径；
	// diverged 形态 start=SharedPrefix，fold-continuation 形态 start=0）。
	SegmentStart int `json:"segmentStart,omitempty"`
	SegmentEnd   int `json:"segmentEnd"`
	// SplicePoint 是主线侧的拼接位置（trim 口径；长链=owner[:point]+段
	// +owner[point:]）。diverged=SharedPrefix，fold-continuation=0。
	SplicePoint int `json:"splicePoint,omitempty"`
	// SegmentMessages 是丢失段的条数（=SegmentEnd-SegmentStart）。
	SegmentMessages int `json:"segmentMessages"`
	// Reason 是不可拼接时的人话原因（可拼接时为空）。
	Reason string `json:"reason,omitempty"`
}

// PreRepairSplicePlanSplicable 报告该判据结果是否可登记拼接（包级函数形态
// ——本支实测该方法形态在本机工具链上不稳定，函数形态与包内其余函数同一
// 已验证形态）。
func PreRepairSplicePlanSplicable(p PreRepairSplicePlan) bool { return p.Kind != "" }

// PreRepairSpliceEntry 是档案袋里的一条拼接登记（全部是元信息：索引、边界
// 与校验值，不含任何消息正文）。
type PreRepairSpliceEntry struct {
	SchemaVersion int    `json:"schemaVersion"`
	BackupDir     string `json:"backupDir"`
	// BackupTranscript 是备份内主线转录的完整路径（查询面按它重放长链）。
	BackupTranscript string `json:"backupTranscript"`
	// Stamp 是备份目录名携带的 UTC 紧凑时间戳（改写点时间戳读数之一）。
	Stamp string `json:"stamp"`
	Kind  string `json:"kind"`
	// RegisteredAt 是登记时刻（RFC3339）。
	RegisteredAt string `json:"registeredAt"`
	// RepairAt 是修复记录锚点（unix 秒；0=无 repairlog 命中）。
	RepairAt int64 `json:"repairAt,omitempty"`
	// SharedPrefix/SplicePoint/SegmentStart/SegmentEnd 与判据同口径（trim 后
	// 的消息索引）。
	SharedPrefix int `json:"sharedPrefix"`
	SplicePoint  int `json:"splicePoint"`
	SegmentStart int `json:"segmentStart"`
	SegmentEnd   int `json:"segmentEnd"`
	// 校验值（登记时本地重算的三条 + 两条摘要）：
	// SegmentHash 是丢失段的消息摘要；BoundaryPrevHash/BoundaryNextHash 是
	// 主线拼接点前后两条消息的指纹（前后接缝可校验）；OwnerMessages/
	// BackupDigest 是登记时两侧的规模读数（漂移检测）。
	SegmentHash      string `json:"segmentHash"`
	BoundaryPrevHash string `json:"boundaryPrevHash,omitempty"`
	BoundaryNextHash string `json:"boundaryNextHash,omitempty"`
	OwnerMessages    int    `json:"ownerMessages"`
	BackupDigest     string `json:"backupDigest"`
}

// preRepairSpliceLedgerDoc 是档案袋文件的文档形态。
type preRepairSpliceLedgerDoc struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Session       string                 `json:"session"`
	Splices       []PreRepairSpliceEntry `json:"splices"`
}

// ReadPreRepairSpliceLedger 读主线的拼接档案袋。缺失=空表非错误（健康会话
// 从未长过这份文件）。
func ReadPreRepairSpliceLedger(sessionPath string) ([]PreRepairSpliceEntry, error) {
	path := store.SessionSpliceLedger(sessionPath)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc preRepairSpliceLedgerDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode splice ledger %s: %w", path, err)
	}
	return doc.Splices, nil
}

// HasPreRepairSplice 报告某备份目录是否已登记过（幂等闸；缺档案=false）。
func HasPreRepairSplice(sessionPath, backupDir string) bool {
	entries, err := ReadPreRepairSpliceLedger(sessionPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.BackupDir == backupDir {
			return true
		}
	}
	return false
}

// CheckPreRepairSplice 是拼接判据的纯读数层（两侧快照+内容连续性；零写入
// 零模型）。repairAt 是调用方从 repairlog 命中的改写锚点（unix 秒；0=无）。
//
// covered 的口径是集合级（备份每条消息都已在主线=主线已是长链），不能用
// 「备份独有前缀为空」判——共享头+分叉尾的形态（备份首轮与主线共享、丢失
// 段在后）独有前缀恰为空，但丢失段真实存在（tl1787 实测）。分叉段的边界
// 取消息级公共前缀并回退到最近的轮起点（接缝永不劈轮——孤儿工具结果会被
// provider 拒绝，promote 前缀对齐同一纪律）。
func CheckPreRepairSplice(ownerPath, backupTranscript string, repairAt int64) (PreRepairSplicePlan, error) {
	canonical, ownerOK := LoadSessionContentSnapshot(ownerPath)
	copied, backupOK := LoadSessionContentSnapshot(backupTranscript)
	if !ownerOK || !backupOK {
		return PreRepairSplicePlan{Reason: "content snapshot unavailable (fail-closed)"}, fmt.Errorf("content snapshot unavailable; the splice verdict needs both sides readable")
	}
	ownerMsgs := trimLeadingSystemMessages(canonical.messages)
	backupMsgs := trimLeadingSystemMessages(copied.messages)

	// covered 口径（集合级）：备份每条消息都已在主线。
	known := make(map[string]struct{}, len(ownerMsgs))
	for _, m := range ownerMsgs {
		known[messageFingerprint(m)] = struct{}{}
	}
	unique := 0
	for _, m := range backupMsgs {
		if _, present := known[messageFingerprint(m)]; !present {
			unique++
		}
	}
	if unique == 0 {
		return PreRepairSplicePlan{Reason: "backup fully contained in the main line (covered; nothing was lost)"}, nil
	}

	// 形态②分叉长链：精确公共前缀（回退到轮边界）+ 备份独有段非空。
	k := exactCommonPrefixLength(ownerMsgs, backupMsgs)
	k = lastTurnStartAtOrBefore(backupMsgs, k)
	if k > 0 && k < len(backupMsgs) {
		return PreRepairSplicePlan{
			Kind:            PreRepairSpliceDiverged,
			SharedPrefix:    k,
			SegmentStart:    k,
			SegmentEnd:      len(backupMsgs),
			SegmentMessages: len(backupMsgs) - k,
			SplicePoint:     k,
		}, nil
	}

	// 形态③折叠续接：无公共前缀，但有改写锚点（调用方已验时间线）——整条
	// 旧链接在新链之前。
	if repairAt > 0 {
		return PreRepairSplicePlan{
			Kind:            PreRepairSpliceFoldContinuation,
			SharedPrefix:    0,
			SegmentStart:    0,
			SegmentEnd:      len(backupMsgs),
			SegmentMessages: len(backupMsgs),
			SplicePoint:     0,
		}, nil
	}

	return PreRepairSplicePlan{Reason: "no content continuity and no repair-record anchor — needs a manual HEAD pick"}, nil
}

// lastTurnStartAtOrBefore 把消息级分歧点对齐到轮边界（备份侧）：idx 本身
// 是轮起点时原样返回；否则回退到 idx 之前最近的轮起点；没有则 0（整段为
// 段）。接缝永不劈轮——孤儿工具结果会被 provider 拒绝。
func lastTurnStartAtOrBefore(msgs []provider.Message, idx int) int {
	if idx > len(msgs) {
		idx = len(msgs)
	}
	if idx < len(msgs) && isUserTurnStart(msgs[idx]) {
		return idx
	}
	for i := idx - 1; i >= 0; i-- {
		if isUserTurnStart(msgs[i]) {
			return i
		}
	}
	return 0
}

// exactCommonPrefixLength 数两侧（trim 后）的精确公共前缀长度（指纹逐条
// 相等；第一条分歧处停）。
func exactCommonPrefixLength(a, b []provider.Message) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && messageFingerprint(a[i]) == messageFingerprint(b[i]) {
		i++
	}
	return i
}

// PreRepairSpliceRequest 是登记入参：判据结果 + 备份身份（校验值由本层在
// 登记时本地重算，调用方不带哈希——单一来源）。
type PreRepairSpliceRequest struct {
	BackupDir        string
	BackupTranscript string
	Stamp            string
	Plan             PreRepairSplicePlan
	RepairAt         int64
}

// RegisterPreRepairSplice 把一条拼接登记写进主线档案袋（纯元信息；主线转录
// 与事件日志零触碰）。重复 backupDir 幂等拒绝；写原子（tmp+rename），失败
// 即无登记——主线无损的平凡推论。校验值在登记时本地重算（零额度）：段哈
// 希/拼接点前后边界指纹/备份内容摘要；主线自身的 ContentDigest/Turns/
// Revision 与 lineage 字段原值保留（内容未动，那些键未失效）。
func RegisterPreRepairSplice(ownerPath string, req PreRepairSpliceRequest) (PreRepairSpliceEntry, error) {
	entry := PreRepairSpliceEntry{SchemaVersion: 1, BackupDir: req.BackupDir, BackupTranscript: req.BackupTranscript, Stamp: req.Stamp, RepairAt: req.RepairAt}
	if entry.BackupDir == "" {
		return entry, fmt.Errorf("splice entry needs a backup dir")
	}
	plan := req.Plan
	if !PreRepairSplicePlanSplicable(plan) {
		return entry, fmt.Errorf("refusing to register a non-splicable verdict: %s", req.Plan.Reason)
	}
	if HasPreRepairSplice(ownerPath, entry.BackupDir) {
		return entry, fmt.Errorf("this backup is already registered in the splice ledger (idempotent)")
	}
	canonical, ownerOK := LoadSessionContentSnapshot(ownerPath)
	copied, backupOK := LoadSessionContentSnapshot(req.BackupTranscript)
	if !ownerOK || !backupOK {
		return entry, fmt.Errorf("content snapshot unavailable at registration (fail-closed; nothing written)")
	}
	ownerMsgs := trimLeadingSystemMessages(canonical.messages)
	backupMsgs := trimLeadingSystemMessages(copied.messages)
	if req.Plan.SegmentStart < 0 || req.Plan.SegmentEnd > len(backupMsgs) || req.Plan.SegmentStart >= req.Plan.SegmentEnd {
		return entry, fmt.Errorf("splice segment bounds out of range: [%d,%d) of %d", req.Plan.SegmentStart, req.Plan.SegmentEnd, len(backupMsgs))
	}
	segment := backupMsgs[req.Plan.SegmentStart:req.Plan.SegmentEnd]
	segDigest, err := digestSessionMessages(segment)
	if err != nil {
		return entry, fmt.Errorf("segment digest: %w", err)
	}
	entry.Kind = string(req.Plan.Kind)
	entry.SharedPrefix = req.Plan.SharedPrefix
	entry.SplicePoint = req.Plan.SplicePoint
	entry.SegmentStart = req.Plan.SegmentStart
	entry.SegmentEnd = req.Plan.SegmentEnd
	entry.SegmentHash = digestString(segDigest)
	if req.Plan.SplicePoint > 0 && req.Plan.SplicePoint <= len(ownerMsgs) {
		entry.BoundaryPrevHash = messageFingerprint(ownerMsgs[req.Plan.SplicePoint-1])
	}
	if req.Plan.SplicePoint < len(ownerMsgs) {
		entry.BoundaryNextHash = messageFingerprint(ownerMsgs[req.Plan.SplicePoint])
	}
	entry.OwnerMessages = len(ownerMsgs)
	entry.BackupDigest = copied.digest
	entry.RegisteredAt = time.Now().UTC().Format(time.RFC3339)

	entries, err := ReadPreRepairSpliceLedger(ownerPath)
	if err != nil {
		return entry, err
	}
	entries = append(entries, entry)
	doc := preRepairSpliceLedgerDoc{SchemaVersion: 1, Session: filepath.Base(ownerPath), Splices: entries}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return entry, err
	}
	buf = append(buf, '\n')
	path := store.SessionSpliceLedger(ownerPath)
	if path == "" {
		return entry, fmt.Errorf("empty splice ledger path")
	}
	if err := fileutil.AtomicWriteFile(path, buf, 0o600); err != nil {
		return entry, err
	}
	return entry, nil
}
