package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"reasonix/internal/fileutil"
)

// tl1454 —— 模板编辑面的存储侧：覆盖前历史归档、无效文件可见清单、并发覆盖
// 保护（同一 store 同一 schema；开关关不归档）。版本号=defFingerprint 前 12 位
// 的派生投影，不新增第二真相源。

// versionsDirName 是定义目录下的历史侧车目录。
const versionsDirName = "versions"

// maxVersionsPerWorkflow 是每个模板保留的历史 head 数（heartbeat
// maxRunHistory=20 同量级先例）。
const maxVersionsPerWorkflow = 20

// TemplateInvalidFile 是模板清单里的一个无效条目：显示定位错误，绝不静默
// 隐藏（zcode saved-workflows 的 invalid 列表同语义），绝不改写文件。
type TemplateInvalidFile struct {
	File    string `json:"file"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// TemplateSummary 是清单里一个有效模板。
type TemplateSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	Nodes       int    `json:"nodes"`
	Edges       int    `json:"edges"`
	Gates       int    `json:"gates"`
	Subagents   int    `json:"subagents"`
	Confirms    int    `json:"confirms"`
	HasBudget   bool   `json:"hasBudget,omitempty"`
}

// TemplateListResult 是模板清单（有效 + 无效分开，无效的带定位）。
type TemplateListResult struct {
	Templates []TemplateSummary     `json:"templates"`
	Invalid   []TemplateInvalidFile `json:"invalid,omitempty"`
}

// TemplateDiagnostic 是一次校验的定位诊断（Diagnose 的 wire 形态）。
type TemplateDiagnostic struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	NodeID  string `json:"nodeId,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// TemplateValidateResult 是「保存前校验」的回执：ok=false 时诊断 + 逐节点
// 契约摘要为空；ok=true 时 Summary 可预览即将写入的版本。
type TemplateValidateResult struct {
	OK         bool                `json:"ok"`
	Diagnostic *TemplateDiagnostic `json:"diagnostic,omitempty"`
	Summary    *TemplateSummary    `json:"summary,omitempty"`
	Contracts  []NodeContract      `json:"contracts,omitempty"`
}

// NodeContract 是一个节点的契约摘要（tl1454 五项之一在编辑面的呈现）。
// Failure 是失败语义码（文案由前端按语言渲染，服务端不出本地化散文）。
type NodeContract struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Inputs  string `json:"inputs,omitempty"`  // 上游节点（可插值引用的祖先）
	Outputs string `json:"outputs,omitempty"` // 声明的 typed result 字段
	Failure string `json:"failure"`           // ContractFailure* 码
	Limits  string `json:"limits,omitempty"`  // timeout / max_steps / loop
}

// Node failure-semantics codes (the contract column; one per node type).
const (
	ContractFailureAbortRun        = "abort_run"        // subagent: error stops the run, findings kept
	ContractFailureBranchOrAbort   = "branch_or_abort"  // gate: exit≠0 is a branch value; no receiver stops the run
	ContractFailureKeepUnconfirmed = "keep_unconfirmed" // confirm: not reproduced keeps findings unconfirmed, run goes on
	ContractFailureAggregate       = "aggregate"        // join: merges active upstream, never fails
	ContractFailureMarker          = "marker"           // phase: section marker, never executes work
)

