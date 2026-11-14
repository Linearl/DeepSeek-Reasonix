//go:build windows

package edge

import (
	"errors"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ── tl1589 (issue #93): renderer 恢复路径归因钉 ─────────────────────────────
// 三条失败路径过去统一上报 "reload_failed"，单条遥测无法 triage 恢复死在哪一步；
// unresponsive（kind=2）与 exited（kind=1）共用同一 10s reload 窗口，hang 的
// renderer 无法配合导航，窗口注定烧满才升级重启。钉住：三路径各产不同
// recovery 值、unresponsive 短窗、exited 形态零回退。

func observeRendererRecovery(t *testing.T) func() []ProcessFailedDiagnostic {
	t.Helper()
	var mu sync.Mutex
	var observed []ProcessFailedDiagnostic
	SetProcessFailedObserver(func(diagnostic ProcessFailedDiagnostic) {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, diagnostic)
	})
	t.Cleanup(func() { SetProcessFailedObserver(nil) })
	return func() []ProcessFailedDiagnostic {
		mu.Lock()
		defer mu.Unlock()
		return append([]ProcessFailedDiagnostic(nil), observed...)
	}
}

// 验收①edge 面钉：unresponsive 用 3s 短窗、exited 保持 10s 全窗。
func TestRendererRecoveryTimeoutPerForm(t *testing.T) {
	if got := rendererRecoveryTimeout(COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_UNRESPONSIVE); got != reasonixUnresponsiveRecoveryTimeout {
		t.Fatalf("unresponsive window = %v, want %v (a hung renderer cannot complete the reload, the full window is a structural wait)", got, reasonixUnresponsiveRecoveryTimeout)
	}
	if got := rendererRecoveryTimeout(COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED); got != reasonixProcessRecoveryTimeout {
		t.Fatalf("exited window = %v, want %v (acceptance ⑤: the exited form must keep its full window)", got, reasonixProcessRecoveryTimeout)
	}
}

// 验收②路径 a 钉：Reload 调用直接报错 → recovery=reload_error。
func TestReloadCallErrorReportsReloadError(t *testing.T) {
	observed := observeRendererRecovery(t)
	chromium := &Chromium{globalErrorCallback: func(error) {}}
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	diagnostic := ProcessFailedDiagnostic{Kind: COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_UNRESPONSIVE}
	chromium.handleFailedRendererRecovery(diagnostic, now, func() error {
		return errors.New("reload rejected")
	})
	observers := observed()
	if len(observers) != 1 {
		t.Fatalf("observed %d diagnostics, want 1: %+v", len(observers), observers)
	}
	if got := observers[0].Recovery; got != reasonixRecoveryReloadError {
		t.Fatalf("reload-error path recovery = %q, want %q", got, reasonixRecoveryReloadError)
	}
}

// 验收②路径 b 钉：等待窗口耗尽（导航从未完成）→ recovery=reload_timeout。
// 定时器机制本身由 reasonixRecoveryState 的既有测试覆盖，这里钉的是超时回调
// 的打标——desktop 端由它升级受控重启。
func TestRendererRecoveryTimeoutReportsReloadTimeout(t *testing.T) {
	observed := observeRendererRecovery(t)
	chromium := &Chromium{}
	diagnostic := ProcessFailedDiagnostic{Kind: COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_UNRESPONSIVE}
	chromium.rendererRecoveryTimedOut(diagnostic)
	observers := observed()
	if len(observers) != 1 {
		t.Fatalf("observed %d diagnostics, want 1: %+v", len(observers), observers)
	}
	if got := observers[0].Recovery; got != reasonixRecoveryReloadTimeout {
		t.Fatalf("timeout path recovery = %q, want %q", got, reasonixRecoveryReloadTimeout)
	}
}

// stubNavigationCompleted builds a NavigationCompletedEventArgs whose
// GetNavigationID and GetIsSuccess answer from the given values.
func stubNavigationCompleted(t *testing.T, navigationID uint64, success bool) *ICoreWebView2NavigationCompletedEventArgs {
	t.Helper()
	return &ICoreWebView2NavigationCompletedEventArgs{vtbl: &_ICoreWebView2NavigationCompletedEventArgsVtbl{
		GetNavigationId: NewComProc(func(_, result uintptr) uintptr {
			*(*uint64)(unsafe.Pointer(result)) = navigationID
			return uintptr(windows.S_OK)
		}),
		GetIsSuccess: NewComProc(func(_, result uintptr) uintptr {
			if success {
				*(*int32)(unsafe.Pointer(result)) = 1
			} else {
				*(*int32)(unsafe.Pointer(result)) = 0
			}
			return uintptr(windows.S_OK)
		}),
	}}
}

// 验收②路径 c 钉：导航完成但 IsSuccess=false → recovery=reload_nav_failed。
func TestNavCompletedFailureReportsNavFailed(t *testing.T) {
	observed := observeRendererRecovery(t)
	chromium := &Chromium{globalErrorCallback: func(error) {}}
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	if !chromium.processRecovery.begin(ProcessFailedDiagnostic{Kind: COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED}, now, time.Minute, time.Hour, nil) {
		t.Fatal("failed to arm recovery")
	}
	if !chromium.processRecovery.bindNavigation(41) {
		t.Fatal("failed to bind navigation")
	}
	diagnostic, ok := chromium.completeFailedRendererRecovery(stubNavigationCompleted(t, 41, false))
	if !ok {
		t.Fatal("matching completion was not consumed")
	}
	if got := diagnostic.Recovery; got != reasonixRecoveryNavFailed {
		t.Fatalf("nav-failed path recovery = %q, want %q", got, reasonixRecoveryNavFailed)
	}
	if len(observed()) != 0 {
		t.Fatalf("completeFailedRendererRecovery must only return the diagnostic, observed=%+v", observed())
	}
}

// 成功路径不回退钉：IsSuccess=true → reload_navigation_succeeded（desktop
// coordinator 据此进入 awaiting_ready 而非重启）。
func TestNavCompletedSuccessReportsPending(t *testing.T) {
	observed := observeRendererRecovery(t)
	chromium := &Chromium{globalErrorCallback: func(error) {}}
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	if !chromium.processRecovery.begin(ProcessFailedDiagnostic{Kind: COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED}, now, time.Minute, time.Hour, nil) {
		t.Fatal("failed to arm recovery")
	}
	if !chromium.processRecovery.bindNavigation(42) {
		t.Fatal("failed to bind navigation")
	}
	diagnostic, ok := chromium.completeFailedRendererRecovery(stubNavigationCompleted(t, 42, true))
	if !ok {
		t.Fatal("matching completion was not consumed")
	}
	if got := diagnostic.Recovery; got != "reload_navigation_succeeded" {
		t.Fatalf("nav-succeeded recovery = %q, want reload_navigation_succeeded (must keep feeding the awaiting-ready phase)", got)
	}
	if len(observed()) != 0 {
		t.Fatalf("completeFailedRendererRecovery must only return the diagnostic, observed=%+v", observed())
	}
}

// 验收⑤edge 面钉：exited 形态行为不变——reload 照常发起、pending 照常建立、
// 导航未完成前不产生任何 observer 事件（升级仍只来自超时/导航结果）。
func TestExitedRendererReloadPathUnchanged(t *testing.T) {
	observed := observeRendererRecovery(t)
	chromium := &Chromium{globalErrorCallback: func(error) {}}
	now := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	reloads := 0
	diagnostic := ProcessFailedDiagnostic{Kind: COREWEBVIEW2_PROCESS_FAILED_KIND_RENDER_PROCESS_EXITED}
	chromium.handleFailedRendererRecovery(diagnostic, now, func() error {
		reloads++
		return nil
	})
	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1 (exited form still gets its in-place reload)", reloads)
	}
	if len(observed()) != 0 {
		t.Fatalf("successful reload start must not emit a diagnostic yet: %+v", observed())
	}
	if !chromium.processRecovery.hasPending() {
		t.Fatal("recovery must stay pending until the bound navigation completes or the window elapses")
	}
	// Consume the armed 10s timer so it cannot fire into a later test.
	chromium.processRecovery.finish()
	if chromium.processRecovery.hasPending() {
		t.Fatal("finish must clear the pending recovery")
	}
}
