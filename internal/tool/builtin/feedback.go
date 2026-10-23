package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"reasonix/internal/config"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(submitFeedback{}) }

// submitFeedback (tasks 121/140) records a product note as a markdown file
// under ReasonixHome/feedback-inbox (env REASONIX_FEEDBACK_INBOX overrides).
type submitFeedback struct{}

func (submitFeedback) Name() string { return "submit_feedback" }

func (submitFeedback) Description() string {
	// Task 324: the trigger rules (user-approved 2026-09-26) live here, not in
	// AGENTS.md or skills — the tool description is the one place a submitting
	// model definitely reads. Low-noise first: 4 evidence-backed triggers,
	// 5 non-triggers, 3 cost gates.
	return "Submit a short product feedback note into the local feedback inbox (意见箱; markdown files under the Reasonix feedback-inbox directory; collected later by the collect_issues skill). Chinese phrasings like 意见箱, 反馈, 记一条意见 all mean this tool, and when the user asks you to record a suggestion, bug report, or opinion about Reasonix itself (not about their code), always use this tool. Agent-initiated notes are limited to four evidence-backed triggers: (A) a tool or guard misbehaved — quote the error verbatim plus the scenario; (B) docs or a tool description contradicts actual behavior — cite the description line vs what actually happened; (C) the same error hit 3+ times in a row (a one-off may be your own mistake — self-check first and do not submit); (D) the user explicitly asks. Do NOT submit: your own misuse from skipping a description or doc (self-check first), tasks that are merely hard or slow, transient network failures (retry first), nice-to-have suggestions, or a topic already in the inbox (check feedback-inbox for duplicates first). Cost gates: at most 2 notes per session (merge the rest into one summary note); one issue per note, at most 5 lines, opening with a category tag ([bug], [gap], or [docs]); only report an error that has cost 5+ minutes or 3+ wasted rounds. Notes stay on this machine and also appear in the desktop feedback panel when that experimental feature is enabled."
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
	// Task 344-A/B optional machine fields, all absent on human notes and on
	// legacy entries (schema compatible): Origin/Fingerprint/EvidenceID/
	// RuleVersion tag signal-scan drafts; Confirmed is the human-review gate
	// (nil = human note or legacy; false = machine draft awaiting review);
	// SupersededBy points at the entry/task that replaces this one — collectors
	// fold superseded entries instead of re-filing them.
	Origin       string `json:"origin,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	EvidenceID   string `json:"evidence-id,omitempty"`
	Confirmed    *bool  `json:"confirmed,omitempty"`
	RuleVersion  string `json:"rule-version,omitempty"`
	SupersededBy string `json:"superseded-by,omitempty"`
}

var (
	feedbackMu     sync.Mutex
	feedbackHomeMu sync.RWMutex
	feedbackHome   string

	// 任务1016：双写通知钩子。md 仍是权威源（写失败则整体失败），通知是
	// 加投递的一半——意见箱会话/信件中心由此可见每条新意见。hook 为 nil
	// （CLI/测试/未接线宿主）时行为与 1016 之前逐字节一致；hook 返回错误
	// 同样不影响 md 落盘结果（通知是尽力而为，兜底由意见箱的定期扫描
	// 按 md 补齐），只在返回体里如实标注。
	feedbackNotifyMu sync.RWMutex
	feedbackNotify   func(FeedbackNote) error
)

// FeedbackNote is what the notification half of a dual-write carries: the
// authoritative md FILENAME (the note's idempotent key — the message cites it
// so a redelivery never re-files the same note) plus a ≤3-line summary. The
// full text deliberately does NOT ride the message: heavy content (screenshots,
// logs) stays in the md only, keeping the message under the single-message
// size ceiling (1016 采集层裁剪).
type FeedbackNote struct {
	// FileName is the base name of the md file just written (e.g.
	// "feedback-20261020-093000-shortcut-for-panel.md"), never a path.
	FileName string
	Kind     string
	Title    string
	// Summary is the first ≤3 non-empty lines of the text — enough to triage
	// in the mail preview, never the whole note.
	Summary string
}

// SetFeedbackNotify wires the dual-write notification hook (host seam, same
// shape as SetHeartbeatManager). Passing nil clears it (tests). Hook errors
// never fail the submission — see feedbackNotifyNote.
func SetFeedbackNotify(fn func(FeedbackNote) error) {
	feedbackNotifyMu.Lock()
	defer feedbackNotifyMu.Unlock()
	feedbackNotify = fn
}

// feedbackSummary folds a note's text to at most maxSummaryLines non-empty
// lines, trimmed — the message-side excerpt (1016: md carries the payload,
// the message carries the pointer).
func feedbackSummary(text string, maxLines int) string {
	return feedbackSummaryBounded(text, maxLines, feedbackSummaryMaxBytes)
}

// feedbackSummaryMaxBytes is the byte ceiling for the folded summary — the
// notification rides a single message, so the summary must stay bounded (1016
// size ceiling). The cut is rune-safe (task 1057): a multi-byte character is
// never sliced mid-sequence.
const feedbackSummaryMaxBytes = 512

// feedbackSummaryBounded is feedbackSummary with an extra byte ceiling: the
// ≤3-line fold runs first, then the joined result is clipped to at most
// maxBytes bytes on a rune boundary.
func feedbackSummaryBounded(text string, maxLines int, maxBytes int) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
		if len(kept) >= maxLines {
			break
		}
	}
	joined := strings.Join(kept, "\n")
	if maxBytes > 0 && len(joined) > maxBytes {
		joined = clipRunesByteSafe(joined, maxBytes)
	}
	return joined
}

// FeedbackSummaryBounded exposes feedbackSummaryBounded for the backfill
// mirror in internal/agent (feedback_mail.go) so both the submit path and the
// scan-backfill path fold text identically (task 1057: rune-safe cut).
func FeedbackSummaryBounded(text string, maxLines int) string {
	return feedbackSummaryBounded(text, maxLines, feedbackSummaryMaxBytes)
}

// clipRunesRuneSafe truncates s to at most max runes, returning the truncated
// string. It preserves complete UTF-8 sequences (rune boundaries) so a
// multi-byte character is never cut mid-sequence.
func clipRunesRuneSafe(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// clipRunesByteSafe truncates s to at most maxBytes bytes, backing up to the
// nearest rune boundary so no multi-byte character is split (task 1057).
func clipRunesByteSafe(s string, maxBytes int) string {
	if maxBytes <= 0 || s == "" {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// feedbackNotifyNote invokes the installed hook, if any. It returns whether a
// hook ran and any error; the caller never turns an error into a failed
// submission (md is the authoritative half).
func feedbackNotifyNote(note FeedbackNote) (bool, error) {
	feedbackNotifyMu.RLock()
	fn := feedbackNotify
	feedbackNotifyMu.RUnlock()
	if fn == nil {
		return false, nil
	}
	return true, fn(note)
}

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

// FeedbackInboxDir returns the local feedback inbox directory (task 121/172),
// creating it when missing so the desktop panel can open it in the file
// manager even before the first note exists.
func FeedbackInboxDir() (string, error) {
	dir := feedbackDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("feedback inbox: %w", err)
	}
	return dir, nil
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
	// Task 344-A/B: optional machine/supersede fields — written only when set
	// so human notes and legacy migrations stay byte-compatible.
	if entry.Origin != "" {
		fmt.Fprintf(&b, "origin: %s\n", entry.Origin)
	}
	if entry.Fingerprint != "" {
		fmt.Fprintf(&b, "fingerprint: %s\n", entry.Fingerprint)
	}
	if entry.EvidenceID != "" {
		fmt.Fprintf(&b, "evidence-id: %s\n", entry.EvidenceID)
	}
	if entry.Confirmed != nil {
		fmt.Fprintf(&b, "confirmed: %t\n", *entry.Confirmed)
	}
	if entry.RuleVersion != "" {
		fmt.Fprintf(&b, "rule-version: %s\n", entry.RuleVersion)
	}
	if entry.SupersededBy != "" {
		fmt.Fprintf(&b, "superseded-by: %s\n", entry.SupersededBy)
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
				case "origin":
					entry.Origin = val
				case "fingerprint":
					entry.Fingerprint = val
				case "evidence-id":
					entry.EvidenceID = val
				case "confirmed":
					if v, err := strconv.ParseBool(val); err == nil {
						entry.Confirmed = &v
					}
				case "rule-version":
					entry.RuleVersion = val
				case "superseded-by":
					entry.SupersededBy = val
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
	// 任务1016 双写：md 落盘之后向意见箱追加一条通知消息（引用 md 文件名 +
	// ≤3 行摘要）。通知失败不影响提交结果——md 是权威源，兜底扫描会补齐。
	note := FeedbackNote{
		FileName: filepath.Base(path),
		Kind:     p.Kind,
		Title:    strings.TrimSpace(p.Title),
		Summary:  feedbackSummary(p.Text, 3),
	}
	if note.Title == "" {
		note.Title = firstLine(p.Text)
	}
	ran, notifyErr := feedbackNotifyNote(note)
	payload := map[string]any{"path": path, "kind": p.Kind}
	if ran {
		payload["notified"] = notifyErr == nil
		if notifyErr != nil {
			payload["notifyError"] = notifyErr.Error()
		}
	}
	result, _ := json.Marshal(payload)
	return string(result), nil
}
