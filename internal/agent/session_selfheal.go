package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1011：会话自愈（GUI 内建）。这是本代码库唯一允许改写历史会话数据的
// 入口：检测层（session_health.go / 任务 1010 的通知）只读，修复动作全部
// 落在这里，且每一步都遵守同一条纪律——先备份、再动手、留修复记录、失败
// 不破坏原件（临时文件 + 原子替换 / CAS 重写）。
//
// 全程不调用任何模型：剥图与折叠都是本地占位（原文在备份与旧日志段），
// 补水物化只是把 events 已有的事实落回 .jsonl 检查点，head 重选追加的是
// 一条 select 标记。

// 自愈动作名（修复记录与 GUI 引用，不翻译）。
const (
	HealActionPromoteHead = "promote_head" // head 重选：切到「更长且更新」链
	HealActionHydrate     = "hydrate"      // 补水物化：events → .jsonl 检查点
	HealActionStripImages = "strip_images" // 剥图：base64 → 占位引用
	HealActionFold        = "fold"         // 折叠：超窗旧消息 → 本地占位
)

// selfHealBackupSuffix 是备份目录的后缀形态：<stem>.pre-repair-<UTC 紧凑时间>。
const selfHealBackupSuffix = ".pre-repair-"

// selfHealRepairLogSuffix 是修复记录文件的后缀：<stem>.repairlog.jsonl。
const selfHealRepairLogSuffix = ".repairlog.jsonl"

// SelfHealOptions picks which repair steps run. Every step is independent and
// idempotent: a step whose fault is absent is reported as skipped.
type SelfHealOptions struct {
	// PromoteHead switches the selection to the strictly longer AND newer
	// live head when the current one is dominated (1010's rotation guard
	// criterion). The old chain stays as a version — the switch is one
	// select marker, so the action is reversible from the versions panel.
	PromoteHead bool
	// Hydrate rewrites the materialized .jsonl checkpoint from the selected
	// events chain, closing the watermark gap for every .jsonl-side reader.
	Hydrate bool
	// Diet strips image payloads and folds over-window old messages into one
	// local placeholder — the pure-local emergency compaction. Only runs when
	// the projection estimate breaches the window.
	Diet bool
	// WindowTokens is the destination window for the overflow judgement;
	// <=0 uses the health default.
	WindowTokens int
}

// SelfHealAction is one executed (or skipped) step, for the report and the
// repair log.
type SelfHealAction struct {
	Action string `json:"action"`
	Status string `json:"status"` // done | skipped | failed
	Detail string `json:"detail,omitempty"`
}

// SelfHealReport is what one repair run produced.
type SelfHealReport struct {
	Path      string          `json:"path"`
	BackupDir string          `json:"backup_dir,omitempty"`
	Actions   []SelfHealAction `json:"actions"`
	HeadFrom  string          `json:"head_from,omitempty"`
	HeadTo    string          `json:"head_to,omitempty"`
	// Healthy is the post-repair re-inspection verdict.
	Healthy     bool                 `json:"healthy"`
	BeforeAfter []SessionHealthIssue `json:"-"`
	// Repaired is false when nothing needed fixing (healthy session) — the
	// caller must surface "no repair needed" and must not have written
	// anything, including the backup directory.
	Repaired bool `json:"repaired"`
}

// repairLogRecord is one line of <stem>.repairlog.jsonl.
type repairLogRecord struct {
	At        string           `json:"at"`
	Path      string           `json:"path"`
	BackupDir string           `json:"backup_dir,omitempty"`
	Actions   []SelfHealAction `json:"actions"`
	Before    string           `json:"before"`
	After     string           `json:"after"`
}

// SelfHealSession runs the chosen repair steps against one closed session
// file. Order follows the field-proven sequence: promote head first (fold
// must operate on the chain the user will actually see), then hydrate (the
// promoted chain lands in the checkpoint), then the diet. An already-healthy
// session returns Repaired=false with zero writes — no backup dir, no log
// line, nothing.
func SelfHealSession(path string, opts SelfHealOptions) (SelfHealReport, error) {
	return SelfHealSessionContext(context.Background(), path, opts)
}

