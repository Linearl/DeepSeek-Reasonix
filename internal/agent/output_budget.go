package agent

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
)

const (
	outputBudgetReserve    = 8 * 1024
	minOutputBudgetReserve = protocolReserveTokens
)

const learnedOutputBudgetTTL = 24 * time.Hour

type learnedOutputBudgetCacheEntry struct {
	completionBudget int
	expiresAt        time.Time
}

var learnedOutputBudgetCache = struct {
	sync.Mutex
	entries map[string]learnedOutputBudgetCacheEntry
}{entries: make(map[string]learnedOutputBudgetCacheEntry)}

// tl 1190 L2: provider-level prompt-estimation correction. Every trusted
// ContextLimitError carries the provider's exact prompt count; the ratio
// real/local-estimate is folded into an EWMA keyed by the provider instance
// name and applied to later COLD estimates on that provider (see
// estimatedShapeTokens — a session-calibrated estimate already carries the
// provider's own measurement and must not stack the factor). Keying IS the
// reset semantics the task asks for: switching provider (or model route) reads
// a different key — no factor bleeds across. The factor only grows on fresh
// provider evidence (ratio > 1 updates; a success re-measures the estimate
// WITH the factor already inside it, so it carries no unbiased signal and
// must not decay it) and is clamped to [1, 4] so one corrupt reply cannot
// price a session out of its window. In-memory, 24h TTL — same lifetime
// contract as the learned output budget above.
const (
	promptCorrectionEWMAAlpha = 0.5
	promptCorrectionFactorMin = 1.0
	promptCorrectionFactorMax = 4.0
	promptCorrectionTTL       = 24 * time.Hour
)

type promptCorrectionEntry struct {
	factor    float64
	expiresAt time.Time
}

var promptCorrectionCache = struct {
	sync.Mutex
	entries map[string]promptCorrectionEntry
}{entries: make(map[string]promptCorrectionEntry)}

// noteProviderPromptCorrection records one trusted overflow observation:
// real is the provider-reported prompt count, estimated our local estimate of
// the same request. Ratios at or below 1 are ignored — the estimator did not
// undershoot, so there is nothing to correct.
func noteProviderPromptCorrection(providerName string, real, estimated int) {
	if strings.TrimSpace(providerName) == "" || real <= 0 || estimated <= 0 {
		return
	}
	ratio := float64(real) / float64(estimated)
	if ratio <= promptCorrectionFactorMin {
		return
	}
	now := time.Now()
	promptCorrectionCache.Lock()
	defer promptCorrectionCache.Unlock()
	factor := ratio
	if cur, ok := promptCorrectionCache.entries[providerName]; ok && cur.expiresAt.After(now) && cur.factor > 0 {
		factor = cur.factor*(1-promptCorrectionEWMAAlpha) + ratio*promptCorrectionEWMAAlpha
	}
	promptCorrectionCache.entries[providerName] = promptCorrectionEntry{
		factor:    min(max(factor, promptCorrectionFactorMin), promptCorrectionFactorMax),
		expiresAt: now.Add(promptCorrectionTTL),
	}
}

func promptCorrectionFactor(providerName string) float64 {
	if strings.TrimSpace(providerName) == "" {
		return 1
	}
	now := time.Now()
	promptCorrectionCache.Lock()
	defer promptCorrectionCache.Unlock()
	cur, ok := promptCorrectionCache.entries[providerName]
	if !ok {
		return 1
	}
	if !cur.expiresAt.After(now) {
		delete(promptCorrectionCache.entries, providerName)
		return 1
	}
	return cur.factor
}

// requestProviderName names the destination serving the next request — the
// L2 correction cache key. Follows providerForRequest so a hot model switch
// re-keys the factor exactly when the wire destination changes.
func (a *Agent) requestProviderName() string {
	if a == nil {
		return ""
	}
	dest := a.providerForRequest()
	if nilutil.IsNil(dest) {
		return ""
	}
	return strings.TrimSpace(dest.Name())
}

type outputBudgetState struct {
	outputBudget int
	// lastUsage caches the latest provider telemetry for per-turn readouts.
	// The run loop writes it while a frontend reads it, so it is atomic.
	lastUsage         atomic.Pointer[provider.Usage]
	activeReqShape    atomic.Pointer[requestCalibrationShape]
	promptCalibration atomic.Pointer[promptTokenCalibration]
	contextUsage      atomic.Pointer[contextUsage] // gauge's memoised prompt size
	learned           atomic.Pointer[learnedContextBudget]
	admission         atomic.Pointer[contextAdmission]
}

