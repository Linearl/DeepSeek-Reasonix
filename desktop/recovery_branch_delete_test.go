package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncatalog"
)

// 任务 1064：面板手动删除分支的两条后端路径。
//
// 验收（1064 正文）：非当前版本行加删除入口 + 复选框批量选中/批量删除；安
// 全约束①当前选中版本不可选不可删 ②删除走回收语义（可反悔）③修复记录/备
// 份关联引用处理；删除后列表即时刷新。本测试钉住后端面：
//  ① 副本分支删除 = forced 归档（不要求覆盖证明），落进可恢复 .trash；
//  ② 主线转录本体拒绝走副本删除路径（面板只允许删分支）。
//
// tl1280: the schema-2 head-delete path (ForkHead / SelectSessionHead /
// ErrSessionHeadUnknown) left with the write-path slow lane, so the
// current-head refusal leg is retired with an explicit skip below.

func newBranchDeleteApp(t *testing.T) *App {
	t.Helper()
	return &App{tabs: map[string]*WorkspaceTab{}, detachedSessions: map[string]*WorkspaceTab{}}
}

// writeRecoveryCopyFixture drops a recovery-named transcript beside main so the
// forced-archive path has a real file to move.
func writeRecoveryCopyFixture(t *testing.T, dir, mainName, copyName string) (mainPath, copyPath string) {
	t.Helper()
	mainPath = filepath.Join(dir, mainName)
	copyPath = filepath.Join(dir, copyName)
	if err := os.WriteFile(mainPath, []byte(`{"id":"m1","role":"user","content":"hi"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte(`{"id":"c1","role":"user","content":"forked"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return mainPath, copyPath
}

func TestDeleteRecoveryBranchArchivesWithoutCoverageProof(t *testing.T) {
	// sessionDirForPath only trusts known session dirs, so the fixture lives
	// under the app's real global session dir (the same contract the versions
	// panel operates under in production).
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-branch-del-1.jsonl", "sess-branch-del-1-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})
	if !agent.LooksLikeRecoveryFilename(copyPath) {
		t.Fatalf("fixture must look like a recovery copy: %s", copyPath)
	}

	app := newBranchDeleteApp(t)
	if err := app.DeleteRecoveryBranch(copyPath); err != nil {
		t.Fatalf("forced archive of a diverged branch must succeed (the user chose it): %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("archived branch file must be gone from the live directory, stat err=%v", err)
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the main transcript must be untouched: %v", err)
	}
	// Recoverability: the archive lands under the directory's .trash, not deleted.
	trashHits := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(p) == filepath.Base(copyPath) {
			trashHits++
		}
		return nil
	})
	if trashHits == 0 {
		t.Fatal("the discarded branch must remain recoverable somewhere under the session directory")
	}
}

