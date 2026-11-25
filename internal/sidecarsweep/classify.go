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
	// 会话拥有的拼写一律由 store 常量派生（task 475 X6 pattern J 拼写门禁：
	// 生产码禁手写已钉常量；无常量形态才落字面量）。
	{store.SessionTranscriptSuffix + ".telemetry.json", ".telemetry.json", "telemetry", classData, formFullPath, false},
	{store.SessionTranscriptSuffix + ".toolstats.json", ".toolstats.json", "toolstats", classData, formFullPath, false},
	{store.SessionLeaseLockSuffix, ".lease.lock", "runtime-lease-lock", classRuntime, formFullPath, false},
	{store.SessionLeaseInfoSuffix, ".lease.json", "runtime-lease-info", classRuntime, formFullPath, false},
	{store.SessionLockFileSuffix, ".lock", "runtime-lock", classRuntime, formFullPath, false},
	{store.SessionTranscriptSuffix + ".imgpack", ".imgpack", "imgpack-dir", classData, formFullPath, true},
	{store.SessionMetaFileSuffix, ".meta", "branch-meta", classData, formFullPath, false},
	// —— guardian 专属（先于泛化形态命中）——
	{".guardian.cursor.json", ".guardian.cursor.json", "guardian-cursor", classData, formGuardian, false},
	// —— 拼在 stem 后的形态（含 guardian 级联：<id>.guardian.<kind> 同表命中）——
	{store.SessionEventLogRotatingSuffix, store.SessionEventLogRotatingSuffix, "event-log-rotating", classData, formStem, false},
	{store.SessionEventLogDamagedSuffix, store.SessionEventLogDamagedSuffix, "event-log-damaged", classData, formStem, false},
	{store.SessionEventLogZstSuffix, store.SessionEventLogZstSuffix, "event-log-zst", classData, formStem, false},
	{store.SessionEventLogSuffix, store.SessionEventLogSuffix, "event-log", classData, formStem, false},
	{store.SessionTurnEventLogSuffix + ".damaged", store.SessionTurnEventLogSuffix + ".damaged", "turn-log-damaged", classData, formStem, false},
	{store.SessionTurnEventLogSuffix, store.SessionTurnEventLogSuffix, "turn-log", classData, formStem, false},
	{store.SessionConflictPreserveLogSuffix, store.SessionConflictPreserveLogSuffix, "conflict-preserve-log", classData, formStem, false},
	{store.SessionConflictLogSuffix, store.SessionConflictLogSuffix, "conflict-log", classData, formStem, false},
	{store.SessionRepairLogSuffix, store.SessionRepairLogSuffix, "repair-log", classData, formStem, false},
	{store.SessionRewriteAttributionLogSuffix, store.SessionRewriteAttributionLogSuffix, "rewrite-log", classData, formStem, false},
	{".pinned-context.json", ".pinned-context.json", "pinned-context", classData, formStem, false},
	{".goal-state.json", ".goal-state.json", "goal-state", classData, formStem, false},
	{".recovery.json", ".recovery.json", "recovery-state", classData, formStem, false},
	{".context.json", ".context.json", "context-projection", classData, formStem, false},
	{".splices.json", ".splices.json", "splice-ledger", classData, formStem, false},
	{store.SessionEventIndexSuffix, store.SessionEventIndexSuffix, "event-index", classData, formStem, false},
	{store.SessionDisplayIndexSuffix, store.SessionDisplayIndexSuffix, "display-index", classData, formStem, false},
	{".ckpt", ".ckpt", "checkpoint-dir", classData, formStem, true},
	{".jobs", ".jobs", "jobs-dir", classData, formStem, true},
	{".inbox", ".inbox", "inbox-dir", classData, formStem, true},
	{".reminders", ".reminders", "reminders-dir", classData, formStem, true},
	{".cleanup-pending.json", ".cleanup-pending.json", "cleanup-pending-marker", classMechanism, formStem, false},
	// —— guardian 归档转录（必须先于泛化 zst 主转录判定）——
	{guardianArchiveSuffix, guardianArchiveSuffix, "guardian-archive", classData, formGuardian, false},
}

