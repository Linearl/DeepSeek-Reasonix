package agentimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	toml "github.com/BurntSushi/toml"

	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/frontmatter"

	"reasonix/internal/config"
)

func runtimeGOOS() string { return runtime.GOOS }

// frontmatterSplit re-exports the permissive frontmatter parser the rest of
// Reasonix uses, so command/skill parsing here agrees with what will load
// after import.
func frontmatterSplit(content string) (map[string]string, string) {
	return frontmatter.Split(content)
}

func frontmatterEscapeValue(value string) string {
	if strings.ContainsAny(value, ":#[]{}\"'\n") {
		return strconv.Quote(value)
	}
	return value
}

// commandRelPath maps a Reasonix command name back to a relative file path
// (":" namespaces become subdirectories) — the exact inverse of
// commandNameFromPath.
func commandRelPath(name string) string {
	return strings.ReplaceAll(filepath.ToSlash(name), ":", "/") + ".md"
}

// --- skills ---

// skillDirItems scans one skills root: directory-layout skills
// (<name>/SKILL.md) and legacy flat skills (<name>.md). The returned items
// keep the whole SKILL.md content as Preview; the import copies the entire
// source directory (SKILL.md plus references and other sibling files).
func scanSkillDirItems(tool Tool, root string, native bool) []Item {
	var items []Item
	entries, err := os.ReadDir(root)
	if err != nil {
		return items
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		full := filepath.Join(root, name)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		switch {
		case info.IsDir():
			skillPath := filepath.Join(full, "SKILL.md")
			raw, err := fileencoding.ReadFileUTF8(skillPath)
			if err != nil {
				continue // a directory without SKILL.md is not a skill
			}
			content := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), string(rune(0xFEFF)))
			fm, _ := frontmatterSplit(content)
			items = append(items, Item{
				ID:             itemID(tool, KindSkill, name),
				Tool:           tool,
				Kind:           KindSkill,
				Name:           name,
				Description:    strings.TrimSpace(fm["description"]),
				SourcePath:     skillPath,
				Preview:        content,
				NativelyActive: native,
				Recommended:    true,
			})
		case info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(name), ".md") && !strings.EqualFold(name, "SKILL.md") && !strings.EqualFold(name, "SKILLS-REMOVED.md"):
			// Legacy flat skill (or a stray top-level note file named like
			// SKILLS-REMOVED.md — excluded explicitly).
			raw, err := fileencoding.ReadFileUTF8(full)
			if err != nil {
				continue
			}
			content := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), string(rune(0xFEFF)))
			fm, _ := frontmatterSplit(content)
			base := strings.TrimSuffix(name, ".md")
			items = append(items, Item{
				ID:             itemID(tool, KindSkill, base),
				Tool:           tool,
				Kind:           KindSkill,
				Name:           base,
				Description:    strings.TrimSpace(fm["description"]),
				SourcePath:     full,
				Preview:        content,
				NativelyActive: native,
				Recommended:    true,
			})
		}
	}
	return items
}

// --- MCP converters ---

// mcpEntryItem wraps one converted PluginEntry as an import item.
func mcpEntryItem(tool Tool, name string, entry config.PluginEntry, disabled bool) Item {
	e := entry
	e.Name = name
	item := Item{
		ID:          itemID(tool, KindMCP, name),
		Tool:        tool,
		Kind:        KindMCP,
		Name:        name,
		Description: mcpSummary(e),
		Preview:     mcpPreview(e),
		Entry:       &e,
		Recommended: true,
	}
	if disabled {
		item.Recommended = false
		item.Notes = append(item.Notes, "disabled in source")
	}
	return item
}

func mcpSummary(e config.PluginEntry) string {
	switch strings.ToLower(strings.TrimSpace(e.Type)) {
	case "http", "sse":
		return "URL: " + e.URL
	default:
		return strings.TrimSpace(e.Command + " " + strings.Join(e.Args, " "))
	}
}

func mcpPreview(e config.PluginEntry) string {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"name\": %s,\n", jsonString(e.Name))
	if t := strings.TrimSpace(e.Type); t != "" && !strings.EqualFold(t, "stdio") {
		fmt.Fprintf(&b, "  \"type\": %s,\n", jsonString(t))
	}
	if e.Command != "" {
		fmt.Fprintf(&b, "  \"command\": %s,\n", jsonString(e.Command))
	}
	if len(e.Args) > 0 {
		b.WriteString("  \"args\": [")
		for i, a := range e.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(jsonString(a))
		}
		b.WriteString("],\n")
	}
	if e.URL != "" {
		fmt.Fprintf(&b, "  \"url\": %s,\n", jsonString(e.URL))
	}
	if len(e.Env) > 0 {
		b.WriteString("  \"env\": {")
		first := true
		for k := range e.Env {
			if first {
				first = false
			} else {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, " %s: <value>", jsonString(k))
		}
		b.WriteString(" },\n")
	}
	if len(e.Headers) > 0 {
		b.WriteString("  \"headers\": {")
		first := true
		for k := range e.Headers {
			if first {
				first = false
			} else {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, " %s: <value>", jsonString(k))
		}
		b.WriteString(" },\n")
	}
	out := strings.TrimSuffix(b.String(), ",\n") + "\n}"
	return out
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- Claude Code MCP (~/.claude.json, top-level mcpServers) ---

type claudeMCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// claudeMCPItems reads the user-scope mcpServers map from ~/.claude.json. The
// file also carries conversation history, so decoding walks only the top-level
// object's mcpServers key.
func claudeMCPItems(home string) ([]Item, string) {
	path := filepath.Join(home, ".claude.json")
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, ""
	}
	var top struct {
		MCPServers map[string]claudeMCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, "" // a malformed file is the tool's own problem; skip
	}
	names := make([]string, 0, len(top.MCPServers))
	for name := range top.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]Item, 0, len(names))
	for _, name := range names {
		s := top.MCPServers[name]
		entry := pluginEntryFromClaude(s)
		items = append(items, mcpEntryItem(ToolClaude, name, entry, false))
	}
	return items, path
}

func pluginEntryFromClaude(s claudeMCPServer) config.PluginEntry {
	entry := config.PluginEntry{
		Type:    strings.TrimSpace(s.Type),
		Command: strings.TrimSpace(s.Command),
		Args:    append([]string(nil), s.Args...),
		Env:     cloneStrMap(s.Env),
		URL:     strings.TrimSpace(s.URL),
		Headers: cloneStrMap(s.Headers),
	}
	return normalizeMCPEntry(entry)
}

// --- Codex MCP (~/.codex/config.toml, [mcp_servers.*]) ---

type codexMCPServer struct {
	Type              string            `toml:"type"`
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env"`
	URL               string            `toml:"url"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec"`
	ToolTimeoutSec    int               `toml:"tool_timeout_sec"`
	Enabled           *bool             `toml:"enabled"`
}

// codexMCPItems reads [mcp_servers.<name>] tables (plus their nested env
// sub-tables) from ~/.codex/config.toml.
func codexMCPItems(codexDir string) ([]Item, string) {
	path := filepath.Join(codexDir, "config.toml")
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, ""
	}
	var doc struct {
		MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil, ""
	}
	if len(doc.MCPServers) == 0 {
		return nil, ""
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]Item, 0, len(names))
	for _, name := range names {
		s := doc.MCPServers[name]
		entry := config.PluginEntry{
			Type:                  strings.TrimSpace(s.Type),
			Command:               strings.TrimSpace(s.Command),
			Args:                  append([]string(nil), s.Args...),
			Env:                   cloneStrMap(s.Env),
			URL:                   strings.TrimSpace(s.URL),
			StartupTimeoutSeconds: s.StartupTimeoutSec,
		}
		if s.ToolTimeoutSec > 0 {
			entry.CallTimeoutSeconds = s.ToolTimeoutSec
		}
		disabled := s.Enabled != nil && !*s.Enabled
		items = append(items, mcpEntryItem(ToolCodex, name, normalizeMCPEntry(entry), disabled))
	}
	return items, path
}

// --- OpenCode MCP (opencode.json / opencode.jsonc, "mcp" key) ---

type openCodeMCPServer struct {
	Type        string            `json:"type"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Enabled     *bool             `json:"enabled"`
}

// openCodeMCPItems reads the "mcp" map from opencode.json, falling back to
// opencode.jsonc (comments stripped, string-aware) when plain JSON fails.
func openCodeMCPItems(base string) ([]Item, string) {
	primary := filepath.Join(base, "opencode.json")
	if items, path := openCodeMCPFromFile(primary, false); path != "" {
		return items, path
	}
	alt := filepath.Join(base, "opencode.jsonc")
	return openCodeMCPFromFile(alt, true)
}

func openCodeMCPFromFile(path string, stripComments bool) ([]Item, string) {
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return nil, ""
	}
	content := string(raw)
	if stripComments {
		content = stripJSONComments(content)
	}
	var top struct {
		MCP map[string]openCodeMCPServer `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return nil, ""
	}
	names := make([]string, 0, len(top.MCP))
	for name := range top.MCP {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]Item, 0, len(names))
	for _, name := range names {
		s := top.MCP[name]
		entry := config.PluginEntry{}
		if len(s.Command) > 0 {
			entry.Command = strings.TrimSpace(s.Command[0])
			entry.Args = append([]string(nil), s.Command[1:]...)
		}
		entry.URL = strings.TrimSpace(s.URL)
		entry.Env = cloneStrMap(s.Environment)
		entry.Headers = cloneStrMap(s.Headers)
		disabled := s.Enabled != nil && !*s.Enabled
		items = append(items, mcpEntryItem(ToolOpenCode, name, normalizeMCPEntry(entry), disabled))
	}
	return items, path
}

// stripJSONComments removes // and /* */ comments outside of JSON strings —
// just enough tolerance for opencode.jsonc without pulling a JSONC dependency.
func stripJSONComments(s string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// --- shared helpers ---

// normalizeMCPEntry applies Reasonix's transport defaults: an explicit http/sse
// type wins; a URL-only entry is http; everything else is stdio. Keeps the
// converted entries valid config.PluginEntry shapes without pulling in the
// desktop form helpers.
func normalizeMCPEntry(entry config.PluginEntry) config.PluginEntry {
	t := strings.ToLower(strings.TrimSpace(entry.Type))
	entry.Command = strings.TrimSpace(entry.Command)
	entry.URL = strings.TrimSpace(entry.URL)
	switch {
	case t == "http" || t == "sse" || t == "streamable-http":
		if t == "streamable-http" {
			t = "http"
		}
		entry.Type = t
		entry.Command = ""
		entry.Args = nil
	case entry.URL != "" && entry.Command == "":
		entry.Type = "http"
	default:
		entry.Type = "stdio"
	}
	return entry
}

func cloneStrMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
