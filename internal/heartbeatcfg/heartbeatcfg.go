// Package heartbeatcfg validates heartbeat-tasks.json (任务 823) the same way
// the desktop engine reads it: per-field type errors with line numbers,
// duplicate ids, schedules the engine can never fire, and a too-new schema.
// It exists so `reasonix heartbeat validate` (internal/cli) can check a
// config without importing the desktop host, which is a separate module only
// main may assemble. The interval/schedule acceptance rules mirror
// desktop/heartbeat_schedule.go on purpose — both sides pin the same golden
// cases; keep them in lockstep when changing either.
package heartbeatcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the newest on-disk config schema this binary understands.
// Keep in sync with desktop/heartbeat.go heartbeatSchemaVersion (4).
const SchemaVersion = 4

// ProblemKind classifies one validation finding.
type ProblemKind string

const (
	KindUnmarshal   ProblemKind = "unmarshal"    // syntax or per-field type error
	KindDuplicateID ProblemKind = "duplicate-id" // two tasks share one id
	KindInterval    ProblemKind = "interval"     // schedule string the engine can never fire
	KindSchema      ProblemKind = "schema"       // schemaVersion newer than this binary
)

// Problem is one validation finding. TaskIndex is the 0-based tasks[] position
// (-1 for envelope-level findings), TaskID the id the task declares (best
// effort — a task whose id itself is the problem may leave it empty), Field the
// dotted field path, and Line the 1-based source line when it can be located.
type Problem struct {
	Kind      ProblemKind `json:"kind"`
	TaskIndex int         `json:"taskIndex"`
	TaskID    string      `json:"taskId,omitempty"`
	Field     string      `json:"field,omitempty"`
	Line      int         `json:"line,omitempty"`
	Message   string      `json:"message"`
}

// Validate checks raw config bytes and returns every problem found, in stable
// source order (envelope first, then tasks by index). An empty result means
// the engine would accept the file.
func Validate(data []byte) []Problem {
	var problems []Problem
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(bytes.TrimSpace(data)) == 0 {
		problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: "(root)", Message: "config file is empty; the engine would treat it as no tasks at all"})
		return problems
	}
	if trimmed[0] == 0xEF { // UTF-8 BOM: ReadFileUTF8 strips it, a hand editor may not
		// Not fatal for Go's json (it rejects BOM), report precisely.
		problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: "(root)", Line: 1, Message: "config starts with a UTF-8 BOM; encoding/json rejects it — save the file without BOM"})
		return problems
	}

	// 1. Whole-document syntax check with line numbers. Unmarshal into a
	// probe rather than json.Valid: only the decode path hands back the
	// SyntaxError offset the line number needs.
	var probe any
	if err := json.Unmarshal(data, &probe); err != nil {
		msg := fmt.Sprintf("config is not valid JSON: %v", err)
		line := 0
		if se, ok := err.(*json.SyntaxError); ok {
			line = lineAt(data, int(se.Offset))
			msg = fmt.Sprintf("config is not valid JSON: %v (line %d)", se, line)
		}
		problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: "(root)", Line: line, Message: msg})
		return problems
	}

	// 2. Envelope: schemaVersion / revision / tasks must have engine types.
	// Same per-field scan as tasks — encoding/json's own error for a
	// string->json.Number miss ("revision":"one") carries no field path, so
	// the table-driven check is what keeps the report precise.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(data, &env); err != nil {
		problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: "(root)", Message: fmt.Sprintf("config envelope rejected: %v", err)})
		return problems
	}
	for _, k := range []string{"schemaVersion", "revision", "tasks"} {
		raw, ok := env[k]
		if !ok {
			continue
		}
		if kindErr := checkFieldKind(raw, envelopeFieldKinds[k]); kindErr != "" {
			problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: k, Message: k + ": " + kindErr})
		}
	}
	if raw, ok := env["schemaVersion"]; ok {
		var sv int
		if err := json.Unmarshal(bytes.TrimSpace(raw), &sv); err == nil && sv > SchemaVersion {
			problems = append(problems, Problem{
				Kind: KindSchema, TaskIndex: -1, Field: "schemaVersion",
				Message: fmt.Sprintf("schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix — the engine refuses to load or overwrite this file", sv, SchemaVersion),
			})
		}
	}
	rawTasksField, hasTasks := env["tasks"]
	if !hasTasks {
		return problems
	}
	var rawTasks []json.RawMessage
	if err := json.Unmarshal(rawTasksField, &rawTasks); err != nil {
		problems = append(problems, Problem{Kind: KindUnmarshal, TaskIndex: -1, Field: "tasks", Message: fmt.Sprintf("tasks: expected an array of task objects: %v", err)})
		return problems
	}
	return append(problems, validateTasks(data, rawTasks)...)
}