// SelfHealSessionContext is SelfHealSession with a cancellation for the
// replays.
func SelfHealSessionContext(ctx context.Context, path string, opts SelfHealOptions) (SelfHealReport, error) {
	report := SelfHealReport{Path: path, Actions: []SelfHealAction{}}
	path = strings.TrimSpace(path)
	if path == "" {
		return report, fmt.Errorf("self-heal: empty session path")
	}
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return report, fmt.Errorf("self-heal: %w", err)
	}
	if !probe.dag {
		return report, fmt.Errorf("self-heal: %s: %w", filepath.Base(path), ErrSessionNotDAG)
	}

	before := InspectSessionHealthContext(ctx, path, HealthInspectOptions{WindowTokens: opts.WindowTokens})
	if before.Healthy {
		return report, nil
	}
	report.BeforeAfter = before.Issues

	// 第一步永远是备份：后面任何动作落笔之前，原件字节已在备份目录里。
	backupDir, err := backupSessionFiles(path)
	if err != nil {
		return report, fmt.Errorf("self-heal: backup: %w", err)
	}
	report.BackupDir = backupDir
	report.Repaired = true

	// 动作 1：head 重选（更长且更新）。SelectSessionHead 自带双锁与重放，
	// 这里不持外层锁，避免与它的锁序互嵌。
	if opts.PromoteHead && before.SuggestedHeadID != "" {
		if err := SelectSessionHead(path, before.SuggestedHeadID); err != nil {
			report.Actions = append(report.Actions, SelfHealAction{Action: HealActionPromoteHead, Status: "failed", Detail: err.Error()})
		} else {
			report.HeadFrom, report.HeadTo = before.SelectedHeadID, before.SuggestedHeadID
			report.Actions = append(report.Actions, SelfHealAction{
				Action: HealActionPromoteHead, Status: "done",
				Detail: fmt.Sprintf("from=%s to=%s", before.SelectedHeadID, before.SuggestedHeadID),
			})
		}
	} else if opts.PromoteHead {
		report.Actions = append(report.Actions, SelfHealAction{Action: HealActionPromoteHead, Status: "skipped", Detail: "no dominating live head"})
	}

	// 动作 2：补水物化。选中链（可能已被上一步切换）落回 .jsonl 检查点。
	if opts.Hydrate {
		if err := hydrateSessionCheckpoint(ctx, path); err != nil {
			report.Actions = append(report.Actions, SelfHealAction{Action: HealActionHydrate, Status: "failed", Detail: err.Error()})
		} else {
			report.Actions = append(report.Actions, SelfHealAction{Action: HealActionHydrate, Status: "done"})
		}
	}

	// 动作 3：饮食（剥图 → 折叠）。只对仍然超窗的投影执行；执行走
	// Rewrite + SaveRewriteCompact 的 CAS 通路，旧字节保留在备份与旧日志段。
	if opts.Diet {
		acted, detail := dietSessionProjection(ctx, path, opts.WindowTokens)
		if acted == nil {
			report.Actions = append(report.Actions, SelfHealAction{Action: HealActionStripImages, Status: "skipped", Detail: detail})
			report.Actions = append(report.Actions, SelfHealAction{Action: HealActionFold, Status: "skipped", Detail: detail})
		} else {
			report.Actions = append(report.Actions, acted...)
		}
	}

	// 复检：自愈后的读数决定 report.Healthy。
	after := InspectSessionHealthContext(ctx, path, HealthInspectOptions{WindowTokens: opts.WindowTokens})
	report.Healthy = after.Healthy

	// 修复记录：判据读数、动作、前后状态、备份路径，一行一条追加。
	writeRepairLogRecord(path, repairLogRecord{
		At:        time.Now().UTC().Format(time.RFC3339),
		Path:      path,
		BackupDir: backupDir,
		Actions:   report.Actions,
		Before:    issueCodesLine(before),
		After:     issueCodesLine(after),
	})
	return report, nil
}

// backupSessionFiles copies every sibling file sharing the session's stem
// into <stem>.pre-repair-<UTC compact>/ beside it. Only regular files are
// copied; the backup dir itself is excluded by construction (it is a
// directory, and its name carries the suffix).
func backupSessionFiles(path string) (string, error) {
	stem := sessionFileStem(path)
	if stem == "" {
		return "", fmt.Errorf("no stem for %s", path)
	}
	dir := filepath.Dir(path)
	backupDir := filepath.Join(dir, stem+selfHealBackupSuffix+time.Now().UTC().Format("20060102T150405"))
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), stem+".") {
			continue
		}
		src := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := copyFile(src, filepath.Join(backupDir, e.Name())); err != nil {
			return "", err
		}
		copied++
	}
	if copied == 0 {
		return "", fmt.Errorf("no session files found beside %s", filepath.Base(path))
	}
	slog.Info("session: self-heal backup written", "path", path, "backup_dir", backupDir, "files", copied)
	return backupDir, nil
}

