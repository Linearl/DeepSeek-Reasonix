package agent

import (
	"strings"
)

// execSpeedModeTag is the transient user-turn tag marking a high-throughput
// model. It mirrors the response-language/reasoning-language transient blocks:
// injected per turn (so the model sees a fresh directive every round), stripped
// from the user-facing preview, and excluded from cache-prefix shape.
const execSpeedModeTag = "exec-speed-mode"

// highSpeedModelSuffixes are model-id markers that identify high-throughput
// models (high TPS / low per-token latency). When such a model is active, tool
// call latency dominates round time: the model generates a batch instantly but
// then blocks on slow tools (tests/build/network). Matching is conservative so
// a normal model is never mislabeled.
var highSpeedModelSuffixes = []string{
	"ultraspeed",
	"ultra-speed",
	"ultra_speed",
	"highspeed",
	"high-speed",
	"high_speed",
	"flash",
}

// isHighSpeedModel reports whether modelRef names a high-throughput model. The
// match is substring-based and lowercase, so provider-qualified IDs and the
// dash/underscore variants are all caught; an empty ref is never high-speed.
func isHighSpeedModel(modelRef string) bool {
	ref := strings.ToLower(strings.TrimSpace(modelRef))
	if ref == "" {
		return false
	}
	for _, s := range highSpeedModelSuffixes {
		if strings.Contains(ref, s) {
			return true
		}
	}
	return false
}

// ExecSpeedModeBlock returns a self-contained "<exec-speed-mode>high</exec-speed-mode>"
// guidance block for a high-throughput model, or "" for a normal model. The
// block carries the full execution-strategy directive inline so the model sees
// the policy every round without needing a static system-prompt section.
func ExecSpeedModeBlock(modelRef string) string {
	if !isHighSpeedModel(modelRef) {
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
// when the active model is high-throughput, unless the turn already starts with
// an injected exec-speed-mode block (host reseed must not double-inject).
func WithExecSpeedMode(content, modelRef string) string {
	block := ExecSpeedModeBlock(modelRef)
	if block == "" || hasLeadingInjectedBlock(content, execSpeedModeTag) {
		return content
	}
	return block + "\n\n" + content
}
