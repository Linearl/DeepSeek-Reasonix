// Package sidecarsweep implements the tl1806 orphan-sidecar scanner and the
// confirm-gated recycle channel (清单 → 用户确认 → 回收语义).
//
// 孤儿 sidecar = 会话目录里「主转录已不在活目录」的 sidecar 残留（tl1793
// §遗留1：历史归档链（tl1442 清单收敛前）把转录搬进回收站桶时漏搬的工件）。
// 本包只做两件事：
//
//  1. 只读扫描（ScanDir/ScanRoots）：产清单，逐条注明孤儿判据；缺省零写盘。
//  2. 回收通道（Recycle）：仅在显式确认（CLI --confirm）下把清单里的孤儿移入
//     <会话目录>/_recycle-YYYYMMDD/，保留原名并落 manifest.tsv——真删是之后
//     另一次用户确认的事，本包绝不执行任何 RemoveAll/Remove。
//
// 分类纪律（单一来源复用，不复制第二份清单）：主转录/守卫伴生转录判定用
// store.IsSessionTranscriptName / IsSessionTranscriptZstName /
// IsGuardianTranscriptName；sidecar 形态表（classify.go 的 suffixTable）与
// store.SessionSidecarFiles / SessionStaticTrashSidecars 的构造函数一一对应，
// 由 classify_test.go 的构造函数全覆盖钉（新增构造函数而不进形态表 = 测试红）
// 把守。运行时句柄（save/lease 锁）与机制状态（cleanup-pending 标记）不属
// 数据面：前者默认不搬（--include-runtime 显式放开），后者整族让位给桌面端
// 补收机制（reconcileDesktopCleanupPending）——有标记的 stem 一律 defer。
package sidecarsweep

import (
	"strings"

	"reasonix/internal/store"
)

// kindClass 把形态分成五个处置面：
//   - classMaster：主转录（plain/zst 档案），活的会话本体，永不入清单。
//   - classStructural：目录结构工件（.trash/.titles.json/_recycle-*），不判孤儿。
//   - classMechanism：机制持有（cleanup-pending 标记）；其所在 stem 的全部
//     残留 defer 给桌面端补收机制，永不搬。
//   - classRuntime：运行时句柄（锁/lease）；无主即陈旧垃圾，但默认不搬
//     （--include-runtime 显式放开）——句柄有独立生命周期，保守优先。
//   - classData：会话拥有的数据工件；无主即孤儿，--confirm 的默认搬运面。
//   - classUnknown：形态表外的不明条目；只进「待人工复核」清单，永远不搬
//     ——判据不明的条目搬不得（防误伤）。
type kindClass int

const (
	classMaster kindClass = iota
	classStructural
	classMechanism
	classRuntime
	classData
	classUnknown
)

// ownerForm 描述 sidecar → owner 转录的拼写派生方式。
//   - formFullPath：sidecar 拼在完整转录路径后（<id>.jsonl.<kind>），剥后缀即转录名。
//   - formStem：sidecar 拼在 stem 后（<id>.<kind>），剥后缀 + ".jsonl" 即转录名。
//   - formGuardian：guardian 专属 sidecar（<id>.guardian.cursor.json），剥后缀
//   - ".guardian.jsonl" 即伴生转录名；master stem = 剥后缀。
type ownerForm int

const (
	formFullPath ownerForm = iota
	formStem
	formGuardian
)

// sidecarShape 是形态表的一行：已知 sidecar 后缀 → 种类 + 处置面 + owner 派生。
// suffix 是「匹配后缀」（含转录身份段，用于判定与消歧）；ownerStrip 是「owner
// 剥离后缀」——formFullPath 形态拼在完整转录路径后（<id>.jsonl.<kind>），名字
// 里转录身份段的 .jsonl 属于 owner 转录名，剥 ownerStrip（不含 .jsonl 段）才
// 能还原 owner 全名（<id>.guardian.jsonl.meta 剥 .meta → <id>.guardian.jsonl）。
type sidecarShape struct {
	suffix     string
	ownerStrip string
	kind       string
	class      kindClass
	form       ownerForm
	isDirArt   bool // 目录工件（.ckpt/.jobs/.inbox/.reminders/.imgpack/.pre-repair-*）
}

