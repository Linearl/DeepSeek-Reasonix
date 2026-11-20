package agent

import (
	"slices"

	"reasonix/internal/provider"
)

// activeTurnKeepRounds is how many of the active turn's newest assistant
// rounds stay verbatim when an overflow forces a fold inside the turn.
const activeTurnKeepRounds = 2

// activeTurnFoldBoundary returns where an overflow rescue may end its fold
// inside the active turn: after the prompt and every completed round except
// the newest keep rounds, on a replay-safe unit boundary. A turn with too few
// rounds to split returns active, keeping the whole turn verbatim.
func activeTurnFoldBoundary(msgs []provider.Message, active, end int) int {
	if active < 0 || end <= active+1 || end > len(msgs) {
		return active
	}
	body := msgs[active+1 : end]
	keep := activeTurnKeepRounds
	for _, unit := range slices.Backward(extractMessageUnits(body)) {
		if body[unit.lo].Role != provider.RoleAssistant {
			continue
		}
		keep--
		if keep < 0 {
			return active + 1 + unit.hi
		}
	}
	return active
}

// activeTurnFullSplitBoundary returns the tl1713fix full intra-turn fold
// boundary for the pressure lane. The split domain is the WHOLE active turn —
// not a budget-truncated tail of it — so the assistant-round count is never
// clamped by the outer boundary: a long turn whose budget tail holds too few
// rounds to split used to fall back to "keep the whole turn", which clamped
// the fold to the active turn and left the prompt verbatim behind it. The
// newest activeTurnKeepRounds assistant rounds stay verbatim; every older
// round folds together with the prompt. Returns active when the turn carries
// no assistant unit at all (no unit edge to anchor a boundary on); the caller
// then keeps its outer planning boundary instead of clamping to active.
func activeTurnFullSplitBoundary(msgs []provider.Message, active int) int {
	if active < 0 || active+1 >= len(msgs) {
		return active
	}
	body := msgs[active+1:]
	seenAssistant := 0
	oldestAssistantLo := -1
	for _, unit := range slices.Backward(extractMessageUnits(body)) {
		if body[unit.lo].Role != provider.RoleAssistant {
			continue
		}
		oldestAssistantLo = unit.lo
		seenAssistant++
		if seenAssistant > activeTurnKeepRounds {
			// The (keep+1)-th newest assistant round and everything older
			// folds; the newest keep rounds stay verbatim.
			return active + 1 + unit.hi
		}
	}
	if oldestAssistantLo >= 0 {
		// Fewer assistant rounds than keep: there is no older round to fold,
		// so the boundary pins at the OLDEST assistant round's start — the
		// prompt (and anything earlier inside the turn) folds, every assistant
		// round stays verbatim with its tool group and acks.
		return active + 1 + oldestAssistantLo
	}
	return active
}
