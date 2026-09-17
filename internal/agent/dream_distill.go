package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"reasonix/internal/memory"
	"reasonix/internal/tool"
)

// DreamDistillConfig carries the host knobs for the task-115 memory curation
// tools. Both tools are experimental and only registered when Enabled is true.
type DreamDistillConfig struct {
	Enabled         bool
	WorkspaceRoot   string
	SessionDir      string
	Memory          memory.Store
	DreamWindowDays int  // default 7
	DistillMinHits  int  // default 3
	DistillDays     int  // default 30
}

func (c DreamDistillConfig) dreamWindow() time.Duration {
	days := c.DreamWindowDays
	if days <= 0 {
		days = 7
	}
	return time.Duration(days) * 24 * time.Hour
}

func (c DreamDistillConfig) distillWindow() time.Duration {
	days := c.DistillDays
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

func (c DreamDistillConfig) distillMinHits() int {
	if c.DistillMinHits <= 0 {
		return 3
	}
	return c.DistillMinHits
}

// sessionUserLine is one scanned user utterance from a project session file.
type sessionUserLine struct {
	Session string
	Text    string
	At      time.Time
}

// sessionToolCall is one tool invocation observed in a project session.
type sessionToolCall struct {
	Session string
	Name    string
	At      time.Time
}

// sessionToolCallRecord is one entry of an assistant message's tool_calls
// array. Transcripts written by this host keep the tool name at the top level
// ({"name":"bash","arguments":{…}}), while OpenAI-shaped payloads nest it under
// function.name ({"function":{"name":"bash","arguments":"…"}}). Both shapes are
// read: task 150 found the scanner looking for a top-level name on the wrong
// role, so live transcripts scanned to zero tool calls and distill nominated
// nothing at all.
type sessionToolCallRecord struct {
	Name         string `json:"name"`
	CapabilityID string `json:"capability_id"`
	Function     struct {
		Name string `json:"name"`
	} `json:"function"`
}

// toolName returns the tool the model invoked, accepting either wire shape. A
// use_capability dispatch is reported under the capability it resolved to
// (mcp-tool:exa/web_search_exa, memory:remember, skill:review) rather than under
// the proxy, because "bash → use_capability" says nothing about the workflow while
// "bash → mcp-tool:exa/web_search_exa" names the step (#task 150).
func (r sessionToolCallRecord) toolName() string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return strings.TrimSpace(r.Function.Name)
	}
	if id := strings.TrimSpace(r.CapabilityID); id != "" {
		return id
	}
	return name
}

// listRecentSessionFiles returns *.jsonl under dir whose mtime is inside window.
func listRecentSessionFiles(dir string, window time.Duration) []string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-window)
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}

