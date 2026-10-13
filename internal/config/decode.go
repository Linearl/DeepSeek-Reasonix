package config

import (
	"fmt"

	"github.com/BurntSushi/toml"

	fileencoding "reasonix/internal/fileutil/encoding"
)

func decodeTOMLFile(path string, v any) (toml.MetaData, error) {
	resolved, err := resolveConfigReadPath(path)
	if err != nil {
		return toml.MetaData{}, err
	}
	return decodeTOMLFileResolved(resolved, v)
}

func decodeTOMLFileResolved(path string, v any) (toml.MetaData, error) {
	data, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return toml.MetaData{}, err
	}
	return decodeTOMLBytes(data, v)
}

func decodeTOMLBytes(data []byte, v any) (toml.MetaData, error) {
	text := string(fileencoding.DecodeToUTF8(data))
	meta, err := toml.Decode(text, v)
	if err != nil {
		return meta, err
	}
	if cfg, ok := v.(*Config); ok {
		if err := isolateDecodedProviders(text, meta, cfg); err != nil {
			return meta, err
		}
		inheritLegacyCollabHubKey(meta, cfg)
	}
	return meta, nil
}

// inheritLegacyCollabHubKey is the task-856 one-shot rename migration (856 ③):
// the collaboration-hub switch was the task-677 group-chat switch before the
// mail-inbox + group-chat icons merged. A config file written before the
// rename has no experimental_collab_hub line — first read inherits the legacy
// key's stored value so the user's entry state survives the merge untouched.
// Files that DO define the new key read it as-is (the legacy line is retired
// by the fixed-key-set render on the next save).
func inheritLegacyCollabHubKey(meta toml.MetaData, cfg *Config) {
	if cfg == nil || meta.IsDefined("desktop", "experimental_collab_hub") {
		return
	}
	cfg.Desktop.ExperimentalCollabHub = cfg.Desktop.ExperimentalCollabGroupView
}

// isolateDecodedProviders stops one vendor's defaults from being written onto
// another vendor's provider entry.
//
// TOML array-of-tables decoding is positional: BurntSushi/toml unifies
// [[providers]][i] onto whatever the destination slice already holds at index i
// instead of onto a zero value. Every config load seeds from Default(), which
// ships two official DeepSeek entries, so a user's first two [[providers]]
// inherit every DeepSeek field they did not set themselves — balance_url,
// the USD price table and context_window = 1000000 (#7357, #7358).
//
// Re-decoding the same bytes onto an empty slice makes a declared provider list
// carry only what the file declares. Defaults that are genuinely correct for an
// entry are then reapplied by the explicit backfill helpers, which key on the
// endpoint rather than on list position.
func isolateDecodedProviders(text string, meta toml.MetaData, cfg *Config) error {
	if cfg == nil || len(cfg.Providers) == 0 || !meta.IsDefined("providers") {
		return nil
	}
	var isolated struct {
		Providers []ProviderEntry `toml:"providers"`
	}
	if _, err := toml.Decode(text, &isolated); err != nil {
		return fmt.Errorf("decode providers: %w", err)
	}
	cfg.Providers = isolated.Providers
	return nil
}
