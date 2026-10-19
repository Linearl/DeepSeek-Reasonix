package provider

import "slices"

// ModelModality identifies an input modality accepted by a model. Keep this
// type open for future audio/video/file capabilities; the first implementation
// only routes text and image inputs.
type ModelModality string

const (
	ModalityText  ModelModality = "text"
	ModalityImage ModelModality = "image"
	// ModalityAudio marks models that accept file audio input (task 1008,
	// mimo-v2.6 全模态). The modality only ever widens the user-turn content
	// shape (input_audio parts); it must never touch tools or the system
	// prompt, so capability caches keep prefix bytes stable.
	ModalityAudio ModelModality = "audio"
)

// ModelInfo is the adapter-owned, model-level capability metadata. A nil
// InputModalities means that the adapter could not determine the capability;
// an explicit []{"text"} is a deliberate negative declaration.
type ModelInfo struct {
	ID              string          `json:"id"`
	Name            string          `json:"name,omitempty"`
	API             string          `json:"api,omitempty"`
	BaseURL         string          `json:"base_url,omitempty"`
	InputModalities []ModelModality `json:"input_modalities,omitempty"`
	ContextWindow   int             `json:"context_window,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Reasoning       bool            `json:"reasoning,omitempty"`
	Pricing         *Pricing        `json:"pricing,omitempty"`
}

// ModelInfoProvider is implemented by providers that can expose metadata for
// the exact model instance they were constructed for. It is optional so older
// third-party providers remain source-compatible.
type ModelInfoProvider interface {
	ModelInfo() ModelInfo
}

// SupportsInput reports whether the model explicitly accepts the modality.
func (m ModelInfo) SupportsInput(modality ModelModality) bool {
	return slices.Contains(m.InputModalities, modality)
}