// validateTasks scans each task object for per-field type errors, duplicate
// ids and unfireable intervals (the three explicit classes of 任务 823 ④).
func validateTasks(data []byte, rawTasks []json.RawMessage) []Problem {
	var problems []Problem
	ids := make(map[string]int, len(rawTasks))
	for i, raw := range rawTasks {
		taskOffset := bytes.Index(data, raw)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			problems = append(problems, Problem{
				Kind: KindUnmarshal, TaskIndex: i,
				Field: fmt.Sprintf("tasks[%d]", i), Line: lineAt(data, taskOffset),
				Message: fmt.Sprintf("tasks[%d]: expected a task object: %v", i, err),
			})
			continue
		}
		taskID := decodedString(fields["id"])
		// Field order in the report must not depend on map iteration: sort keys.
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			want, known := taskFieldKinds[k]
			if !known {
				continue // unknown fields are ignored by encoding/json too
			}
			if kindErr := checkFieldKind(fields[k], want); kindErr != "" {
				problems = append(problems, Problem{
					Kind: KindUnmarshal, TaskIndex: i, TaskID: taskID,
					Field:   fmt.Sprintf("tasks[%d].%s", i, k),
					Line:    lineAt(data, fieldOffset(data, taskOffset, raw, k)),
					Message: fmt.Sprintf("tasks[%d].%s: %s", i, k, kindErr),
				})
			}
		}
		if rawID, ok := fields["id"]; ok {
			if id := decodedString(rawID); id != "" {
				if prev, dup := ids[id]; dup {
					problems = append(problems, Problem{
						Kind: KindDuplicateID, TaskIndex: i, TaskID: id,
						Field: fmt.Sprintf("tasks[%d].id", i), Line: lineAt(data, taskOffset),
						Message: fmt.Sprintf("tasks[%d].id %q duplicates tasks[%d]: execution leases and run-state merges key on id, so the duplicates would fight over one slot", i, id, prev),
					})
				} else {
					ids[id] = i
				}
			}
		}
		if rawInterval, ok := fields["interval"]; ok {
			interval := decodedString(rawInterval)
			if !ValidInterval(interval) {
				problems = append(problems, Problem{
					Kind: KindInterval, TaskIndex: i, TaskID: taskID,
					Field: fmt.Sprintf("tasks[%d].interval", i), Line: lineAt(data, taskOffset),
					Message: fmt.Sprintf("tasks[%d].interval %q: the engine cannot parse this schedule, so the task would never fire (accepted: \"5m\"/\"1h\"/\"30s\", bare minutes like \"30\", a five-field cron like \"0 9 * * 1-5\", or \"daily@09:00\" / \"weekly:mon,fri@08:30\" / \"monthly:15@09:00\" / \"yearly:3-1@09:00\")", i, interval),
				})
			}
		}
	}
	return problems
}

// ValidateFile reads path and validates it. A missing file is an error (the
// caller decides the exit code), not a validation problem.
func ValidateFile(path string) ([]Problem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Validate(data), nil
}

// FormatProblems writes the human-readable report: one numbered line per
// problem, then a summary. Human-readable is the contract (任务 823 ①): a
// hand-editing user must see which field is wrong without decoding JSON errors.
func FormatProblems(w io.Writer, path string, problems []Problem) {
	fmt.Fprintf(w, "heartbeat config %s: %d problem(s)\n", path, len(problems))
	for i, p := range problems {
		where := ""
		if p.Line > 0 {
			where = fmt.Sprintf(" (line %d)", p.Line)
		}
		fmt.Fprintf(w, "  %d. [%s] %s%s: %s\n", i+1, p.Kind, p.Field, where, p.Message)
	}
}