// scanSessionJSONL extracts user texts and tool names from one session file.
// Malformed lines are skipped: dream must never fail a curation pass because
// one record is unreadable.
func scanSessionJSONL(path string, cutoff time.Time) (users []sessionUserLine, tools []sessionToolCall) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	base := filepath.Base(path)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dispatched, results []sessionToolCall
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			Role      string                  `json:"role"`
			Content   json.RawMessage         `json:"content"`
			Kind      string                  `json:"kind"`
			Name      string                  `json:"name"`
			ToolCalls []sessionToolCallRecord `json:"tool_calls"`
			TS        string                  `json:"ts"`
			Time      string                  `json:"time"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		at := parseRecordTime(rec.TS, rec.Time)
		if !at.IsZero() && at.Before(cutoff) {
			continue
		}
		switch rec.Role {
		case "user":
			if text := rawContentText(rec.Content); text != "" {
				users = append(users, sessionUserLine{Session: base, Text: text, At: at})
			}
		case "tool":
			// One row per tool result, named at the top level. This is the
			// fallback channel resolved after the scan.
			if name := strings.TrimSpace(rec.Name); name != "" {
				results = append(results, sessionToolCall{Session: base, Name: name, At: at})
			}
		case "assistant", "":
			if len(rec.ToolCalls) > 0 {
				for _, call := range rec.ToolCalls {
					if name := call.toolName(); name != "" {
						dispatched = append(dispatched, sessionToolCall{Session: base, Name: name, At: at})
					}
				}
				continue
			}
			// Older transcripts name the tool on the record itself.
			if rec.Kind == "tool" || rec.Name != "" {
				if name := strings.TrimSpace(rec.Name); name != "" {
					dispatched = append(dispatched, sessionToolCall{Session: base, Name: name, At: at})
				}
			}
		}
	}
	// Both channels describe the same invocations — a transcript persists the
	// assistant dispatch and the tool result for every call — so exactly one is
	// returned. The dispatch array wins because it keeps the model's call order
	// inside a batch; the result rows are the fallback for transcripts that
	// never persisted it. Returning both would double every call and destroy
	// the sequence statistics distill is built on.
	tools = dispatched
	if len(dispatched) == 0 {
		tools = results
	}
	return users, tools
}

func parseRecordTime(values ...string) time.Time {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func rawContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// preferenceMarkers are the host-side "this looks like a durable preference"
// heuristics. They are deliberately conservative: dream only promotes a line
// that both matches a marker and appears in a real user turn.
var preferenceMarkers = []string{
	"prefer", "always ", "never ", "don't ", "do not ", "instead of",
	"from now on", "next time", "remember that", "make sure", "avoid ",
	"must ", "no need to", "use ", "stop ",
	"以后", "不要", "记住", "改成", "换成", "一直", "每次",
	// Constraint markers. The 2026-09-17 dream pass matched 1 line out of 58
	// sessions because the list only held pronouns of instruction -- real
	// preferences read "必须…", "统一…", "避免…" and were all missed
	// (#task 150). Only constraint-shaped words are added: plain statements
	// ("我想…") would turn every request into a "preference".
	"必须", "禁止", "避免", "统一", "一律", "优先", "别用", "下次",
}

func looksLikePreference(text string) bool {
	lower := strings.ToLower(text)
	for _, m := range preferenceMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// slugTrimCutset is stripped from the edges of a slug word.
const slugTrimCutset = ".,!?;:()[]{}\"'`、，。；：！？（）【】《》…—"

// maxSlugRunes caps a slug by runes, not bytes: a Chinese preference would
// otherwise be cut mid-character by a byte split.
const maxSlugRunes = 40

// slugifyDreamFact builds a short, stable, human-readable name fragment for a
// fact. It used to keep only [a-z0-9-], which stripped every CJK rune: a
// Chinese preference became "dream-fact", so unrelated facts collided on one
// name and every later one was silently skipped as a duplicate (#task 150).
// Letters and digits of any script are kept now, and runs of punctuation
// collapse to a single dash.
func slugifyDreamFact(text string) string {
	words := strings.Fields(strings.ToLower(text))
	clean := make([]string, 0, 6)
	for _, w := range words {
		w = strings.Trim(w, slugTrimCutset)
		if w == "" {
			continue
		}
		clean = append(clean, w)
		if len(clean) >= 6 {
			break
		}
	}
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.Join(clean, " ") {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingDash && b.Len() > 0 {
				b.WriteRune('-')
			}
			pendingDash = false
			b.WriteRune(r)
			continue
		}
		pendingDash = true
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "fact"
	}
	if runes := []rune(slug); len(runes) > maxSlugRunes {
		slug = strings.Trim(string(runes[:maxSlugRunes]), "-")
	}
	return slug
}

// dreamFactName is the memory name a dream pass writes for one preference
// line. The slug stays readable (Chinese included); the content hash makes two
// facts that slugify alike land on different names instead of the second being
// skipped as a duplicate.
func dreamFactName(text string) string {
	return "dream-" + slugifyDreamFact(text) + "-" + shortContentHash(text)
}

// shortContentHash is a stable 6-hex-digit digest of a fact's text. Two
// different preferences can still agree on a capped, human-readable slug, and
// the hash keeps them on distinct names instead of one being dropped as a
// duplicate (#task 150). Whitespace is normalised so a re-run of the same
// sentence always hashes the same.
func shortContentHash(text string) string {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(strings.Join(strings.Fields(text), " ")))
	return fmt.Sprintf("%06x", sum.Sum32()&0xffffff)
}

