package main

// tl1603: 改写前备份（*.pre-repair-*）的手动清理桥。
//
// tl1572 交付的只有「查看」（面板分区只读枚举）；用户实测存量 168 条 3.1GB
// 只能看不能清。本文件在只读面之上补清理能力，设计约束源自 tl1116「无备份
// 不减肥」判据，其中「唯一退路⇒禁删」一条已被用户裁决推翻（tl1646，
// 2026-11-16：『不应该限制我删除』）——删除决定权在用户。现行语义
// （§ DeletePreRepairBackup）：
//
//  1. 宿主健康（重放级，只在删除时执行）：活宿主经 InspectSessionHealth
//     体检不过（超窗/加载超时）时拒绝——修复失败会话的改写前快照是它唯一
//     的回头路，删退路正是 tl1116 禁止的「无备份减肥」。「已合并进主线」形
//     态由本闸覆盖（合并的前提本就是宿主是权威线）；「超期」形态不做自动
//     判定（non-goal：先给手动入口，过期策略后议）。
//  2. 孤儿副本放行（tl1646 修订）：宿主已不存在（含宿主已走删除/归档链的
//     形态）的备份一律允许删除。tl1603 原设计在此拒绝「仅存副本」，用户
//     裁决改为：前端确认弹窗明示「唯一副本、删除后不可恢复」（三语
//     settings.recoveryCopiesPreRepairSoleWarn），确认即删、走系统回收站
//     既有通道；后端不再设 sole-copy 闸。
//  3. 超长路径死锁解除（tl1658，2026-11-17）：备份树内层路径超 MAX_PATH(260)
//     时 Shell 收不进桶（预检诚实拒绝），此前单删/批量两条路全堵=保守拒绝
//     变死锁。现在两段出路：①短路径中转（移到 %TEMP% 暂存区再整体入桶，
//     移动不受 MAX_PATH 限制、回收站语义保住；还原位置差异在前端文案披露）；
//     ②中转也不可行时返回 permanentOnly 裁决，前端出「永久删除不可恢复」
//     明示弹窗，确认后才走 DeletePreRepairBackupPermanent（健康闸照常，
//     os.RemoveAll 显式通道，任务 855「彻底删除是独立显式操作」条款）。
//     批量面另有逐项进度事件（pre-repair-cleanup:progress）——3.4GB/181
//     项的批量删除不再静默。
//
// 回收语义（任务 855 铁律）：删除必须经平台 API 送进系统回收站（trashWorkspacePaths
// → SHFileOperation FOF_ALLOWUNDO / gio trash），禁止 os.RemoveAll 裸删、
// 禁止自建 .trash 冒充；恢复走 OS 回收站原位还原。
//
// 并发面：备份树永不作为会话加载（一切枚举排除），自己没有 runtime/lease；
// 但删除/归档链会把备份当宿主侧车整批搬进回收站桶（store.SessionTrashSidecars
// 的 glob），所以整段删除持 a.sessionRemovalMu 与那些链互斥——闸与 rename
// 之间不会穿插搬桶（搬桶链都持同一把锁）；延迟搬桶 goroutine（teardown 超时
// 形态）不持锁，与它撞上的表现是 rename ENOENT=诚实失败行，零数据损失。

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// preRepairHealthInspect 是健康闸的读数入口。Var 仅为测试缝（workspaceTrashPlatform
// 同一先例）：健康闸钉「删除前必须体检活宿主」这条路径，拒绝分支用假报告喂。
var preRepairHealthInspect = agent.InspectSessionHealth

// 删除闸拒绝令牌（wire 契约：前端按令牌出三语文案；Detail 带原始细节）。
// tl1603 的 sole-copy 令牌随 tl1646 用户裁决退役（孤儿副本放行，不再有该拒绝）。
const (
	preRepairReasonInvalid        = "invalid"         // 路径形态/越界（永远不该发生，防御面）
	preRepairReasonGone           = "gone"            // 枚举后已被别的链搬走
	preRepairReasonOwnerUnhealthy = "owner-unhealthy" // 健康闸：宿主体检不过
	preRepairReasonTrashFailed    = "trash-failed"    // 平台回收站调用失败
	// preRepairReasonPermanentOnly（tl1658）：超长路径+短路径中转也不可行——
	// 回收语义走不通，唯一出路是用户在明示弹窗确认后的永久删除。批量回执里
	// 携带该令牌让前端逐条归因；单删走 PreRepairDeleteView.PermanentOnly 裁决。
	preRepairReasonPermanentOnly = "permanent-only"
)

