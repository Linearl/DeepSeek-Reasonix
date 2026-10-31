package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 任务 934 —— 工作流定义存储：agent 工具（workflow_define/list/get）与桌面
// 686 面板共用的目录收口。schema 契约只有一份（本包 Parse/Validate），存储
// 层不引入第二种文档形态：每个工作流一个 canonical JSON 文件（Parse 的结构
// 体 marshal 回写），名字即键。
//
// 位置策略与 heartbeat-tasks.json 同构（config.WorkflowsDir，用户状态目录）：
// 应用级目录而非工作区目录——面板与应用全局视角看同一目录，任何工作区的
// 会话定义的工作流都互相可见。

// fileNamePattern 限制存储文件名（即 name 的 slug 形态）的字符面，防路径
// 分隔符与点文件逃逸；原始 name 仍保留在文档内。
var fileNamePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// slug 把工作流名折叠成存储文件名 stem。原名与 slug 的映射不保证双射
// （"A B" 与 "A-B" 同 slug）——define 对同 slug 冲突要求 overwrite，冲突
// 提示里报原名。
func slug(name string) string {
	s := fileNamePattern.ReplaceAllString(strings.TrimSpace(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "unnamed"
	}
	return s
}

// store 是定义存储。零值不可用，经 newStore 构造；dir 为空 = 不落盘
// （user state dir 无法解析的极端环境），define/list 退化为内存目录。
type store struct {
	dir string
	// mem 在 dir 为空时兜底（按 slug 键）；dir 非空时恒 nil。
	mem map[string][]byte
}

func newStore(dir string) *store {
	if strings.TrimSpace(dir) == "" {
		return &store{mem: map[string][]byte{}}
	}
	return &store{dir: dir}
}

func (s *store) path(name string) string {
	return filepath.Join(s.dir, slug(name)+".json")
}

// save 校验并落盘（或入内存目录）。existing 非空 = 覆盖写，须 overwrite；
// 否则视为创建。返回 (创建与否, 错误)。校验失败的错误原样透传（Parse 的
// 逐字段报错就是 agent 的自纠线索，不再包一层）。
func (s *store) save(doc []byte, overwrite bool) (bool, *Workflow, error) {
	wf, err := Parse(doc)
	if err != nil {
		return false, nil, err
	}
	raw, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return false, nil, fmt.Errorf("workflow: encode: %w", err)
	}
	if s.mem != nil {
		_, exists := s.mem[slug(wf.Name)]
		if exists && !overwrite {
			return false, nil, ErrExists(wf.Name)
		}
		s.mem[slug(wf.Name)] = raw
		return !exists, wf, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return false, nil, fmt.Errorf("workflow: store: %w", err)
	}
	path := s.path(wf.Name)
	_, err = os.Stat(path)
	exists := err == nil
	if exists && !overwrite {
		return false, nil, ErrExists(wf.Name)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return false, nil, fmt.Errorf("workflow: store: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, nil, fmt.Errorf("workflow: store: %w", err)
	}
	return !exists, wf, nil
}

// load 读一个定义并 Parse。未知名返回 os.ErrNotExist 包裹错误（工具层翻译
// 成可读提示）。
func (s *store) load(name string) (*Workflow, error) {
	if s.mem != nil {
		raw, ok := s.mem[slug(name)]
		if !ok {
			return nil, fmt.Errorf("workflow %q: %w", name, os.ErrNotExist)
		}
		return Parse(raw)
	}
	raw, err := os.ReadFile(s.path(name))
	if err != nil {
		return nil, fmt.Errorf("workflow %q: %w", name, err)
	}
	return Parse(raw)
}

// ErrExists 是同名冲突错误；文案给出已存在的名字与下一步（overwrite）。
func ErrExists(name string) error {
	return fmt.Errorf("workflow %q already exists; pass overwrite=true to replace it, or pick another name", name)
}

// list 返回全部可解析定义的 canonical JSON（名字序）。损坏文件（手改坏）
// 跳过不炸目录——单文件损坏不应遮蔽其余目录。
func (s *store) list() [][]byte {
	names := s.names()
	out := make([][]byte, 0, len(names))
	for _, n := range names {
		wf, err := s.load(n)
		if err != nil {
			continue
		}
		raw, err := json.MarshalIndent(wf, "", "  ")
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// names 枚举存储内的全部工作流名（文档内的 name，非 slug；名字序）。
func (s *store) names() []string {
	var names []string
	if s.mem != nil {
		for _, raw := range s.mem {
			var wf Workflow
			if err := json.Unmarshal(raw, &wf); err == nil && wf.Name != "" {
				names = append(names, wf.Name)
			}
		}
		sort.Strings(names)
		return names
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var wf Workflow
		if err := json.Unmarshal(raw, &wf); err != nil || wf.Name == "" {
			continue
		}
		names = append(names, wf.Name)
	}
	sort.Strings(names)
	return names
}