// genericToolNames are the everyday host verbs: reading, running commands,
// editing files, and the task-list bookkeeping around them. A repeated
// sequence made only of these is not a workflow worth packaging. Task 150
// measured the unfiltered nomination list on 30 days of live transcripts:
// 1877 entries passed the hit threshold and the top of the list was
// "bash→bash" (11211 hits) followed by "bash→write_file→bash".
var genericToolNames = map[string]bool{
	"bash": true, "bash_output": true, "kill_shell": true,
	"read_file": true, "write_file": true, "edit_file": true, "multi_edit": true,
	"grep": true, "ls": true, "glob": true, "view_image": true, "wait": true,
	"todo_write": true, "complete_step": true, "update_goal": true, "ask": true,
	"__reasonix_local_only__": true,
	// The capability proxy itself: what it did is carried by the capability id
	// the scanner reports in its place.
	"use_capability": true,
}

// genericCapabilityPrefixes are capability ids that only proxy a tool the host
// already counts as generic (tool:bash, tool:read_file). Everything else -- a
// skill, a memory operation, an MCP tool -- names a step worth distilling.
var genericCapabilityPrefixes = []string{"tool:"}

// isGenericTool reports whether a reported tool or capability is an everyday
// host verb rather than a workflow step.
func isGenericTool(name string) bool {
	if genericToolNames[name] {
		return true
	}
	for _, prefix := range genericCapabilityPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// minDistinctToolsPerSequence rejects a workflow proposal built from one tool
// repeated: a loop is a defect, not a skill.
const minDistinctToolsPerSequence = 2

// workflowLikeSequence reports whether a tool-name sequence is specific enough
// to nominate. It must use at least two distinct tools and at least one tool
// that is not a generic host verb -- a capability proxy, a subagent, a skill
// call or an MCP tool -- because those are what a distilled workflow is made
// of (#task 150).
func workflowLikeSequence(names []string) bool {
	distinct := map[string]bool{}
	specific := false
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		distinct[name] = true
		if !isGenericTool(name) {
			specific = true
		}
	}
	return len(distinct) >= minDistinctToolsPerSequence && specific
}

// DreamReport is what one dream pass produces.
type DreamReport struct {
	WindowDays   int      `json:"windowDays"`
	Sessions     int      `json:"sessions"`
	Candidates   int      `json:"candidates"`
	Saved        int      `json:"saved"`
	SkippedDupe  int      `json:"skippedDuplicate"`
	SavedNames   []string `json:"savedNames,omitempty"`
	SampleLines  []string `json:"sampleLines,omitempty"`
}

// RunDream scans recent project sessions for preference-shaped user lines,
// validates each against the raw trace (it must appear in a user turn), and
// writes new project-scope memories. Existing memories with the same slug are
// left untouched so a re-run does not inflate the index.
func RunDream(cfg DreamDistillConfig) (DreamReport, error) {
	if !cfg.Enabled {
		return DreamReport{}, fmt.Errorf("dream is disabled (enable agent.experimental_dream)")
	}
	if cfg.Memory.Dir == "" && cfg.Memory.GlobalDir == "" {
		return DreamReport{}, fmt.Errorf("memory store unavailable")
	}
	window := cfg.dreamWindow()
	files := listRecentSessionFiles(cfg.SessionDir, window)
	report := DreamReport{WindowDays: int(window.Hours() / 24)}
	existing := map[string]bool{}
	for _, m := range cfg.Memory.List() {
		existing[m.Name] = true
	}
	seen := map[string]bool{}
	for _, path := range files {
		users, _ := scanSessionJSONL(path, time.Now().Add(-window))
		report.Sessions++
		for _, u := range users {
			if !looksLikePreference(u.Text) {
				continue
			}
			// Only consider reasonably short preference lines; a 2KB dump is
			// not a durable fact.
			text := strings.TrimSpace(u.Text)
			if len(text) < 8 || len(text) > 400 {
				continue
			}
			report.Candidates++
			if len(report.SampleLines) < 8 {
				report.SampleLines = append(report.SampleLines, text)
			}
			name := dreamFactName(text)
			if seen[name] || existing[name] {
				report.SkippedDupe++
				continue
			}
			seen[name] = true
			body := fmt.Sprintf("%s\n\n**Why:** extracted by dream from project session %s.\n\n**How to apply:** treat as a user preference unless a later session contradicts it.", text, filepath.Base(u.Session))
			if _, err := cfg.Memory.Save(memory.Memory{
				Name:        name,
				Title:       truncateTitle(text, 48),
				Description: truncateTitle(text, 80),
				Type:        memory.TypeUser,
				Scope:       memory.FactScopeProject,
				Body:        body,
				Keywords:    "dream auto",
			}); err != nil {
				return report, fmt.Errorf("dream save %s: %w", name, err)
			}
			report.Saved++
			report.SavedNames = append(report.SavedNames, name)
		}
	}
	return report, nil
}

