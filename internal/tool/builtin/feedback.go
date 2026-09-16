package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(submitFeedback{}) }

// submitFeedback (tasks 121/140) records a product note as a markdown file
// under ReasonixHome/feedback-inbox (env REASONIX_FEEDBACK_INBOX overrides).
type submitFeedback struct{}

func (submitFeedback) Name() string { return "submit_feedback" }

func (submitFeedback) Description() string {
	return "Submit a short product feedback note into the local feedback inbox (markdown files under the Reasonix feedback-inbox directory). Use it when the user asks you to record a suggestion, bug report, or opinion about Reasonix itself (not about their code). Notes stay on this machine and appear in the desktop feedback panel when that experimental feature is enabled. Keep each note focused: one observation or suggestion per call."
}

func (submitFeedback) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "kind":{"type":"string","enum":["bug","idea","praise","other"],"description":"bug = something is wrong; idea = a suggestion; praise = what worked well; other = anything else."},
  "text":{"type":"string","minLength":1,"maxLength":4000,"description":"The feedback itself. Be concrete: what happened, what you expected, or what you suggest."},
  "title":{"type":"string","maxLength":120,"description":"Optional short title; defaults to the first line of text."},
  "tags":{"type":"array","items":{"type":"string","maxLength":40},"maxItems":8,"description":"Optional short labels, for example ['startup','mcp','ui']."}
},
"required":["kind","text"]
}`)
}

func (submitFeedback) ReadOnly() bool     { return true }
func (submitFeedback) PlanModeSafe() bool { return true }

// FeedbackEntry is the parsed view of one inbox markdown file.
type FeedbackEntry struct {
	At      string   `json:"at"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title,omitempty"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
	Session string   `json:"session,omitempty"`
	Model   string   `json:"model,omitempty"`
}

var (
	feedbackMu     sync.Mutex
	feedbackHomeMu sync.RWMutex
	feedbackHome   string
)

// SetFeedbackHome overrides the inbox directory (tests only).
func SetFeedbackHome(dir string) {
	feedbackHomeMu.Lock()
	defer feedbackHomeMu.Unlock()
	feedbackHome = dir
}

func feedbackDir() string {
	feedbackHomeMu.RLock()
	override := feedbackHome
	feedbackHomeMu.RUnlock()
	if strings.TrimSpace(override) != "" {
		return override
	}
	if env := strings.TrimSpace(os.Getenv("REASONIX_FEEDBACK_INBOX")); env != "" {
		return env
	}
	return filepath.Join(config.ReasonixHomeDir(), "feedback-inbox")
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func feedbackSlug(text string) string {
	s := strings.ToLower(text)
	if len(s) > 40 {
		s = s[:40]
	}
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "note"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

func writeFeedbackMD(dir string, entry FeedbackEntry) (string, error) {
	at := entry.At
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	ts := time.Now().UTC().Format("20060102-150405")
	if parsed, err := time.Parse(time.RFC3339, at); err == nil {
		ts = parsed.UTC().Format("20060102-150405")
	}
	title := strings.TrimSpace(entry.Title)
	if title == "" {
		title = firstLine(entry.Text)
	}
	slug := feedbackSlug(title)
	path := filepath.Join(dir, fmt.Sprintf("feedback-%s-%s.md", ts, slug))
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(dir, fmt.Sprintf("feedback-%s-%s-%d.md", ts, slug, i))
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "at: %s\n", at)
	fmt.Fprintf(&b, "category: %s\n", entry.Kind)
	if entry.Session != "" {
		fmt.Fprintf(&b, "session: %s\n", entry.Session)
	}
	if entry.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", entry.Model)
	}
	if len(entry.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(entry.Tags, ", "))
	}
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString(entry.Text)
	if !strings.HasSuffix(entry.Text, "\n") {
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func parseFeedbackMD(raw string) FeedbackEntry {
	entry := FeedbackEntry{}
	body := raw
	if strings.HasPrefix(raw, "---\n") {
		if i := strings.Index(raw[4:], "\n---"); i >= 0 {
			front := raw[4 : 4+i]
			body = raw[4+i+4:]
			for _, line := range strings.Split(front, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				key, val, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				val = strings.TrimSpace(val)
				switch key {
				case "at":
					entry.At = val
				case "category":
					entry.Kind = val
				case "session":
					entry.Session = val
				case "model":
					entry.Model = val
				case "tags":
					val = strings.TrimPrefix(val, "[")
					val = strings.TrimSuffix(val, "]")
					for _, tag := range strings.Split(val, ",") {
						if t := strings.TrimSpace(tag); t != "" {
							entry.Tags = append(entry.Tags, t)
						}
					}
				}
			}
		}
	}
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "#")
	body = strings.TrimSpace(body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		entry.Title = strings.TrimSpace(body[:i])
		entry.Text = strings.TrimSpace(body[i+1:])
	} else {
		entry.Title = body
		entry.Text = body
	}
	if entry.Title == "" {
		entry.Title = firstLine(entry.Text)
	}
	return entry
}

