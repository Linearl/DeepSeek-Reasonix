package main

// tl1827 e2e 回归（RecoverFailedInstall 启动接线，审计 A3）：
//  ① 写失败标记 + 未完成事务 → 启动恢复链路回滚发布单元、清标记、清事务，
//     并落 "[update-recover]" 锚日志（tl1795 锚日志先例）。钉住
//     desktop/updater.go applyLinux 注释与 apply_failure.go 头注释共同承诺
//     的「启动时回滚」——接线前无人据标记回滚。
//  ② 无标记 → 零动作：不产生标记、不动事务、info 级零日志。
//  ③ 启动链确实踢出恢复（startupStartLocalServices 的接线经
//     startupRecoverFailedInstall 生效）。
//  ④ 回滚出错路径可观测：Warn 锚日志（repair 语义=标记与事务都保留，下轮重试）。

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/repair"
)

// tl1827CaptureStartupLog swaps the default slog handler for a buffer and
// restores it at cleanup (tl1795 precedent).
func tl1827CaptureStartupLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logBuf
}

// tl1827InstallFixture stages a v1→v2 file transaction whose apply half-failed,
// mirroring the Windows helper handoff: install dir is the running
// executable's directory (repair validates the target against
// os.Executable and cannot be stubbed from outside the repair package),
// guard carries the partially-installed new payload, and the helper's
// failure marker is on disk.
func tl1827InstallFixture(t *testing.T) (target, guard string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	exePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatalf("eval test executable: %v", err)
	}
	installDir := filepath.Dir(exePath)
	target = filepath.Join(installDir, "reasonix-desktop")
	guard = filepath.Join(installDir, "reasonix-guard")
	t.Cleanup(func() {
		_ = os.Remove(target)
		_ = os.Remove(guard)
	})
	for path, body := range map[string]string{target: "old-desktop", guard: "old-guard"} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("stage %s: %v", filepath.Base(path), err)
		}
	}
	if _, err := repair.PrepareFileUpdate("v1", "v2", target, guard); err != nil {
		t.Fatalf("prepare update transaction: %v", err)
	}
	// Simulate the partial NSIS run: guard replaced, desktop untouched.
	if err := os.WriteFile(guard, []byte("new-guard"), 0o700); err != nil {
		t.Fatalf("simulate partial install: %v", err)
	}
	if err := repair.MarkUpdateApplyFailed("v2", "installer exited with 1"); err != nil {
		t.Fatalf("record installer failure marker: %v", err)
	}
	if _, ok := repair.ReadUpdateApplyFailure(); !ok {
		t.Fatal("precondition failed: failure marker must be on disk")
	}
	return target, guard
}

// ① 有标记：启动回滚发生 + 标记清除 + 锚日志。
func TestTl1827StartupRecoveryRollsBackAndClearsMarker(t *testing.T) {
	isolateDesktopUserDirs(t)
	logBuf := tl1827CaptureStartupLog(t)
	target, guard := tl1827InstallFixture(t)

	runUpdateApplyFailureRecovery()

	// 回滚发生：发布单元回到 v1 内容。
	for path, want := range map[string]string{target: "old-desktop", guard: "old-guard"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restored %s = %q (%v), want %q", filepath.Base(path), got, err, want)
		}
	}
	// 标记清除。
	if _, ok := repair.ReadUpdateApplyFailure(); ok {
		t.Fatal("failure marker survived the startup rollback (tl1827)")
	}
	// 事务清除。
	if repair.HasPendingUpdate() {
		t.Fatal("pending transaction survived the startup rollback (tl1827)")
	}
	// 锚日志可观测。
	if !strings.Contains(logBuf.String(), "[update-recover] rolled back failed update at startup") {
		t.Fatalf("rollback anchor log missing; got:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), "restoredVersion=v1") {
		t.Fatalf("anchor log must name the restored version; got:\n%s", logBuf.String())
	}
	// 幂等：再次启动（无标记）零动作。
	runUpdateApplyFailureRecovery()
	if strings.Count(logBuf.String(), "[update-recover]") != 1 {
		t.Fatalf("second startup must be silent; log:\n%s", logBuf.String())
	}
}

// ② 无标记：零动作（无标记落盘、无事务、info 级零日志）。
func TestTl1827StartupRecoveryNoMarkerIsZeroAction(t *testing.T) {
	isolateDesktopUserDirs(t)
	logBuf := tl1827CaptureStartupLog(t)

	runUpdateApplyFailureRecovery()

	if _, ok := repair.ReadUpdateApplyFailure(); ok {
		t.Fatal("recovery pass without a marker must not create one")
	}
	if repair.HasPendingUpdate() {
		t.Fatal("recovery pass without a marker must not create a transaction")
	}
	if strings.Contains(logBuf.String(), "[update-recover]") {
		t.Fatalf("markerless startup must stay silent at info level; log:\n%s", logBuf.String())
	}
}

// ③ 启动链确实踢出恢复（接线钉：startupStartLocalServices →
// startupRecoverFailedInstall → goSafe）。
func TestTl1827StartupChainKicksRecovery(t *testing.T) {
	isolateDesktopUserDirs(t)
	kicked := make(chan struct{}, 1)
	original := repairRecoverFailedInstall
	repairRecoverFailedInstall = func() (repair.UpdateRollbackResult, *repair.UpdateApplyFailure, error) {
		kicked <- struct{}{}
		return repair.UpdateRollbackResult{}, nil, nil
	}
	t.Cleanup(func() { repairRecoverFailedInstall = original })

	app := &App{}
	app.startupRecoverFailedInstall()
	select {
	case <-kicked:
	case <-time.After(5 * time.Second):
		t.Fatal("startup chain did not kick the failed-install recovery (tl1827 wiring)")
	}
}

// ④ 回滚出错可观测：Warn 锚日志，repair 语义=标记保留待下轮重试。
func TestTl1827StartupRecoveryErrorIsObservable(t *testing.T) {
	isolateDesktopUserDirs(t)
	logBuf := tl1827CaptureStartupLog(t)
	original := repairRecoverFailedInstall
	repairRecoverFailedInstall = func() (repair.UpdateRollbackResult, *repair.UpdateApplyFailure, error) {
		return repair.UpdateRollbackResult{},
			&repair.UpdateApplyFailure{SchemaVersion: 1, ToVersion: "v2", Reason: "installer exited with 1"},
			errors.New("rollback update: backup hash mismatch")
	}
	t.Cleanup(func() { repairRecoverFailedInstall = original })

	runUpdateApplyFailureRecovery()

	if !strings.Contains(logBuf.String(), "[update-recover] failed-update rollback errored; marker retained for retry") {
		t.Fatalf("error anchor log missing; got:\n%s", logBuf.String())
	}
}