func TestDeleteRecoveryBranchRefusesPlainTranscript(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, _ := writeRecoveryCopyFixture(t, dir, "sess-branch-del-2.jsonl", "sess-branch-del-2-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() { _ = os.Remove(mainPath) })

	app := newBranchDeleteApp(t)
	if err := app.DeleteRecoveryBranch(mainPath); err == nil {
		t.Fatal("deleting the main transcript through the branch path must be refused")
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the main transcript must survive a refused call: %v", err)
	}
}

// tl1280: the schema-2 head-delete fixture (ForkHead multi-head log + the
// current-head refusal) left with the write-path slow lane. ForkHead and
// ErrSessionHeadUnknown are gone, and ListSessionHeads is an always-nil shim,
// so the two-head scenario is unreachable — retired with an explicit skip
// (no silent SKIP). The removed 1064 contract (current head not deletable,
// retired head reads back retired, unknown head refused) is documented in the
// skip reason; the file-level paths above still pin the live surface.
func TestDeleteSessionVersionHeadRefusesSelectedHead(t *testing.T) {
	t.Skip("tl1280 DAG 写路径慢车道删除: 1064 选中头删除约束依赖 ForkHead/SelectSessionHead/ErrSessionHeadUnknown，均随慢车道退役；ListSessionHeads 垫片恒空，显式退役非静默")
}

// tl1414：删除链的每条路径必须留痕（desktop.log 可观测性）。
//
// 事故形态（2026-11-05 用户实测）：面板副本行点删除→确认→静默无反应，
// desktop.log 零 delete-recovery-branch 痕迹——lockRuntimeMutation 与 busy
// 拒全不落日志，事后无法区分「前端没调用」还是「后端拒绝」。真机证据钉死
// 断点：活 tab 自己的转录本以副本行出现在面板（recovery-8a3a…jsonl 正被
// 每 8 秒 save），点删它 → sessionOpenInAnyTab 拒 → 全链路零日志零 toast。
// 本测试钉三条拒绝路径与成功路径各留一行日志，拒绝原因（reason）可读。
func TestDeleteRecoveryBranchEveryPathLeavesALogTrace(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	capture := func(t *testing.T, run func(app *App)) string {
		t.Helper()
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		t.Cleanup(func() { slog.SetDefault(prev) })
		run(newBranchDeleteApp(t))
		return buf.String()
	}

	// ① 活 tab 占用拒（本次事故断点）：副本被任一 tab 持有时拒绝且留痕。
	{
		mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-branch-del-3.jsonl", "sess-branch-del-3-recovery-0123456789abcdef.jsonl")
		t.Cleanup(func() { _ = os.Remove(mainPath) })
		logs := capture(t, func(app *App) {
			app.mu.Lock()
			app.tabs = map[string]*WorkspaceTab{"tab": {SessionPath: copyPath}}
			app.mu.Unlock()
			if err := app.DeleteRecoveryBranch(copyPath); err == nil {
				t.Fatal("deleting a copy that is open in a tab must be refused")
			}
			if _, err := os.Stat(copyPath); err != nil {
				t.Fatalf("the refused copy must survive: %v", err)
			}
		})
		for _, want := range []string{"recovery branch delete refused", "session open in a tab"} {
			if !strings.Contains(logs, want) {
				t.Fatalf("busy refusal must leave a trace naming the reason %q; log:\n%s", want, logs)
			}
		}
	}

	// ② 非 recovery 文件名拒：主线走副本删除入口被拒且留痕。tl1415h 刀③后
	// 该闸改名降级主线通道：无 catalog（无法证明组内+权威指别处）仍拒，reason
	// 携带通道判定原因。
	{
		mainPath, _ := writeRecoveryCopyFixture(t, dir, "sess-branch-del-4.jsonl", "sess-branch-del-4-recovery-0123456789abcdef.jsonl")
		t.Cleanup(func() { _ = os.Remove(mainPath) })
		logs := capture(t, func(app *App) {
			if err := app.DeleteRecoveryBranch(mainPath); err == nil {
				t.Fatal("deleting the main transcript through the branch path must be refused")
			}
		})
		for _, want := range []string{"recovery branch delete refused", "not a recovery copy: no session catalog"} {
			if !strings.Contains(logs, want) {
				t.Fatalf("filename-gate refusal must leave a trace naming the reason %q; log:\n%s", want, logs)
			}
		}
	}

	// ③ 未知目录拒：面板之外的任意路径被拒且留痕。
	{
		logs := capture(t, func(app *App) {
			if err := app.DeleteRecoveryBranch(filepath.Join(t.TempDir(), "x-recovery-0123456789abcdef.jsonl")); err == nil {
				t.Fatal("a path outside known session dirs must be refused")
			}
		})
		for _, want := range []string{"recovery branch delete refused", "path outside known session dirs"} {
			if !strings.Contains(logs, want) {
				t.Fatalf("dir-gate refusal must leave a trace naming the reason %q; log:\n%s", want, logs)
			}
		}
	}

	// ④ 成功路径留痕：文件归档成功必须有「deleted」一行，事后可对账。
	{
		mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-branch-del-5.jsonl", "sess-branch-del-5-recovery-0123456789abcdef.jsonl")
		t.Cleanup(func() { _ = os.Remove(mainPath) })
		logs := capture(t, func(app *App) {
			if err := app.DeleteRecoveryBranch(copyPath); err != nil {
				t.Fatalf("forced archive must succeed: %v", err)
			}
		})
		if !strings.Contains(logs, "recovery branch deleted") {
			t.Fatalf("a successful delete must leave a trace; log:\n%s", logs)
		}
	}
}

// ── tl1415h 刀③：删除入口与守卫形态对齐 ──
//
// 用户现场（2026-11-08）：tl1415f 把「采纳后被降级的扫描起点主线」（普通命
// 名、投影 normal 行）纳入面板「清理非主线副本」入口后，点到该行即拒——
// 「not a recovery copy: only discarded branches may be deleted here」。入口
// 放行的行形态（版本区文件形行）与守卫认的形态（-recovery- 命名）脱节。
// 裁定：守卫扩认（降级主线通道），入口不动。四枚钉：
//  ① 降级主线（组内+权威#3 指副本）可删——同一条 .trash 可恢复协议；
//  ② 现任主线（权威=自己）拒 errSessionVersionNotDeletable（下一步明确）；
//  ③ 无显式采纳证据（组在、无旗标无 filing）拒——防线②；
//  ④ catalog 开着但路径未入册（组未建）仍拒——防线①的窗口面。

// writeDemotedLineageFixture 落一份主线+副本+可选旗标位的族 fixture，并按
// 暖机形态 reconcile 进测试 catalog（组分类完成）。flagCopy=true 时旗标落在
// 副本（用户采纳副本的取证现场形态）；false 时无旗标（无显式采纳证据）。
func writeDemotedLineageFixture(t *testing.T, stem, topicID string, flagCopy bool) (mainPath, copyPath string, catalog *sessioncatalog.Catalog) {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath = filepath.Join(dir, stem+".jsonl")
	copyPath = filepath.Join(dir, stem+"-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, copyPath, mainPath + ".meta", copyPath + ".meta"} {
			_ = os.Remove(p)
		}
	})
	saveVerdictFixture(t, mainPath, 3, topicID)
	saveVerdictFixture(t, copyPath, 1, topicID)
	if err := agent.UpdateBranchMeta(copyPath, false, func(meta *agent.BranchMeta) error {
		meta.Recovered = true
		meta.ParentID = agent.BranchID(mainPath)
		if flagCopy {
			meta.RecoveryPreferred = true
		}
		return nil
	}); err != nil {
		t.Fatalf("stamp copy meta: %v", err)
	}
	catalog = openVerdictWarmCatalog(t, dir)
	return mainPath, copyPath, catalog
}

