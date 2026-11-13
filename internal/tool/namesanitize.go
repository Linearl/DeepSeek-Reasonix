package tool

import (
	"encoding/json"
	"strings"
)

// Control-character sanitization for model-supplied NAME-DOMAIN tokens
// (tl1310/tl1554 lineage; task 975 origin).
//
// Two pollution forms reach error echoes and exact-match resolution:
//  1. real control bytes — a JSON "\r" escape decodes to a bare CR byte and
//     travels inside tool names, capability ids, and argument key names;
//  2. literal escape text — the two-character sequence backslash+letter
//     arrives verbatim (double-escaped model output, or the model copying a
//     previously escaped echo back — the 2026-11-11 eight-failure loop).
//
// Both forms are stripped here. The functions live in internal/tool (not
// internal/agent) so the agent loop, the builtin tools, and the config tool
// share ONE implementation: internal/agent keeps thin same-name wrappers
// around them for its task-975 call sites, builtin and config call these
// directly. VALUE domains (long prompts, file paths, message bodies) never
// go through SanitizeNameToken — literal escape text can be legitimate
// content there, so values keep the strip-real-bytes-only semantics.

// StripControlCharacters removes ASCII control characters (C0 range 0x00-0x1f
// and DEL 0x7f) from s. Bytes >= 0x80 pass untouched so multi-byte UTF-8 is
// never reinterpreted. The bool reports whether anything was removed so
// callers can keep the heal observable instead of silently rewriting model
// output. Single repo-wide implementation (tl1554 SSOT sink; formerly
// internal/agent usecapability_mcp_arguments.go).
func StripControlCharacters(s string) (string, bool) {
	if !containsControlCharacter(s) {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// literalControlEscapes is the closed set of JSON named escape sequences that
// can arrive as literal two-character text inside a name-domain token. The
// \uXXXX form is deliberately not handled: unlike the five named escapes it
// is not a control character and stripping it would reach into tokens where a
// backslash-u sequence can be meaningful (none of which are name domains).
var literalControlEscapes = [...]string{`\b`, `\f`, `\n`, `\r`, `\t`}

// stripLiteralControlEscapes removes the literal two-character text forms of
// the JSON named control escapes (backslash + letter) from s. Returns the
// cleaned text and whether anything was removed.
func stripLiteralControlEscapes(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
		return s, false
	}
	changed := false
	for _, escape := range literalControlEscapes {
		if strings.Contains(s, escape) {
			s = strings.ReplaceAll(s, escape, "")
			changed = true
		}
	}
	return s, changed
}

// containsControlCharacter reports whether s contains any C0 control
// character or DEL (0x00-0x1f, 0x7f). Bytes >= 0x80 pass untouched so
// multi-byte UTF-8 names are never reinterpreted.
func containsControlCharacter(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// SanitizeNameToken cleans a NAME-DOMAIN token for exact-match resolution or
// error echo: tool names, capability ids, argument key names, echoed enum
// values, config keys. Real C0/DEL bytes AND the literal `\r`-style escape
// text are stripped, then surrounding space is trimmed (the parser always
// trimmed capability ids, task 975).
//
// Name domains have no legitimate backslash-letter semantics — a decoded key
// or id carrying the two-character `\r` text can only be double-escaped
// model output — so literal stripping has zero false-positive surface there.
// This is why the function must never be applied to VALUE domains: a prompt
// or path may legitimately discuss or contain such text.
//
// The bool reports whether anything was removed so heal sites can stay
// observable. A token that cleans to empty is returned as empty; callers
// decide their own fallback (the established one, capabilityQuotedID
// tl1553, is to echo the raw form under strconv.Quote — escaped, never raw
// bytes — or to skip the heal and let the exact-match fail).
func SanitizeNameToken(s string) (string, bool) {
	cleaned, stripped := StripControlCharacters(s)
	if lit, litStripped := stripLiteralControlEscapes(cleaned); litStripped {
		cleaned = lit
		stripped = true
	}
	return strings.TrimSpace(cleaned), stripped
}

// SanitizeErrorLines strips control characters from a multi-line error text
// while preserving its newline structure. Error texts embed name-domain
// tokens (ids, keys) but are free text overall: they can legitimately
// contain a backslash (Windows paths), so literal escape forms are NOT
// stripped here — only real control bytes, per line, with `\n` kept as the
// line separator so coaching layout survives (CR inside a line is removed,
// which also normalizes CRLF to LF).
func SanitizeErrorLines(s string) string {
	if !containsControlCharacter(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i], _ = StripControlCharacters(line)
	}
	return strings.Join(lines, "\n")
}

// SanitizeArgumentKeys strips control characters AND literal escape text from
// JSON object KEY NAMES at every depth of raw (tl1310, extended to the
// literal-escape form in tl1554). Values — including long prompt strings with
// legitimate \n/\r escapes — are preserved byte-for-byte; only key names are
// rewritten, and only when a key actually needs cleaning. Returns the cleaned
// raw message and the number of keys rewritten; a count of zero always
// returns the input byte-identical. Keys that clean to empty are kept (never
// dropped) so the schema gate still reports the unknown field instead of a
// misleading missing-required diagnosis.
func SanitizeArgumentKeys(raw json.RawMessage) (json.RawMessage, int) {
	if len(raw) == 0 || !argumentKeysNeedClean(raw) {
		return raw, 0
	}
	return rebuildArgumentKeys(raw)
}

// argumentKeysNeedClean reports whether any object key at any depth of raw
// carries a real control byte or literal escape text. Detection walks the
// structure without rewriting anything, so a payload whose keys are clean
// (including one whose values contain legitimate control escapes in long
// prompt strings) returns false and the caller keeps the input
// byte-identical.
func argumentKeysNeedClean(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &object) != nil {
			return false
		}
		for key, value := range object {
			if keyTokenNeedsClean(key) || argumentKeysNeedClean(value) {
				return true
			}
		}
	case strings.HasPrefix(trimmed, "["):
		var array []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &array) != nil {
			return false
		}
		for _, item := range array {
			if argumentKeysNeedClean(item) {
				return true
			}
		}
	}
	return false
}

