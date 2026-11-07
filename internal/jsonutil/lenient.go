package jsonutil

// RemoveTrailingCommas repairs the trailing-comma shape models emit
// ("{"a":1,}" / "[1,2,]") — invalid JSON per RFC 8259, common enough in
// sub-agent output to deserve a last-chance lenient pass (tl1432 A2 stage 4,
// after extraction stages 1-3 could not find an already-valid object).
//
// The walk is string-aware: a comma inside a string literal ("x,y}") or an
// escaped quote never triggers a drop, and only a comma whose next
// non-whitespace byte is a closing '}' or ']' is removed — a comma that
// separates real members stays. Input without any repairable comma is
// returned byte-for-byte unchanged (the caller uses that to skip a no-op
// unmarshal and to tell "repaired" from "untouched").
//
// This is deliberately the ONLY rewrite performed: quoting fixes, comment
// stripping and truncation completion would rewrite meaning, and a repaired
// value must never diverge from what the model actually asserted.
func RemoveTrailingCommas(text string) string {
	var b []byte // nil until the first drop — unchanged input returns without allocating
	inString, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			if b != nil {
				b = append(b, c)
			}
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(text) {
				switch text[j] {
				case ' ', '\t', '\r', '\n':
					j++
				default:
					goto looked
				}
			}
		looked:
			if j < len(text) && (text[j] == '}' || text[j] == ']') {
				if b == nil {
					b = make([]byte, 0, len(text))
					b = append(b, text[:i]...)
				}
				continue // drop the comma; the closer stays
			}
		}
		if c == '"' {
			inString = true
		}
		if b != nil {
			b = append(b, c)
		}
	}
	if b == nil {
		return text
	}
	return string(b)
}