func truncateTitle(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// DistillNomination is one proposed skill distilled from a repeated workflow.
type DistillNomination struct {
	Name        string   `json:"name"`
	Tools       []string `json:"tools"`
	Hits        int      `json:"hits"`
	Sessions    []string `json:"sessions,omitempty"`
	Description string   `json:"description"`
}

// DistillReport is what one distill pass produces.
type DistillReport struct {
	WindowDays int `json:"windowDays"`
	Sessions   int `json:"sessions"`
	MinHits    int `json:"minHits"`
	// CandidateSequences counts repeated sequences that passed the workflow
	// filter, GenericFiltered counts the repeated-but-generic ones dropped by
	// it. A pass that nominates nothing still reports whether the window held
	// no tools at all or only generic combinations.
	CandidateSequences int                 `json:"candidateSequences,omitempty"`
	GenericFiltered    int                 `json:"genericFiltered,omitempty"`
	Nominations        []DistillNomination `json:"nominations,omitempty"`
	OutputPath         string              `json:"outputPath,omitempty"`
}

// RunDistill scans recent project sessions for repeated tool-name sequences
// (length 2–5) and writes a nomination file under the project memory dir.
// Nothing is auto-installed: a human reviews the draft and promotes it to a
// real skill.
func RunDistill(cfg DreamDistillConfig) (DistillReport, error) {
	if !cfg.Enabled {
		return DistillReport{}, fmt.Errorf("distill is disabled (enable agent.experimental_dream)")
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		return DistillReport{}, fmt.Errorf("workspace root is required")
	}
	window := cfg.distillWindow()
	minHits := cfg.distillMinHits()
	files := listRecentSessionFiles(cfg.SessionDir, window)
	report := DistillReport{WindowDays: int(window.Hours() / 24), MinHits: minHits}
	type seqKey struct {
		seq string
	}
	counts := map[string]int{}
	sessions := map[string]map[string]bool{}
	// all keeps the pre-filter histogram so one pass can report how much of the
	// repeated material the workflow filter rejected (#task 150).
	all := map[string]int{}
	for _, path := range files {
		_, tools := scanSessionJSONL(path, time.Now().Add(-window))
		report.Sessions++
		if len(tools) < 2 {
			continue
		}
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name)
		}
		for _, n := range []int{2, 3, 4, 5} {
			for i := 0; i+n <= len(names); i++ {
				window := names[i : i+n]
				key := strings.Join(window, "→")
				all[key]++
				if !workflowLikeSequence(window) {
					continue
				}
				counts[key]++
				if sessions[key] == nil {
					sessions[key] = map[string]bool{}
				}
				sessions[key][filepath.Base(path)] = true
			}
		}
	}
	for key, hits := range all {
		if hits < minHits {
			continue
		}
		if counts[key] > 0 {
			report.CandidateSequences++
			continue
		}
		report.GenericFiltered++
	}
	type scored struct {
		seq  string
		hits int
	}
	var ranked []scored
	for seq, hits := range counts {
		if hits >= minHits {
			ranked = append(ranked, scored{seq: seq, hits: hits})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].hits != ranked[j].hits {
			return ranked[i].hits > ranked[j].hits
		}
		return ranked[i].seq < ranked[j].seq
	})
	if len(ranked) > 12 {
		ranked = ranked[:12]
	}
	for _, r := range ranked {
		parts := strings.Split(r.seq, "→")
		name := "distill-" + slugifyDreamFact(strings.Join(parts, "-"))
		var sessionList []string
		for s := range sessions[r.seq] {
			sessionList = append(sessionList, s)
		}
		sort.Strings(sessionList)
		report.Nominations = append(report.Nominations, DistillNomination{
			Name:        name,
			Tools:       parts,
			Hits:        r.hits,
			Sessions:    sessionList,
			Description: fmt.Sprintf("Repeated workflow observed %d times: %s", r.hits, strings.Join(parts, " → ")),
		})
	}
	if len(report.Nominations) == 0 {
		return report, nil
	}
	outDir := filepath.Join(cfg.Memory.Dir, "distill")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return report, err
	}
	outPath := filepath.Join(outDir, time.Now().UTC().Format("20060102-150405")+"-nominations.json")
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(outPath, payload, 0o644); err != nil {
		return report, err
	}
	report.OutputPath = outPath
	return report, nil
}