// migrateLegacyJSONLLocked folds entries.jsonl into markdown once.
// Caller must hold feedbackMu.
func migrateLegacyJSONLLocked() {
	oldPath := filepath.Join(config.ReasonixHomeDir(), "feedback", "entries.jsonl")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return
	}
	dir := feedbackDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	any := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry FeedbackEntry
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if _, err := writeFeedbackMD(dir, entry); err == nil {
			any = true
		}
	}
	if any {
		_ = os.Rename(oldPath, oldPath+".migrated")
	}
}

// MigrateLegacyFeedbackEntries converts any leftover entries.jsonl into md files.
func MigrateLegacyFeedbackEntries() (int, error) {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	before, _ := countFeedbackMD()
	migrateLegacyJSONLLocked()
	after, _ := countFeedbackMD()
	return after - before, nil
}

func countFeedbackMD() (int, error) {
	files, err := os.ReadDir(feedbackDir())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, fi := range files {
		if !fi.IsDir() && strings.HasSuffix(fi.Name(), ".md") && !strings.HasPrefix(fi.Name(), "analysis-") {
			n++
		}
	}
	return n, nil
}

// AppendFeedbackEntry writes one markdown note and returns its path.
func AppendFeedbackEntry(entry FeedbackEntry) (string, error) {
	if strings.TrimSpace(entry.Text) == "" {
		return "", fmt.Errorf("submit_feedback: text is required")
	}
	switch entry.Kind {
	case "bug", "idea", "praise", "other":
	default:
		return "", fmt.Errorf("submit_feedback: kind must be one of bug|idea|praise|other, got %q", entry.Kind)
	}
	if entry.At == "" {
		entry.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	dir := feedbackDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	migrateLegacyJSONLLocked()
	path, err := writeFeedbackMD(dir, entry)
	if err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	return path, nil
}

// ListFeedbackEntries reads markdown notes sorted by filename (timestamp).
func ListFeedbackEntries(limit int) ([]FeedbackEntry, error) {
	dir := feedbackDir()
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	migrateLegacyJSONLLocked()
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, fi := range files {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".md") || strings.HasPrefix(fi.Name(), "analysis-") {
			continue
		}
		names = append(names, fi.Name())
	}
	sort.Strings(names)
	out := make([]FeedbackEntry, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out = append(out, parseFeedbackMD(string(raw)))
	}
	// Same-second filenames are not a stable "newest last" order; sort by At.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At < out[j].At
	})
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// ClearFeedbackEntries removes feedback-*.md notes (keeps analysis-*.md).
func ClearFeedbackEntries() error {
	dir := feedbackDir()
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, fi := range files {
		name := fi.Name()
		if fi.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, "analysis-") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

func (submitFeedback) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Kind  string   `json:"kind"`
		Text  string   `json:"text"`
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid submit_feedback args: %w", err)
		}
	}
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.Text = strings.TrimSpace(p.Text)
	p.Title = strings.TrimSpace(p.Title)
	tags := make([]string, 0, len(p.Tags))
	for _, tag := range p.Tags {
		if t := strings.TrimSpace(tag); t != "" && len(t) <= 40 {
			tags = append(tags, t)
		}
	}
	if len(tags) > 8 {
		tags = tags[:8]
	}
	path, err := AppendFeedbackEntry(FeedbackEntry{Kind: p.Kind, Text: p.Text, Title: p.Title, Tags: tags})
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]any{"path": path, "kind": p.Kind})
	return string(payload), nil
}
