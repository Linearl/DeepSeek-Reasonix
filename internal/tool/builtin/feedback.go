package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(submitFeedback{}) }

// submitFeedback (task 121) lets the agent leave a structured product note in a
// local inbox the desktop can open. It is an experimental surface: the Settings
// switch that enables the viewer also documents that the tool exists. The write
// is a plain append to a JSONL file under the Reasonix home directory — no
// network, no host side effects beyond that file, and no approval.
type submitFeedback struct{}

func (submitFeedback) Name() string { return "submit_feedback" }

func (submitFeedback) Description() string {
	return "Submit a short product feedback note into the local feedback inbox. Use it when the user asks you to record a suggestion, bug report, or opinion about Reasonix itself (not about their code). The note is stored on this machine only and is visible in the desktop feedback panel when that experimental feature is enabled. Keep each note focused: one observation or suggestion per call."
}

func (submitFeedback) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"additionalProperties":false,
"properties":{
  "kind":{"type":"string","enum":["bug","idea","praise","other"],"description":"bug = something is wrong; idea = a suggestion; praise = what worked well; other = anything else."},
  "text":{"type":"string","minLength":1,"maxLength":4000,"description":"The feedback itself. Be concrete: what happened, what you expected, or what you suggest."},
  "tags":{"type":"array","items":{"type":"string","maxLength":40},"maxItems":8,"description":"Optional short labels, for example ['startup','mcp','ui']."}
},
"required":["kind","text"]
}`)
}

// ReadOnly is true: the tool only appends a local inbox entry.
func (submitFeedback) ReadOnly() bool { return true }

// PlanModeSafe: recording a note does not mutate the workspace under study.
func (submitFeedback) PlanModeSafe() bool { return true }

// FeedbackEntry is one inbox row (task 121). Fields are stable wire names so
// the desktop panel and any future exporter share one schema.
type FeedbackEntry struct {
	At      string   `json:"at"`
	Kind    string   `json:"kind"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
	Session string   `json:"session,omitempty"`
	Model   string   `json:"model,omitempty"`
}

var (
	// feedbackMu serializes file I/O on the inbox. The home override uses its
	// own mutex so path resolution can run while a file operation holds
	// feedbackMu (ClearFeedbackEntries used to deadlock by re-entering it).
	feedbackMu      sync.Mutex
	feedbackHomeMu  sync.RWMutex
	feedbackHome    string // override for tests; empty uses config.ReasonixHomeDir
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
	return filepath.Join(config.ReasonixHomeDir(), "feedback")
}

func feedbackPath() string {
	return filepath.Join(feedbackDir(), "entries.jsonl")
}

// AppendFeedbackEntry writes one entry to the local inbox and returns its path.
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
		entry.At = time.Now().UTC().Format(time.RFC3339)
	}
	dir := feedbackDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	path := feedbackPath()
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("submit_feedback: %w", err)
	}
	return path, nil
}

// ListFeedbackEntries reads the local inbox, newest last. A missing file is an
// empty list, not an error: the panel must open before any note exists.
func ListFeedbackEntries(limit int) ([]FeedbackEntry, error) {
	path := feedbackPath()
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FeedbackEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry FeedbackEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// ClearFeedbackEntries empties the local inbox.
func ClearFeedbackEntries() error {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	err := os.Remove(feedbackPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (submitFeedback) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Kind string   `json:"kind"`
		Text string   `json:"text"`
		Tags []string `json:"tags"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid submit_feedback args: %w", err)
		}
	}
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.Text = strings.TrimSpace(p.Text)
	tags := make([]string, 0, len(p.Tags))
	for _, tag := range p.Tags {
		if t := strings.TrimSpace(tag); t != "" && len(t) <= 40 {
			tags = append(tags, t)
		}
	}
	if len(tags) > 8 {
		tags = tags[:8]
	}
	path, err := AppendFeedbackEntry(FeedbackEntry{Kind: p.Kind, Text: p.Text, Tags: tags})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Feedback recorded (%s). It is stored locally at %s and appears in the desktop feedback panel when that experimental feature is enabled.", p.Kind, path), nil
}
