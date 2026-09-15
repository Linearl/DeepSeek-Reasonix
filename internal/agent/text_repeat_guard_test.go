package agent

import (
	"strings"
	"testing"
)

func TestTokenizeForNgramCJK(t *testing.T) {
	tokens := tokenizeForNgram("你好世界hello")
	if len(tokens) < 5 {
		t.Fatalf("CJK should split per rune, got %v", tokens)
	}
	joined := strings.Join(tokens, "|")
	if !strings.Contains(joined, "你") || !strings.Contains(joined, "hello") {
		t.Fatalf("tokens = %v", tokens)
	}
}

func TestDetectRepeatedNgramEnglish(t *testing.T) {
	// One phrase repeated many times must trip; a varied sentence must not.
	loop := strings.Repeat("the quick brown fox jumps over the lazy dog and then ", 40)
	tokens := tokenizeForNgram(loop)
	if !detectRepeatedNgram(tokens, 4, DefaultTextRepeatThreshold) {
		t.Fatal("repeated English phrase should trip n-gram detector")
	}
	diverse := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega a b c d e f g h i j k l m n o p"
	if detectRepeatedNgram(tokenizeForNgram(diverse), 4, DefaultTextRepeatThreshold) {
		t.Fatal("diverse text must not trip")
	}
}

func TestDetectConsecutiveRepeatWithDistinct(t *testing.T) {
	block := "alpha beta gamma delta "
	tokens := tokenizeForNgram(strings.Repeat(block, 6))
	if !detectConsecutiveRepeat(tokens, 4, 4, textRepeatMinDistinct) {
		t.Fatal("periodic block should trip consecutive detector")
	}
	// Trivial single-token block is ignored via minDistinct.
	trivial := strings.Repeat("ok ", 80)
	if detectConsecutiveRepeat(tokenizeForNgram(trivial), 4, 4, textRepeatMinDistinct) {
		t.Fatal("single-token repeat must not trip (minDistinct)")
	}
}

func TestTextRepeatMonitorAppend(t *testing.T) {
	m := NewTextRepeatMonitor()
	payload := strings.Repeat("same sentence over and over again and again ", 40)
	fired := false
	// Feed in small deltas like a stream.
	for i := 0; i < len(payload); i += 16 {
		end := i + 16
		if end > len(payload) {
			end = len(payload)
		}
		if m.Append(payload[i:end]) {
			fired = true
			break
		}
	}
	if !fired || !m.Fired() {
		t.Fatal("monitor should fire on a long repeated payload")
	}
	// After firing, further appends are no-ops.
	if m.Append(" more") {
		t.Fatal("monitor must not re-fire")
	}
}

func TestTextRepeatMonitorNormalProse(t *testing.T) {
	m := NewTextRepeatMonitor()
	// Long but varied technical prose should not fire. Each sentence starts
	// with a unique token so common 4-grams do not accumulate to the threshold.
	digits := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"u", "v", "w", "x", "y", "z", "A", "B", "C", "D"}
	var para strings.Builder
	for i := 0; i < 40; i++ {
		para.WriteString(digits[i])
		para.WriteString(" inspect call path and record receipt before the host confirms mutation landed for this step of the work. ")
	}
	s := para.String()
	for i := 0; i < len(s); i += 32 {
		end := i + 32
		if end > len(s) {
			end = len(s)
		}
		if m.Append(s[i:end]) {
			t.Fatalf("varied prose fired at offset %d", i)
		}
	}
}
