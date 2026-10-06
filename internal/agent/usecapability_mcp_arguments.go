package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"reasonix/internal/tool"
)

// capabilityInputError distinguishes repairable envelope errors from target
// availability and authorization failures without changing their error text.
type capabilityInputError struct{ error }

func (e *capabilityInputError) Unwrap() error { return e.error }
func capabilityInputErrorf(format string, args ...any) error {
	return &capabilityInputError{fmt.Errorf(format, args...)}
}

type useCapabilityArgs struct {
	Action       string          `json:"action"`
	CapabilityID string          `json:"capability_id"`
	// CapabilityIDs lets action=decline dismiss several unrelated capabilities
	// in one call (fork: the per-turn capability route can list dozens of
	// candidates, and declining them one call at a time burns the turn).
	CapabilityIDs []string        `json:"capability_ids"`
	Query         string          `json:"query"`
	Limit         int             `json:"limit"`
	Arguments     json.RawMessage `json:"arguments"`
	Reason        string          `json:"reason"`
	// healedDoubleEnvelope is set by parseUseCapabilityArgs when a
	// stringified call envelope was unwrapped (task 212). It never
	// serializes; ResolveCall uses it to keep the self-heal observable.
	healedDoubleEnvelope bool `json:"-"`
	// healedStringified is set by parseUseCapabilityArgs when a JSON-string
	// arguments value was parsed into an object for an MCP target (task 457).
	// It never serializes; ResolveCall uses it to keep the self-heal
	// observable.
	healedStringified bool `json:"-"`
	// spreadParameters holds envelope-foreign top-level members captured when
	// action=call arrived with target parameters spread across the envelope
	// (message+to etc.) and no arguments object at all (task 457). It never
	// serializes; ResolveCall may merge them into one schema-gated arguments
	// object, and the capture only happens when "arguments" was absent so a
	// call that carried one is never rewritten.
	spreadParameters map[string]json.RawMessage `json:"-"`
}

// capabilityEnvelopeKeys are the top-level members of a use_capability call
// envelope. An arguments value is only treated as a nested envelope when ALL
// of its object members fall inside this set — a legitimate target argument
// object that merely happens to contain an "arguments" field (run_skill's
// own schema, for example) must never be unwrapped.
var capabilityEnvelopeKeys = map[string]bool{
	"action":         true,
	"capability_id":  true,
	"capability_ids": true,
	"query":          true,
	"limit":          true,
	"arguments":      true,
	"reason":         true,
}

// detectDoubleEnvelopedArguments reports whether the arguments value looks
// like a full use_capability call envelope nested one level too deep, and
// returns the inner "arguments" value to retry with (task 212). Detection is
// purely structural: raw must be a JSON object — directly or wrapped in a
// JSON string — whose members all belong to the envelope key set while
// including "arguments" plus at least one envelope marker
// (capability_id/capability_ids/action). Only one level is unwrapped; the
// result is never recursively re-examined.
func detectDoubleEnvelopedArguments(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, false
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false
		}
		trimmed = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		return nil, false
	}
	inner, exists := object["arguments"]
	if !exists {
		return nil, false
	}
	marker := false
	for key := range object {
		if !capabilityEnvelopeKeys[key] {
			return nil, false
		}
		if key == "action" || key == "capability_id" || key == "capability_ids" {
			marker = true
		}
	}
	if !marker {
		return nil, false
	}
	return json.RawMessage(append([]byte(nil), inner...)), true
}

// targetAcceptsArguments mirrors the dispatch gate's validation outcome for
// one candidate arguments value. Skipped validation (no usable schema) must
// not enable self-healing: without a schema there is no evidence the
// unwrapped form is the intended one.
func targetAcceptsArguments(target tool.Tool, args json.RawMessage) bool {
	if target == nil {
		return false
	}
	result := tool.ValidateArguments(target, args)
	return !result.Skipped && result.CompileErr == nil && len(result.Violations) == 0
}

// noteArgumentSelfHeal keeps an arguments self-heal observable (tasks 212 and
// 457): one audit counter plus one WARN log line naming the target and what
// the host corrected. Self-healing must never be silent, but it also must not
// leak argument values into the log — detail names shapes and key names only.
func (t *UseCapabilityTool) noteArgumentSelfHeal(id, detail string) {
	if t.audit != nil {
		t.audit.RecordArgumentSelfHeal()
	}
	log.Printf("[use_capability] WARN: self-healed arguments for %q (%s)", id, detail)
}

// mergedSpreadArguments builds one object from the envelope-foreign top-level
// parameters captured at parse time (task 457). spreadParameters is only
// populated when the arguments member was absent, so a call that carried an
// arguments object is never rewritten here. The caller must still verify the
// merged object against the target schema before dispatching.
func (a useCapabilityArgs) mergedSpreadArguments() (json.RawMessage, []string, bool) {
	if len(a.spreadParameters) == 0 {
		return nil, nil, false
	}
	keys := make([]string, 0, len(a.spreadParameters))
	for key := range a.spreadParameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged, err := json.Marshal(a.spreadParameters) // map marshal sorts keys
	if err != nil {
		return nil, nil, false
	}
	return merged, keys, true
}

