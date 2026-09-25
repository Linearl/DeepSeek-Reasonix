package agent

import (
	"context"
	"strings"
)

// Task 242: the fallback target rides the turn context (bound once per turn
// with the recovery-fence bindings so every entry carries it), resolved live
// from config — a settings change applies to newly arriving turns without a
// restart, mirroring CascadeApprovalLive's read-per-call pattern. Empty means
// the experimental switch is off or unconfigured: quota errors keep their
// pre-242 behavior exactly (default-off zero regression).
type modelFallbackTargetKey struct{}

// WithModelFallback stamps the resolved "provider/model" fallback target
// (empty = feature off) onto a turn context.
func WithModelFallback(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, modelFallbackTargetKey{}, strings.TrimSpace(target))
}

// ModelFallbackTarget returns the fallback target stamped by WithModelFallback.
func ModelFallbackTarget(ctx context.Context) string {
	target, _ := ctx.Value(modelFallbackTargetKey{}).(string)
	return target
}