// TemplateSaveResult 是保存回执。
type TemplateSaveResult struct {
	Name        string `json:"name"`
	Created     bool   `json:"created"`
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

// TemplateHistoryEntry 是一份历史 head。
type TemplateHistoryEntry struct {
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
	SavedAt     string `json:"savedAt"`
}

// versionOf 把指纹折叠成显示版本号。
func versionOf(fingerprint string) string {
	if len(fingerprint) < 12 {
		return "v·" + fingerprint
	}
	return "v·" + fingerprint[:12]
}

// summarizeTemplate 投影一个模板的清单行。
func summarizeTemplate(wf *Workflow) TemplateSummary {
	fp := defFingerprint(wf)
	sum := TemplateSummary{
		Name:        wf.Name,
		Description: wf.Description,
		Version:     versionOf(fp),
		Fingerprint: fp,
		Nodes:       len(wf.Nodes),
		Edges:       len(wf.Edges),
		HasBudget:   wf.Budget != nil,
	}
	for i := range wf.Nodes {
		switch wf.Nodes[i].Type {
		case NodeGate:
			sum.Gates++
		case NodeSubagent:
			sum.Subagents++
		case NodeConfirm:
			sum.Confirms++
		}
	}
	return sum
}

// nodeContracts 摘要每个节点的契约（输入引用/输出字段/失败语义/限额）。
func nodeContracts(wf *Workflow) []NodeContract {
	index := make(map[string]int, len(wf.Nodes))
	for i := range wf.Nodes {
		index[wf.Nodes[i].ID] = i
	}
	ancestors := transitiveDeps(func() map[string][]string {
		deps, _ := wf.validateEdges(index)
		return deps
	}())
	out := make([]NodeContract, 0, len(wf.Nodes))
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		c := NodeContract{ID: n.ID, Type: n.Type}
		var inputs []string
		for target := range ancestors[n.ID] {
			inputs = append(inputs, target)
		}
		sort.Strings(inputs)
		c.Inputs = strings.Join(inputs, ", ")
		if len(n.Result) > 0 {
			names := make([]string, 0, len(n.Result))
			for _, f := range n.Result {
				names = append(names, f.Name)
			}
			c.Outputs = strings.Join(names, ", ")
		}
		switch n.Type {
		case NodeSubagent:
			c.Failure = ContractFailureAbortRun
			c.Limits = describeLimits(n.Timeout, n.MaxSteps, "")
		case NodeGate:
			c.Failure = ContractFailureBranchOrAbort
			c.Limits = describeLimits(n.Timeout, 0, gateLoopText(n))
		case NodeConfirm:
			c.Failure = ContractFailureKeepUnconfirmed
			c.Limits = describeLimits(n.Timeout, n.MaxSteps, "")
		case NodeJoin:
			c.Failure = ContractFailureAggregate
		case NodePhase:
			c.Failure = ContractFailureMarker
		}
		out = append(out, c)
	}
	return out
}

