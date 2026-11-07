package main

// tl1383/tl1392 销案复现钉：按两件正文的最小现场构造，钉「修后绿」——
// 若未来回退（归档被 holder 残留卡死/force 死循环/误导文案回潮），本文件双红。
//
// tl1383 最小现场（2026-11-04 08:26 事故）：单实例、会话转录文件已不存在
// （侧栏僵尸条目，tl1363 同族）、lease 残留无 holder 身份（holder_class=
// unknown pid=0）——常规归档必须一次成功，绝不回「被另一个 Reasonix 窗口
// 或进程占用」误导文案（holder 身份缺失时真实持有者不可指认）。
//
// tl1392 最小现场（2026-11-04 16:13 事故，包73）：强制归档撕掉磁盘残留记录
// 成功，但下一次 acquire 仍被「进程内孤儿 owner」拒绝（无 tab 绑定，
// reclaimTopicSelfLeasesLocked 与 zombie sweep 都够不着）——force 必须一次
// 成功（不再循环），归档后同路径立即可重新 acquire（不再「撕完仍被持有」），
// 文案绝不再次建议 force（自指引死循环）。
//
// 两件共用一套自包含布景 helper（tlRepro* 前缀，避免与本包既有钉撞名）；
// 不引用 tl1383 修复2 之后才存在的 sentinel 符号，使本文件在
// c38af6bef^（两修均无）与 b7bbeea44^（仅 tl1383 修入）上也能编译，
// 供「修前红/修后绿」矩阵运行。

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"

	"golang.org/x/sys/windows"
)

// tlReproArchiveBudget is the single-call archive ceiling used here (the
// tl1404 user-visible budget is 10s; the margin absorbs slow-machine CI).
const tlReproArchiveBudget = 15 * time.Second

// tlReproStageTopic stages one single-session topic the way the force pins
// do, returning the transcript path and the project root so the sidebar-entry
// assertion can read the topic registry.
func tlReproStageTopic(t *testing.T, name, topicID, title string) (string, string) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, title); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, name, topicID, title, projectRoot, "preserve me", time.Now())
	return sessionPath, projectRoot
}

// tlReproCorruptLeaseLock tears the lease lock file's owner-identity region
// in place while every handle and the byte-range lock stay exactly where they
// are (self-contained twin of the tl1392-era corruptLeaseLockContent, kept
// local so this file also compiles on pre-fix baselines). The write starts at
// offset 1 (sessionLeaseOwnerOffset): the holder's LockFileEx pins byte 0.
func tlReproCorruptLeaseLock(t *testing.T, lockPath string) {
	t.Helper()
	pathp, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		t.Fatalf("lock path utf16: %v", err)
	}
	handle, err := windows.CreateFile(pathp,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open lease lock for corrupting: %v", err)
	}
	f := os.NewFile(uintptr(handle), lockPath)
	if _, err := f.WriteAt([]byte("{tl1383-torn-identity"), 1); err != nil {
		f.Close()
		t.Fatalf("corrupt lease lock content: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close lease lock: %v", err)
	}
}

// tlReproAssertNoMisleadingBusyCopy pins the tl1383 copy contract on any
// refusal: a holder-less refusal must never tell the user to look for
// "another Reasonix window" (2026-11-04 实测误导：用户去找不存在的另一窗口).
func tlReproAssertNoMisleadingBusyCopy(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, errSessionBusyElsewhere) {
		t.Fatalf("refusal classifies as the another-window sentinel without a holder identity: %v", err)
	}
	if strings.Contains(err.Error(), "another Reasonix window") {
		t.Fatalf("refusal copy misdirects to a nonexistent window (holder-less residue form): %v", err)
	}
}

// TestTl1383ReproZombieEntryRegularArchiveWithHolderlessResidue is the
// 2026-11-04 08:26 scene: one Reasonix instance, the session transcript is
// already gone (sidebar zombie entry), and a lease residue with NO holder
// identity still books the path (an in-process orphan whose disk identity was
// torn). The REGULAR archive must converge in one call — the heal adopts the
// residue — and leave zero lease residue behind.
func TestTl1383ReproZombieEntryRegularArchiveWithHolderlessResidue(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1383_repro_zombie"
	sessionPath, projectRoot := tlReproStageTopic(t, "tl1383-repro-zombie.jsonl", topicID, "tl1383 repro zombie")
	key := sessionRuntimeKey(sessionPath)

	// 残留活体：本进程一个无 tab 绑定的 lease 对象持有 OS 锁，盘上身份被撕
	// 成垃圾（holder 信息为空 = 事故里的 holder_class=unknown pid=0 形态）。
	orphan, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("stage orphan lease: %v", err)
	}
	t.Cleanup(func() { orphan.Release() })
	tlReproCorruptLeaseLock(t, store.SessionLeaseLock(key))

	// 转录文件已不存在：侧栏条目是僵尸（tl1363 同族）。
	if err := os.Remove(sessionPath); err != nil {
		t.Fatalf("remove transcript: %v", err)
	}

	app := NewApp()
	started := time.Now()
	archiveErr := app.TrashTopic(topicID)
	if archiveErr != nil {
		tlReproAssertNoMisleadingBusyCopy(t, archiveErr)
		t.Fatalf("regular archive of the zombie entry = %v, want one-call success", archiveErr)
	}
	if elapsed := time.Since(started); elapsed > tlReproArchiveBudget {
		t.Fatalf("regular archive took %v, want <%v", elapsed, tlReproArchiveBudget)
	}

	// 零残留：身份记录与锁文件都随归档消失，侧栏条目清掉，同路径可干净
	// 重新 acquire。
	if _, err := os.Stat(store.SessionLeaseInfo(key)); !os.IsNotExist(err) {
		t.Fatalf("holder-less lease identity residue survived the archive: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseLock(key)); !os.IsNotExist(err) {
		t.Fatalf("orphan lease lock file survived the archive: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("archived topic title = %q, want cleared (sidebar entry gone)", got)
	}
	reattach, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("reacquire after archive = %v, want a clean lease state", err)
	}
	reattach.Release()
}

