package openai

// MiMo request-scoped effort vocabulary (task 354).

// mimoRequestEffortVocabulary is the canonical output domain of
// NormalizeEffort for a MiMo entry — internal/config's normalizeMimoEffort
// folds the vendor's 8-level input scale (minimal→low, xhigh/max/ultra→high,
// none/disabled/off→none) down to these four values before any switch
// reaches SetSessionEffortOverride. The client-side probe must accept exactly
// what that normalizer can emit, otherwise every medium/high switch is
// declined as level-not-in-vocabulary and falls back to a full rebuild
// (1855 installed log: 25-26s per switch). Keep in sync with
// internal/config.normalizeMimoEffort.
var mimoRequestEffortVocabulary = []string{"none", "low", "medium", "high"}

// requestEffortVocabularyFor picks the per-request EffortOverride vocabulary
// for a client: MiMo gets its canonical four-level set regardless of
// configured supported_efforts (the vendor scale is folded upstream, and the
// config-layer capability list is the input face, not this probe's face);
// every other endpoint keeps the configured/derived vocabulary unchanged.
func requestEffortVocabularyFor(baseURL string, configured []string) []string {
	if IsMiMo(baseURL) {
		return mimoRequestEffortVocabulary
	}
	return configured
}
