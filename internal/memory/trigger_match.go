// tl1349③: the trigger lane — a fact declaring which words should summon it.
//
// Why a second lane: BM25 can only find a fact through words its own text
// contains. A fact written in English ("proxy") is invisible to a Chinese
// query ("代理端口") and vice versa, even when a human would call them the same
// thing. The keywords field tries to paper over this by being folded into the
// BM25 text, which means a keyword only helps if the query happens to share a
// token with it — the same lexical wall, one step removed.
//
// Triggers solve it differently: the fact declares phrases, and a phrase fires
// when every one of its tokens appears in the query. That is a membership test
// on declared wording, not a scoring coincidence, so it reaches facts whose
// body never uses the query's words.
//
// Adoption shape (openclaw's lane-1 trigger recall, with one deliberate
// correction): ANY fully-matched phrase fires the lane. openclaw's
// scoreTriggerPhrase blends coverage-of-declared-phrases with query overlap,
// which punishes a fact for declaring many aliases — the more synonyms you
// list, the lower every score gets. That is backwards for a field whose whole
// point is listing aliases, so phrases here are independent triggers and only
// their length gates adoption: a phrase too short to be specific is not
// evidence.
package memory

import (
	"strings"

	"reasonix/internal/retrieval"
)

// triggerMinTokens is the floor below which a matched phrase is too generic to
// adopt: a single common bigram would fire on noise.
const triggerMinTokens = 2

// triggerPhrases splits a triggers field into phrases. Spaces, commas, and
// semicolons all separate, so "proxy 代理端口", "proxy,代理端口", and
// "proxy;代理端口" are equivalent.
func triggerPhrases(field string) []string {
	parts := strings.FieldsFunc(field, func(r rune) bool {
		switch r {
		case ',', ';', '，', '；', '、', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// triggerHit reports whether any declared phrase is fully contained in the
// query. A phrase must carry at least triggerMinTokens tokens, and at least as
// many tokens as the query itself when the query is short — otherwise a single
// bigram alias would fire on half the turns.
//
// A fact without triggers never fires, which keeps every pre-existing memory
// byte-for-byte unchanged (acceptance 5).
func triggerHit(field string, queryTerms []string) bool {
	phrases := triggerPhrases(field)
	if len(phrases) == 0 || len(queryTerms) == 0 {
		return false
	}
	query := make(map[string]bool, len(queryTerms))
	for _, term := range queryTerms {
		query[term] = true
	}
	need := triggerMinTokens
	if len(queryTerms) < need {
		need = len(queryTerms)
	}
	for _, phrase := range phrases {
		terms := retrieval.Tokens(phrase)
		if len(terms) < need {
			continue
		}
		all := true
		for _, term := range terms {
			if !query[term] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// triggerMatchScore ranks how specific a firing trigger is: the fraction of the
// query covered by the longest fully-matched phrase (1.0 = that phrase spans
// the whole query). It orders trigger hits against each other; adoption is
// triggerHit's decision, not a threshold on this number.
func triggerMatchScore(field string, queryTerms []string) float64 {
	phrases := triggerPhrases(field)
	if len(phrases) == 0 || len(queryTerms) == 0 {
		return 0
	}
	query := make(map[string]bool, len(queryTerms))
	for _, term := range queryTerms {
		query[term] = true
	}
	best := 0.0
	for _, phrase := range phrases {
		terms := retrieval.Tokens(phrase)
		if len(terms) == 0 {
			continue
		}
		all := true
		for _, term := range terms {
			if !query[term] {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		if coverage := float64(len(terms)) / float64(len(query)); coverage > best {
			best = coverage
		}
	}
	if best > 1 {
		best = 1
	}
	return best
}