// hydrateSessionCheckpoint materializes the selected head's chain into the
// .jsonl checkpoint (tmp file + atomic rename) and refreshes the meta mirror
// so listings and .jsonl-side readers see the promoted watermark. Read path
// is the events log itself — no recovery bolt, no manifest (the v3_only
// storage shape from the incident has neither).
func hydrateSessionCheckpoint(ctx context.Context, path string) error {
	limits := limitsForSessionLog(path, defaultSessionReplayLimits)
	st, err := replaySessionDAG(ctx, store.SessionEventLog(path), limits)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	selected := st.selectedHead()
	msgs, times := st.materialize(selected)
	if len(msgs) == 0 {
		return fmt.Errorf("selected head %s materialized to zero messages", selected)
	}
	if err := writeJSONLCheckpointAtomic(path, msgs, times); err != nil {
		return err
	}
	preview, turns := SessionPreviewFromMessages(msgs)
	if err := UpdateBranchMeta(path, true, func(meta *BranchMeta) error {
		meta.HeadID = selected
		meta.HeadCount = len(st.heads)
		meta.LogSchema = sessionDAGSchemaVersion
		meta.LogGeneration = st.generation
		meta.Turns = turns
		meta.Preview = preview
		return nil
	}); err != nil {
		return fmt.Errorf("meta refresh: %w", err)
	}
	slog.Info("session: self-heal hydrated checkpoint", "path", path, "head", selected, "messages", len(msgs))
	return nil
}

// writeJSONLCheckpointAtomic writes the transcript as the canonical .jsonl
// checkpoint: serialize to <path>.selfheal-tmp in the same directory, then
// rename over the original. A crash before the rename leaves the original
// untouched; a crash after leaves only the tmp file to clean up.
func writeJSONLCheckpointAtomic(path string, msgs []provider.Message, times []time.Time) error {
	tmp := path + ".selfheal-tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for i, m := range msgs {
		if i < len(times) && !times[i].IsZero() {
			m.CreatedAt = times[i].UnixMilli()
		}
		blob, err := json.Marshal(m)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if _, err := w.Write(append(blob, '\n')); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// dietSessionProjection strips embedded images and folds over-window old
// messages into one local placeholder, then persists the dieted transcript
// through the CAS-protected compact rewrite. actions is nil when the
// projection already fits (nothing to do — the caller reports skipped).
func dietSessionProjection(ctx context.Context, path string, windowTokens int) (actions []SelfHealAction, skipDetail string) {
	if windowTokens <= 0 {
		windowTokens = defaultHealthWindowTokens
	}
	s, err := LoadSession(path)
	if err != nil {
		return nil, "load failed: " + err.Error()
	}
	msgs := s.Snapshot()
	tokens := estimateProjectionTokens(msgs)
	if tokens < int(float64(windowTokens)*projectionOverflowRatio) {
		return nil, fmt.Sprintf("projection_tokens=%d window_tokens=%d already fits", tokens, windowTokens)
	}

	imgBytes := projectionImageBytes(msgs)
	dieted := stripMessageImages(msgs)
	tokensAfterStrip := estimateProjectionTokens(dieted)
	acts := []SelfHealAction{}
	if imgBytes > 0 {
		acts = append(acts, SelfHealAction{
			Action: HealActionStripImages, Status: "done",
			Detail: fmt.Sprintf("images_bytes=%d tokens_before=%d tokens_after=%d", imgBytes, tokens, tokensAfterStrip),
		})
	} else {
		acts = append(acts, SelfHealAction{Action: HealActionStripImages, Status: "skipped", Detail: "no embedded images"})
	}

	// 折叠：把保留窗口之外的旧消息折成一条本地占位。预算取窗口的 60%：
	// 估算器偏保守，留出系统提示、工具框架与下一次请求的输出余量。
	budget := int(float64(windowTokens) * 0.6)
	folded, foldedCount := foldProjectionTail(dieted, budget)
	if foldedCount > 0 {
		acts = append(acts, SelfHealAction{
			Action: HealActionFold, Status: "done",
			Detail: fmt.Sprintf("folded=%d tokens_after_fold=%d budget=%d", foldedCount, estimateProjectionTokens(folded), budget),
		})
	} else {
		acts = append(acts, SelfHealAction{Action: HealActionFold, Status: "skipped", Detail: "tail already within budget"})
	}

	if len(acts) == 2 && acts[0].Status == "skipped" && acts[1].Status == "skipped" {
		return nil, "nothing to strip or fold"
	}
	s.Rewrite(folded, "selfheal_diet")
	if err := s.SaveRewriteCompact(path); err != nil {
		for i := range acts {
			acts[i].Status = "failed"
			acts[i].Detail += " rewrite_error=" + err.Error()
		}
		return acts, ""
	}
	return acts, ""
}

// stripMessageImages replaces embedded data-URL payloads with a short local
// placeholder reference. http(s) refs and Files-API ids stay: they are tiny
// and still resolvable. The originals live in the repair backup and in the
// pre-rewrite log segments.
func stripMessageImages(msgs []provider.Message) []provider.Message {
	changed := false
	out := make([]provider.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		images := make([]string, len(out[i].Images))
		strippedAny := false
		for j, img := range out[i].Images {
			if strings.HasPrefix(img, "data:") {
				images[j] = fmt.Sprintf("[image: embedded payload stripped, sha8=%s, original in repair backup]", shortSHA8(img))
				strippedAny = true
			} else {
				images[j] = img
			}
		}
		if strippedAny {
			out[i].Images = images
			changed = true
		}
	}
	if !changed {
		return msgs
	}
	return out
}

// foldProjectionTail keeps the newest messages whose estimated projection
// tokens fit the budget and folds everything older into one local placeholder
// message. No model is called: the placeholder names the count and where the
// originals live (repair backup + pre-rewrite log segments).
func foldProjectionTail(msgs []provider.Message, budgetTokens int) ([]provider.Message, int) {
	if budgetTokens <= 0 || len(msgs) == 0 {
		return msgs, 0
	}
	// Walk from the newest message backwards accumulating tokens; acc always
	// equals estimate(msgs[keep:]). Stop at the first message that no longer
	// fits: everything before keep folds.
	acc := 0
	keep := len(msgs)
	for i := len(msgs) - 1; i >= 1; i-- {
		cost := estimateProjectionTokens(msgs[i : i+1])
		if acc+cost > budgetTokens {
			keep = i + 1
			break
		}
		acc += cost
		keep = i
	}
	if keep <= 1 || keep >= len(msgs) {
		// Either the whole tail fits (nothing to fold) or even the newest
		// message alone busts the budget (nothing safe to fold around).
		return msgs, 0
	}
	foldedCount := keep
	placeholder := provider.Message{
		Role:    provider.RoleUser,
		Content: fmt.Sprintf("[本地折叠] 已省略 %d 条早期消息以恢复会话可用（投影超窗应急，纯本地操作，未调用模型）；原文在修复备份目录与本次改写前的日志段中。", foldedCount),
	}
	out := make([]provider.Message, 0, len(msgs)-keep+2)
	out = append(out, placeholder)
	out = append(out, msgs[keep:]...)
	return out, foldedCount
}

// writeRepairLogRecord appends one JSON line to <stem>.repairlog.jsonl. A
// failed log write must never fail the repair itself — the log is the
// audit trail, not the fix.
func writeRepairLogRecord(path string, rec repairLogRecord) {
	logPath := sessionSiblingPath(path, selfHealRepairLogSuffix)
	if logPath == "" {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("session: self-heal repair log open failed", "path", logPath, "err", err)
		return
	}
	defer f.Close()
	blob, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("session: self-heal repair log marshal failed", "err", err)
		return
	}
	if _, err := f.Write(append(blob, '\n')); err != nil {
		slog.Warn("session: self-heal repair log write failed", "path", logPath, "err", err)
	}
}

