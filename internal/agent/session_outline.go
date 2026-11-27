package agent

// tl1833: outline rendering for cross-session tail reads. read_session_tail in
// full mode returns raw transcript bytes; at the default 10 KiB budget that is
// roughly two or three turns of a tool-heavy session (the tool OUTPUT rows
// dominate every byte). The outline mode renders the same trailing window as
// intent-summary lines — one [user]/[asst] line per turn plus one
// "tool(state): intent" line per tool call — which the tl1833 survey measured
// at 22–175x fewer bytes for the same window (17.5x more turns per fixed
// budget) with enough fidelity for "what has this session been doing"
// coordination checks.
//
// The intent is NOT extracted by UI heuristics: the model authors a
// `description` argument on every call whose schema carries one (tl1613 armed
// bash/grep/glob/read_file/edit_file/write_file/multi_edit; modeled on zcode),
// and it persists inside the transcript's tool_calls. The parser therefore
// only reads what is already on disk — zero protocol change. Calls without a
// description fall back to a key argument (same fields the zcode UI summary
// uses), and calls with neither (todo/wait bookkeeping) render the bare tool
// name. Survey rates: 61.8% strict (description), 91.7% with argument
// fallback.

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// Rendering caps, in runes (not bytes): the survey samples are mostly Chinese,
// so a byte cut would both truncate too early and risk splitting a UTF-8
// sequence. Values are the tl1833 prototype's hand-tuned settings, kept
// unchanged so the measured 22–175x compression and the judged fidelity carry
// over to the shipped renderer.
const (
	outlineAssistantChars = 240
	outlineUserChars      = 80
	outlineIntentChars    = 110
	// Rolling window cap. The parser streams the file record by record and
	// keeps only the most recent turns; this bounds its memory to well above
	// the 64 KiB max budget (an outline turn is a few hundred bytes), so the
	// budget walk always has enough history to fill the caller's budget.
	outlineWindowCapBytes = 512 * 1024
)

// outlineKeyArgFields is the argument-fallback priority, ordered the same way
// the zcode UI summary (getInputSummary) and the tl1833 prototype order it.
// `description` is not listed: it is checked first for every call, before this
// table.
var outlineKeyArgFields = []string{
	"command", "path", "file_path", "filePath", "pattern", "query", "url",
	"prompt", "skill", "name", "message", "task", "to",
}

// OutlineOptions holds the renderer caps; zero fields take the survey defaults.
type OutlineOptions struct {
	MaxAssistantChars int
	MaxUserChars      int
	MaxIntentChars    int
	// WindowCapBytes bounds how much rendered outline history the streaming
	// parser retains; 0 takes the default.
	WindowCapBytes int
}

func (o OutlineOptions) withDefaults() OutlineOptions {
	if o.MaxAssistantChars <= 0 {
		o.MaxAssistantChars = outlineAssistantChars
	}
	if o.MaxUserChars <= 0 {
		o.MaxUserChars = outlineUserChars
	}
	if o.MaxIntentChars <= 0 {
		o.MaxIntentChars = outlineIntentChars
	}
	if o.WindowCapBytes <= 0 {
		o.WindowCapBytes = outlineWindowCapBytes
	}
	return o
}

// outlineRecord is one transcript line worth looking at. The struct mirrors the
// persisted shapes of provider.Message / provider.ToolCall (the writer side)
// plus the legacy OpenAI nesting accepted by scanSessionJSONL. Unknown fields
// are ignored by json.Unmarshal, so new writer-side additions cannot break the
// reader.
type outlineRecord struct {
	Role      string            `json:"role"`
	Content   json.RawMessage   `json:"content"`
	ToolCalls []outlineToolCall `json:"tool_calls"`
}

type outlineToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	// Provider-excluded display metadata for proxied calls: a use_capability
	// dispatch persists name="use_capability" plus the resolved identity, and
	// "use_capability" alone says nothing about the workflow (#task 150).
	ResolvedName string `json:"resolved_name"`
	CapabilityID string `json:"capability_id"`
	Function     struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// Recovery is the durable execution receipt (provider.ToolCall.Recovery):
	// its Arguments carry the same call arguments as parsed JSON and its State
	// is the ToolRunState the outline renders.
	Recovery struct {
		Arguments json.RawMessage `json:"arguments"`
		State     string          `json:"state"`
	} `json:"tool_recovery"`
}

