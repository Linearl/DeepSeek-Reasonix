package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1403 追加面验收（主会话转达两条同族新增）：
//  1. fork/recovery 后 contact_id 必须随 meta 继承到分叉——现场：新11 主 meta
//     有 contact_id，其 recovery 分叉 b26cee 的 meta 无 contact_id（复制元
//     数据不继承身份字段）；
//  2. 发信 from 解析在自身身份缺失时禁止 fallback 到他会话 identity——现场：
//     分叉态发信 from 错落到 ai-novel 分叉的 contact_id，信以错误身份落库。

func Test1403RecoveryForkInheritsContactID(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.jsonl")
	if err := SaveBranchMeta(mainPath, BranchMeta{ID: BranchID(mainPath), ContactID: "sc_main_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta main: %v", err)
	}
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "parent turn"})
	copyPath := filepath.Join(dir, "main-recovery-inherit1234.jsonl")
	meta, err := sess.prepareRecoveryBranchMetaLocked(copyPath, RecoveryBranchOptions{
		OriginalPath: mainPath,
		Reason:       "snapshot conflict",
	}, "preview", 1, "digest", 1, false)
	if err != nil {
		t.Fatalf("prepareRecoveryBranchMetaLocked: %v", err)
	}
	if meta.ContactID != "sc_main_identity" {
		t.Fatalf("fork meta contact_id = %q, want inherited %q", meta.ContactID, "sc_main_identity")
	}
}

// 主会话自身没有地址时分叉不得自铸：铸了就把同一会话的身份永久劈成两半，
// 等主会话随后获得地址时父子各持一个 id。
func Test1403RecoveryForkNeverMintsContactID(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.jsonl")
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "parent turn"})
	copyPath := filepath.Join(dir, "main-recovery-nomint1234.jsonl")
	meta, err := sess.prepareRecoveryBranchMetaLocked(copyPath, RecoveryBranchOptions{
		OriginalPath: mainPath,
		Reason:       "snapshot conflict",
	}, "preview", 1, "digest", 1, false)
	if err != nil {
		t.Fatalf("prepareRecoveryBranchMetaLocked: %v", err)
	}
	if meta.ContactID != "" {
		t.Fatalf("fork meta contact_id = %q, want empty (inherit-only, no mint)", meta.ContactID)
	}
	if _, err := os.Stat(strings.TrimSuffix(copyPath, ".jsonl") + ".meta.json"); !os.IsNotExist(err) {
		t.Fatalf("sidecar must not be written by meta preparation alone: %v", err)
	}
}

// 显式携带的身份优先于继承（宿主已知的身份不被覆盖）。
func Test1403RecoveryForkKeepsExplicitContactID(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.jsonl")
	if err := SaveBranchMeta(mainPath, BranchMeta{ID: BranchID(mainPath), ContactID: "sc_main_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta main: %v", err)
	}
	sess := NewSession("sys")
	copyPath := filepath.Join(dir, "main-recovery-explicit123.jsonl")
	meta, err := sess.prepareRecoveryBranchMetaLocked(copyPath, RecoveryBranchOptions{
		OriginalPath: mainPath,
		Reason:       "snapshot conflict",
		BranchMeta:   BranchMeta{ContactID: "sc_host_supplied"},
	}, "preview", 1, "digest", 1, false)
	if err != nil {
		t.Fatalf("prepareRecoveryBranchMetaLocked: %v", err)
	}
	if meta.ContactID != "sc_host_supplied" {
		t.Fatalf("fork meta contact_id = %q, want explicit %q", meta.ContactID, "sc_host_supplied")
	}
}

func Test1403SendFromNeverFallsBackToAnotherSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.jsonl")
	otherPath := filepath.Join(dir, "other.jsonl")
	if err := SaveBranchMeta(livePath, BranchMeta{ID: BranchID(livePath), ContactID: "sc_live_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta live: %v", err)
	}
	if err := SaveBranchMeta(otherPath, BranchMeta{ID: BranchID(otherPath), ContactID: "sc_other_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta other: %v", err)
	}
	cfg := SessionCollabConfig{
		// boot 快照带着上一会话（assembly 复用/换道烤入）的身份。
		CurrentSessionPath: otherPath,
		CurrentContactID:   "sc_other_identity",
	}
	cfg.ResolveSessionPath = func() string { return livePath }
	if got := cfg.currentContactID(); got != "sc_live_identity" {
		t.Fatalf("from identity = %q, want the live session's own %q (never the snapshot's %q)", got, "sc_live_identity", "sc_other_identity")
	}

	// 活路径自身无地址：自铸（保证可寻址），绝不落到快照的他者身份。
	nakedPath := filepath.Join(dir, "naked.jsonl")
	cfg2 := SessionCollabConfig{
		CurrentSessionPath: otherPath,
		CurrentContactID:   "sc_other_identity",
	}
	cfg2.ResolveSessionPath = func() string { return nakedPath }
	minted := cfg2.currentContactID()
	if minted == "" || minted == "sc_other_identity" {
		t.Fatalf("from identity = %q, want a freshly minted own id (not the snapshot's)", minted)
	}
	if got := SessionContactID(nakedPath); got != minted {
		t.Fatalf("minted id %q not persisted on the live session (%q)", minted, got)
	}

	// 无活路径（boot 期未绑定）：快照是唯一线索，原样信任。
	cfg3 := SessionCollabConfig{CurrentContactID: "sc_boot_only"}
	if got := cfg3.currentContactID(); got != "sc_boot_only" {
		t.Fatalf("boot-only snapshot identity = %q, want %q", got, "sc_boot_only")
	}
}

func Test1403ReplyNudgeIdentitySameAuthority(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "nudge-live.jsonl")
	if err := SaveBranchMeta(livePath, BranchMeta{ID: BranchID(livePath), ContactID: "sc_nudge_own"}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	cfg := &CollabReplyNudgeConfig{ContactID: "sc_stale_snapshot"}
	cfg.ResolveSessionPath = func() string { return livePath }
	if got := cfg.contactID(); got != "sc_nudge_own" {
		t.Fatalf("reply-nudge identity = %q, want live %q", got, "sc_nudge_own")
	}
}

// 同路径快照 = 显式身份裁定（越权拒绝语义，channel_tools_test ④ 同款产品
// 规则）：路径互证通过时快照优先于 meta。
func Test1403SamePathSnapshotIsExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "override.jsonl")
	if err := SaveBranchMeta(livePath, BranchMeta{ID: BranchID(livePath), ContactID: "sc_meta_identity"}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	cfg := SessionCollabConfig{
		CurrentSessionPath: livePath,
		CurrentContactID:   "sc_explicit_override",
	}
	if got := cfg.currentContactID(); got != "sc_explicit_override" {
		t.Fatalf("same-path snapshot identity = %q, want the explicit %q", got, "sc_explicit_override")
	}
}