// learnedContextBudget is an Agent-local observation of the live provider/model
// window. Completion limits are additionally shared through the short-lived
// provider/model cache below so a model rebuild does not immediately repeat a
// known over-limit request. The cache is deliberately in-memory and expires
// after one day; it is not a persisted global provider limit.
type learnedContextBudget struct {
	windowTokens     int
	completionBudget int
}

const (
	contextRecoveryNone          = "none"
	contextRecoveryProactiveClip = "proactive_clip"
	contextRecoveryLearnedRetry  = "learned_retry"
	contextRecoveryCompacted     = "compacted"
	contextRecoveryFailed        = "failed"
)

type contextAdmission struct {
	WindowMode            string
	LimitMode             string
	Source                string
	WindowTokens          int
	PromptTokens          int
	AutoOutputTokens      int
	MaxOutputTokens       int
	RequestedOutputTokens int
	EffectiveOutputTokens int
	ReserveTokens         int
	PhysicalRemaining     int
	Clipped               bool
	ApplyMaxTokens        bool
	LastRecovery          string
	ObservedWindow        int
	ObservedPrompt        int
	ObservedCompletion    int
}

type promptTokenCalibration struct {
	promptTokens int
	// imageTokens is the local image price carried by the calibrated request
	// (任务 1025). Provider promptTokens bill images too, so the text ratio
	// subtracts them: pairing all-in usage against image-free requestChars
	// would inflate the per-char ratio and over-price later text turns.
	imageTokens  int
	requestChars int64
	compactChars int64
	cjkRunes     int64
	cjkBytes     int64
}

// requestCalibrationShape pairs the conservative provider-visible text and CJK
// composition used for overflow protection with the legacy content-only shape
// used by fold economics. Images are priced separately in imageTokens (任务
// 1025: by resolution, like the provider's vision tokenizer — see
// imageEstimateTokens), so the per-character calibration ratio stays a pure
// text measurement. Keeping them in one immutable pointer ensures readers
// never combine calibration fields from different prepared requests.
type requestCalibrationShape struct {
	requestChars int64
	imageTokens  int
	compactChars int64
	cjkRunes     int64
	cjkBytes     int64
}

// reset drops what belongs to the transcript being replaced. Prompt-token
// calibration and the learned window are properties of the bound model/provider,
// and a model switch rebuilds the Agent, so they outlive SetSession. Admission
// describes one transcript's latest request and must never bleed into the next.
func (o *outputBudgetState) reset() {
	o.lastUsage.Store(nil)
	o.activeReqShape.Store(nil)
	o.admission.Store(nil)
}

func (a *Agent) setPromptTokenCalibration(promptTokens int, shape requestCalibrationShape) {
	if a == nil || promptTokens <= 0 || shape.requestChars <= 0 {
		return
	}
	a.sess.output.promptCalibration.Store(&promptTokenCalibration{
		promptTokens: promptTokens,
		imageTokens:  shape.imageTokens,
		requestChars: shape.requestChars,
		compactChars: shape.compactChars,
		cjkRunes:     shape.cjkRunes,
		cjkBytes:     shape.cjkBytes,
	})
}

func (a *Agent) setPromptTokenCalibrationFromActive(promptTokens int) {
	if a == nil {
		return
	}
	if shape := a.sess.output.activeReqShape.Load(); shape != nil {
		a.setPromptTokenCalibration(promptTokens, *shape)
	}
}

// setPromptTokenCalibrationFromUsage trusts provider telemetry only;
// reconstructed usage remains available for accounting but not admission.
func (a *Agent) setPromptTokenCalibrationFromUsage(usage *provider.Usage) {
	if a == nil || usage == nil || usage.Estimated {
		return
	}
	a.setPromptTokenCalibrationFromActive(usage.LatestPromptTokens())
}

func outputBudgetOf(p provider.Provider) int {
	if nilutil.IsNil(p) {
		return 0
	}
	if budget, ok := p.(provider.OutputBudgetProvider); ok {
		return budget.OutputBudget()
	}
	return 0
}

