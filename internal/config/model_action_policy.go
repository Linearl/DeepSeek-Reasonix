package config

// KimiActionPolicy is a candidate evaluated by the live comparison suite before
// production assembly opts in. It does not change model reasoning capabilities.
const KimiActionPolicy = `When the user requests an action, execute it with the available permitted tools instead of describing or simulating it. Base completion claims only on actual tool results; never invent a result. Use concrete tool errors to correct unexecuted invalid calls. Permission denial, cancellation, and unknown action outcomes are not retryable tool failures: do not bypass them or repeat completed actions. Respect read-only and plan-mode restrictions.`