// preRepairOwnerArchived 报告 owner 转录是否躺在 .trash 回收站桶里（tl1787
// 新 14 案例：归档链把宿主转录与它的 pre-repair 备份**一起**搬进
// <dir>/.trash/<桶>/（store.SessionTrashSidecars 的 glob），于是备份在桶内
// 枚举得到、旁侧 owner「存在」——但那是归档工件而非活宿主；按活宿主体检
// 会对已归档会话报 owner-unhealthy（projection_overflow 现场）拒删。桶 meta
// 文件是桶身份标记（reserveUniqueSessionTrashItemDir 落盘），有它 = owner
// 在桶里 = 归档态。
func preRepairOwnerArchived(owner string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(owner), sessionTrashMetaFile))
	return err == nil
}

// preRepairBackupBytes 是备份目录的体量读数（一层平铺 stat 和，与枚举同一口径）。
func preRepairBackupBytes(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// preRepairBackupLabel 是删除回执里的展示名（与枚举同一来源：备份内 meta
// 标题优先，回退 stem）。
func preRepairBackupLabel(backupDir, stem string) string {
	label := recoveryDisplayName(filepath.Join(backupDir, stem+store.SessionTranscriptSuffix))
	if label == "" {
		label = stem
	}
	return label
}

// preRepairCleanupGate 是删除闸：校验 backupDir 可删，通过时返回 stem
// 与体量。所有拒绝都各留一行 slog（tl1414 纪律：拒绝必须可归因）。
//
// tl1646（2026-11-16 用户裁决）：tl1603 的「唯一退路」闸退役——宿主已不在
// 的孤儿副本一律放行（前端确认弹窗承担「唯一副本、不可恢复」警示），此处
// 只保留健康闸；宿主在不在只决定要不要体检。
//
// healthCache 是批量面的宿主健康读数缓存（owner→healthy；nil=单删不缓存）——
// 同一会话的多份备份在批内只重放一次宿主日志。
//
// 路径闸与 validateSessionPath 同一纪律：会话目录包含关系（Rel 不越界）+
// symlink 解析后的二次包含校验；目录名必须能拆出 <stem>.pre-repair-<stamp>
// 形态。任意文件系统路径在这里全部拒绝。
func (a *App) preRepairCleanupGate(backupDir string, healthCache map[string]bool) (stem string, bytes int64, err error) {
	backupDir = filepath.Clean(strings.TrimSpace(backupDir))
	if backupDir == "." || filepath.VolumeName(backupDir) == backupDir {
		slog.Warn("desktop: pre-repair delete refused", "reason", "empty path")
		return "", 0, fmt.Errorf("no backup path given")
	}
	base := filepath.Base(backupDir)
	stem, _, ok := preRepairStemOf(base)
	if !ok {
		slog.Warn("desktop: pre-repair delete refused", "reason", "not a pre-repair backup name", "path", backupDir)
		return "", 0, fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	parent := filepath.Dir(backupDir)
	info, statErr := os.Lstat(backupDir)
	if statErr != nil {
		slog.Warn("desktop: pre-repair delete refused", "reason", "backup vanished", "path", backupDir, "err", statErr)
		return "", 0, fmt.Errorf("backup no longer exists: %s", base)
	}
	if !info.IsDir() {
		slog.Warn("desktop: pre-repair delete refused", "reason", "backup is not a directory", "path", backupDir)
		return "", 0, fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	// 已知会话目录包含闸（宿主文件在哪，备份就在哪；列表只见于这些根）。
	if !a.pathInsideKnownSessionDirs(parent, backupDir) {
		slog.Warn("desktop: pre-repair delete refused", "reason", "outside known session dirs", "path", backupDir)
		return "", 0, fmt.Errorf("backup path outside known session directories")
	}
	// ── 健康闸：活宿主才体检（宿主不在=孤儿副本，tl1646 放行；宿主的内容
	// 已随删除链搬走的形态同样无从体检也不必体检）。tl1787 新 14 案例的同族
	// 扩展：宿主转录躺在 .trash 桶里 = 已归档态而非活宿主——归档链把宿主与
	// 备份一起搬进桶（store.SessionTrashSidecars glob），桶内 owner「存在」
	// 但它是随对话归档的工件；按活宿主体检会对已归档会话报 owner-unhealthy
	// 拒删（fork开发-新14 现场：projection_overflow）。已归档放行，活跃宿主
	// 保护不变。──
	owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
	ownerLive := false
	if oinfo, oerr := os.Lstat(owner); oerr == nil && !oinfo.IsDir() {
		ownerLive = !preRepairOwnerArchived(owner)
	}
	if ownerLive {
		var report agent.SessionHealthReport
		if cached, hit := healthCache[owner]; hit {
			// 批内缓存只存 verdict；缓存命中即视为健康通过（未通过的宿主在第
			// 一份备份上就已经拒绝，带 codes 的日志已经落过，这里不重放）。
			if !cached {
				return "", 0, fmt.Errorf("the owner session is not healthy (cached verdict) — this backup is its way back, repair the session first: %s", base)
			}
		} else {
			report = preRepairHealthInspect(owner, agent.HealthInspectOptions{})
			if healthCache != nil {
				healthCache[owner] = report.Healthy
			}
			if !report.Healthy {
				codes := make([]string, 0, len(report.Issues))
				for _, issue := range report.Issues {
					codes = append(codes, issue.Code)
				}
				slog.Warn("desktop: pre-repair delete refused", "reason", "owner session unhealthy",
					"path", backupDir, "owner", owner, "codes", strings.Join(codes, ","))
				return "", 0, fmt.Errorf("the owner session is not healthy (%s) — this backup is its way back, repair the session first: %s",
					strings.Join(codes, ","), base)
			}
		}
	}
	return stem, preRepairBackupBytes(backupDir), nil
}

// pathInsideKnownSessionDirs 判 target 是否落在某个已知会话目录内（含 symlink
// 解析后的二次校验，validateSessionPath 同一纪律）。
func (a *App) pathInsideKnownSessionDirs(parent, target string) bool {
	if !pathContainedBy(target, parent) {
		return false
	}
	for _, dir := range a.knownSessionDirs() {
		if pathContainedBy(parent, dir) {
			return true
		}
	}
	return false
}

// pathContainedBy 报告 path 是否位于 root 之内（含相等——备份目录的直接宿主
// 就是会话目录本身，parent==dir 是合法形态；validateSessionPath 的 rel=="."
// 排除针对的是「会话文件==目录」这个不可能形态，目录对目录的包含语义里相等
// 成立）。两侧都做 symlink 解析，解析失败的侧退回原拼写。
func pathContainedBy(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = path
	}
	rel, err = filepath.Rel(realRoot, realPath)
	return err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)
}

// preRepairStagingMkdir 是短路径暂存区的创建缝（tl1658）：var 仅为测试缝
// （workspaceTrashPlatform 同一先例）——「中转不可行」分支靠替换它钉死。
var preRepairStagingMkdir = os.MkdirTemp

// preRepairStagingMove 是把一个文件挪进暂存树的执行缝（tl1658；var 仅为测试
// 可注入，生产=os.Rename 的同卷元数据改名，零字节拷贝）。
var preRepairStagingMove = os.Rename

// movedStagedFile 记录一次「原长路径 → 暂存短路径」的文件搬移，入桶失败时
// 按映射逆向搬回（骨架目录尚未清，父目录必然还在）。
type movedStagedFile struct{ from, to string }

// preRepairShortenComponent 把单个路径组件截到 ≤40 字符：前 30 rune（不劈
// UTF-8）+ "~" + 8 位 FNV 指纹（同输入同输出，防截断前缀碰撞）。短组件原样
// 保留——只有超长组件才需要动，短路径树少改名少惊喜。
func preRepairShortenComponent(component string) string {
	if len(component) <= 40 {
		return component
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(component))
	runes := []rune(component)
	if len(runes) > 30 {
		runes = runes[:30]
	}
	return string(runes) + "~" + fmt.Sprintf("%08x", hash.Sum32())
}

// preRepairMoveTreeToStaging 把 srcRoot 树内全部文件逐个 rename 进 dstRoot
// （相对结构保留、超长组件截短）。返回搬移映射；中途失败时返回已完成的映
// 射供调用方逆向回滚。目录不搬——文件全部挪走后源树只剩空壳，由调用方在
// 入桶成功后 RemoveAll 清掉（骨架清理放在入桶之后：入桶失败时父目录还在，
// 逆向 rename 不需要重建任何目录）。
func preRepairMoveTreeToStaging(srcRoot, dstRoot string) ([]movedStagedFile, error) {
	moves := []movedStagedFile{}
	walkErr := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, path)
		if relErr != nil {
			return relErr
		}
		relParts := strings.Split(rel, string(filepath.Separator))
		for i, part := range relParts {
			relParts[i] = preRepairShortenComponent(part)
		}
		dst := filepath.Join(dstRoot, filepath.Join(relParts...))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := preRepairStagingMove(path, dst); err != nil {
			return err
		}
		moves = append(moves, movedStagedFile{from: path, to: dst})
		return nil
	})
	return moves, walkErr
}