func sharesContextWindow(p provider.Provider) bool {
	return contextBudgetPolicyOf(p).WindowMode == provider.ContextWindowShared
}

func contextBudgetPolicyOf(p provider.Provider) provider.ContextBudgetPolicy {
	if nilutil.IsNil(p) {
		return provider.ContextBudgetPolicy{}
	}
	return provider.ResolveContextBudgetPolicy(p)
}

func (a *Agent) learnOutputBudget(limit int) {
	if a == nil || limit <= 0 {
		return
	}
	cacheLearnedOutputBudget(outputBudgetCacheKey(a), limit)
	current := a.sess.output.learned.Load()
	if current != nil && current.completionBudget > 0 && current.completionBudget <= limit {
		return
	}
	learned := &learnedContextBudget{completionBudget: limit}
	if current != nil {
		learned.windowTokens = current.windowTokens
	}
	a.sess.output.learned.Store(learned)
}

func outputBudgetCacheKey(a *Agent) string {
	if a == nil {
		return ""
	}
	// Task 602: the cache key follows the request destination, so a hot model
	// switch never reuses the previous provider's learned output budget.
	dest := a.providerForRequest()
	providerName := ""
	if !nilutil.IsNil(dest) {
		providerName = strings.TrimSpace(dest.Name())
	}
	modelRef := strings.TrimSpace(a.destinationModelRef())
	if providerName == "" && modelRef == "" {
		return ""
	}
	// Provider names are route-specific for the built-in OpenCode Go entries;
	// retaining modelRef as a second component keeps custom routes isolated too.
	return providerName + "|" + modelRef
}

func cacheLearnedOutputBudget(key string, limit int) {
	if strings.TrimSpace(key) == "" || limit <= 0 {
		return
	}
	now := time.Now()
	learnedOutputBudgetCache.Lock()
	defer learnedOutputBudgetCache.Unlock()
	if current, ok := learnedOutputBudgetCache.entries[key]; ok && current.expiresAt.After(now) && current.completionBudget > 0 && current.completionBudget <= limit {
		return
	}
	learnedOutputBudgetCache.entries[key] = learnedOutputBudgetCacheEntry{
		completionBudget: limit,
		expiresAt:        now.Add(learnedOutputBudgetTTL),
	}
}

func cachedLearnedOutputBudget(key string) int {
	if strings.TrimSpace(key) == "" {
		return 0
	}
	now := time.Now()
	learnedOutputBudgetCache.Lock()
	defer learnedOutputBudgetCache.Unlock()
	entry, ok := learnedOutputBudgetCache.entries[key]
	if !ok {
		return 0
	}
	if !entry.expiresAt.After(now) {
		delete(learnedOutputBudgetCache.entries, key)
		return 0
	}
	return entry.completionBudget
}

func sharedWindowInputPolicyOf(p provider.Provider) provider.SharedWindowInputPolicy {
	if nilutil.IsNil(p) {
		return provider.SharedWindowInputPolicy{}
	}
	policy, ok := p.(provider.SharedWindowInputPolicyProvider)
	if !ok {
		return provider.SharedWindowInputPolicy{}
	}
	return policy.SharedWindowInputPolicy()
}

func requestCalibrationShapeOf(req provider.Request) requestCalibrationShape {
	return requestCalibrationShapeWithPolicy(req, provider.SharedWindowInputPolicy{})
}

func (a *Agent) requestCalibrationShape(req provider.Request) requestCalibrationShape {
	return requestCalibrationShapeWithPolicy(req, sharedWindowInputPolicyOf(a.providerForRequest()))
}

func requestCalibrationShapeWithPolicy(req provider.Request, policy provider.SharedWindowInputPolicy) requestCalibrationShape {
	requestChars, cjkRunes, cjkBytes := requestCalibrationTextShape(req, policy)
	// 任务 1025: images price by resolution (imageEstimateTokens), not as base64
	// characters — the text shape stays image-free because the chunk-budget
	// plane (messageWireBytes) is built on it and counts full image bytes
	// itself, and the char calibration ratio must stay a pure text measure.
	return requestCalibrationShape{
		requestChars: requestChars,
		imageTokens:  imageEstimateTokensOf(req),
		compactChars: int64(charsOfMessages(req.Messages)),
		cjkRunes:     cjkRunes,
		cjkBytes:     cjkBytes,
	}
}

