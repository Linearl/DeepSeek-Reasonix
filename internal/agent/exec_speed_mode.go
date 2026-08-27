package agent

import (
	"strings"
)

// execSpeedModeTag is the transient user-turn tag marking a high-throughput
// model. It mirrors the response-language/reasoning-language transient blocks:
// injected per turn (so the model sees a fresh directive every round), stripped
// from the user-facing preview, and excluded from cache-prefix shape.
const execSpeedModeTag = "exec-speed-mode"

// isHighSpeedConfigured reports whether modelRef (the running
// "provider/model" identity) matches one of the user-maintained high-speed
// models of its provider. High-speed is an explicit user choice (the Model
// panel checkbox), never inferred from the model id: a "flash" suffix does not
// imply high TPS, and an unmarked low-TPS model must stay on the sync path.
// Matching accepts the plain model name as well as a provider-prefixed ref.
func isHighSpeedConfigured(modelRef string, highSpeedModels []string) bool {
	ref := strings.TrimSpace(modelRef)
	if ref == "" || len(highSpeedModels) == 0 {
		return false
	}
	// Ref forms: "model", "provider/model", or a canonical "provider|model" —
	// accept equality on the trailing model segment.
	last := ref
	if i := strings.LastIndexAny(ref, "/|"); i >= 0 && i+1 < len(ref) {
		last = ref[i+1:]
	}
	for _, m := range highSpeedModels {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if ref == m || last == m {
			return true
		}
	}
	return false
}

// ExecSpeedModeBlock returns a self-contained "<exec-speed-mode>high</exec-speed-mode>"
// guidance block, or "" when modelRef is not marked high-speed. The block
// carries the full execution-strategy directive inline so the model sees the
// policy every round without needing a static system-prompt section.
func ExecSpeedModeBlock(modelRef string, highSpeedModels []string) string {
	if !isHighSpeedConfigured(modelRef, highSpeedModels) {
		return ""
	}
	return "<" + execSpeedModeTag + ">high</" + execSpeedModeTag + ">\n" +
		"High-throughput model: tool-call latency now dominates round time, so waiting dismisses most of the speed gain. Adjust execution strategy:\n" +
		"- Merge independent operations into the same round (parallel tool calls).\n" +
		"- Dispatch slow operations (tests/build/network) with run_in_background=true instead of waiting synchronously.\n" +
		"- After dispatching, immediately advance independent work; poll the result via bash_output/wait only when you need it.\n" +
		"- Re-plan more often: fine-grained planning is cheap at high TPS."
}

// WithExecSpeedMode prefixes content with the transient high-speed-model block
// when the running model has been marked high-speed, unless the turn already
// starts with an injected exec-speed-mode block (host reseed must not
// double-inject).
func WithExecSpeedMode(content, modelRef string, highSpeedModels []string) string {
	block := ExecSpeedModeBlock(modelRef, highSpeedModels)
	if block == "" || hasLeadingInjectedBlock(content, execSpeedModeTag) {
		return content
	}
	return block + "\n\n" + content
}