// suffixTable 是本包对 store sidecar 全集的逆查表（长后缀优先，首个命中即止）。
// 与 store.SessionSidecarFiles / SessionStaticTrashSidecars 逐条对应；
// classify_test.go 的 TestShapeTableCoversStoreConstructors 用构造函数实测
// 钉住全覆盖——store 新增 sidecar 构造函数而本表不接，测试即红（上限+新增
// 即红纪律）。
var suffixTable = []sidecarShape{
	// —— 拼在完整转录路径后的形态（最长优先）——
	{".jsonl.telemetry.json", ".telemetry.json", "telemetry", classData, formFullPath, false},
	{".jsonl.toolstats.json", ".toolstats.json", "toolstats", classData, formFullPath, false},
	{".jsonl.lease.lock", ".lease.lock", "runtime-lease-lock", classRuntime, formFullPath, false},
	{".jsonl.lease.json", ".lease.json", "runtime-lease-info", classRuntime, formFullPath, false},
	{".jsonl.lock", ".lock", "runtime-lock", classRuntime, formFullPath, false},
	{".jsonl.imgpack", ".imgpack", "imgpack-dir", classData, formFullPath, true},
	{".jsonl.meta", ".meta", "branch-meta", classData, formFullPath, false},
	// —— guardian 专属（先于泛化形态命中）——
	{".guardian.cursor.json", ".guardian.cursor.json", "guardian-cursor", classData, formGuardian, false},
	// —— 拼在 stem 后的形态（含 guardian 级联：<id>.guardian.<kind> 同表命中）——
	{".events.jsonl.rotating", ".events.jsonl.rotating", "event-log-rotating", classData, formStem, false},
	{".events.jsonl.damaged", ".events.jsonl.damaged", "event-log-damaged", classData, formStem, false},
	{".events.jsonl.zst", ".events.jsonl.zst", "event-log-zst", classData, formStem, false},
	{".events.jsonl", ".events.jsonl", "event-log", classData, formStem, false},
	{".turns.jsonl.damaged", ".turns.jsonl.damaged", "turn-log-damaged", classData, formStem, false},
	{".turns.jsonl", ".turns.jsonl", "turn-log", classData, formStem, false},
	{".conflict-preserved.jsonl", ".conflict-preserved.jsonl", "conflict-preserve-log", classData, formStem, false},
	{".conflicts.jsonl", ".conflicts.jsonl", "conflict-log", classData, formStem, false},
	{".repairlog.jsonl", ".repairlog.jsonl", "repair-log", classData, formStem, false},
	{".rewritelog.jsonl", ".rewritelog.jsonl", "rewrite-log", classData, formStem, false},
	{".pinned-context.json", ".pinned-context.json", "pinned-context", classData, formStem, false},
	{".goal-state.json", ".goal-state.json", "goal-state", classData, formStem, false},
	{".recovery.json", ".recovery.json", "recovery-state", classData, formStem, false},
	{".context.json", ".context.json", "context-projection", classData, formStem, false},
	{".splices.json", ".splices.json", "splice-ledger", classData, formStem, false},
	{".event-index.json", ".event-index.json", "event-index", classData, formStem, false},
	{".display-index.json", ".display-index.json", "display-index", classData, formStem, false},
	{".ckpt", ".ckpt", "checkpoint-dir", classData, formStem, true},
	{".jobs", ".jobs", "jobs-dir", classData, formStem, true},
	{".inbox", ".inbox", "inbox-dir", classData, formStem, true},
	{".reminders", ".reminders", "reminders-dir", classData, formStem, true},
	{".cleanup-pending.json", ".cleanup-pending.json", "cleanup-pending-marker", classMechanism, formStem, false},
	// —— guardian 归档转录（必须先于泛化 .jsonl.zst 主转录判定）——
	{".guardian.jsonl.zst", ".guardian.jsonl.zst", "guardian-archive", classData, formGuardian, false},
}

