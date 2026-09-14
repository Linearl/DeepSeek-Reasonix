package provider

import (
	"slices"
	"strings"
)

// OfficialDeepSeekVisionModel is the pinned vision SKU DeepSeek still routes to
// V4.1 Flash. It stays exported for pricing, effort and backfill keying.
const OfficialDeepSeekVisionModel = "deepseek-v4-flash-vision-exp"

// officialDeepSeekImageModels lists official DeepSeek IDs that accept image
// input natively. This is the single authority for image capability on the
// official endpoint: the capability resolver, the runtime vision gate and the
// local model catalog all consult it, so a new SKU is added in one place.
//
// deepseek-v4-flash and the beta alias are routed to V4.1 Flash for now; drop
// the alias once the vendor closes that transition window.
var officialDeepSeekImageModels = []string{
	"deepseek-flash",
	"deepseek-v4.1-flash-expires-on-0910",
	"deepseek-v4-flash",
	OfficialDeepSeekVisionModel,
}

// IsOfficialDeepSeekImageModel reports whether model is an official DeepSeek SKU
// with native image input. Matching is case-insensitive and trims space.
func IsOfficialDeepSeekImageModel(model string) bool {
	model = strings.TrimSpace(model)
	return slices.ContainsFunc(officialDeepSeekImageModels, func(candidate string) bool {
		return strings.EqualFold(candidate, model)
	})
}

// IsOfficialDeepSeekTextModel identifies models the vendor has confirmed are
// text-only, and is the ONLY hard image block on the official endpoint.
//
// The list is deliberately short and stays short: a desktop release always
// trails the vendor's launches, so a name-based blocklist that guesses at
// "unknown means text-only" keeps refusing new multimodal models even for a
// user who has turned images on (the 1.38.1 lesson). Everything not listed here
// falls through to resolved capability metadata and the user's switch.
func IsOfficialDeepSeekTextModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	// Confirmed text-only by the vendor. deepseek-v4-pro was the pre-V4.1
	// workhorse and is still text-only; the flash ids moved to a multimodal
	// model and are no longer here.
	case "deepseek-v4-pro":
		return true
	}
	return false
}