// preRepairTrashViaShortStaging 是 tl1658① 的中转执行：把备份树搬进 %TEMP%
// 短路径暂存区（逐文件 rename，超长组件截短——**顶层改名不够**：guard 量的
// 是树内最长完整路径，实测夹具抓出「短根+189 字符内层名=314 仍被拒」），落
// 稳后整体送 OS 回收站。回收站还原语义的差异（经中转的备份从回收站还原时
// 会回到暂存目录、且超长名已截短）已在前端确认文案里披露
// （settings.recoveryCopiesPreRepairRecycleNote）。数据安全序：文件先搬 →
// 入桶 → 成功后才清源树空壳；入桶失败按映射逆向搬回原位（父目录都在），
// 数据滞留 TEMP 的窗口被压缩到「入桶调用本身」，且失败路径自愈。
func preRepairTrashViaShortStaging(backupDir string) error {
	// 暂存名保住「识别度」：stem 过长时截断到 30 字符，后缀
	// .pre-repair-<stamp> 原样保留——回收站里的条目名仍能对上备份。
	stem, stamp, ok := preRepairStemOf(filepath.Base(backupDir))
	if !ok {
		return fmt.Errorf("not a pre-repair backup directory: %s", filepath.Base(backupDir))
	}
	if runes := []rune(stem); len(runes) > 30 {
		stem = string(runes[:30])
	}
	stagedName := stem + store.SessionPreRepairBackupSuffix + stamp
	root, err := preRepairStagingMkdir("", "reasonix-prepair-")
	if err != nil {
		return fmt.Errorf("long-path staging unavailable: %w", err)
	}
	staged := filepath.Join(root, stagedName)
	if err := os.Mkdir(staged, 0o755); err != nil {
		_ = os.Remove(root)
		return fmt.Errorf("long-path staging unavailable: %w", err)
	}
	moves, moveErr := preRepairMoveTreeToStaging(backupDir, staged)
	if moveErr != nil {
		rollbackMoves(moves)
		_ = os.RemoveAll(root)
		return fmt.Errorf("long-path staging move failed: %w", moveErr)
	}
	// 入桶：暂存树全程远短于 MAX_PATH，trashRecycleGuard 预检通过，
	// SHFileOperation 搬进回收站=数据离开暂存区。
	if trashErr := trashWorkspacePaths([]string{staged}); trashErr != nil {
		// 入桶失败必须搬回原位：数据滞留在 TEMP 会让面板「看不见」这份备份
		// （枚举只扫会话目录），用户会以为删没了——比失败更糟。源树骨架
		// 未清，逆向 rename 即可完整复原。
		rollbackMoves(moves)
		_ = os.RemoveAll(root)
		return fmt.Errorf("staged delete failed, the backup was moved back: %w", trashErr)
	}
	// 成功：清源树空壳（文件已全部随桶走）与暂存根；清不掉只留空目录，
	// 不遮掩成功。
	_ = os.RemoveAll(backupDir)
	_ = os.Remove(root)
	return nil
}

