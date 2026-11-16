//go:build windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// FO_DELETE with FOF_ALLOWUNDO is the Windows Shell API for recycle-bin
// deletion (send to Recycle Bin, restorable), replacing a self-managed mv.
const (
	shFileOpDelete         uint32 = 3
	fofSilent              uint16 = 0x0004
	fofNoConfirmation      uint16 = 0x0010
	fofAllowUndo           uint16 = 0x0040
	fofNoErrorUI           uint16 = 0x0400
	fofNoConnectedElements uint16 = 0x2000
)

// trashDeleteFlags 是删除调用实际使用的标志集（var 仅为测试缝：钉「静默回收
// 语义」的标志组合）。tl1603-r2 缺陷①勘误：原组合里挂了 FOF_WANTNUKEWARNING
// ——该标志在微软文档里**部分覆盖 FOF_NOCONFIRMATION**：当条目放不进回收站时，
// Shell 会弹系统确认框「是否要永久删除此文件夹？」。用户实测（20261116 本机
// 复现钉死的触发分支）= 改写前备份树里有**超过 MAX_PATH(260) 的内层路径**
// （实测现场最长 285 字符），这类条目 Shell 拒绝放入回收站、转永久删除路径，
// WANTNUKEWARNING 就在此处弹窗——弹窗文案逐字捕获：
//
//	「是否要永久删除此文件夹? 该文件夹包含名称过长且无法放入回收站的项目。」
//
// 移除后 Shell 对放不进回收站的条目会**静默永久删除**——那是任务 855 铁律禁
// 止的裸删，所以同时加了 trashRecycleGuard 预检：一切放不进回收站的路径在调
// Shell 之前就被诚实拒绝（应用内逐条报错，零弹窗、零裸删）。
var trashDeleteFlags = fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofNoConnectedElements

// maxRecyclePathLen 是 Shell 回收站能接受的完整路径长度上限（MAX_PATH=260；
// 实弹对照：645 字符深树触发「无法放入回收站」弹窗、285 字符真实现场同样
// 触发，≤260 的小路径 211ms 静默入桶）。Go 侧创建/枚举长路径没问题，但
// SHFileOperationW 的回收语义在此封顶——超限条目只能诚实拒绝。
const maxRecyclePathLen = 260

type shFileOpStructW struct {
	hwnd                  windows.HWND
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// shQueryRBInfo 镜像 Windows 的 SHQUERYRBINFO（cbSize+3×int64；amd64/arm64
// 下 cbSize 后有 4 字节对齐填充，总量 32）。
type shQueryRBInfo struct {
	cbSize         uint32
	i64Size        int64
	i64NumItems    int64
	i64MaxCapacity int64
}

var shell32SHFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")
var shell32SHQueryRecycleBinW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryRecycleBinW")
var kernel32GetVolumeNameForVolumeMountPointW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetVolumeNameForVolumeMountPointW")

// recycleBinUsedBytes 是预检的测试缝之一：读某盘回收站当前已用字节
// （SHQueryRecycleBinW）。注意 i64MaxCapacity 字段在 Win10/11 上实测恒报 0
// （本机 20261116：返回 S_OK、bin 内 11 项 50183 字节，MaxCapacity=0，而注册
// 表 BitBucket\Volume 每卷都有真实 MB 上限）——容量读数不能信 API，预检改读
// 注册表（见 recycleBinVolumeConfig）。
var recycleBinUsedBytes = func(driveRoot string) (usedBytes int64, err error) {
	var info shQueryRBInfo
	info.cbSize = uint32(unsafe.Sizeof(info))
	ptr, err := windows.UTF16PtrFromString(driveRoot)
	if err != nil {
		return 0, err
	}
	ret, _, _ := shell32SHQueryRecycleBinW.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&info)))
	if ret != 0 {
		return 0, fmt.Errorf("SHQueryRecycleBinW failed (code %#x)", ret)
	}
	return info.i64Size, nil
}