// imageEstimateTokensOf prices every provider-visible image of a request at
// its resolution-derived token cost (任务 1025). Unreadable headers fall back
// to the 任务 1010 flat cap so an unscannable image never turns invisible.
func imageEstimateTokensOf(req provider.Request) int {
	n := 0
	for _, msg := range req.Messages {
		if msg.LocalOnly {
			continue
		}
		for _, img := range msg.Images {
			n += imageEstimateTokens(img)
		}
	}
	return n
}

// requestImageEstimateByteCap is the flat per-image budget for images whose
// dimensions cannot be sniffed (任务 1010, refined by 任务 1025: readable data
// URLs now price by resolution via imageEstimateTokens; this cap remains the
// conservative floor for HTTP URLs / file ids / unreadable headers). On the
// wire an image is a data URL or a structured vision part priced by tiles
// (~1–2K tokens each), not by base64 length — but the ai-passport incident
// (2026-10-19) showed the failure mode of counting them at ZERO: hundreds of
// accumulated screenshots were invisible to every threshold, so the fold cap,
// admission and the truncation rescue all believed a 2.1M-token view fit a 1M
// window and the freeze loop never broke.
// The cap ≈ 2K tokens at the ~4-chars-per-token cold rate: real multi-image
// pressure shows up (compaction fires, the summary prefix caps), while a single
// 2 MB screenshot no longer estimates at ~500K tokens and over-folds healthy
// image sessions. Image bytes are base64 — no CJK accounting applies.
const requestImageEstimateByteCap = 8 * 1024

// requestImageFootprint reports the raw image surface of a request: how many
// images ride on it and their encoded byte total (uncapped). The admission
// error uses it to name the images an over-window request still carries, so
// the degradation names the repair instead of a bare token count (任务 1010
// 需求 1：超窗前自主降级，错误可操作).
func requestImageFootprint(req provider.Request) (count int, bytes int64) {
	for _, msg := range req.Messages {
		if msg.LocalOnly {
			continue
		}
		count += len(msg.Images)
		for _, img := range msg.Images {
			bytes += int64(len(img))
		}
	}
	return count, bytes
}

// requestCalibrationTextShape counts common shared-window text plus only the
// adapter-specific replay fields declared by the active provider. This keeps
// omitted bytes out of the ratio without missing newly appended wire content.
// Images are NOT counted here: the chunk-budget plane (messageWireBytes) is
// built on this function and adds full image bytes itself — the capped image
// pricing lives in the agent wire shape (requestCalibrationShapeWithPolicy).
func requestCalibrationTextShape(req provider.Request, policy provider.SharedWindowInputPolicy) (chars, cjkRunes, cjkBytes int64) {
	add := func(s string) {
		chars += int64(len(s))
		for _, r := range s {
			if isCJKRune(r) {
				cjkRunes++
				cjkBytes += int64(utf8.RuneLen(r))
			}
		}
	}
	for _, msg := range req.Messages {
		if msg.LocalOnly {
			continue
		}
		chars += 4
		add(string(msg.Role))
		add(msg.Content)
		if msg.Role == provider.RoleAssistant && (len(msg.ToolCalls) > 0 || policy.ReplaysOrdinaryReasoning) {
			add(msg.ReasoningContent)
		}
		add(msg.Name)
		add(msg.ToolCallID)
		for _, call := range msg.ToolCalls {
			chars += 8
			add(call.ID)
			add(call.Name)
			add(call.Arguments)
		}
		if policy.ReplaysResponsesItems {
			for _, item := range msg.ResponsesItems {
				add(string(item))
			}
		}
		for _, search := range msg.ServerSearch {
			provider.WalkServerSearchEstimate(search, add)
		}
	}
	for _, schema := range req.Tools {
		chars += 8
		add(schema.Name)
		add(schema.Description)
		add(string(schema.Parameters))
	}
	return chars, cjkRunes, cjkBytes
}