// guardianArchiveSuffix 是 guardian 转录的 zst 档案形态。store 的
// IsSessionTranscriptZstName 不排除它（该判定只排除 events 档案），目录扫描
// 若直接用它会把无主 guardian 档案误判成活主转录、把整族孤儿藏起来——本包
// 在主转录判定之前先剥掉这个形态。
const guardianArchiveSuffix = ".guardian.jsonl.zst"

// preRepairBackupMarker / externalRepairCopyMarker 是两个「含前缀」形态（时间
// 戳挡在业务外，store.SessionTrashSidecars 同款 glob 纪律）：
//   - <stem>.pre-repair-<UTC紧凑时间>：自愈修复备份目录；
//   - <stem>.jsonl.bak-<stamp>-<tag>：外部修复副本（2026-11-05 实测形态）。
const (
	preRepairBackupMarker     = ".pre-repair-"
	externalRepairCopyMarker  = ".jsonl.bak-"
	guardianTranscriptSegment = ".guardian"
)

// classification 是一个目录条目的判定结果。
type classification struct {
	class           kindClass
	kind            string   // 人类可读种类名
	owner           string   // 期望的 owner 转录名（含 .jsonl）；主转录/结构/不明 = ""
	masterStem      string   // 所属会话的 stem（剥 .jsonl）；无归属 = ""
	isDirArt        bool     // 目录工件
	moveDefault     bool     // --confirm 缺省搬运面（仅 classData 且归属明确）
	requiresFlag    string   // 需要的显式放开旗（classRuntime = --include-runtime）
	deferredReason  string   // 非空 = defer 给既有机制的原因
	externalMarkers []string // 命中的含前缀标记（诊断用）
}