// unwrapStringifiedJSONObject parses a JSON-string arguments value whose
// content is itself a JSON object (task 457). Pretty-printed content —
// newlines and indentation — passes because encoding/json skips whitespace,
// and a single trailing comma before a closing brace/bracket is tolerated.
// Anything else (plain text, arrays, scalars, double-encoded JSON) reports
// false so the original value reaches the precise validation error.
func unwrapStringifiedJSONObject(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, `"`) {
		return nil, false
	}
	var inner string
	if json.Unmarshal(raw, &inner) != nil {
		return nil, false
	}
	inner = strings.TrimSpace(inner)
	if !strings.HasPrefix(inner, "{") {
		return nil, false
	}
	candidate := []byte(inner)
	var object map[string]json.RawMessage
	if json.Unmarshal(candidate, &object) != nil || object == nil {
		candidate = stripTrailingCommas(candidate)
		if json.Unmarshal(candidate, &object) != nil || object == nil {
			return nil, false
		}
	}
	compact, err := json.Marshal(object) // sorted keys, normalized whitespace
	if err != nil {
		return nil, false
	}
	return compact, true
}

// stripTrailingCommas removes commas directly preceding a closing brace or
// bracket outside JSON strings, tolerating whitespace between them. It runs
// only on the decoded content of a stringified arguments value, never on
// provider envelopes, so normal envelope parsing stays strict.
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case ',':
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // drop the trailing comma
			}
		}
		out = append(out, c)
	}
	return out
}

// captureSpreadParameters remembers envelope-foreign top-level members for
// action=call when the arguments object was absent (task 457). Detection is
// purely structural; ResolveCall decides against the target schema whether the
// merged form may dispatch.
func captureSpreadParameters(raw json.RawMessage, args *useCapabilityArgs) {
	if len(raw) == 0 {
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return
	}
	extras := make(map[string]json.RawMessage)
	for key, value := range envelope {
		if !capabilityEnvelopeKeys[key] {
			extras[key] = value
		}
	}
	if len(extras) > 0 {
		args.spreadParameters = extras
	}
}

func parseUseCapabilityArgs(raw json.RawMessage) (useCapabilityArgs, string, string, error) {
	var args useCapabilityArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, "", "", fmt.Errorf("invalid args: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	id := strings.TrimSpace(args.CapabilityID)
	if args.Limit < 0 || args.Limit > 8 {
		return args, "", "", fmt.Errorf("limit must be between 1 and 8 when provided")
	}
	if action == "call" {
		// Task 457: remember target parameters spread across the envelope
		// top level (message+to etc. with no arguments object) so ResolveCall
		// can attempt one schema-gated merge instead of dispatching {}.
		if argumentsAbsent(args.Arguments) {
			captureSpreadParameters(raw, &args)
		}
		if strings.HasPrefix(id, "mcp-tool:") {
			normalized, err := normalizeMCPToolArguments(args.Arguments)
			if err != nil {
				// Task 212: the arguments may be a stringified full call
				// envelope (the observed output-layer habit). Unwrap one level
				// instead of failing; ResolveCall still requires the unwrapped
				// value to satisfy the target schema before it dispatches.
				if inner, ok := detectDoubleEnvelopedArguments(args.Arguments); ok {
					args.Arguments = inner
					args.healedDoubleEnvelope = true
				} else if unwrapped, ok := unwrapStringifiedJSONObject(args.Arguments); ok {
					// Task 457: a stringified plain target-arguments object
					// (pretty-printed or single-line) parses instead of
					// failing; ResolveCall keeps the heal observable.
					args.Arguments = unwrapped
					args.healedStringified = true
				} else {
					return args, "", "", err
				}
			} else {
				args.Arguments = normalized
			}
		}
	}
	return args, action, id, nil
}

// argumentsAbsent reports whether the arguments member carried no value at
// all. A present-but-empty object is NOT absent: rewriting it would betray
// calls whose target genuinely takes no parameters.
func argumentsAbsent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// normalizeMCPToolArguments accepts only an object. It deliberately does not
// unwrap JSON strings, rename fields, coerce values, or guess enums: schema
// mistakes must produce one precise repair contract instead of hidden behavior
// that differs between direct and proxied MCP calls. (Stringified objects are
// unwrapped by the parse-time tolerance in parseUseCapabilityArgs, which keeps
// the correction observable.)
func normalizeMCPToolArguments(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		// Task 457: name the received shape and the expected envelope so one
		// round of feedback is enough to self-correct. Values never leak.
		return nil, fmt.Errorf("arguments for an MCP tool must be a JSON object; arrays, scalars, malformed JSON, and non-JSON strings are not supported. Actual arguments received: %s. Expected shape: {\"action\":\"call\",\"capability_id\":\"mcp-tool:<server>/<tool>\",\"arguments\":{ ...the target tool's own parameters... }}", describeActualArguments(raw))
	}
	return json.RawMessage(append([]byte(nil), trimmed...)), nil
}