// recycleBinVolumeConfig 是预检的另一条测试缝：读某盘回收站的注册表配置
// （HKCU 优先、HKLM 兜底：...\Explorer\BitBucket\Volume\<卷 GUID>）。
// nukeOnDelete=该盘被配置为「删除不进回收站直接永久删除」（此时删任何东西
// 都是裸删，必须拒绝）；capacityBytes=该盘回收站容量上限（注册表单位是 MB；
// absent=未设上限，Shell 自动管理，不需要容量预检）。两级键都查无该卷=从未
// 配置过 = 默认「启用+无上限」。
var recycleBinVolumeConfig = func(volumeGUID string) (nukeOnDelete bool, capacityBytes int64, hasLimit bool, err error) {
	type binConfig struct {
		nuke     bool
		capBytes int64
		hasLimit bool
	}
	read := func(root registry.Key) (binConfig, error) {
		key, kerr := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`+volumeGUID, registry.QUERY_VALUE)
		if kerr != nil {
			return binConfig{}, kerr
		}
		defer key.Close()
		nuke, _, nerr := key.GetIntegerValue("NukeOnDelete")
		_ = nerr // 缺值=启用（0 语义）
		capMB, _, cerr := key.GetIntegerValue("MaxCapacity")
		if cerr != nil {
			return binConfig{nuke: nuke != 0}, nil
		}
		return binConfig{nuke: nuke != 0, capBytes: int64(capMB) * 1024 * 1024, hasLimit: true}, nil
	}
	lastErr := error(registry.ErrNotExist)
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		cfg, rerr := read(root)
		if rerr == nil {
			return cfg.nuke, cfg.capBytes, cfg.hasLimit, nil
		}
		lastErr = rerr
	}
	if lastErr == registry.ErrNotExist {
		return false, 0, false, nil
	}
	return false, 0, false, lastErr
}

// volumeGUIDOf 返回盘符对应的 NT 卷 GUID（"{...}" 形态，BitBucket 注册表键名）。
func volumeGUIDOf(driveRoot string) (string, error) {
	buf := make([]uint16, 100)
	ptr, err := windows.UTF16PtrFromString(driveRoot)
	if err != nil {
		return "", err
	}
	ret, _, _ := kernel32GetVolumeNameForVolumeMountPointW.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		return "", errors.New("cannot resolve the volume GUID of " + driveRoot)
	}
	guid := windows.UTF16ToString(buf)
	guid = strings.TrimPrefix(guid, `\\?\Volume`)
	guid = strings.TrimSuffix(guid, `\`)
	return guid, nil
}

// trashTreeMeasure 一次遍历读齐待删路径的两个预检读数：总字节与最长完整路径。
// 读不动（权限/中途消失）就如实报错——读数未知就无法担保「放得进回收站」。
func trashTreeMeasure(path string) (bytes int64, maxPathLen int, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	maxPathLen = len(path)
	if !info.IsDir() {
		return info.Size(), maxPathLen, nil
	}
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if n := len(p); n > maxPathLen {
			maxPathLen = n
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			bytes += fi.Size()
		}
		return nil
	})
	return bytes, maxPathLen, walkErr
}

// trashRecycleGuard 是 tl1603-r2 缺陷①的预检闸：删条目之前担保「这次删除真
// 的能静默进回收站」。分支逐一排查（验收「逐分支排查修到静默入回收站」）：
//
//  1. 路径形态——SHFileOperationW 的 FOF_ALLOWUNDO 只认**全限定绝对路径**，
//     相对路径会让该标志被忽略、条目被永久删除；UNC（\\srv\share\…）没有本机
//     回收站。统一 filepath.Abs 归一 + VolumeName 必须是「X:」，否则诚实拒绝。
//  2. 路径长度——树内任一完整路径超 MAX_PATH(260) 的条目 Shell 拒绝放入回收
//     站（缺陷①弹窗的实锤触发分支），必须拒绝；连同体量一起在一次遍历里读齐。
//  3. 回收站配置——该卷被配置为 NukeOnDelete（删除不进桶直接永久删）时拒绝；
//     注册表配了 MaxCapacity 上限时校验剩余容量装不下也拒绝（装不下时 Shell
//     会转永久删除）；SHQueryRecycleBinW 读已用字节失败且存在上限时无法担保，
//     诚实拒绝。
//
// 通过时返回归一后的绝对路径（Shell 调用必须用这一形态）。任何拒绝都发生在
// Shell 调用**之前**——全程零系统弹窗的可能。
func trashRecycleGuard(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve an absolute path for the recycle bin: %w", err)
	}
	// 卷形态检查在存在性检查之前：UNC 拼写的 Lstat 会真实探测网络（秒级），
	// 而形态不对的路径无论如何都不可回收，先拒先省。
	vol := filepath.VolumeName(abs)
	if len(vol) != 2 || vol[1] != ':' {
		return "", fmt.Errorf("%s is not on a local drive with a recycle bin (refusing a deletion that cannot be restored)", abs)
	}
	if _, statErr := os.Lstat(abs); statErr != nil {
		return "", fmt.Errorf("nothing to recycle at %s: %w", abs, statErr)
	}
	guid, guidErr := volumeGUIDOf(vol + `\`)
	if guidErr != nil {
		return "", fmt.Errorf("the recycle bin for drive %s is unavailable: %w", vol, guidErr)
	}
	nukeOnDelete, capacityBytes, hasLimit, cfgErr := recycleBinVolumeConfig(guid)
	if cfgErr != nil {
		return "", fmt.Errorf("the recycle bin configuration for drive %s is unreadable: %w", vol, cfgErr)
	}
	if nukeOnDelete {
		return "", fmt.Errorf("the recycle bin is disabled for drive %s (NukeOnDelete) — refusing a deletion that would be permanent", vol)
	}
	bytes, maxPathLen, measureErr := trashTreeMeasure(abs)
	if measureErr != nil {
		return "", fmt.Errorf("cannot measure %s for the recycle-bin pre-flight: %w", abs, measureErr)
	}
	if maxPathLen > maxRecyclePathLen {
		return "", fmt.Errorf("%s contains items whose full paths exceed %d characters (%d) — Windows cannot put them in the recycle bin and would permanently delete them, refusing", abs, maxRecyclePathLen, maxPathLen)
	}
	if hasLimit {
		used, usedErr := recycleBinUsedBytes(vol + `\`)
		if usedErr != nil {
			return "", fmt.Errorf("the recycle bin usage for drive %s is unreadable: %w", vol, usedErr)
		}
		free := capacityBytes - used
		if free < 0 {
			free = 0
		}
		if bytes > free {
			return "", fmt.Errorf("%s (%d bytes) does not fit the recycle bin of drive %s (%d of %d bytes free) — Windows would permanently delete it, refusing", abs, bytes, vol, free, capacityBytes)
		}
	}
	return abs, nil
}

func trashPlatformPaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := shell32SHFileOperationW.Find(); err != nil {
		return fmt.Errorf("shell32 SHFileOperationW unavailable: %w", err)
	}
	var builder strings.Builder
	for _, p := range paths {
		// 预检先行：放不进回收站的路径在这里就被诚实拒绝（应用内逐条报错），
		// 绝不带着 FOF_ALLOWUNDO 调 Shell 去赌「Shell 自己兜底」——那会弹
		// 「是否永久删除」（WANTNUKEWARNING）或静默裸删（去掉该标志后），
		// 两者都在缺陷①的排查单上。
		abs, guardErr := trashRecycleGuard(p)
		if guardErr != nil {
			return guardErr
		}
		builder.WriteString(abs)
		builder.WriteString("\x00")
	}
	builder.WriteString("\x00")
	// Manual UTF-16 encoding: UTF16PtrFromString rejects interior NULs, but the
	// double-NUL-terminated multi-path list is exactly what SHFileOperationW needs.
	encoded := utf16.Encode([]rune(builder.String()))
	if len(encoded) == 0 {
		return errors.New("no paths to trash")
	}
	op := shFileOpStructW{
		wFunc: shFileOpDelete,
		pFrom: &encoded[0],
		// FOF_ALLOWUNDO is the recycle-bin semantic; NOCONFIRMATION/SILENT/
		// NOERRORUI keep the panel in control of feedback. The nuke-warning
		// flag stays out (see trashDeleteFlags): the guard has already
		// refused everything the bin cannot hold, so the shell always takes
		// the silent recycle path.
		fFlags: trashDeleteFlags,
	}
	ret, _, _ := shell32SHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if ret != 0 {
		return fmt.Errorf("recycle-bin delete failed (SHFileOperationW code %#x)", ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("recycle-bin delete was aborted")
	}
	return nil
}
