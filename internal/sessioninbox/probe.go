package sessioninbox

// 任务 887：空闲开轮桥的 dormant 反查面——只读探测一个会话的 durable inbox
// manifest，供 desktop 桥在「有排队 follow-up 但本进程没有任何 runtime」的
// 第三态（从未打开 / 重启后 detached 不落盘）里发现待唤醒会话。
//
// 与 Store.Open / TryFreshSnapshot 的边界（为什么它必须是独立的只读入口）：
//   - Open 会 ensurePrivateDir（给从未用过 inbox 的会话建目录）并跑跨进程
//     recovery（RunID 不同就标 Uncertain + Paused+Recovered 并提交）——4s/10s
//     一轮的桥探测绝不能改写 manifest、更不能替 controller 决定 pause 语义；
//   - TryFreshSnapshot 是 Store 方法，前提就是 Store 已经 Open 过（副作用已
//     付过），不适合「只为看一眼」的反查；
//   - 本函数：manifest 不存在 ⇒ (zero, false, nil)，不建目录、不碰锁；
//     存在 ⇒ 与 Store 写者同一把 transaction.lock 上 TryAcquire（写者持锁
//     期间直接返回 ErrProbeBusy，由调用方跳过本轮），读+decode 后立即释放。
//     Windows 上 rename-over-open-file 会让写者提交失败，所以这把锁不是
//     为了读一致（atomic rename 已保证），而是为了不挡写者的路。
//   - manifest 损坏 ⇒ 返回 error，**不 quarantine**：清道夫是 Store.Open 的
//     职责（真 runtime 打开时按原逻辑处理），只读探测不得代行。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/filelock"
	"reasonix/internal/store"
)

// ErrProbeBusy reports that a Store writer currently holds the inbox disk
// lock. Not an error in the manifest — the caller should skip this session
// for this probe round and try again next round.
var ErrProbeBusy = errors.New("sessioninbox: probe busy")

// ProbeSnapshot is the read-only, side-effect-free view of one session's
// durable inbox manifest (任务 887). found=false means the session has no
// inbox manifest at all (never had queue activity) — no directory was created
// to learn that. found=true carries the same snapshot fields the live bridge
// reads off ctrl.InboxSnapshot(): Items (manifest order, FIFO), Paused and
// UserPaused provenance. Corrupt manifests surface as an error without any
// mutation; a held disk lock surfaces as ErrProbeBusy.
func ProbeSnapshot(sessionPath string) (snap InboxSnapshot, found bool, err error) {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return InboxSnapshot{}, false, nil
	}
	dir := store.SessionInboxDir(sessionPath)
	if dir == "" {
		return InboxSnapshot{}, false, nil
	}
	manifestPath := filepath.Join(dir, manifestName)
	// Existence first: the common case for a dormant roster session is "never
	// had an inbox" and that must cost one stat, no lock, no read.
	if _, serr := os.Stat(manifestPath); serr != nil {
		if errors.Is(serr, os.ErrNotExist) {
			return InboxSnapshot{}, false, nil
		}
		return InboxSnapshot{}, false, fmt.Errorf("sessioninbox: stat manifest: %w", serr)
	}
	if derr := validatePrivateDir(dir); derr != nil {
		return InboxSnapshot{}, false, fmt.Errorf("sessioninbox: validate inbox dir: %w", derr)
	}
	release, lerr := filelock.TryAcquire(filepath.Join(dir, diskLockName))
	if lerr != nil {
		if errors.Is(lerr, filelock.ErrHeld) {
			return InboxSnapshot{}, false, ErrProbeBusy
		}
		return InboxSnapshot{}, false, fmt.Errorf("sessioninbox: acquire probe lock: %w", lerr)
	}
	defer release()
	data, rerr := readRegularFile(manifestPath, maxManifestBytes)
	if errors.Is(rerr, os.ErrNotExist) {
		return InboxSnapshot{}, false, nil
	}
	if rerr != nil {
		return InboxSnapshot{}, false, fmt.Errorf("sessioninbox: read manifest: %w", rerr)
	}
	man, derr := decodeManifest(data)
	if derr != nil {
		// Deliberately no quarantine here — see the package note above.
		return InboxSnapshot{}, false, fmt.Errorf("sessioninbox: decode manifest: %w", derr)
	}
	// Throwaway Store purely to reuse the manifest→snapshot mapping; no lock
	// is needed on it (it is local, never shared) and Open() is never called.
	s := &Store{session: sessionPath, dir: dir, man: man, limits: Limits{}.withDefaults()}
	return s.snapshotFromManifestLocked(man, man.SchemaVersion > SchemaVersion), true, nil
}
