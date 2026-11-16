//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fofWantNukeWarningPinned 是 Windows 文档的标志位值（0x4000），只在测试里
// 出现：源码的标志集必须**不含**它（tl1603-r2 缺陷①的弹窗源，见
// trashDeleteFlags 注释）。
const fofWantNukeWarningPinned uint16 = 0x4000

// TestTrashPlatformPathsWindowsRecycleBin exercises the real Shell API: the
// entries must disappear from their original location (sent to the Recycle
// Bin, restorable) — pinning that the trash path is a true recycle-bin
// deletion, not a rename or a hard remove. tl1603-r2: this now runs through
// the trashRecycleGuard pre-flight first (real volume config + real bin read
// on this machine), so it is also the end-to-end pin that a guard-passing
// short path still reaches the shell and recycles silently.
func TestTrashPlatformPathsWindowsRecycleBin(t *testing.T) {
	root := robustTempDir(t)
	file := filepath.Join(root, "recycle-me.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "recycle-dir")
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner", "b.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := trashPlatformPaths([]string{file, dir}); err != nil {
		t.Fatalf("trashPlatformPaths: %v", err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("trashed file still present: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("trashed dir still present: %v", err)
	}
	if err := trashPlatformPaths([]string{filepath.Join(root, "never-existed.txt")}); err == nil {
		t.Fatal("trashing a missing path succeeded")
	}
}

// TestTrashDeleteFlagsSilentRecycleSemantics（tl1603-r2 缺陷①静态钉）——删除
// 调用的标志组合必须：①不含 FOF_WANTNUKEWARNING（微软文档：该标志**部分覆盖
// FOF_NOCONFIRMATION**，条目放不进回收站时弹「是否要永久删除此文件夹?」的系
// 统确认框——用户实测的 Windows 弹窗就是它，20261116 本机实弹复现逐字捕获：
// 「该文件夹包含名称过长且无法放入回收站的项目」）；②保留 FOF_ALLOWUNDO（回
// 收站语义本体，任务 855 铁律）；③保留 NOCONFIRMATION/SILENT/NOERRORUI（全
// 程应用内反馈，Shell 零 UI）。
func TestTrashDeleteFlagsSilentRecycleSemantics(t *testing.T) {
	if trashDeleteFlags&fofWantNukeWarningPinned != 0 {
		t.Fatal("trashDeleteFlags must not carry FOF_WANTNUKEWARNING — it pops the OS nuke-confirm dialog for bin-ineligible entries (tl1603-r2 defect ①)")
	}
	if trashDeleteFlags&fofAllowUndo == 0 {
		t.Fatal("trashDeleteFlags must carry FOF_ALLOWUNDO — the recycle-bin semantic itself (task 855)")
	}
	if trashDeleteFlags&fofNoConfirmation == 0 {
		t.Fatal("trashDeleteFlags must carry FOF_NOCONFIRMATION — no OS prompt")
	}
	if trashDeleteFlags&fofSilent == 0 {
		t.Fatal("trashDeleteFlags must carry FOF_SILENT — no progress UI")
	}
	if trashDeleteFlags&fofNoErrorUI == 0 {
		t.Fatal("trashDeleteFlags must carry FOF_NOERRORUI — failures surface in-app, not in OS dialogs")
	}
}

// withRecycleBinSeam 替换预检的两条读数缝（注册表卷配置 + 已用字节），钉死
// 预检的拒绝/放行分支——不需要构造真实的回收站状态。
func withRecycleBinSeam(t *testing.T, nukeOnDelete bool, capacityBytes int64, hasLimit bool, usedBytes int64, usedErr error) {
	t.Helper()
	previousCfg := recycleBinVolumeConfig
	recycleBinVolumeConfig = func(volumeGUID string) (bool, int64, bool, error) {
		return nukeOnDelete, capacityBytes, hasLimit, nil
	}
	previousUsed := recycleBinUsedBytes
	recycleBinUsedBytes = func(driveRoot string) (int64, error) {
		return usedBytes, usedErr
	}
	t.Cleanup(func() {
		recycleBinVolumeConfig = previousCfg
		recycleBinUsedBytes = previousUsed
	})
}

// TestTrashPlatformPathsRefusesOverLongPaths（tl1603-r2 缺陷①实锤分支钉）——
// 树内任一完整路径超 MAX_PATH(260)：Shell 拒绝放入回收站、弹「是否要永久删
// 除此文件夹?」确认框（本机逐字捕获的弹窗文案）。预检必须在调 Shell 之前
// 诚实拒绝：目录原样、零弹窗可能。这正对用户实测现场（175 项 pre-repair
// 备份，最长内层路径 285 字符）。
func TestTrashPlatformPathsRefusesOverLongPaths(t *testing.T) {
	root := robustTempDir(t)
	// 深层长段名树，制造 >260 的完整路径（与实测形态同构）。
	base := filepath.Join(root, "long-tree")
	p := base
	for i := 0; i < 8; i++ {
		p = filepath.Join(p, "very-long-segment-name-20260804-063617-recovery-7c889e4550364306-padding")
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "events.jsonl"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	innerLen := len(filepath.Join(p, "events.jsonl"))
	if innerLen <= maxRecyclePathLen {
		t.Fatalf("fixture too short to matter: %d (test bug)", innerLen)
	}

	err := trashPlatformPaths([]string{base})
	if err == nil {
		t.Fatal("a tree with over-MAX_PATH items must be refused before the shell call")
	}
	if !strings.Contains(err.Error(), "recycle bin") || !strings.Contains(err.Error(), "permanently delete") {
		t.Fatalf("err = %v, want the long-path refusal naming the bin and the would-be nuke", err)
	}
	if _, statErr := os.Lstat(base); statErr != nil {
		t.Fatalf("refused path must stay untouched: %v", statErr)
	}
}

// TestTrashPlatformPathsRefusesWhenBinTooSmall（缺陷①失败路径钉）——注册表
// 配了容量上限且条目装不下：Shell 会转永久删除，预检必须先行拒绝。
func TestTrashPlatformPathsRefusesWhenBinTooSmall(t *testing.T) {
	root := robustTempDir(t)
	dir := filepath.Join(root, "big-backup.pre-repair-20261110T010000")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.jsonl"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	withRecycleBinSeam(t, false, 1<<20, true, (1<<20)-1024, nil) // cap=1MB, free=1KB

	err := trashPlatformPaths([]string{dir})
	if err == nil {
		t.Fatal("a path that cannot fit the recycle bin must be refused before the shell call")
	}
	if !strings.Contains(err.Error(), "does not fit the recycle bin") {
		t.Fatalf("err = %v, want the capacity refusal with the numbers", err)
	}
	if _, statErr := os.Lstat(dir); statErr != nil {
		t.Fatalf("refused path must stay untouched: %v", statErr)
	}
}

// TestTrashPlatformPathsRefusesWhenBinDisabled（缺陷①失败路径钉②）——该卷
// 配置 NukeOnDelete（删除不进桶直接永久删）：删任何东西都是裸删，预检拒绝。
func TestTrashPlatformPathsRefusesWhenBinDisabled(t *testing.T) {
	root := robustTempDir(t)
	file := filepath.Join(root, "recycle-me.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRecycleBinSeam(t, true, 0, false, 0, nil)

	err := trashPlatformPaths([]string{file})
	if err == nil {
		t.Fatal("a NukeOnDelete volume must be refused")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err = %v, want the disabled-bin refusal", err)
	}
	if _, statErr := os.Lstat(file); statErr != nil {
		t.Fatalf("refused path must stay untouched: %v", statErr)
	}
}

// TestTrashPlatformPathsRefusesWhenLimitButUsageUnreadable（缺陷①失败路径钉
// ③）——存在容量上限但已用字节读不到：无法担保「装得下」，诚实拒绝（无上
// 限时不依赖该读数，见 AllowsWithinCapacity 的 hasLimit=false 形态）。
func TestTrashPlatformPathsRefusesWhenLimitButUsageUnreadable(t *testing.T) {
	root := robustTempDir(t)
	file := filepath.Join(root, "recycle-me.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRecycleBinSeam(t, false, 1<<30, true, 0, os.ErrPermission)

	err := trashPlatformPaths([]string{file})
	if err == nil {
		t.Fatal("an unreadable bin usage with a configured limit must be refused")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("err = %v, want the usage-unreadable refusal", err)
	}
}

// TestTrashPlatformPathsRefusesRelativeAndUNC（缺陷①路径形态钉）——相对路径
// （FOF_ALLOWUNDO 会被忽略→永久删除）与非本地盘形态（UNC 无本机回收站）都在
// 预检被拒：错误明确、Shell 零触碰。
func TestTrashPlatformPathsRefusesRelativeAndUNC(t *testing.T) {
	// 相对路径：cwd 下不存在这个名字，Lstat 先拒；即便 cwd 里恰好有同名条目，
	// 归一后的绝对形态也不会以相对拼写抵达 Shell（相对形态会让 FOF_ALLOWUNDO
	// 失效转永久删除）。
	rel := filepath.Join("definitely-missing-relative-dir", "x.txt")
	if err := trashPlatformPaths([]string{rel}); err == nil {
		t.Fatal("a missing path must be refused")
	}
	// UNC 形态：不落盘的 UNC 拼写（VolumeName=\\host\share≠X:）。
	unc := `\\localhost\c$\definitely-missing\x.txt`
	err := trashPlatformPaths([]string{unc})
	if err == nil {
		t.Fatal("a UNC path must be refused (no local recycle bin)")
	}
	if !strings.Contains(err.Error(), "local drive") {
		t.Fatalf("err = %v, want the local-drive refusal", err)
	}
}

// TestTrashPlatformPathsAllowsWithinCapacity（缺陷①放行路径钉）——卷配置为
// 「启用+有上限+装得下」时，小条目走真 Shell 静默入回收站（端到端：预检通过
// →SHFileOperation→原位消失）。
func TestTrashPlatformPathsAllowsWithinCapacity(t *testing.T) {
	root := robustTempDir(t)
	file := filepath.Join(root, "small-recycle-me.txt")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRecycleBinSeam(t, false, 1<<30, true, 0, nil) // 1GB cap, empty bin

	if err := trashPlatformPaths([]string{file}); err != nil {
		t.Fatalf("a guard-passing path must reach the shell and recycle: %v", err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("trashed file still present: %v", err)
	}
}

// TestTrashDeleteFlagsCarryRecycleSemantics 与 Windows 文档逐位对照：标志集
// 的位值不许漂移（0x0004/0x0010/0x0040/0x0400/0x2000；WANTNUKEWARNING=0x4000
// 必须缺席）。
func TestTrashDeleteFlagsCarryRecycleSemantics(t *testing.T) {
	want := fofSilent | fofNoConfirmation | fofAllowUndo | fofNoErrorUI | fofNoConnectedElements
	if trashDeleteFlags != want {
		t.Fatalf("trashDeleteFlags = %#x, want exactly %#x", trashDeleteFlags, want)
	}
}