func (a *Agent) calibratedPromptTokens(shape requestCalibrationShape) (int, bool) {
	if shape.requestChars <= 0 {
		return 0, false
	}
	if cal := a.sess.output.promptCalibration.Load(); cal != nil && cal.requestChars > 0 {
		// Provider usage bills images; the ratio must stay a pure text measure
		// (任务 1025), so the calibrated request's local image price is removed
		// from the numerator. Over-subtracting our conservative image price
		// leaves the text ratio at or below the true text ratio — the estimate
		// stays conservative overall because images add their own price back.
		textTokens := cal.promptTokens - cal.imageTokens
		if textTokens <= 0 {
			return 0, false
		}
		ratio := float64(textTokens) / float64(cal.requestChars)
		if ratio > 0.05 && ratio < 2 {
			trustedChars := shape.requestChars
			excessCJKBytes := int64(0)
			// A higher CJK share cannot safely reuse the aggregate ratio. Scale its
			// represented share and price only the excess at the cold rate,
			// preserving exact calibration for stable CJK sessions.
			if shape.cjkRunes*cal.requestChars > cal.cjkRunes*shape.requestChars {
				trustedCJKBytes := min(cal.cjkBytes*shape.requestChars/cal.requestChars, shape.cjkBytes)
				excessCJKBytes = shape.cjkBytes - trustedCJKBytes
				trustedChars -= excessCJKBytes
			}
			cold := math.Ceil(float64(excessCJKBytes) * fallbackTokPerChar)
			return int(math.Ceil(float64(trustedChars)*ratio) + cold), true
		}
	}
	return 0, false
}

// estimatedPromptTokens sizes the provider-visible messages in real tokens —
// the only unit comparable against the context window. Same-session usage
// calibrates it; before that the wire character count carries the ~4 chars per
// token shape. estimateMessagesTokens counts characters and is for internal
// planning budgets only; against the window it would compact 4x early.
func (a *Agent) estimatedPromptTokens(msgs []provider.Message) int {
	return a.estimatedShapeTokens(a.requestCalibrationShape(provider.Request{Messages: msgs}))
}

func (a *Agent) estimatedRequestTokens(req provider.Request) int {
	return a.estimatedShapeTokens(a.requestCalibrationShape(req))
}

func (a *Agent) estimatedShapeTokens(shape requestCalibrationShape) int {
	if shape.requestChars <= 0 && shape.imageTokens <= 0 {
		return 0
	}
	// 任务 1025: text prices through the session's calibration ratio (or the
	// cold ~4-chars-per-token fallback) and images add their resolution-derived
	// token cost on top — one caliber for admission, the UI gauge and telemetry
	// projection (任务 988's reconciliation slice).
	text := 0
	if calibrated, ok := a.calibratedPromptTokens(shape); ok {
		text = calibrated
	} else {
		text = int(float64(shape.requestChars) * fallbackTokPerChar)
	}
	total := text + shape.imageTokens
	// tl 1190 L2: the provider-level EWMA correction from trusted overflows
	// rides on the COLD estimate only. The session calibration is measured on
	// this same provider's usage — stacking the factor on top of it would
	// double-count (a calibrated-to-real estimate ×4 priced a 1M view at 4.2M
	// in the incident replay). A calibrated estimate needs no provider-level
	// bias correction; a cold one does.
	if _, calibrated := a.calibratedPromptTokens(shape); !calibrated {
		if factor := promptCorrectionFactor(a.requestProviderName()); factor > 1 {
			total = int(math.Ceil(float64(total) * factor))
		}
	}
	return total
}

func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0xAC00 && r <= 0xD7AF)
}

func (a *Agent) effectiveContextWindow() int {
	if a == nil {
		return 0
	}
	// Task 602: the configured side follows the request destination, so a hot
	// model switch sizes against the destination's window; the learned side
	// keeps its min-composition (a stale learned window from the previous
	// provider can only shrink the estimate, never exceed the destination).
	cfg := a.destinationContextWindow()
	learned := 0
	if snap := a.sess.output.learned.Load(); snap != nil {
		learned = snap.windowTokens
	}
	switch {
	case cfg > 0 && learned > 0:
		return min(cfg, learned)
	case learned > 0:
		return learned
	default:
		return cfg
	}
}

func (a *Agent) learnedCompletionBudget() int {
	if a == nil {
		return 0
	}
	cached := cachedLearnedOutputBudget(outputBudgetCacheKey(a))
	if snap := a.sess.output.learned.Load(); snap != nil {
		if cached > 0 && (snap.completionBudget <= 0 || cached < snap.completionBudget) {
			a.learnOutputBudget(cached)
			return cached
		}
		return snap.completionBudget
	}
	if cached > 0 {
		a.learnOutputBudget(cached)
		return cached
	}
	return 0
}

