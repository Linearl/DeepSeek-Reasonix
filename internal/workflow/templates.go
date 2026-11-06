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

// tl1454 —— 模板编辑面（桌面模板视图 + 桥的存储侧）。同一份 store、同一份
// schema 契约；本文件只加三件事：覆盖前历史归档、无效文件可见列表、并发
// 覆盖保护。开关关闭时不归档（存储行为与 tl1454 之前逐字节一致）。
//
// 版本号 = 内容指纹前 12 位（defFingerprint 的派生投影，不新增第二真相源）；
// run 登记早已携带 defFingerprint，「这次运行跑的是哪一版」由此可追溯。

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
type NodeContract struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Inputs  string `json:"inputs,omitempty"`  // 引用的上游节点/字段
	Outputs string `json:"outputs,omitempty"` // 声明的 typed result 字段
	Failure string `json:"failure"`           // 该类型的失败语义
	Limits  string `json:"limits,omitempty"`  // timeout / max_steps / loop
}

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
			c.Failure = "子代理报错 ⇒ 整跑停止（此前发现保留）；声明字段缺失仅提示"
			c.Limits = describeLimits(n.Timeout, n.MaxSteps, "")
		case NodeGate:
			c.Failure = "exit≠0 是分支值（pass/fail）；无 fail 承接 ⇒ 整跑停止"
			c.Limits = describeLimits(n.Timeout, 0, gateLoopText(n))
		case NodeConfirm:
			c.Failure = "未复现 ⇒ 发现标 unconfirmed，运行继续（只读）"
			c.Limits = describeLimits(n.Timeout, n.MaxSteps, "")
		case NodeJoin:
			c.Failure = "聚合活跃上游输出，不失败"
		case NodePhase:
			c.Failure = "仅分区标记，不执行"
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