// rollbackMoves 按搬移映射逆向 rename（入桶失败的自愈路径；映射序=搬出序，
// 逆序撤回）。搬回失败的文件如实落日志——数据仍在暂存区，绝不静默吞。
func rollbackMoves(moves []movedStagedFile) {
	for i := len(moves) - 1; i >= 0; i-- {
		if err := os.Rename(moves[i].to, moves[i].from); err != nil {
			slog.Error("desktop: pre-repair staging rollback failed — the file stays at the staging path", "from", moves[i].to, "to", moves[i].from, "err", err)
		}
	}
}

// trashPreRepairBackupDir 是删除执行的单点：E5 闸通过后把整个备份目录送进
// OS 回收站；预检判「树内超长路径收不进桶」时（tl1658 现场②，285>260 实测
// 现场）自动走短路径中转二段入桶——回收站语义保住，死锁解除。返回是否经
// 中转（回执/裁决文案需要区分：中转项的还原位置不同）。锁
// （a.sessionRemovalMu）由调用方持有。
func (a *App) trashPreRepairBackupDir(backupDir string) (staged bool, err error) {
	trashErr := trashWorkspacePaths([]string{backupDir})
	if trashErr == nil {
		slog.Info("desktop: pre-repair backup deleted to recycle bin", "path", backupDir)
		return false, nil
	}
	if errors.Is(trashErr, errTrashLongPath) {
		if stageErr := preRepairTrashViaShortStaging(backupDir); stageErr == nil {
			slog.Info("desktop: pre-repair backup deleted to recycle bin via short-path staging", "path", backupDir)
			return true, nil
		} else {
			// 中转也不可行：把原始长路径拒绝原样上抛（errors.Is 链保留）——
			// 上层据此给 permanentOnly 裁决，用户明示后才允许永久删除。
			slog.Warn("desktop: pre-repair delete could not be staged for the recycle bin", "path", backupDir, "trash_err", trashErr, "staging_err", stageErr)
			return false, trashErr
		}
	}
	slog.Warn("desktop: pre-repair delete failed at the recycle bin", "path", backupDir, "err", trashErr)
	return false, trashErr
}