// ── Field type table ────────────────────────────────────────────────────────

// taskFieldKinds mirrors desktop/heartbeat.go's HeartbeatTask JSON fields.
// "int" fields are unix-millis timestamps, counters or budget numbers — the
// incident's createdAt came in exactly here as a quoted string.
var taskFieldKinds = map[string]string{
	"id":                     "string",
	"title":                  "string",
	"prompt":                 "string",
	"interval":               "string",
	"enabled":                "bool",
	"scope":                  "string",
	"workspaceRoot":          "string",
	"topicId":                "string",
	"lastRunAt":              "int",
	"newConversationEachRun": "bool",
	"runHistory":             "array",
	"createdAt":              "int",
	"approvalMode":           "string",
	"timeWindowStart":        "string",
	"timeWindowEnd":          "string",
	"notifyChannels":         "bool-null",
	"provider":               "string",
	"model":                  "string",
	"goalMode":               "bool",
	"goalText":               "string",
	"reuseSession":           "bool",
	"idleStreak":             "int",
	"maxRuns":                "int",
	"runsUsed":               "int",
}

// envelopeFieldKinds mirrors heartbeatConfig's envelope fields (desktop
// heartbeat.go): schemaVersion/revision are whole numbers, tasks an array.
var envelopeFieldKinds = map[string]string{
	"schemaVersion": "int",
	"revision":      "int",
	"tasks":         "array",
}

// checkFieldKind returns "" when raw matches the declared kind, otherwise a
// human-readable sentence saying what the engine needed and what it found.
func checkFieldKind(raw json.RawMessage, kind string) string {
	trimmed := bytes.TrimSpace(raw)
	switch kind {
	case "string":
		if len(trimmed) == 0 || trimmed[0] != '"' {
			return "expected a string (quoted text), got " + jsonTypeName(trimmed)
		}
	case "bool":
		if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
			return "expected true or false, got " + jsonTypeName(trimmed) + " " + snippet(trimmed)
		}
	case "bool-null":
		if bytes.Equal(trimmed, []byte("null")) {
			return ""
		}
		if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
			return "expected true, false or null, got " + jsonTypeName(trimmed) + " " + snippet(trimmed)
		}
	case "int":
		// Shape check first: encoding/json accepts a quoted number ("3")
		// into a json.Number target, but the engine's int64 field does not —
		// the check must match the engine, not json.Number.
		if len(trimmed) == 0 || trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9') {
			return "expected a number (unix milliseconds / counter), got " + jsonTypeName(trimmed) + " " + snippet(trimmed)
		}
		var n json.Number
		if err := json.Unmarshal(trimmed, &n); err != nil {
			return "expected a number (unix milliseconds / counter), got " + jsonTypeName(trimmed) + " " + snippet(trimmed)
		}
		if _, err := n.Int64(); err != nil {
			return fmt.Sprintf("expected a whole number, got %s (fractions would be truncated)", n.String())
		}
	case "array":
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return "expected an array, got " + jsonTypeName(trimmed) + " " + snippet(trimmed)
		}
	}
	return ""
}

func jsonTypeName(raw []byte) string {
	if len(raw) == 0 {
		return "nothing"
	}
	switch raw[0] {
	case '"':
		return "a string"
	case '{':
		return "an object"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	default:
		return "a number"
	}
}

func snippet(raw []byte) string {
	s := string(bytes.TrimSpace(raw))
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return "(" + s + ")"
}

func decodedString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// fieldOffset locates the source offset of a key inside a task's raw bytes so
// the report can carry a line number. Best effort: -1 offsets fall back to the
// task's own offset.
func fieldOffset(data []byte, taskOffset int, taskRaw json.RawMessage, key string) int {
	if taskOffset < 0 {
		return -1
	}
	needle, _ := json.Marshal(key)
	idx := bytes.Index(taskRaw, needle)
	if idx < 0 {
		return -1
	}
	return taskOffset + idx
}

func lineAt(data []byte, offset int) int {
	if offset < 0 || offset > len(data) {
		return 0
	}
	return 1 + bytes.Count(data[:offset], []byte("\n"))
}

// ── Interval acceptance (mirrors desktop/heartbeat_schedule.go) ─────────────