// NewDreamTool returns the `dream` host tool (task 115). It is only added to
// the registry when the experimental flag is on.
func NewDreamTool(cfg DreamDistillConfig) tool.Tool { return dreamTool{cfg: cfg} }

type dreamTool struct{ cfg DreamDistillConfig }

func (dreamTool) Name() string { return "dream" }

func (dreamTool) Description() string {
	return "Run one dream pass: scan recent project session traces for user preference lines, validate them against the raw user turns, and save new project-scope memories. Experimental. Does not modify code or global memory."
}

func (dreamTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"confirm":{"type":"boolean","description":"Set true to actually write memories. Without it the tool only reports candidates."}},"additionalProperties":false}`)
}

func (dreamTool) ReadOnly() bool { return true }

func (t dreamTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	_ = ctx
	var p struct {
		Confirm bool `json:"confirm"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	if !p.Confirm {
		// Dry-run: scan but do not Save. Reuse RunDream with a store-less
		// config? Simpler: always run, but only Save when confirm — so
		// implement a dry path by temporarily clearing memory write.
		cfg := t.cfg
		cfg.Memory = memory.Store{} // no write target → RunDream errors
		// Instead of erroring, report candidates only via a scan helper.
		return dreamDryRun(t.cfg)
	}
	report, err := RunDream(t.cfg)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(report)
	return string(b), nil
}

func dreamDryRun(cfg DreamDistillConfig) (string, error) {
	window := cfg.dreamWindow()
	files := listRecentSessionFiles(cfg.SessionDir, window)
	existing := map[string]bool{}
	if cfg.Memory.Dir != "" || cfg.Memory.GlobalDir != "" {
		for _, m := range cfg.Memory.List() {
			existing[m.Name] = true
		}
	}
	report := DreamReport{WindowDays: int(window.Hours() / 24)}
	for _, path := range files {
		users, _ := scanSessionJSONL(path, time.Now().Add(-window))
		report.Sessions++
		for _, u := range users {
			if !looksLikePreference(u.Text) {
				continue
			}
			text := strings.TrimSpace(u.Text)
			if len(text) < 8 || len(text) > 400 {
				continue
			}
			report.Candidates++
			name := dreamFactName(text)
			if existing[name] {
				report.SkippedDupe++
			}
			if len(report.SampleLines) < 8 {
				report.SampleLines = append(report.SampleLines, text)
			}
		}
	}
	b, _ := json.Marshal(report)
	return string(b), nil
}

// NewDistillTool returns the `distill` host tool (task 115).
func NewDistillTool(cfg DreamDistillConfig) tool.Tool { return distillTool{cfg: cfg} }

type distillTool struct{ cfg DreamDistillConfig }

func (distillTool) Name() string { return "distill" }

func (distillTool) Description() string {
	return "Run one distill pass: scan recent project sessions for repeated tool-call workflows and write a nomination draft for human review. Experimental. Nominations are not installed automatically."
}

func (distillTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (distillTool) ReadOnly() bool { return true }

func (t distillTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	_ = ctx
	_ = args
	report, err := RunDistill(t.cfg)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(report)
	return string(b), nil
}