// displayName prefers the resolved identity over the raw dispatch name, then
// the legacy nested-function shape. ResolvedName beats CapabilityID here
// because the outline is a model-facing rendering and resolved_name is the
// display field per the provider.ToolCall comment (the sessionScan.go
// statistics scanner orders them the other way; both refuse "use_capability").
func (tc outlineToolCall) displayName() string {
	for _, c := range []string{tc.ResolvedName, tc.CapabilityID, tc.Name, tc.Function.Name} {
		if s := strings.TrimSpace(c); s != "" {
			return s
		}
	}
	return "?"
}

// parsedArguments returns the call arguments as a map, trying the wire string
// first (provider.ToolCall.Arguments is JSON-encoded), then the legacy nested
// shape, then the recovery receipt's pre-parsed copy.
func (tc outlineToolCall) parsedArguments() map[string]any {
	for _, raw := range []string{tc.Arguments, tc.Function.Arguments, string(tc.Recovery.Arguments)} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "null" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
			continue
		}
		return m
	}
	return map[string]any{}
}

// outlineTurn is one rendered user or assistant record. size counts rendered
// bytes plus one separating newline, the unit the budget walk accumulates.
type outlineTurn struct {
	lines []string
}

func (t outlineTurn) size() int {
	n := 0
	for _, l := range t.lines {
		n += len(l) + 1
	}
	return n
}

// outlineTurnFor renders one transcript record into outline lines, reporting
// false for records that contribute nothing (system/tool rows, empty
// assistant acks): tool OUTPUT rows are exactly the bulk the outline exists to
// shed.
func outlineTurnFor(rec outlineRecord, opts OutlineOptions) (outlineTurn, bool) {
	var lines []string
	text := outlineContentText(rec.Content)
	switch rec.Role {
	case "user":
		if h := outlineHead(text, opts.MaxUserChars); h != "" {
			lines = append(lines, "[user] "+h)
		}
	case "assistant", "":
		// "" keeps the scanSessionJSONL convention: the oldest transcripts
		// omit the role on assistant records.
		if h := outlineHead(text, opts.MaxAssistantChars); h != "" {
			lines = append(lines, "[asst] "+h)
		}
		for _, tc := range rec.ToolCalls {
			name := tc.displayName()
			state := strings.TrimSpace(tc.Recovery.State)
			if state == "" {
				// Transcripts without a recovery receipt (older writers,
				// in-flight dispatches): the prototype rendered these "done"
				// and the judged samples carried that wording.
				state = "done"
			}
			if intent := outlineToolIntent(tc.parsedArguments(), opts.MaxIntentChars); intent != "" {
				lines = append(lines, "  - "+name+"("+state+"): "+intent)
			} else {
				lines = append(lines, "  - "+name+"("+state+")")
			}
		}
	default:
		// system / tool / anything else: not part of an outline.
		return outlineTurn{}, false
	}
	if len(lines) == 0 {
		return outlineTurn{}, false
	}
	return outlineTurn{lines: lines}, true
}

// outlineToolIntent implements the survey's intent ladder: the model-authored
// `description` first (any tool whose schema arms it — tl1613 family), then a
// key argument, then nothing (caller renders the bare tool name).
func outlineToolIntent(args map[string]any, maxChars int) string {
	if d, ok := args["description"].(string); ok && strings.TrimSpace(d) != "" {
		return outlineHead(d, maxChars)
	}
	for _, k := range outlineKeyArgFields {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			return outlineHead(v, maxChars)
		}
	}
	// Catch-all for tools with none of the known fields. Go map iteration is
	// randomized, so the fallback must sort keys to keep the outline
	// deterministic (the Python prototype relied on insertion order; JSON
	// object order is not preserved through the Go decoder).
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			return outlineHead(v, maxChars)
		}
	}
	return ""
}