// ValidInterval reports whether the engine can schedule interval at all.
// Acceptance chain, identical to desktop heartbeatTaskDueAt's:
//  1. a five-field cron expression (with per-field range checks);
//  2. a "|<schedule>" suffix form ("24h|daily@09:00") — the bare "daily@09:00"
//     form is NOT accepted: desktop parseHeartbeatSchedule requires the
//     '<duration>|' prefix. When the schedule part is not a known form the
//     engine falls back to the duration before '|', so the check falls back
//     the same way;
//  3. a duration: "5m"/"1h"/"30s" via time.ParseDuration, or a bare number
//     which the desktop parser treats as minutes. Empty or unparseable strings
//     fire never — exactly what [interval] problems warn about.
func ValidInterval(interval string) bool {
	if IsCronExpr(interval) {
		return true
	}
	if _, after, ok := strings.Cut(interval, "|"); ok {
		if validScheduleSuffix(after) {
			return true
		}
	}
	d, err := ParseInterval(interval)
	return err == nil && d > 0
}

// ParseInterval mirrors desktop parseInterval: "5m"/"1h"/"30s" via
// time.ParseDuration; a suffix-less string is assumed to be minutes ("30" →
// 30m). Everything before '|' is the duration part of a schedule form.
func ParseInterval(s string) (time.Duration, error) {
	if idx := strings.Index(s, "|"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) == 0 {
		return 0, nil
	}
	switch s[len(s)-1] {
	case 's', 'm', 'h':
		return time.ParseDuration(s)
	default:
		return time.ParseDuration(s + "m")
	}
}

// IsCronExpr mirrors desktop isCronExpr: five fields, restricted charset, and
// per-field range checks so an expression that can never match ("99 * * * *")
// is rejected rather than silently firing never.
func IsCronExpr(s string) bool {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	limits := []int{59, 23, 31, 12, 7}
	mins := []int{0, 0, 1, 1, 0}
	for i, f := range fields {
		if f == "" {
			return false
		}
		for _, c := range f {
			if !strings.ContainsRune("0123456789*/-,", c) {
				return false
			}
		}
		for part := range strings.SplitSeq(f, ",") {
			part = strings.TrimSpace(part)
			base := part
			if stepBase, stepText, ok := strings.Cut(part, "/"); ok {
				step, err := strconv.Atoi(stepText)
				if err != nil || step < 1 {
					return false
				}
				base = stepBase
			}
			if base == "*" {
				continue
			}
			if loText, hiText, ok := strings.Cut(base, "-"); ok {
				lo, err1 := strconv.Atoi(loText)
				hi, err2 := strconv.Atoi(hiText)
				if err1 != nil || err2 != nil || lo < mins[i] || hi > limits[i] || lo > hi {
					return false
				}
			} else {
				v, err := strconv.Atoi(base)
				if err != nil || v < mins[i] || v > limits[i] {
					return false
				}
			}
		}
	}
	return true
}

// validScheduleSuffix mirrors desktop parseHeartbeatSchedule's acceptance:
// "daily", "weekly:mon,fri@08:30", "biweekly:wed@09:00", "monthly:15@09:00",
// "yearly:3-1@09:00" — the @HH:MM part defaults to 09:00 when absent.
func validScheduleSuffix(after string) bool {
	raw := strings.TrimSpace(after)
	if raw == "" {
		return false
	}
	if _, at, ok := strings.Cut(raw, "@"); ok {
		if !validClock(at) {
			return false
		}
	}
	kind, rule, hasRule := strings.Cut(raw, ":")
	switch kind {
	case "daily":
		return true
	case "weekly", "biweekly":
		if !hasRule {
			return false // desktop requires at least one valid weekday
		}
		for part := range strings.SplitSeq(rule, ",") {
			if validWeekday(part) {
				return true
			}
		}
		return false
	case "monthly":
		return true // desktop falls back to day 1 for anything unparseable
	case "yearly":
		return true // desktop falls back to March-1st-style defaults
	default:
		return false
	}
}

func validClock(s string) bool {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return false
	}
	hour, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return false
	}
	return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59
}

func validWeekday(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sun", "mon", "tue", "wed", "thu", "fri", "sat":
		return true
	}
	return false
}