// PreRepairDeleteView 是单删的裁决回执（tl1658）：删除走没走成、有没有经
// 短路径中转，以及「回收语义走不通、只剩明示永久删除一条路」的裁决。
// permanentOnly=true 时目录原样未动、error 为 nil——这不是成功也不是失败，
// 是把裁决交给前端出第二段明示弹窗（「永久删除不可恢复」，确认后才调
// DeletePreRepairBackupPermanent）。文案与实际行为一致的三态就挂在这三个
// 字段上：Trashed=已进回收站（toast 说「进回收站」）、PermanentOnly=未删
// （弹窗说「永久删除不可恢复」）、其余=诚实报错。
type PreRepairDeleteView struct {
	// Trashed 表示备份已送进系统回收站（可还原）。
	Trashed bool `json:"trashed"`
	// ViaStaging 表示走了短路径中转（回收站还原时会回到中转目录而非原位置，
	// 前端 receipt 对该形态附披露说明）。
	ViaStaging bool `json:"viaStaging,omitempty"`
	// PermanentOnly 表示无法进回收站（超长路径且中转不可行）——未删除任何
	// 字节，等用户在明示弹窗确认后的永久删除。
	PermanentOnly bool `json:"permanentOnly,omitempty"`
	// Detail 是 permanentOnly 裁决的原始细节（预检拒绝全文，诊断与展示用）。
	Detail string `json:"detail,omitempty"`
}

