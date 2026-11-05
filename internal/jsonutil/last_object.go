// Package jsonutil holds small, dependency-free helpers for parsing model
// output that mixes prose with JSON.
package jsonutil

// LastJSONObject returns the last complete top-level JSON object in text.
//
// Thinking models routinely quote example objects before the real answer
// ("I considered {"outcome":"continue"} … Final: {"outcome":"complete"}"), and
// a first-brace/last-brace slice spans both and fails to unmarshal. Scan for
// balanced top-level braces instead, ignoring braces inside strings and any
// trailing fragment that never closes.
//
// Markdown fences need no separate stripping pass (tl1432 A): the fence
// markers and their language tags contain no braces, so the balanced scan
// skips over them the same way it skips prose.
func LastJSONObject(text string) (string, bool) {
	last, _ := scanTopLevelObjects(text)
	return last, last != ""
}

// HasUnclosedObject reports whether text ends mid-object: a brace opened but
// never closed, or a string left unterminated inside an object (the two
// shapes a token-budget truncation leaves behind). It is the truncation
// detector for typed-result parse errors (tl1432 A) — the error can then say
// "the output looks truncated" instead of just "not a JSON object".
func HasUnclosedObject(text string) bool {
	_, unclosed := scanTopLevelObjects(text)
	return unclosed
}

// scanTopLevelObjects is the single brace scanner behind both helpers: it
// walks text once, tracking string/escape state, and reports the last
// complete top-level object plus whether the text never came back to depth 0
// (an open object or an open string at EOF).
func scanTopLevelObjects(text string) (last string, unclosed bool) {
	depth, start := 0, -1
	inString, escaped := false, false
	for i, r := range text {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					last = text[start : i+1]
				}
			}
		}
	}
	return last, depth > 0 || inString
}