func describeLimits(timeout string, maxSteps int, extra string) string {
	var parts []string
	if t := strings.TrimSpace(timeout); t != "" {
		parts = append(parts, "timeout "+t)
	}
	if maxSteps > 0 {
		parts = append(parts, fmt.Sprintf("max_steps %d", maxSteps))
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func gateLoopText(n *Node) string {
	if n.Loop == nil {
		return ""
	}
	return fmt.Sprintf("loop fixer=%s ×%d", n.Loop.Fixer, n.Loop.MaxRounds)
}

// templateDiagnostic 把 Diagnose 的结果折叠成 wire 形态。
func templateDiagnostic(d *Diagnostic) *TemplateDiagnostic {
	if d == nil {
		return nil
	}
	return &TemplateDiagnostic{Kind: d.Kind, Message: d.Message, Path: d.Path, NodeID: d.NodeID, Line: d.Line, Column: d.Column}
}

// validateTemplateDoc 校验（不写盘）：定位诊断 + 版本预览 + 契约摘要。
func (s *store) validateTemplateDoc(doc []byte) TemplateValidateResult {
	wf, d := Diagnose(doc)
	if d != nil {
		return TemplateValidateResult{OK: false, Diagnostic: templateDiagnostic(d)}
	}
	sum := summarizeTemplate(wf)
	return TemplateValidateResult{OK: true, Summary: &sum, Contracts: nodeContracts(wf)}
}

// versionsRoot 是某模板的历史目录（slug 与 store.path 同一收敛）。
func (s *store) versionsRoot(name string) string {
	safe, err := fileutil.SanitizeComponent(slug(name))
	if err != nil {
		safe = "unnamed"
	}
	return filepath.Join(s.dir, versionsDirName, safe)
}

// archiveVersion 在覆盖写之前把旧 head 原字节搬进历史目录。读不出来的旧件
// （手改坏、未来版本）同样按原字节归档——修复保存不得静默丢掉用户文件。
// 归档是增强不是门禁：任何失败只跳过，绝不阻断保存。
func (s *store) archiveVersion(name string) {
	if s.dir == "" {
		return
	}
	raw, err := os.ReadFile(s.path(name))
	if err != nil {
		return
	}
	fp := ""
	var wf Workflow
	if json.Unmarshal(raw, &wf) == nil && strings.TrimSpace(wf.Name) != "" {
		fp = defFingerprint(&wf)
	}
	if fp == "" {
		sum := sha256.Sum256(raw)
		fp = hex.EncodeToString(sum[:])
	}
	root := s.versionsRoot(name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(root, fmt.Sprintf("%s-%s.json", stamp, fp[:12]))
	if _, err := os.Stat(dst); err == nil {
		return // 同秒同内容重写：已归档
	}
	_ = os.WriteFile(dst, raw, 0o644)
	s.pruneVersions(name)
}

// exists 报告名字对应的存储文件是否存在（有效与否不论）。
func (s *store) exists(name string) bool {
	if s.mem != nil {
		_, ok := s.mem[slug(name)]
		return ok
	}
	_, err := os.Stat(s.path(name))
	return err == nil
}

var storeFilePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.json$`)

// readFile 读定义目录里一个文件的原文（文件名白名单，绝不跟随路径）。
func (s *store) readFile(file string) (string, error) {
	if s.dir == "" || !storeFilePattern.MatchString(file) {
		return "", fmt.Errorf("workflow template file %q is not a catalog file", file)
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, file))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

var versionFilePattern = regexp.MustCompile(`^(\d{8}-\d{6})-([0-9a-f]{12})\.json$`)

// pruneVersions 只保留最近 maxVersionsPerWorkflow 份（文件名时间戳序）。
func (s *store) pruneVersions(name string) {
	root := s.versionsRoot(name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && versionFilePattern.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for len(files) > maxVersionsPerWorkflow {
		_ = os.Remove(filepath.Join(root, files[0]))
		files = files[1:]
	}
}

// versionHistory 列出某模板的历史 head（新→旧）。
func (s *store) versionHistory(name string) []TemplateHistoryEntry {
	root := s.versionsRoot(name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []TemplateHistoryEntry
	for _, e := range entries {
		m := versionFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		t, err := time.ParseInLocation("20060102-150405", m[1], time.UTC)
		if err != nil {
			continue
		}
		out = append(out, TemplateHistoryEntry{Version: "v·" + m[2], Fingerprint: m[2], SavedAt: t.Format(time.RFC3339)})
	}
	// 文件名时间戳升序枚举 → 反转成新→旧。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// loadVersionRaw 读一份历史 head 的原字节。
func (s *store) loadVersionRaw(name, fingerprint string) ([]byte, error) {
	if len(fingerprint) != 12 {
		return nil, fmt.Errorf("workflow: version fingerprint must be 12 hex chars")
	}
	root := s.versionsRoot(name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("workflow %q: no version history", name)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "-"+fingerprint+".json") {
			return os.ReadFile(filepath.Join(root, e.Name()))
		}
	}
	return nil, fmt.Errorf("workflow %q: version %s not found in history", name, fingerprint)
}

// headVersion 返回目录内当前 head 的指纹（并发覆盖保护的比对基准）。
// 文件缺失或损坏返回空串（首建场景天然匹配）。
func (s *store) headVersion(name string) string {
	raw, err := os.ReadFile(s.path(name))
	if err != nil {
		return ""
	}
	var wf Workflow
	if json.Unmarshal(raw, &wf) != nil || strings.TrimSpace(wf.Name) == "" {
		return ""
	}
	return defFingerprint(&wf)
}

// removeVersionsRoot 删掉某模板的历史目录（tl1300 删除面）。目录不存在=
// 无历史，天然幂等；失败只跳过（与 archiveVersion 同口径：历史维护是增强
// 不是门禁），head 文件的删除结果由调用方裁决。
func (s *store) removeVersionsRoot(name string) {
	if s.dir == "" {
		return
	}
	_ = os.RemoveAll(s.versionsRoot(name))
}

// deleteFile 删掉一个模板（head 文件 + 历史目录，tl1300）。未知名返回
// os.ErrNotExist 包裹错误（与 load 同口径，桥层原样透传）。内存目录同样
// 支持（删键）。
func (s *store) deleteFile(name string) error {
	if s.mem != nil {
		key := slug(name)
		if _, ok := s.mem[key]; !ok {
			return fmt.Errorf("workflow %q: %w", name, os.ErrNotExist)
		}
		delete(s.mem, key)
		return nil
	}
	path := s.path(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("workflow %q: %w", name, os.ErrNotExist)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("workflow %q: remove: %w", name, err)
	}
	s.removeVersionsRoot(name)
	return nil
}

// renameFile 改名一个模板：文档内 name 字段更新 + 存储文件（slug 键）迁移，
// 历史目录随迁（tl1300）。名字折叠同 slug（如 "A B"→"A-B"）= 只改显示名，
// 不动文件；目标 slug 已被别的定义占用则拒绝（ErrExists 同语义），绝不
// 静默覆盖。改 name 会改指纹——interrupted run 的续跑会按既有口径拒绝
// （「定义已变」），这是改名的固有语义，不是缺陷。
func (s *store) renameFile(oldName, newName string) (*Workflow, error) {
	trimmed := strings.TrimSpace(newName)
	if trimmed == "" {
		return nil, fmt.Errorf("workflow: the new name is empty; pass a non-empty name to rename %q", oldName)
	}
	wf, err := s.load(oldName)
	if err != nil {
		return nil, err
	}
	if s.mem != nil {
		oldKey, newKey := slug(oldName), slug(trimmed)
		if newKey != oldKey {
			if _, exists := s.mem[newKey]; exists {
				return nil, ErrExists(trimmed)
			}
			delete(s.mem, oldKey)
		}
		wf.Name = trimmed
		fresh, err := json.MarshalIndent(wf, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("workflow: encode: %w", err)
		}
		s.mem[newKey] = fresh
		return wf, nil
	}
	oldPath := s.path(oldName)
	newPath := s.path(trimmed)
	if newPath != oldPath {
		if _, err := os.Stat(newPath); err == nil {
			return nil, ErrExists(trimmed)
		}
	}
	wf.Name = trimmed
	fresh, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("workflow: encode: %w", err)
	}
	tmp := newPath + ".tmp"
	if err := os.WriteFile(tmp, fresh, 0o644); err != nil {
		return nil, fmt.Errorf("workflow: store: %w", err)
	}
	if err := os.Rename(tmp, newPath); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("workflow: store: %w", err)
	}
	if newPath != oldPath {
		if err := os.Remove(oldPath); err != nil {
			return nil, fmt.Errorf("workflow %q: remove old file after rename: %w", oldName, err)
		}
		// 历史目录随迁：目标已存在（残留目录）则留在原地（孤儿无害，head
		// 已就位）。
		oldRoot, newRoot := s.versionsRoot(oldName), s.versionsRoot(trimmed)
		if _, err := os.Stat(oldRoot); err == nil {
			if _, err := os.Stat(newRoot); err != nil {
				if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err == nil {
					_ = os.Rename(oldRoot, newRoot)
				}
			}
		}
	}
	return wf, nil
}

// invalidFiles 枚举目录里不可用的定义文件（定位错误，供清单显示）。
// 名字保留 slug 文件名——原始 name 可能解不出来（这正是它无效的原因）。
func (s *store) invalidFiles() []TemplateInvalidFile {
	if s.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []TemplateInvalidFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var probe Workflow
		if json.Unmarshal(raw, &probe) == nil && strings.TrimSpace(probe.Name) != "" {
			// 名字解得出 → load() 会给出真正的校验定位；再 Parse 一次取诊断。
			if _, perr := Parse(raw); perr == nil {
				continue // 有效文件，不进 invalid
			} else {
				_, d := Diagnose(raw)
				if d != nil {
					out = append(out, TemplateInvalidFile{
						File: e.Name(), Kind: d.Kind, Message: d.Message, Path: d.Path,
						Line: d.Line, Column: d.Column,
					})
				}
				continue
			}
		}
		// 连 name 都解不出（坏 JSON / 顶层不是对象）。
		_, d := Diagnose(raw)
		inv := TemplateInvalidFile{File: e.Name(), Kind: DiagSyntax}
		if d != nil {
			inv.Kind, inv.Message, inv.Path, inv.Line, inv.Column = d.Kind, d.Message, d.Path, d.Line, d.Column
		} else {
			inv.Message = "unreadable workflow document"
		}
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}