func (a *Agent) learnContextBudget(window, completion int, omittedOutput bool) {
	if a == nil {
		return
	}
	cur := learnedContextBudget{}
	if prev := a.sess.output.learned.Load(); prev != nil {
		cur = *prev
	}
	if window > 0 {
		if cur.windowTokens <= 0 || window < cur.windowTokens {
			cur.windowTokens = window
		}
	}
	if omittedOutput && completion > 0 {
		cur.completionBudget = completion
	}
	next := cur
	a.sess.output.learned.Store(&next)
}

func (a *Agent) storeAdmission(adm contextAdmission) {
	if a == nil {
		return
	}
	cp := adm
	a.sess.output.admission.Store(&cp)
}

func (a *Agent) lastAdmission() contextAdmission {
	if a == nil {
		return contextAdmission{LastRecovery: contextRecoveryNone}
	}
	if snap := a.sess.output.admission.Load(); snap != nil {
		return *snap
	}
	return contextAdmission{LastRecovery: contextRecoveryNone}
}

func (a *Agent) setLastRecovery(kind string) {
	if a == nil {
		return
	}
	adm := a.lastAdmission()
	adm.LastRecovery = kind
	a.storeAdmission(adm)
}

func admissionSource(userMax int, policy provider.ContextBudgetPolicy, learnedWindow bool) string {
	if learnedWindow {
		return provider.ContextBudgetSourceLearned
	}
	if userMax > 0 {
		return provider.ContextBudgetSourceExplicit
	}
	switch {
	case policy.AutoOutputTokens == provider.DeepSeekMaxOutputTokens && policy.LimitMode == provider.OutputLimitOmitWhenSafe:
		return provider.ContextBudgetSourceOfficial
	case policy.LimitMode == provider.OutputLimitAlways && policy.MaxOutputTokens > 0:
		return provider.ContextBudgetSourceOpenCode
	case policy.WindowMode == provider.ContextWindowUnknown || policy.AutoOutputTokens <= 0:
		return provider.ContextBudgetSourceUnknown
	default:
		return provider.ContextBudgetSourceOfficial
	}
}

// effectiveOutputBudget clips completion tokens at send time only; it never
// moves compact_ratio. Calibrated exhausted windows fail locally; a cold
// estimate that differs from the provider tokenizer uses bounded 400 recovery.
func (a *Agent) effectiveOutputBudget(req provider.Request) (int, bool, error) {
	adm, err := a.admitOutputBudget(req)
	if err != nil {
		return 0, false, err
	}
	if !adm.ApplyMaxTokens || !adm.Clipped {
		if adm.ApplyMaxTokens && adm.EffectiveOutputTokens > 0 && !adm.Clipped {
			return adm.EffectiveOutputTokens, false, nil
		}
		return 0, false, nil
	}
	return adm.EffectiveOutputTokens, true, nil
}

func (a *Agent) admitOutputBudget(req provider.Request) (contextAdmission, error) {
	return a.admitOutputBudgetWithReserve(req, outputBudgetReserveForWindow(a.effectiveContextWindow()), false, true)
}

// outputBudgetReserveForWindow preserves the original safety ratio: the 8K
// tokenizer/protocol cushion was chosen for a 1M-token window, so smaller
// windows reserve roughly the same 1/128 share. A 256-token floor covers
// framing, while the 8K cap keeps larger windows byte-stable.
func outputBudgetReserveForWindow(window int) int {
	if window <= 0 {
		return outputBudgetReserve
	}
	return min(outputBudgetReserve, max(minOutputBudgetReserve, window/128))
}

// tl 1190 L0 (2026-10-27 deepseek incident): the estimator's residual bias is
// absorbed by a proportional margin instead of flowing 1:1 into the output
// cap. The incident's cold estimate undershot the provider's real prompt count
// by ~8.5% (679,724 local vs 737,203 real); admission clipped max_tokens to
// exactly window−est−reserve, so the whole estimation error landed on the
// wire and the request 400'd. The margin is max(est×10%, 8192): proportional
// so it scales with the request, floored at the protocol cushion so tiny
// requests stay capped by the reserve alone.
const contextSafetyMarginRatio = 0.10

