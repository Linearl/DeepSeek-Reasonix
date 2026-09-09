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
func LastJSONObject(text string) (string, bool) {
	depth, start := 0, -1
	inString, escaped := false, false
	last := ""
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
	return last, last != ""
}