// keyTokenNeedsClean reports whether a decoded JSON key needs cleaning: a
// real control byte, or literal escape text (a key name containing a
// backslash-escape sequence is always double-escaped model output — key
// domains have no legitimate backslash-letter semantics).
func keyTokenNeedsClean(key string) bool {
	if containsControlCharacter(key) {
		return true
	}
	_, changed := stripLiteralControlEscapes(key)
	return changed
}

// rebuildArgumentKeys re-marshals the object graph replacing every key that
// needs cleaning with its cleaned form. Values travel as json.RawMessage and
// are never re-encoded, so long prompt strings keep their exact bytes; clean
// subtrees keep their original bytes too. Key order follows map marshal
// (sorted) — acceptable because this path only runs on an already-polluted
// payload where byte identity is off the table. A count of zero with the
// input returned unchanged means re-marshaling failed; the call then proceeds
// exactly as before the tolerance existed.
func rebuildArgumentKeys(raw json.RawMessage) (json.RawMessage, int) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
			return raw, 0
		}
		rebuilt := make(map[string]json.RawMessage, len(object))
		cleaned := 0
		for key, value := range object {
			cleanKey, stripped := cleanKeyToken(key)
			if stripped {
				cleaned++
			}
			if inner, innerCleaned := rebuildArgumentKeys(value); innerCleaned > 0 {
				value = inner
				cleaned += innerCleaned
			}
			rebuilt[cleanKey] = value
		}
		merged, err := json.Marshal(rebuilt)
		if err != nil {
			return raw, 0
		}
		return merged, cleaned
	}
	if strings.HasPrefix(trimmed, "[") {
		var array []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &array) != nil {
			return raw, 0
		}
		rebuilt := make([]json.RawMessage, len(array))
		cleaned := 0
		for i, item := range array {
			rebuilt[i] = item
			if inner, innerCleaned := rebuildArgumentKeys(item); innerCleaned > 0 {
				rebuilt[i] = inner
				cleaned += innerCleaned
			}
		}
		merged, err := json.Marshal(rebuilt)
		if err != nil {
			return raw, 0
		}
		return merged, cleaned
	}
	return raw, 0
}

// cleanKeyToken cleans one decoded key name: real control bytes and literal
// escape text are stripped. Unlike SanitizeNameToken it does NOT trim space —
// rebuild must rewrite exactly the bytes the detection flagged, never
// touching clean keys that happen to carry surrounding spaces.
func cleanKeyToken(key string) (string, bool) {
	cleaned, stripped := StripControlCharacters(key)
	if lit, litStripped := stripLiteralControlEscapes(cleaned); litStripped {
		cleaned = lit
		stripped = true
	}
	return cleaned, stripped
}