// guardianArchiveSuffix 是 guardian 转录的 zst 档案形态。store 的
// IsSessionTranscriptZstName 不排除它（该判定只排除 events 档案），目录扫描
// 若直接用它会把无主 guardian 档案误判成活主转录、把整族孤儿藏起来——本包
// 在主转录判定之前先剥掉这个形态。拼写由 store 常量派生（task 475 门禁）。
const guardianArchiveSuffix = store.SessionGuardianTranscriptSuffix + zstArchiveTail

// preRepairBackupMarker 是「含前缀」形态（时间戳挡在业务外，store.SessionTrashSidecars
// 同款 glob 纪律）：<stem>.pre-repair-<UTC紧凑时间>，自愈修复备份目录。
// 拼接常量取 store 单一来源（SessionPreRepairBackupSuffix）。
const preRepairBackupMarker = store.SessionPreRepairBackupSuffix

// guardianTranscriptSegment 是伴生转录名里的 guardian 段（stem 形态级联用：
// <id>.guardian.<kind> 剥 kind 后剩余 <id>.guardian）。
const guardianTranscriptSegment = ".guardian"

// 目录级机制状态（非会话 sidecar，desktop/agent 各机制的目录级状态文件，
// 2026-11-25 真机扫描 2174 条不明复核后定性）：display/ planner-display 标题
// 面及其锁、serve 标题缓存、topic 迁移/索引修复标记、subagent 信箱枢纽、
// 待交接、运行日志 DB。这些与 .titles.json 同位阶——结构工件，不判孤儿。
var dirLevelStateFiles = map[string]bool{
	".display.json":              true, // desktop sessionDisplayFile
	".planner-display.json":      true, // desktop sessionPlannerDisplayFile
	".session-titles.json":       true, // serve 标题缓存（titlecache.go）
	".topics-migrated":           true, // topic_migration_marker 迁移标记
	".topics-migrated-v2":        true,
	".topic-indexes-repaired":    true, // topic 索引修复标记
	".topic-indexes-repaired-v2": true,
	"subagents":                  true, // subagent 信箱/历史枢纽目录（subagentmailbox.Hub）
	"pending-handoff.md":         true, // agent 待交接（pending_handoff.go）
	"runjournal.db":              true, // 运行日志 DB（runjournal.Open）
}

// dirLevelTitlesFiles 是「主文件 + .lock」成对的结构工件：主文件在 → 锁在。
var dirLevelTitlesFiles = map[string]bool{
	".titles.json":          true,
	".display.json":         true,
	".planner-display.json": true,
}

// 换位中转/修复救援的「包装后缀」（内层名 + 标记）：剥掉标记后对内层名递归
// 判定，归属继承内层。判据出处：
//   - .promote-staging：merge-promote 换位中转（save_recovery_lifecycle_1352.go
//     promoteStagingSuffix，mainArts[i]+suffix，tl1390 幂等续跑形态）；
//   - .reasonix-quarantine-<UTC>：repair 隔离（repair/config.go item.path+suffix）；
//   - .quarantine-<date>：同族历史拼写（2026-11-25 真机现场）；
//   - .bak-<stamp>-<tag>：外部修复副本（tl1442 实测 .jsonl.bak- 形态 + 真机
//     <stem>.jsonl.meta.bak- 形态——内层已是完整工件名，剥标后递归定性）。
var wrapperMarkers = []string{
	".promote-staging",
	".reasonix-quarantine-",
	".quarantine-",
	".bak-",
}

// 会话保存的临时文件形态（os.CreateTemp(dir, ".atomic-*.tmp"/".session.*.tmp")，
// session_persistence_context.go / migrate.go）：运行时瞬态，断链残留无归属。
var saveTempPrefixes = []string{".atomic-", ".session."}

// metaLockKind 是会话 meta 跨进程保存锁（save_locks.go sessionMetaLockPath：
// "."+sha256(canonical(会话路径))[:8字节hex]+".meta.lock"）。键是哈希不是 stem
// ——归属要靠 ScanDir 对活主转录算同名哈希来配对（needsHashPair）。
const metaLockKind = "meta-lock"