// outlineContentText extracts the text of a transcript `content` field: a JSON
// string on every live writer, with a defensive parts-array branch matching
// rawContentText.
func outlineContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
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
		return b.String()
	}
	return ""
}

// outlineHead collapses whitespace and cuts to max runes, appending an
// ellipsis when truncated — the prototype's `head` with rune-safe slicing
// (Python string indexing is rune-based; Go byte slicing would split CJK).
func outlineHead(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return strings.TrimRight(s, " ")
	}
	return strings.TrimRight(string(r[:max]), " ") + "…"
}

// outlineTruncateUTF8 hard-caps rendered text at max bytes without splitting a
// rune, the last-resort cap for a single turn bigger than the whole budget.
func outlineTruncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// outlineWindow is the rolling history of recent rendered turns, capped by
// rendered bytes so streaming a multi-megabyte transcript stays bounded.
type outlineWindow struct {
	turns    []outlineTurn
	bytes    int
	capBytes int
	// dropped records that at least one older turn fell off the front.
	dropped bool
}

func (w *outlineWindow) add(t outlineTurn) {
	w.turns = append(w.turns, t)
	w.bytes += t.size()
	for w.bytes > w.capBytes && len(w.turns) > 1 {
		w.bytes -= w.turns[0].size()
		w.turns = w.turns[1:]
		w.dropped = true
	}
}

// RenderSessionOutline streams a session transcript from r and renders the
// most recent turns whose outline fits budgetBytes. It returns the outline
// text, the number of turns included, and whether anything earlier was cut
// (budget exhaustion or the window cap on very long transcripts).
//
// Streaming discipline (tl1833 设计要点④): session files reach tens of MB in
// full mode, so the file is never loaded whole — json.Decoder feeds one record
// at a time and the window holds only rendered lines. A torn trailing record
// (crash mid-append) ends the scan and keeps everything decoded before it,
// mirroring the full mode's line-boundary cut. An error before the first
// record (unreadable file, non-transcript content) is returned to the caller
// instead of rendering an empty outline.
func RenderSessionOutline(r io.Reader, budgetBytes int, opts OutlineOptions) (text string, turns int, truncated bool, err error) {
	if budgetBytes <= 0 {
		budgetBytes = 10 * 1024
	}
	opts = opts.withDefaults()
	dec := json.NewDecoder(bufio.NewReaderSize(r, 64*1024))
	win := &outlineWindow{capBytes: opts.WindowCapBytes}
	decoded := 0
	for {
		var rec outlineRecord
		derr := dec.Decode(&rec)
		if derr != nil {
			// io.EOF is the clean end; anything else (torn tail, one
			// malformed record) stops the scan and keeps the prefix. Only a
			// failure before ANY record is reported: a transcript that never
			// decoded is not an empty outline, it is an error.
			if decoded == 0 && derr != io.EOF {
				return "", 0, false, derr
			}
			break
		}
		decoded++
		if turn, ok := outlineTurnFor(rec, opts); ok {
			win.add(turn)
		}
	}
	// Budget walk, newest first: always include at least the newest turn so
	// the caller never gets an empty outline from a non-empty transcript.
	picked := make([]outlineTurn, 0, len(win.turns))
	acc := 0
	for i := len(win.turns) - 1; i >= 0; i-- {
		s := win.turns[i].size()
		if acc+s > budgetBytes && len(picked) > 0 {
			break
		}
		picked = append(picked, win.turns[i])
		acc += s
	}
	truncated = win.dropped || len(picked) < len(win.turns)
	flat := make([]string, 0, acc)
	for i := len(picked) - 1; i >= 0; i-- {
		flat = append(flat, picked[i].lines...)
	}
	text = strings.Join(flat, "\n")
	if len(text) > budgetBytes {
		// A single turn bigger than the whole budget (a wall of narration +
		// tool lines) gets hard-capped here; the caller must see that bytes
		// were cut even though every rendered turn is present.
		text = outlineTruncateUTF8(text, budgetBytes)
		truncated = true
	}
	return text, len(picked), truncated, nil
}