// ① 降级主线可删：用户采纳副本（旗标）后，被降级的原主线（普通命名文件）
// 走分支删除入口成功归档——「not a recovery copy」拒绝消失，且仍是可恢复
// 的 .trash 归档，不是硬删。
func TestDeleteRecoveryBranchAllowsDemotedMainlineOfAdoptedLineage(t *testing.T) {
	mainPath, copyPath, catalog := writeDemotedLineageFixture(t, "demoted-1415h-adopted", "topic_demoted_1415h", true)
	app := newBranchDeleteApp(t)
	app.sessionCatalog.Store(catalog)

	logs := captureVerdictLogs(t, func() {
		if err := app.DeleteRecoveryBranch(mainPath); err != nil {
			t.Fatalf("the demoted mainline of an adopted lineage must be deletable (tl1415f 入口承诺): %v", err)
		}
	})
	if _, err := os.Stat(mainPath); !os.IsNotExist(err) {
		t.Fatalf("the demoted mainline must be gone from the live directory, stat err=%v", err)
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the adopted copy (current main) must be untouched: %v", err)
	}
	trashHits := 0
	_ = filepath.Walk(config.SessionDir(), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(p) == filepath.Base(mainPath) {
			trashHits++
		}
		return nil
	})
	if trashHits == 0 {
		t.Fatal("the demoted mainline must remain recoverable somewhere under the session directory")
	}
	if !strings.Contains(logs, "admits demoted mainline") {
		t.Fatalf("the demoted-mainline admission must leave a trace; log:\n%s", logs)
	}
}

// ② 现任主线拒：权威=自己（旗标在自己身上）时给 errSessionVersionNotDeletable
// ——下一步明确（先切换到别的版本），不是模糊的形态拒。
func TestDeleteRecoveryBranchRefusesCurrentMainline(t *testing.T) {
	mainPath, _, catalog := writeDemotedLineageFixture(t, "demoted-1415h-current", "topic_demoted_1415h_current", false)
	// 旗标落在主线上=权威#3 命中主线自己：它是现任主线，不是降级行。
	if err := agent.UpdateBranchMeta(mainPath, false, func(meta *agent.BranchMeta) error {
		meta.RecoveryPreferred = true
		return nil
	}); err != nil {
		t.Fatalf("stamp mainline flag: %v", err)
	}
	app := newBranchDeleteApp(t)
	app.sessionCatalog.Store(catalog)

	if err := app.DeleteRecoveryBranch(mainPath); !errors.Is(err, errSessionVersionNotDeletable) {
		t.Fatalf("the current mainline must be refused with errSessionVersionNotDeletable, got %v", err)
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the refused mainline must survive: %v", err)
	}
}

// ③ 无显式采纳证据拒：组在（副本 recovered 入组）但无旗标无 filing——权威
// 空，删除拒绝（防线②：没有「用户已选了别的成员」的证据不删主线文件）。
func TestDeleteRecoveryBranchRefusesUnprovenLineageMainline(t *testing.T) {
	mainPath, _, catalog := writeDemotedLineageFixture(t, "demoted-1415h-unproven", "topic_demoted_1415h_unproven", false)
	app := newBranchDeleteApp(t)
	app.sessionCatalog.Store(catalog)

	if err := app.DeleteRecoveryBranch(mainPath); err == nil {
		t.Fatal("a lineage mainline with no explicit adoption evidence must be refused")
	}
	if _, err := os.Stat(mainPath); err != nil {
		t.Fatalf("the refused mainline must survive: %v", err)
	}
}

// ④ catalog 开着但路径未入册（目录未 reconcile、组未建）仍拒——删除入口不
// 做窗口 reconcile（那是打开漏斗的刀②），冷机形态保守拒绝。
func TestDeleteRecoveryBranchRefusesUnfiledPathEvenWithCatalog(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "demoted-1415h-unfiled.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, mainPath + ".meta"} {
			_ = os.Remove(p)
		}
	})
	saveVerdictFixture(t, mainPath, 1, "topic_demoted_1415h_unfiled")

	app := openVerdictApp(t, openVerdictColdCatalog(t))

	logs := captureVerdictLogs(t, func() {
		if err := app.DeleteRecoveryBranch(mainPath); err == nil {
			t.Fatal("an unfiled path must stay refused even with a live catalog")
		}
	})
	if !strings.Contains(logs, "not a recovery copy: path is not filed") {
		t.Fatalf("the unfiled refusal must name the channel reason; log:\n%s", logs)
	}
}