// DeletePreRepairBackup 删单个改写前备份目录（面板行内「删除」按钮）。先过
// 健康闸（活宿主体检，tl1646 后唯一保留的闸），通过后整目录送 OS 回收站
// （可从系统回收站原位还原）；树内超长路径收不进桶时自动走短路径中转二段
// 入桶（tl1658①）。孤儿副本（宿主已不在）不再拒绝——删除决定权在用户，
// 前端确认弹窗明示「唯一副本、不可恢复」（tl1646）。中转也不可行时返回
// permanentOnly 裁决（tl1658②），由前端出明示弹窗。
func (a *App) DeletePreRepairBackup(dir string) (PreRepairDeleteView, error) {
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	if backupDir == "." {
		return PreRepairDeleteView{}, fmt.Errorf("no backup path given")
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	gatedStem, _, err := a.preRepairCleanupGate(backupDir, nil)
	if err != nil {
		return PreRepairDeleteView{}, friendlySessionFileError(err)
	}
	staged, trashErr := a.trashPreRepairBackupDir(backupDir)
	if trashErr != nil {
		if errors.Is(trashErr, errTrashLongPath) {
			// 死锁出口（tl1658②）：中转不可行，回收语义走不通。目录原样
			// 未动，裁决交前端——只有用户在明示弹窗确认后才会走永久删除桥。
			slog.Warn("desktop: pre-repair delete verdict is permanent-only (recycle bin and staging both refused)", "path", backupDir, "err", trashErr)
			return PreRepairDeleteView{PermanentOnly: true, Detail: trashErr.Error()}, nil
		}
		return PreRepairDeleteView{}, friendlySessionFileError(trashErr)
	}
	slog.Info("desktop: pre-repair backup deleted", "label", preRepairBackupLabel(backupDir, gatedStem), "path", backupDir)
	return PreRepairDeleteView{Trashed: true, ViaStaging: staged}, nil
}

// DeletePreRepairBackupPermanent 永久删除单个改写前备份目录（tl1658②兜底，
// DeleteWorkspaceEntriesPermanent 同一先例：前端必须先弹「永久删除不可恢复」
// 的明示确认再调用）。健康闸照常体检——活宿主不健康时其改写前快照是唯一的
// 回头路，兜底不允许旁路它（tl1116 纪律在永久路径上同等生效）。任务 855 铁
// 律的「彻底删除是独立显式操作」条款：本桥只服务明示确认后的删除，绝不作为
// 任何删除流的默认通道。
func (a *App) DeletePreRepairBackupPermanent(dir string) error {
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	if backupDir == "." {
		return fmt.Errorf("no backup path given")
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	gatedStem, _, err := a.preRepairCleanupGate(backupDir, nil)
	if err != nil {
		return friendlySessionFileError(err)
	}
	if err := os.RemoveAll(backupDir); err != nil {
		slog.Warn("desktop: pre-repair permanent delete failed", "path", backupDir, "err", err)
		return friendlySessionFileError(err)
	}
	slog.Info("desktop: pre-repair backup permanently deleted (explicit user verdict)", "label", preRepairBackupLabel(backupDir, gatedStem), "path", backupDir)
	return nil
}

// PreRepairCleanupFailure 是批量清理回执的一行拒绝（reason=稳定令牌，前端
// 三语；detail=原始细节，诊断用）。
type PreRepairCleanupFailure struct {
	Dir    string `json:"dir"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// PreRepairBatchCleanupView 是批量清理的回执：几条进了回收站（含体量）、
// 几条被拒（逐条带令牌）、几条经短路径中转入桶（tl1658：中转项的回收站还
// 原位置不同，前端 receipt 对该形态附披露说明）。Failures 永不为 nil（nil
// 经 Wails 序列化成 JSON null，前端 .filter 直接崩——dag 迁移面同款钉）。
type PreRepairBatchCleanupView struct {
	Trashed      int   `json:"trashed"`
	TrashedBytes int64 `json:"trashedBytes"`
	// Staged（tl1658）是 Trashed 里经短路径中转入桶的条数。
	Staged   int                       `json:"staged"`
	Failed   int                       `json:"failed"`
	Failures []PreRepairCleanupFailure `json:"failures"`
}

// preRepairCleanupProgressEvent 前端订阅的批量清理进度事件名（tl1658 现场①：
// 3.4GB/181 项的批量删除必须有可见进度；dag-migration:progress 同一先例）。
const preRepairCleanupProgressEvent = "pre-repair-cleanup:progress"

// emitPreRepairCleanupProgress 把一条批量进度推给前端；ctx 缺失（离屏/测试）
// 时静默跳过（emitDagMigrationProgress 同一形态）。
func (a *App) emitPreRepairCleanupProgress(index, total int, label, status string) {
	if a == nil || a.ctx == nil {
		return
	}
	runtimeEventsEmitFallback(a.ctx, preRepairCleanupProgressEvent, map[string]any{
		"index":  index,
		"total":  total,
		"label":  label,
		"status": status,
	})
}

// DeletePreRepairBackups 批量清理改写前备份（面板「清理所选/清理全组」）。
// 逐条过同一健康闸（活宿主健康读数在批内按宿主缓存——同一会话的多份备份只
// 重放一次），逐条送回收站（超长路径自动短路径中转，tl1658①）：一条被拒/
// 失败不拖累其余，回执逐条可归因。逐项 emit 进度事件（tl1658 现场①）——
// 每项开始（processing）与落定（trashed/staged/refused）各一条，前端据此
// 渲染「已处理 n/N」。
func (a *App) DeletePreRepairBackups(dirs []string) (PreRepairBatchCleanupView, error) {
	view := PreRepairBatchCleanupView{Failures: []PreRepairCleanupFailure{}}
	// 去重保序：前端勾选集不会有重复，防御面仍按集合语义收口。
	seen := map[string]bool{}
	ordered := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		ordered = append(ordered, dir)
	}
	if len(ordered) == 0 {
		return view, nil
	}
	slog.Info("desktop: pre-repair batch cleanup begin", "requested", len(ordered))
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	// 健康读数批内缓存：同一宿主的多份备份只体检一次（一次重放一份日志）。
	health := map[string]bool{}
	refuse := func(index int, dir, label, reason, detail string) {
		view.Failed++
		view.Failures = append(view.Failures, PreRepairCleanupFailure{
			Dir: dir, Label: label, Reason: reason, Detail: detail,
		})
		a.emitPreRepairCleanupProgress(index, len(ordered), label, "refused")
	}
	for i, backupDir := range ordered {
		base := filepath.Base(backupDir)
		stem, _, ok := preRepairStemOf(base)
		if !ok {
			refuse(i+1, backupDir, base, preRepairReasonInvalid, "not a pre-repair backup directory: "+base)
			continue
		}
		label := preRepairBackupLabel(backupDir, stem)
		a.emitPreRepairCleanupProgress(i+1, len(ordered), label, "processing")
		_, bytes, err := a.preRepairCleanupGate(backupDir, health)
		if err != nil {
			refuse(i+1, backupDir, label, preRepairFailureReason(err), err.Error())
			continue
		}
		staged, trashErr := a.trashPreRepairBackupDir(backupDir)
		if trashErr != nil {
			if errors.Is(trashErr, errTrashLongPath) {
				// tl1658②：中转也不可行——令牌归因，用户可在面板对单项走明示永久删除。
				refuse(i+1, backupDir, label, preRepairReasonPermanentOnly, trashErr.Error())
				continue
			}
			refuse(i+1, backupDir, label, preRepairReasonTrashFailed, trashErr.Error())
			continue
		}
		view.Trashed++
		view.TrashedBytes += bytes
		if staged {
			view.Staged++
			a.emitPreRepairCleanupProgress(i+1, len(ordered), label, "staged")
		} else {
			a.emitPreRepairCleanupProgress(i+1, len(ordered), label, "trashed")
		}
	}
	slog.Info("desktop: pre-repair batch cleanup done",
		"requested", len(ordered), "trashed", view.Trashed, "staged", view.Staged, "failed", view.Failed,
		"bytes", view.TrashedBytes)
	return view, nil
}

// preRepairFailureReason 把删除闸的错误映射成回执令牌。
func preRepairFailureReason(err error) string {
	switch {
	case strings.Contains(err.Error(), "owner session is not healthy"):
		return preRepairReasonOwnerUnhealthy
	case strings.Contains(err.Error(), "no longer exists"):
		return preRepairReasonGone
	default:
		return preRepairReasonInvalid
	}
}
