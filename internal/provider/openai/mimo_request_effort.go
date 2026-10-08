package openai

// Request-scoped effort vocabularies (task 354 MiMo, task 601 GLM).

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

// zhipuRequestEffortVocabulary is the canonical output domain of
// NormalizeEffort for a Zhipu GLM entry — internal/config's normalizeGLMEffort
// emits exactly these five values ("auto" folds to empty before any switch
// reaches SetSessionEffortOverride). A stock GLM entry declares no
// supported_efforts, so without this set the probe came up empty and every
// /effort switch declined as level-not-in-vocabulary and paid a full runtime
// rebuild (task 601: observed 5.3s per switch on the GLM coding plan). Keep
// in sync with internal/config.normalizeGLMEffort.
var zhipuRequestEffortVocabulary = []string{"disabled", "low", "medium", "high", "max"}

// requestEffortVocabularyFor picks the per-request EffortOverride vocabulary
// for a client: MiMo gets its canonical four-level set when the entry declares
// no supported_efforts of its own — the canonical set is exactly what
// internal/config's normalizeMimoEffort emits and what the resolved capability
// lists (task 606), so the probe face, the Stream gate, and the boot gate all
// agree; an explicit supported_efforts list stays the probe face for MiMo too
// (task 606 alignment with the GLM branch): DeclaredReasoning replaces the
// capability with that same list, so probe and gates keep a single face
// instead of the probe admitting a level the next request rejects; GLM gets
// its canonical five-level set under the same condition — an explicit list
// stays the probe face because internal/config's NormalizeEffort emits from it
// verbatim; every other endpoint keeps the configured/derived vocabulary
// unchanged.
func requestEffortVocabularyFor(baseURL string, configured []string) []string {
	if IsMiMo(baseURL) && !hasExplicitSupportedEfforts(configured) {
		return mimoRequestEffortVocabulary
	}
	if IsZhipu(baseURL) && !hasExplicitSupportedEfforts(configured) {
		return zhipuRequestEffortVocabulary
	}
	return configured
}
