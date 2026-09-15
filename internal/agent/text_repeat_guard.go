package agent

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// Text-level stream repetition guard (task 110). The existing progress/storm
// guards all score tool receipts; a model that repeats the same sentence in
// text without calling tools is invisible to them. This layer watches the
// streamed assistant text only.

// ErrModelTextRepeat is the typed interrupt for a detected text loop. The run
// loop turns the first hit into a host reminder and a second into a pause.
var ErrModelTextRepeat = errors.New("model text repeat: the assistant is looping the same output")

// DefaultTextRepeatN is the n-gram size (mimocode default).
const DefaultTextRepeatN = 4

// DefaultTextRepeatThreshold is how many identical n-grams count as a loop.
// Deliberately high so ordinary repeated phrases in long technical prose stay
// safe (task 110: prefer under-triggering to false positives).
const DefaultTextRepeatThreshold = 24

// DefaultTextConsecutiveMinBlock / Threshold detect periodic repeats.
const (
	DefaultTextConsecutiveMinBlock = 8
	DefaultTextConsecutiveThreshold = 4
	// textRepeatMinDistinct requires several distinct tokens in the repeated
	// block so a trivial token cannot false-positive (mimocode minDistinct=3).
	textRepeatMinDistinct = 3
	// textRepeatWindowCap bounds the sliding window so long documents stay O(cap).
	textRepeatWindowCap = 8192
)

var cjkSplit = regexp.MustCompile(`([　-ヿ㐀-䶿一-鿿豈-﫿＀-￯])`)

// tokenizeForNgram splits text into tokens, inserting spaces around CJK so
// n-grams work for Chinese without a real segmenter.
func tokenizeForNgram(s string) []string {
	s = cjkSplit.ReplaceAllString(s, " $1 ")
	fields := strings.Fields(s)
	out := fields[:0:0]
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func ngrams(tokens []string, n int) map[string]int {
	counts := make(map[string]int)
	if n <= 0 || len(tokens) < n {
		return counts
	}
	for i := 0; i+n <= len(tokens); i++ {
		counts[strings.Join(tokens[i:i+n], " ")]++
	}
	return counts
}

// detectRepeatedNgram reports true when any n-gram repeats at least threshold times.
func detectRepeatedNgram(tokens []string, n, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	for _, c := range ngrams(tokens, n) {
		if c >= threshold {
			return true
		}
	}
	return false
}

// detectConsecutiveRepeat finds a short block repeated threshold times with
// enough distinct tokens to avoid trivial false positives.
func detectConsecutiveRepeat(tokens []string, minBlockSize, threshold, minDistinct int) bool {
	if minBlockSize <= 0 || threshold <= 1 || len(tokens) < minBlockSize*threshold {
		return false
	}
	for size := minBlockSize; size <= minBlockSize*4 && size*threshold <= len(tokens); size++ {
		for start := 0; start+size*threshold <= len(tokens); start++ {
			block := tokens[start : start+size]
			distinct := make(map[string]struct{}, size)
			for _, t := range block {
				distinct[t] = struct{}{}
			}
			if len(distinct) < minDistinct {
				continue
			}
			ok := true
			for rep := 1; rep < threshold; rep++ {
				for i := 0; i < size; i++ {
					if tokens[start+rep*size+i] != block[i] {
						ok = false
						break
					}
				}
				if !ok {
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

// TextRepeatMonitor accumulates streamed text and flags a loop once.
type TextRepeatMonitor struct {
	n, threshold           int
	minBlock, consecThresh int
	minDistinct            int
	buf                    strings.Builder
	lastCheckLen           int
	fired                  bool
}

// NewTextRepeatMonitor builds a monitor with the fork defaults.
func NewTextRepeatMonitor() *TextRepeatMonitor {
	return NewTextRepeatMonitorWith(DefaultTextRepeatN, DefaultTextRepeatThreshold)
}

// NewTextRepeatMonitorWith builds a monitor with per-run overrides (task 110).
// A non-positive n keeps the default; a negative threshold disables detection
// entirely so a config can turn the guard off for prose-heavy work.
func NewTextRepeatMonitorWith(n, threshold int) *TextRepeatMonitor {
	if n <= 0 {
		n = DefaultTextRepeatN
	}
	if threshold == 0 {
		threshold = DefaultTextRepeatThreshold
	}
	minBlock, consecThresh := DefaultTextConsecutiveMinBlock, DefaultTextConsecutiveThreshold
	if threshold < 0 {
		minBlock, consecThresh = 0, 0
	} else {
		// Raise the periodic detector in step with the n-gram threshold: a
		// caller who raises the limit for prose-heavy work means "fewer false
		// positives", and leaving the periodic limit fixed would keep firing on
		// exactly the repeated-block shapes they raised it for.
		scale := threshold / DefaultTextRepeatThreshold
		if scale < 1 {
			scale = 1
		}
		consecThresh = DefaultTextConsecutiveThreshold * scale
	}
	return &TextRepeatMonitor{
		n:            n,
		threshold:    threshold,
		minBlock:     minBlock,
		consecThresh: consecThresh,
		minDistinct:  textRepeatMinDistinct,
	}
}

// Append folds a stream delta and reports whether this chunk tripped the loop.
// Detection only runs on a growing buffer and never crosses a tool boundary
// (the caller must construct a fresh monitor per stream attempt).
func (m *TextRepeatMonitor) Append(delta string) bool {
	if m == nil || m.fired || delta == "" {
		return false
	}
	m.buf.WriteString(delta)
	// Re-check at most every 64 runes of new text to keep long streams cheap.
	if m.buf.Len()-m.lastCheckLen < 64 && m.buf.Len() < m.n*4 {
		return false
	}
	m.lastCheckLen = m.buf.Len()
	s := m.buf.String()
	// Only the recent tail participates: a long document that legitimately
	// repeats a phrase earlier must not keep tripping forever.
	const tailCap = 2048
	if len(s) > tailCap {
		s = s[len(s)-tailCap:]
	}
	tokens := tokenizeForNgram(s)
	if detectRepeatedNgram(tokens, m.n, m.threshold) ||
		detectConsecutiveRepeat(tokens, m.minBlock, m.consecThresh, m.minDistinct) {
		m.fired = true
		return true
	}
	return false
}

// Fired reports whether a loop was already detected this stream.
func (m *TextRepeatMonitor) Fired() bool {
	return m != nil && m.fired
}

// isMostlySpace is a tiny helper kept for tests that build pathological input.
func isMostlySpace(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			n++
		}
	}
	return len([]rune(s)) > 0 && n*2 > len([]rune(s))
}
