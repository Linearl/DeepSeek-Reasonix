package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CustomAgentDefinition is a lightweight agent role loaded from
// .reasonix/agent/*.md (task 115). Frontmatter carries identity; the body is
// the system prompt for that role. This is the same "behavior lives in a
// convention directory of md files" pattern as rules and skills.
type CustomAgentDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`  // primary | subagent
	Model       string `json:"model"` // optional provider/model ref
	Tools       string `json:"tools"` // optional comma-separated allowlist
	Body        string `json:"body"`
	Path        string `json:"path"`
}

// LoadCustomAgents scans dir (typically <workspace>/.reasonix/agent) for *.md
// files with YAML-like frontmatter. Missing dir is not an error.
func LoadCustomAgents(dir string) ([]CustomAgentDefinition, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CustomAgentDefinition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		def, ok := parseCustomAgentMarkdown(string(raw), path)
		if ok {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseCustomAgentMarkdown(raw, path string) (CustomAgentDefinition, bool) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	body := raw
	var fm map[string]string
	if strings.HasPrefix(raw, "---\n") {
		end := strings.Index(raw[4:], "\n---")
		if end < 0 {
			return CustomAgentDefinition{}, false
		}
		fmBlock := raw[4 : 4+end]
		body = strings.TrimLeft(raw[4+end+4:], "\n")
		fm = map[string]string{}
		for _, line := range strings.Split(fmBlock, "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			fm[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	name := strings.TrimSpace(fm["name"])
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	mode := strings.ToLower(strings.TrimSpace(fm["mode"]))
	// Only the two dispatch modes are meaningful; a typo falls back to primary
	// instead of becoming a third, unroutable mode.
	if mode != "subagent" {
		mode = "primary"
	}
	return CustomAgentDefinition{
		Name:        name,
		Description: strings.TrimSpace(fm["description"]),
		Mode:        mode,
		Model:       strings.TrimSpace(fm["model"]),
		Tools:       strings.TrimSpace(fm["tools"]),
		Body:        strings.TrimSpace(body),
		Path:        path,
	}, true
}