// wrapperDepthCap 限制包装剥标递归深度（防御病态嵌套名）。
const wrapperDepthCap = 3

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
	needsHashPair   bool     // meta-lock：归属需 ScanDir 按活主哈希配对
	hashKey         string   // meta-lock：名字里的 16 位 hex 键
}

// classifyName 对单个目录条目名做形态判定。纯函数：只看名字，不看盘。
//
// 判定顺序即安全序：结构工件 → 保存临时文件 → meta 保存锁 → 主转录 →
// guardian 档案/伴生转录 → 含前缀标记（包装剥标递归）→ pre-repair → 形态表
// （长后缀优先）→ 不明。任何一步归属解析出空 stem（退化名，如整名叫
// ".jsonl.meta"）都降级为不明——宁进复核清单，不冒充孤儿。
func classifyName(name string) classification {
	return classifyNameDepth(name, 0)
}

func classifyNameDepth(name string, depth int) classification {
	// 结构工件：回收站桶根、标题映射、本包自己的回收区（含历史日期）、
	// 目录级机制状态及其锁。
	if name == ".trash" || name == ".titles.json" || strings.HasPrefix(name, "_recycle-") {
		return classification{class: classStructural, kind: "structural"}
	}
	if dirLevelStateFiles[name] {
		return classification{class: classStructural, kind: "dir-level-state"}
	}
	if dirLevelTitlesFiles[name] {
		return classification{class: classStructural, kind: "dir-level-state"}
	}
	if dirLevelTitlesFiles[strings.TrimSuffix(name, ".lock")] && strings.HasSuffix(name, ".lock") {
		return classification{class: classStructural, kind: "dir-level-state-lock"}
	}
	// 会话保存临时文件（os.CreateTemp 形态）：运行时瞬态，无归属键。
	for _, p := range saveTempPrefixes {
		if strings.HasPrefix(name, p) && strings.HasSuffix(name, ".tmp") {
			return classification{class: classRuntime, kind: "save-temp", requiresFlag: "include-runtime"}
		}
	}
	// meta 跨进程保存锁（哈希键，归属由 ScanDir 配对）。
	if hash, ok := metaLockHashKey(name); ok {
		return classification{class: classRuntime, kind: metaLockKind,
			requiresFlag: "include-runtime", needsHashPair: true, hashKey: hash}
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
	// 包装标记形态（换位中转/修复救援）：剥标后对内层名递归判定，归属与
	// 处置面继承内层（内层是主转录的——如换位中转的 <id>.jsonl.promote-staging
	// ——是数据而非活主，处置面改判 data，绝不给 masterAlive 供数）。
	if depth < wrapperDepthCap {
		for _, marker := range wrapperMarkers {
			idx := strings.Index(name, marker)
			if idx <= 0 {
				continue // idx==0：整名以标记开头，内层为空，落不明
			}
			inner := classifyNameDepth(name[:idx], depth+1)
			switch inner.class {
			case classMaster:
				return classification{class: classData,
					kind:            "wrapped-" + inner.kind,
					owner:           name[:idx],
					masterStem:      inner.masterStem,
					moveDefault:     true,
					externalMarkers: append(inner.externalMarkers, marker)}
			case classData, classRuntime:
				out := inner
				out.externalMarkers = append(inner.externalMarkers, marker)
				out.kind = "wrapped-" + inner.kind
				return out
			default:
				// 内层结构/不明：包装名不给归属（宁复核不冒充）。
				return classification{class: classUnknown, kind: "wrapped-unresolvable",
					externalMarkers: []string{marker}}
			}
		}
	}
	// pre-repair 备份目录：时间戳在名字中段，后缀表够不着。
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

// metaLockHashKey 从目录条目名剥出 meta 保存锁的 16 位 hex 键
// （".<16hex>.meta.lock"）；非该形态返回 false。
func metaLockHashKey(name string) (string, bool) {
	if !strings.HasSuffix(name, ".meta.lock") {
		return "", false
	}
	body := strings.TrimSuffix(name, ".meta.lock") // 形如 ".<16hex>"
	if len(body) != 17 || body[0] != '.' {
		return "", false
	}
	for _, r := range body[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", false
		}
	}
	return body[1:], true
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
