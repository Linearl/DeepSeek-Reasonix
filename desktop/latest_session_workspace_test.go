package main

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 546: the Wails payload mirrors the shared agent resolution one-to-one —
// usable verdict, stale path preservation, and a best-effort display title.
func TestLastSessionWorkspaceInfoMapsLatest(t *testing.T) {
	project := t.TempDir()
	at := time.Now().Add(-time.Hour)
	latest := agent.LatestSessionWorkspace{
		SessionPath:    filepath.Join("store", "20261006_100000_alpha.jsonl"),
		WorkspaceRoot:  filepath.Clean(project),
		Usable:         true,
		LastActivityAt: at,
		CustomTitle:    "Alpha work",
		TopicTitle:     "topic title",
	}
	info := lastSessionWorkspaceInfo(latest, true, " (recovered)")
	if info.Path != filepath.Clean(project) {
		t.Fatalf("Path = %q, want %q", info.Path, filepath.Clean(project))
	}
	if !info.Usable {
		t.Fatal("Usable must carry over")
	}
	if info.SessionTitle != "Alpha work" {
		t.Fatalf("SessionTitle = %q, want the custom title", info.SessionTitle)
	}
	if info.LastActivityAt != at.UnixMilli() {
		t.Fatalf("LastActivityAt = %d, want %d", info.LastActivityAt, at.UnixMilli())
	}
}

// A stale root must reach the frontend unchanged so the fallback notice can
// name the dead path; the title falls back to the topic title.
func TestLastSessionWorkspaceInfoKeepsStalePathAndTitleFallback(t *testing.T) {
	latest := agent.LatestSessionWorkspace{
		SessionPath:   filepath.Join("store", "20261006_100000_beta.jsonl"),
		WorkspaceRoot: filepath.Clean(filepath.Join(t.TempDir(), "gone")),
		Usable:        false,
		TopicTitle:    "Beta topic",
	}
	info := lastSessionWorkspaceInfo(latest, true, " (recovered)")
	if info.Usable {
		t.Fatal("stale root must stay not-usable")
	}
	if info.SessionTitle != "Beta topic" {
		t.Fatalf("SessionTitle = %q, want the topic title fallback", info.SessionTitle)
	}
	if info.Path == "" {
		t.Fatal("stale path must not be blanked out")
	}
}

// No candidate anywhere → empty payload (frontend hides the option).
func TestLastSessionWorkspaceInfoEmptyWhenNoCandidate(t *testing.T) {
	if info := lastSessionWorkspaceInfo(agent.LatestSessionWorkspace{}, false, " (recovered)"); info != (LastSessionWorkspaceInfo{}) {
		t.Fatalf("expected zero payload, got %+v", info)
	}
}

// tl1330 改钉（原「Title fallback ends at the transcript file name」）：
// 用户 20261101 报「启动时模型名带一长串」——transcript id
// （<stamp>-<provider>-<model>[-recovery-<hash>]）是内部命名，作为 fallback
// 标题读起来就是内部模型标识泄漏。合同改为：custom > topic > 文件名时间戳
// 截取；无可截取时返回空（FE 对空 sessionTitle 整行不渲染，不给猜测值）。
// tl1547 (C2/C3) 再改钉：链中插入 preview 档（与侧栏六档链同位），且恢复
// 分支在任一档胜出时都追加本地化后缀（继承父标题导致两行同名不可分的
// sibling 撞名，与侧栏 sessionDisplayLabelForPath 同机制）。
func TestLatestSessionDisplayTitleFallsBackToFileName(t *testing.T) {
	got := latestSessionDisplayTitle("", "", "", filepath.Join("store", "20261006-100000.123-provider-model.jsonl"), "", false)
	if got != "20261006 10:00" {
		t.Fatalf("title = %q, want the carved stamp label", got)
	}
	if latestSessionDisplayTitle("  ", "", "", "x.jsonl", "", false) != "" {
		t.Fatal("blank custom/topic titles and an unstampable name must fall through to empty (FE omits the line)")
	}
}

// tl1547 (C3): a titleless session shows its first-message preview in the
// sidebar; the reuse-directory source line must read the same instead of
// dropping to the timestamp tier.
func TestLatestSessionDisplayTitleUsesPreviewTier(t *testing.T) {
	got := latestSessionDisplayTitle("", "", "帮我修一下登录页", filepath.Join("store", "20261006-100000.123-provider-model.jsonl"), "", false)
	if got != "帮我修一下登录页" {
		t.Fatalf("title = %q, want the preview tier", got)
	}
}

// tl1547 (C2): a recovered branch inherits the parent's custom/topic titles;
// every winning tier carries the localized suffix so the two rows stay
// tellable apart. A non-recovered label stays suffix-free.
func TestLatestSessionDisplayTitleMarksRecoveredBranch(t *testing.T) {
	const suffix = "（已恢复）"
	if got := latestSessionDisplayTitle("Alpha work", "topic", "", "x.jsonl", suffix, true); got != "Alpha work"+suffix {
		t.Fatalf("custom title = %q, want the recovered suffix", got)
	}
	if got := latestSessionDisplayTitle("", "topic", "", "x.jsonl", suffix, true); got != "topic"+suffix {
		t.Fatalf("topic title = %q, want the recovered suffix", got)
	}
	if got := latestSessionDisplayTitle("", "", "预览文本", "x.jsonl", suffix, true); got != "预览文本"+suffix {
		t.Fatalf("preview tier = %q, want the recovered suffix", got)
	}
	if got := latestSessionDisplayTitle("", "", "", filepath.Join("store", "20261006-100000.123-provider-model.jsonl"), suffix, true); got != "20261006 10:00"+suffix {
		t.Fatalf("stamp tier = %q, want the recovered suffix", got)
	}
	if got := latestSessionDisplayTitle("Alpha work", "", "", "x.jsonl", suffix, false); got != "Alpha work" {
		t.Fatalf("non-recovered label = %q, want no suffix", got)
	}
}
