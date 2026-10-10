package session

import (
	"reasonix/internal/sessioncontent"
)

// Task 234 scope-c slice: only the two symbols management.go's picked
// history-export face actually uses. The upstream collectInboxContentRefs
// pair above them lives on sessioninbox.FrozenContentRefs — a core (#10545)
// addition this fork does not carry — and has no caller here, so it is
// deliberately not brought over (dependency chain stopped on purpose).
//
//nolint:unused // The history export layer deduplicates its content closure with this key.
func contentRefKey(ref sessioncontent.Ref) string {
	return ref.Digest + ":" + itoa64(ref.Bytes) + ":" + ref.IndexDigest
}

//nolint:unused // Kept allocation-free for contentRefKey in the history export layer.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
