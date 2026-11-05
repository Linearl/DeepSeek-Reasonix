package agent

// tl1404 终局兜底原语回归钉。常规原语(证据纪律)之外的最后一级:
//   - ForceReleaseInProcessSessionLease 必须撤掉本进程持有的活 lease 对象、
//     owner 条目、盘上身份记录与锁文件,且撤完后同路径可干净重 acquire;
//   - 锁被本进程一个未注册句柄(模型化外部进程/未知句柄)持有时,原语
//     不得越界抢锁,只如实报告「无可释放」;
//   - 健康自有 holder 的常规状态不被误撕(原语只对被点名调用的路径生效,
//     但活对象撤场后注册表/条目必须三处一致)。

import (
	"os"
	"testing"

	"reasonix/internal/store"

	"golang.org/x/sys/windows"
)

// tearLeaseIdentityInPlace overwrites the lock file's identity region while
// the holder's handle and byte-range lock stay where they are (the lock file
// must be opened through CreateFile with the full share mask: Go's os.OpenFile
// share mode collides with the handle's DELETE access).
func tearLeaseIdentityInPlace(t *testing.T, lockPath string) {
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
		t.Fatalf("open lease lock for tearing: %v", err)
	}
	f := os.NewFile(uintptr(handle), lockPath)
	if _, err := f.WriteAt([]byte("{torn-garbage"), 1); err != nil {
		f.Close()
		t.Fatalf("tear lease identity: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close lease lock: %v", err)
	}
}

// 孤儿活体(锁句柄在、盘上身份被撕成垃圾、无任何调用方引用)被原语一次清场,
// 重 acquire 立即成功——终局兜底「撕本进程全部相关锁」的实锤。
func TestForceReleaseInProcessSessionLeaseClearsLiveOrphan(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	orphan, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("acquire orphan: %v", err)
	}
	// 撕盘上身份(锁句柄保持活体)——tl1392 孤儿形态的布景。
	tearLeaseIdentityInPlace(t, store.SessionLeaseLock(key))
	if _, err := LoadSessionLeaseInfo(key); err == nil {
		t.Fatal("precondition: identity should read as corrupt")
	}

	if released := ForceReleaseInProcessSessionLease(key); !released {
		t.Fatal("ForceReleaseInProcessSessionLease released nothing for a live orphan")
	}
	if orphan.Released() == false {
		t.Fatal("the orphan lease object survived the force release")
	}
	if SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("the owner entry survived the force release")
	}
	if _, err := os.Stat(store.SessionLeaseLock(key)); !os.IsNotExist(err) {
		t.Fatalf("the lease lock file survived the force release: %v", err)
	}
	// 撤场后同路径必须可干净重 acquire——零残留的实锤。
	reattach, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("reacquire after force release = %v, want clean", err)
	}
	reattach.Release()
}

// 本进程一个未注册裸句柄长持锁(卡死保存/未知句柄形态):原语拿不下锁就
// 如实返回「无可释放」,绝不越界。锁与锁文件保持原样。
func TestForceReleaseInProcessSessionLeaseRespectsUnregisteredLockHolder(t *testing.T) {
	userPath, key := leaseTestPath(t)
	if err := os.WriteFile(userPath, []byte("turn"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	unlock, err := tryLockSessionLeaseFile(key)
	if err != nil {
		t.Fatalf("hold raw lock: %v", err)
	}
	defer unlock()

	if released := ForceReleaseInProcessSessionLease(key); released {
		t.Fatal("the primitive must not claim a release it cannot perform")
	}
	if _, err := os.Stat(store.SessionLeaseLock(key)); err != nil {
		t.Fatalf("the raw holder's lock file must stay untouched: %v", err)
	}
}

// ForceReleaseNow 幂等:已释放的 lease 二次调用零副作用(既有 Release 同契约)。
func TestForceReleaseNowIdempotent(t *testing.T) {
	userPath, _ := leaseTestPath(t)
	lease, err := TryAcquireSessionLease(userPath)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.ForceReleaseNow()
	lease.ForceReleaseNow()
	if !lease.Released() {
		t.Fatal("lease must read released after ForceReleaseNow")
	}
}