// classifyName 对单个目录条目名做形态判定。纯函数：只看名字，不看盘。
//
// 判定顺序即安全序：结构工件 → 主转录 → guardian 档案/伴生转录 → 含前缀
// 标记 → 形态表（长后缀优先）→ 不明。任何一步归属解析出空 stem（退化名，
// 如整名叫 ".jsonl.meta"）都降级为不明——宁进复核清单，不冒充孤儿。
func classifyName(name string) classification {
	// 结构工件：回收站桶根、标题映射、本包自己的回收区（含历史日期）。
	if name == ".trash" || name == ".titles.json" || strings.HasPrefix(name, "_recycle-") {
		return classification{class: classStructural, kind: "structural"}
	}
	// 主转录（plain）。IsSessionTranscriptName 已排除 events/turns/conflicts/
	// repairlog/rewritelog/conflict-preserved/guardian 形态。
	if store.IsSessionTranscriptName(name) {
		return classification{class: classMaster, kind: "transcript",
			masterStem: strings.TrimSuffix(name, store.SessionTranscriptSuffix)}
	}
	// 主转录 zst 档案；guardian 的 zst 档案不是主转录，先剥。
	if strings.HasSuffix(name, guardianArchiveSuffix) {
		stem := strings.TrimSuffix(name, guardianArchiveSuffix)
		return classification{class: classData, kind: "guardian-archive",
			owner: stem + store.SessionGuardianTranscriptSuffix, masterStem: stem,
			moveDefault: true}
	}
	if store.IsSessionTranscriptZstName(name) {
		return classification{class: classMaster, kind: "transcript-archive",
			masterStem: strings.TrimSuffix(name, zstArchiveTail)}
	}
	// guardian 伴生转录本体（<id>.guardian.jsonl）：数据面，master = 剥后缀。
	if store.IsGuardianTranscriptName(name) {
		stem := strings.TrimSuffix(name, store.SessionGuardianTranscriptSuffix)
		if stem == "" {
			return classification{class: classUnknown, kind: "degenerate-name"}
		}
		return classification{class: classData, kind: "guardian-transcript",
			owner: name, masterStem: stem, moveDefault: true}
	}
	// 含前缀标记形态：时间戳在名字中段，后缀表够不着。
	if idx := strings.Index(name, externalRepairCopyMarker); idx > 0 {
		stem := name[:idx]
		return classification{class: classData, kind: "external-repair-copy",
			owner: stem + store.SessionTranscriptSuffix, masterStem: stem,
			moveDefault: true, externalMarkers: []string{externalRepairCopyMarker}}
	}
	if idx := strings.Index(name, preRepairBackupMarker); idx > 0 {
		stem := name[:idx]
		return classification{class: classData, kind: "pre-repair-backup",
			owner: stem + store.SessionTranscriptSuffix, masterStem: stem,
			moveDefault: true, isDirArt: true, externalMarkers: []string{preRepairBackupMarker}}
	}
	// 形态表：首个（最长优先）后缀命中即止。
	for _, shape := range suffixTable {
		if !strings.HasSuffix(name, shape.suffix) {
			continue
		}
		// formFullPath：owner = 剥 ownerStrip（转录身份段 .jsonl 留给 owner 名）；
		// 其余形态：base = 剥匹配后缀（stem 或 guardian 段）。
		base := name
		if shape.form == formFullPath {
			base = strings.TrimSuffix(name, shape.ownerStrip)
		} else {
			base = strings.TrimSuffix(name, shape.suffix)
		}
		c := classification{class: shape.class, kind: shape.kind, isDirArt: shape.isDirArt}
		switch shape.form {
		case formFullPath:
			// 剥后缀即转录名（<id>.jsonl 或 <id>.guardian.jsonl）。
			c.owner = base
			c.masterStem = guardianMasterStem(base)
		case formGuardian:
			if base == "" {
				return classification{class: classUnknown, kind: "degenerate-name"}
			}
			c.owner = base + store.SessionGuardianTranscriptSuffix
			c.masterStem = base
		case formStem:
			if base == "" {
				return classification{class: classUnknown, kind: "degenerate-name"}
			}
			if strings.HasSuffix(base, guardianTranscriptSegment) {
				// guardian 级联（<id>.guardian.<kind>）：owner 是伴生转录。
				gstem := strings.TrimSuffix(base, guardianTranscriptSegment)
				if gstem == "" {
					return classification{class: classUnknown, kind: "degenerate-name"}
				}
				c.owner = base + store.SessionTranscriptSuffix
				c.masterStem = gstem
			} else {
				c.owner = base + store.SessionTranscriptSuffix
				c.masterStem = base
			}
		}
		if c.masterStem == "" {
			// 退化名（整名就是后缀）：归属解析不出 stem，降级为不明。
			return classification{class: classUnknown, kind: "degenerate-name"}
		}
		if shape.class == classRuntime {
			c.requiresFlag = "include-runtime"
		} else if shape.class == classData {
			c.moveDefault = true
		}
		return c
	}
	return classification{class: classUnknown, kind: "unrecognized"}
}

// guardianMasterStem 从转录名（<id>.jsonl 或 <id>.guardian.jsonl）解析 master
// stem；guardian 伴生转录的 master 是去掉 .guardian 段的主会话。
func guardianMasterStem(transcriptName string) string {
	if strings.HasSuffix(transcriptName, store.SessionGuardianTranscriptSuffix) {
		return strings.TrimSuffix(transcriptName, store.SessionGuardianTranscriptSuffix)
	}
	return strings.TrimSuffix(transcriptName, store.SessionTranscriptSuffix)
}

// zstArchiveTail 与 store 内部同款（小写常量本地化，避免引未导出符号）。
const zstArchiveTail = ".zst"