func contextSafetyMargin(est int) int {
	return max(int(float64(est)*contextSafetyMarginRatio), outputBudgetReserve)
}

// admitSummaryOutputBudget uses the summary request's dedicated protocol
// reserve instead of the ordinary-turn reserve. Unknown gateways are treated
// as shared when an effective window exists: summary planning already makes
// that conservative assumption, so execution must enforce the same contract.
// tl 1190: the summary path deliberately KEEPS the pre-1190 arithmetic (no
// proportional safety margin) — its input is pre-planned by the fold ladder
// (summaryInputBudget / 任务 1003 pre-fold / slim rung), and a margin here
// would refuse the slim rung locally before the provider ever sees it — the
// one rung dense-tokenizer overflows recover through
// (TestUnnumberedSummaryOverflowSkipsReplanToTranscript pins this).
func (a *Agent) admitSummaryOutputBudget(req provider.Request) (contextAdmission, error) {
	return a.admitOutputBudgetWithReserve(req, protocolReserveTokens, true, false)
}

func shouldUseSharedWindowForAdmission(mode provider.ContextWindowMode, observedWindow int, conservativeUnknown bool) bool {
	return mode == provider.ContextWindowUnknown && (observedWindow > 0 || conservativeUnknown)
}

func (a *Agent) admitOutputBudgetWithReserve(req provider.Request, reserveTokens int, conservativeUnknown bool, applySafetyMargin bool) (contextAdmission, error) {
	adm := contextAdmission{
		ReserveTokens: reserveTokens,
		LastRecovery:  a.lastAdmission().LastRecovery,
		Source:        provider.ContextBudgetSourceUnknown,
	}
	if adm.LastRecovery == "" {
		adm.LastRecovery = contextRecoveryNone
	}
	if a == nil {
		return adm, nil
	}
	if learned := a.sess.output.learned.Load(); learned != nil {
		adm.ObservedWindow = learned.windowTokens
		adm.ObservedCompletion = learned.completionBudget
	}
	policy := contextBudgetPolicyOf(a.providerForRequest())
	if shouldUseSharedWindowForAdmission(policy.WindowMode, adm.ObservedWindow, conservativeUnknown) {
		policy.WindowMode = provider.ContextWindowShared
	}
	if policy.AutoOutputTokens <= 0 && a.learnedCompletionBudget() > 0 {
		policy.AutoOutputTokens = a.learnedCompletionBudget()
	}
	if learned := a.learnedCompletionBudget(); learned > 0 {
		if policy.AutoOutputTokens <= 0 || learned < policy.AutoOutputTokens {
			policy.AutoOutputTokens = learned
		}
		if policy.MaxOutputTokens <= 0 || learned < policy.MaxOutputTokens {
			policy.MaxOutputTokens = learned
		}
	}
	adm.WindowMode = policy.WindowMode.String()
	adm.LimitMode = policy.LimitMode.String()
	adm.AutoOutputTokens = policy.AutoOutputTokens
	adm.MaxOutputTokens = policy.MaxOutputTokens
	window := a.effectiveContextWindow()
	adm.WindowTokens = window
	learnedWindow := window > 0 && (a.destinationContextWindow() <= 0 || window < a.destinationContextWindow())
	adm.Source = admissionSource(req.MaxTokens, policy, learnedWindow)
	if window <= 0 {
		a.storeAdmission(adm)
		return adm, nil
	}
	est := a.estimatedRequestTokens(req)
	adm.PromptTokens = est
	// tl 1190 L0: the send-time cap is window − est − reserve − safetyMargin.
	// A cold estimate that undershoots the provider tokenizer (the incident:
	// −8.5%) used to consume the margin 1:1; now the margin is explicit and
	// the estimated error must exceed it before a request can overflow. The
	// summary admission opts out (see admitSummaryOutputBudget).
	safety := 0
	if applySafetyMargin {
		safety = contextSafetyMargin(est)
	}
	physical := window - est - reserveTokens - safety
	adm.PhysicalRemaining = physical
	shared := policy.WindowMode == provider.ContextWindowShared
	if !shared {
		a.applyLimitMode(&adm, req.MaxTokens, policy, physical)
		a.storeAdmission(adm)
		return adm, nil
	}
	if physical <= 0 {
		a.storeAdmission(adm)
		// 任务 1010 需求 1: an over-window request that still carries images
		// names them and the repair instead of a bare token count — the
		// ai-passport freeze kept the user staring at "2.1M/1M" with no
		// actionable next step while every retry re-sent the same images.
		if count, bytes := requestImageFootprint(req); count > 0 {
			return adm, fmt.Errorf("%w: estimated prompt %d leaves no shared-window output budget; the request still carries %d image(s) (~%.1f MB encoded) — compact the conversation, or repair it to strip the images, before retrying", ErrCompactionRequired, est, count, float64(bytes)/(1<<20))
		}
		return adm, fmt.Errorf("%w: estimated prompt %d leaves no shared-window output budget", ErrCompactionRequired, est)
	}
	requested := 0
	switch {
	case req.MaxTokens > 0:
		requested = req.MaxTokens
	default:
		requested = policy.AutoOutputTokens
	}
	if policy.MaxOutputTokens > 0 && requested > policy.MaxOutputTokens {
		requested = policy.MaxOutputTokens
	}
	adm.RequestedOutputTokens = requested
	if req.MaxTokens < 0 {
		if requested > 0 && requested > physical {
			a.storeAdmission(adm)
			return adm, fmt.Errorf("%w: estimated prompt %d leaves no room for omitted auto output %d", ErrCompactionRequired, est, requested)
		}
		a.storeAdmission(adm)
		return adm, nil
	}
	if requested <= 0 {
		a.applyLimitMode(&adm, req.MaxTokens, policy, physical)
		a.storeAdmission(adm)
		return adm, nil
	}
	effective := requested
	if effective > physical {
		effective = physical
		adm.Clipped = true
	}
	adm.EffectiveOutputTokens = effective
	a.applyLimitMode(&adm, req.MaxTokens, policy, physical)
	if adm.Clipped {
		adm.ApplyMaxTokens = req.MaxTokens >= 0 && policy.LimitMode != provider.OutputLimitUnsupported
		adm.EffectiveOutputTokens = effective
	}
	if adm.Clipped && adm.LastRecovery == contextRecoveryNone {
		adm.LastRecovery = contextRecoveryProactiveClip
	}
	a.storeAdmission(adm)
	return adm, nil
}