// TestTl1392ReproForceArchiveOrphanOwnerOneShot is the 2026-11-04 16:13 scene
// (package 73): the force archive retires the disk residue ("force archive
// retired a stale lease record", holder_pid=0) yet the NEXT acquire is still
// refused by the in-process orphan owner (holder_class=unknown pid=0, no
// usable lease.lock identity on disk). Force must succeed ONCE, and the same
// path must be re-acquirable immediately afterwards — no loop, no "still held
// right after force".
func TestTl1392ReproForceArchiveOrphanOwnerOneShot(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_tl1392_repro_orphan"
	sessionPath, _ := tlReproStageTopic(t, "tl1392-repro-orphan.jsonl", topicID, "tl1392 repro orphan")
	key := sessionRuntimeKey(sessionPath)

	// 进程内孤儿 owner：无 tab 绑定（topic 自愈与 zombie sweep 双盲区），
	// 磁盘身份撕成垃圾但 OS 锁句柄仍活——撕磁盘记录治不了它。
	orphan, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("stage orphan lease: %v", err)
	}
	t.Cleanup(func() { orphan.Release() })
	tlReproCorruptLeaseLock(t, store.SessionLeaseLock(key))

	app := NewApp()
	// 阶梯(事故现场①→②):常规归档——heal 链覆盖 torn 身份后必须一次
	// 成功(孤儿自愈);若拒绝,拒绝文案不得误导「另一个窗口」,且 force
	// 必须一次成功(撕掉磁盘记录只是一半,进程内孤儿 owner 必须同场清掉,
	// 绝不弹回同一句文案)。两条路汇入同一组终态断言。
	archiveStarted := time.Now()
	if regularErr := app.TrashTopic(topicID); regularErr != nil {
		tlReproAssertNoMisleadingBusyCopy(t, regularErr)
		if _, err := os.Stat(sessionPath); err != nil {
			t.Fatalf("refused archive removed the session: %v", err)
		}
		forceStarted := time.Now()
		forceErr := app.TrashTopicForce(topicID)
		if forceErr != nil {
			tlReproAssertNoMisleadingBusyCopy(t, forceErr)
			if strings.Contains(strings.ToLower(forceErr.Error()), "use force archive") {
				t.Fatalf("after-force copy recommends force again (package-73 self-referential loop): %v", forceErr)
			}
			t.Fatalf("force archive against the in-process orphan = %v, want one-shot success", forceErr)
		}
		if elapsed := time.Since(forceStarted); elapsed > tlReproArchiveBudget {
			t.Fatalf("force archive took %v, want <%v", elapsed, tlReproArchiveBudget)
		}
	} else if elapsed := time.Since(archiveStarted); elapsed > tlReproArchiveBudget {
		t.Fatalf("regular archive (orphan self-heal) took %v, want <%v", elapsed, tlReproArchiveBudget)
	}

	// 归档收尾：转录进回收站、身份残留清零。
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("force archive left the transcript in place: %v", err)
	}
	if _, err := os.Stat(store.SessionLeaseInfo(key)); !os.IsNotExist(err) {
		t.Fatalf("lease identity residue survived the force archive: %v", err)
	}

	// 验收核心（「撕记录成功但下一次仍被拒」死循环的反面）：force 返回后
	// 立即重新 acquire 必须成功且不再撞上任何残留持有者。
	reacquireStart := time.Now()
	reattach, reacquireErr := agent.TryAcquireSessionLease(sessionPath)
	if reacquireErr != nil {
		t.Fatalf("reacquire right after force = %v, want the lock free (no in-process orphan left)", reacquireErr)
	}
	if elapsed := time.Since(reacquireStart); elapsed > 100*time.Millisecond {
		t.Logf("reacquire took %v (acceptance target: free well within 100ms)", elapsed)
	}
	reattach.Release()
}
