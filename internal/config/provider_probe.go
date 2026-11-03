package config

import "strings"

// WithAPIKeyForProbe returns a private copy with a draft credential. It neither
// persists the value nor mutates process environment shared by other requests.
//
// The copy is marked as a probe override (KeyIsProbeOverride): the caller
// supplied the exact credential to put on the wire for this one probe, so a
// live credential re-read (任务1308) must not silently swap it for the
// env/store value — that swap is what broke TestProviderModel with an
// APIKeyEnv-backed provider (the probe kept 401-ing with the env key).
func (e ProviderEntry) WithAPIKeyForProbe(value string) ProviderEntry {
	e.resolvedAPIKey = strings.TrimSpace(value)
	e.resolvedSource = CredentialSource{Kind: CredentialSourceEnvironment, Label: "settings probe"}
	e.probeAPIKey = true
	return e
}

// KeyIsProbeOverride reports whether this entry's key came from
// WithAPIKeyForProbe — a caller-issued draft credential that must reach the
// wire verbatim and therefore opts out of live credential refresh (任务1308).
func (e *ProviderEntry) KeyIsProbeOverride() bool {
	return e != nil && e.probeAPIKey
}