// ListSessionRepairRecords reads the repair log of one session, oldest
// first. A missing log is an empty list, not an error — healthy sessions
// never grew one.
func ListSessionRepairRecords(path string) ([]repairLogRecord, error) {
	logPath := sessionSiblingPath(strings.TrimSpace(path), selfHealRepairLogSuffix)
	if logPath == "" {
		return nil, nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []repairLogRecord
	dec := json.NewDecoder(f)
	for {
		var rec repairLogRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func issueCodesLine(r SessionHealthReport) string {
	if r.Healthy {
		return "healthy"
	}
	codes := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		codes = append(codes, issue.Code)
	}
	return strings.Join(codes, ",")
}

// sessionSiblingPath maps a session file onto a sibling sidecar sharing its
// stem: "<dir>/<stem><suffix>". Empty stem yields empty path.
func sessionSiblingPath(path, suffix string) string {
	stem := sessionFileStem(path)
	if stem == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(strings.TrimSpace(path)), stem+suffix)
}

// sessionFileStem strips every known session extension from base name,
// innermost first: "dag.jsonl.meta" and "dag.events.jsonl" both reduce to
// "dag". Unknown suffixes stay, so a foreign file never collapses to "".
func sessionFileStem(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	for {
		stripped := false
		for _, suffix := range []string{
			".repairlog.jsonl", ".events.jsonl.damaged", ".events.jsonl",
			".turns.jsonl", ".context.json", ".recovery.json", ".meta", ".jsonl",
		} {
			if strings.HasSuffix(base, suffix) {
				base = strings.TrimSuffix(base, suffix)
				stripped = true
				break
			}
		}
		if !stripped {
			return base
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// shortSHA8 is the first 8 hex chars of the payload's SHA-256, so a stripped
// image can be matched against its backup copy without storing the payload.
func shortSHA8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