func (a *Agent) applyAdmissionToRequest(req *provider.Request) error {
	if a == nil || req == nil {
		return nil
	}
	adm, err := a.admitOutputBudget(*req)
	if err != nil {
		return err
	}
	if adm.ApplyMaxTokens && adm.EffectiveOutputTokens > 0 {
		req.MaxTokens = adm.EffectiveOutputTokens
	}
	return nil
}

func (a *Agent) applySummaryAdmissionToRequest(req *provider.Request) error {
	if a == nil || req == nil {
		return nil
	}
	adm, err := a.admitSummaryOutputBudget(*req)
	if err != nil {
		return err
	}
	if adm.ApplyMaxTokens && adm.EffectiveOutputTokens > 0 {
		req.MaxTokens = adm.EffectiveOutputTokens
	}
	return nil
}

func (a *Agent) applyLimitMode(adm *contextAdmission, userMax int, policy provider.ContextBudgetPolicy, physical int) {
	if userMax < 0 || policy.LimitMode == provider.OutputLimitUnsupported {
		adm.ApplyMaxTokens = false
		return
	}
	effective := adm.EffectiveOutputTokens
	if effective <= 0 {
		if userMax > 0 {
			effective = userMax
		} else {
			effective = policy.AutoOutputTokens
		}
		if policy.MaxOutputTokens > 0 && effective > policy.MaxOutputTokens {
			effective = policy.MaxOutputTokens
		}
		if policy.WindowMode == provider.ContextWindowShared && physical > 0 && effective > physical {
			effective = physical
			adm.Clipped = true
		}
	}
	switch policy.LimitMode {
	case provider.OutputLimitAlways, provider.OutputLimitRequired:
		if effective > 0 {
			adm.ApplyMaxTokens = true
			adm.EffectiveOutputTokens = effective
		}
	case provider.OutputLimitOmitWhenSafe:
		if userMax > 0 || adm.Clipped {
			adm.ApplyMaxTokens = true
			adm.EffectiveOutputTokens = effective
		}
	}
}
