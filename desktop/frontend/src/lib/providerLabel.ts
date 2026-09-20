// Provider connections carry two names, and only one of them is user facing.
//
//  - `name` is the stable routing identity: it is persisted inside every
//    `<provider>/<model>` reference (tab state, session meta, heartbeat tasks),
//    so renaming it would break every existing reference.
//  - `displayName` is the label the user picked for that connection. It is
//    optional and purely cosmetic.
//
// User-facing surfaces must show the label and keep the identity out of sight.
// Falling back to `name` keeps installs that never set a display name
// byte-identical to the previous behaviour (task 198 acceptance #3).
//
// This is the upstream contract, not a local convention: Go already implements the
// same fallback in internal/provider/failure_diagnostic.go (ProviderDisplayLabel
// keeps the display name and falls back to the provider id) and the config layer
// already renders the same key in internal/config/render_provider_identity.go.
// Frontend surfaces should go through here rather than invent a second rule.
export function providerDisplayLabel(provider: { name: string; displayName?: string }): string {
  return provider.displayName?.trim() || provider.name;
}
