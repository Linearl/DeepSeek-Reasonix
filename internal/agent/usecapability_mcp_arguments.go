package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"reasonix/internal/tool"
)

// capabilityInputError distinguishes repairable envelope errors from target
// availability and authorization failures without changing their error text.
type capabilityInputError struct{ error }

func (e *capabilityInputError) Unwrap() error { return e.error }
func capabilityInputErrorf(format string, args ...any) error {
	return &capabilityInputError{fmt.Errorf(format, args...)}
}

type useCapabilityArgs struct {
	Action       string `json:"action"`
	CapabilityID string `json:"capability_id"`
	// CapabilityIDs lets action=decline dismiss several unrelated capabilities
	// in one call (fork: the per-turn capability route can list dozens of
	// candidates, and declining them one call at a time burns the turn).
	CapabilityIDs []string `json:"capability_ids"`
	Query         string   `json:"query"`
	Limit         int      `json:"limit"`
	// Task 953 list v2 parameters (advertised in the schema only while
	// experimental_capability_groups is on; ignored when off).
	Kind      string          `json:"kind"`
	Offset    int             `json:"offset"`
	Detail    bool            `json:"detail"`
	Arguments json.RawMessage `json:"arguments"`
	Reason    string          `json:"reason"`
	// healedDoubleEnvelope is set by parseUseCapabilityArgs when a
	// stringified call envelope was unwrapped (task 212). It never
	// serializes; ResolveCall uses it to keep the self-heal observable.
	healedDoubleEnvelope bool `json:"-"`
	// healedStringified is set by parseUseCapabilityArgs when a JSON-string
	// arguments value was parsed into an object for an MCP target (task 457).
	// It never serializes; ResolveCall uses it to keep the self-heal
	// observable.
	healedStringified bool `json:"-"`
	// spreadParameters holds envelope-foreign top-level members captured when
	// action=call arrived with target parameters spread across the envelope
	// top level (message+to etc.) and no arguments object at all (task 457). It never
	// serializes; ResolveCall may merge them into one schema-gated arguments
	// object, and the capture only happens when "arguments" was absent so a
	// call that carried one is never rewritten.
	spreadParameters map[string]json.RawMessage `json:"-"`
	// healedControlChars is set by parseUseCapabilityArgs when capability_id
	// carried embedded control characters that sanitizeCapabilityID removed
	// (task 975: the 1018 screenshot form "tool:talk\r_to\r_session"). It
	// never serializes; ResolveCall uses it to keep the heal observable
	// (audit counter + WARN log, task 798 discipline).
	healedControlChars bool `json:"-"`
	// healedArgumentKeys counts argument object key names that
	// sanitizeArgumentKeys cleaned of embedded control characters at parse
	// time (tl1310: the 10-31 ai-passport field report had target parameter
	// "expected_revision" arrive as "expected\r_revision" — the id survived
	// via the 975 sanitize but the polluted key failed the target schema gate
	// and dragged the call into a 5+ retry loop). Values are never rewritten,
	// only key NAMES. It never serializes; ResolveCall uses the count to keep
	// the heal observable (audit counter + WARN log, task 798 discipline). No
	// model-visible note: like the 975 id cleanup, the model cannot act on
	// tokenizer-emitted \r, so a result note would only add noise.
	healedArgumentKeys int `json:"-"`
	// controlOnlyID is set when capability_id was present but consisted solely
	// of control characters, so nothing usable remained after cleaning (task
	// 975). ResolveCall names this cause instead of the generic missing-id
	// error, which would wrongly blame the field placement.
	controlOnlyID bool `json:"-"`
	// healedAliasKey records which spelling-alias key (ability_id /
	// capabilityId) parseUseCapabilityArgs accepted in place of
	// capability_id (task 996). It never serializes; ResolveCall uses it to
	// keep the heal observable — audit counter + WARN log host-side AND a
	// model-visible notice, because unlike a tokenizer-emitted control
	// character the model CAN correct a key name it chose.
	healedAliasKey string `json:"-"`
}

// capabilityEnvelopeKeys are the top-level members of a use_capability call
// envelope. An arguments value is only treated as a nested envelope when ALL
// of its object members fall inside this set — a legitimate target argument
// object that merely happens to contain an "arguments" field (run_skill's
// own schema, for example) must never be unwrapped.
var capabilityEnvelopeKeys = map[string]bool{
	"action":         true,
	"capability_id":  true,
	"capability_ids": true,
	"query":          true,
	"limit":          true,
	"kind":           true, // task 953 list v2
	"offset":         true, // task 953 list v2
	"detail":         true, // task 953 list v2
	"arguments":      true,
	"reason":         true,
	// Task 996: the converged spelling-alias keys are envelope members, not
	// spread target parameters — without this the alias would ride the 457
	// spread merge into the target's argument object and fail its schema.
	"ability_id":   true,
	"capabilityId": true,
}

// capabilityIDAliasKeys is the CONVERGED list of capability_id spelling
// variants accepted at the envelope top level (task 996). Every entry is a
// form actually observed failing in the field (975 交付回板实测：ability_id
// 与 capabilityId 均触发 "capability_id is required") — the tolerance is a
// closed list, never fuzzy matching, so a genuinely wrong key still gets the
// missing-id correction instead of silent acceptance.
var capabilityIDAliasKeys = []string{"ability_id", "capabilityId"}

// capabilityIDFromEnvelopeAlias extracts the envelope capability_id accepting
// the converged alias keys when the canonical key yields nothing usable. It
// returns the cleaned id and the alias key it came from ("" when no alias
// fired). Both callers that parse the envelope independently of
// parseUseCapabilityArgs (the ledger key and the subagent allowlist check)
// must agree with the resolver on this, or an alias call splits into a
// phantom ledger entry or is rejected one layer above a resolver that would
// accept it (task 975 same-discipline).
func capabilityIDFromEnvelopeAlias(raw json.RawMessage) (id, aliasKey string) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return "", ""
	}
	for _, key := range capabilityIDAliasKeys {
		var value string
		if json.Unmarshal(envelope[key], &value) != nil {
			continue
		}
		cleaned, _ := sanitizeCapabilityID(value)
		if cleaned == "" {
			continue
		}
		return cleaned, key
	}
	return "", ""
}

// canonicalCapabilityIDEnvelope returns a validation-only copy of the envelope
// with a fired alias key renamed to capability_id, so the schema check in
// diagnoseCapabilityInputFailure judges the call the resolver actually
// accepted instead of flagging the accepted alias as an unknown property
// ("remove: ability_id" right after the alias dispatched would teach the
// model the opposite of the success-path note). An envelope where no alias
// fires passes through byte-identical — lookalike keys keep their additional
// properties violation, which is the desired明确报错 for non-converged
// spellings.
func canonicalCapabilityIDEnvelope(raw json.RawMessage) json.RawMessage {
	_, aliasKey := capabilityIDFromEnvelopeAlias(raw)
	if aliasKey == "" {
		return raw
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return raw
	}
	// Canonical key present and usable → parse uses it and never records the
	// alias heal; keep the envelope byte-identical in that case too.
	if cleaned, _ := sanitizeCapabilityID(capabilityStringField(envelope, "capability_id")); cleaned != "" {
		return raw
	}
	canonical := make(map[string]json.RawMessage, len(envelope))
	for key, value := range envelope {
		canonical[key] = value
	}
	delete(canonical, aliasKey)
	canonical["capability_id"] = envelope[aliasKey]
	rewritten, err := json.Marshal(canonical)
	if err != nil {
		return raw
	}
	return rewritten
}

// detectDoubleEnvelopedArguments reports whether the arguments value looks
// like a full use_capability call envelope nested one level too deep, and
// returns the inner "arguments" value to retry with (task 212). Detection is
// purely structural: raw must be a JSON object — directly or wrapped in a
// JSON string — whose members all belong to the envelope key set while
// including "arguments" plus at least one envelope marker
// (capability_id/capability_ids/action). Only one level is unwrapped; the
// result is never recursively re-examined.
func detectDoubleEnvelopedArguments(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, false
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, false
		}
		trimmed = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		return nil, false
	}
	inner, exists := object["arguments"]
	if !exists {
		return nil, false
	}
	marker := false
	for key := range object {
		if !capabilityEnvelopeKeys[key] {
			return nil, false
		}
		if key == "action" || key == "capability_id" || key == "capability_ids" {
			marker = true
		}
	}
	if !marker {
		return nil, false
	}
	// Task 798 (2233 field report): a stringified inner value must itself be a
	// complete call envelope. A stringified plain target-parameters object
	// under {"action":"call"} used to trip this detector, and the validation
	// guidance then told the model to unwrap a payload that was never an
	// envelope — the retry was guaranteed to fail again. Verify the decoded
	// object one level; pass the decoded object through so consumers gate
	// against the target schema instead of the string.
	if strings.HasPrefix(strings.TrimSpace(string(inner)), `"`) {
		decoded, ok := unwrapStringifiedJSONObject(inner)
		if !ok {
			return nil, false
		}
		if _, ok := detectDoubleEnvelopedArguments(decoded); !ok {
			return nil, false
		}
		return decoded, true
	}
	return json.RawMessage(append([]byte(nil), inner...)), true
}

// targetAcceptsArguments mirrors the dispatch gate's validation outcome for
// one candidate arguments value. Skipped validation (no usable schema) must
// not enable self-healing: without a schema there is no evidence the
// unwrapped form is the intended one.
func targetAcceptsArguments(target tool.Tool, args json.RawMessage) bool {
	if target == nil {
		return false
	}
	result := tool.ValidateArguments(target, args)
	return !result.Skipped && result.CompileErr == nil && len(result.Violations) == 0
}

// noteArgumentSelfHeal keeps an arguments self-heal observable (tasks 212 and
// 457): one audit counter plus one WARN log line naming the target and what
// the host corrected. Self-healing must never be silent, but it also must not
// leak argument values into the log — detail names shapes and key names only.
func (t *UseCapabilityTool) noteArgumentSelfHeal(id, detail string) {
	if t.audit != nil {
		t.audit.RecordArgumentSelfHeal()
	}
	log.Printf("[use_capability] WARN: self-healed arguments for %q (%s)", id, detail)
}

// mergedSpreadArguments builds one object from the envelope-foreign top-level
// parameters captured at parse time (task 457). spreadParameters is only
// populated when the arguments member was absent, so a call that carried an
// arguments object is never rewritten here. The caller must still verify the
// merged object against the target schema before dispatching.
func (a useCapabilityArgs) mergedSpreadArguments() (json.RawMessage, []string, bool) {
	if len(a.spreadParameters) == 0 {
		return nil, nil, false
	}
	keys := make([]string, 0, len(a.spreadParameters))
	for key := range a.spreadParameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged, err := json.Marshal(a.spreadParameters) // map marshal sorts keys
	if err != nil {
		return nil, nil, false
	}
	return merged, keys, true
}

// unwrapStringifiedJSONObject parses a JSON-string arguments value whose
// content is itself a JSON object (task 457). Pretty-printed content —
// newlines and indentation — passes because encoding/json skips whitespace,
// and a single trailing comma before a closing brace/bracket is tolerated.
// Anything else (plain text, arrays, scalars, double-encoded JSON) reports
// false so the original value reaches the precise validation error.
func unwrapStringifiedJSONObject(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, `"`) {
		return nil, false
	}
	var inner string
	if json.Unmarshal(raw, &inner) != nil {
		return nil, false
	}
	inner = strings.TrimSpace(inner)
	if !strings.HasPrefix(inner, "{") {
		return nil, false
	}
	candidate := []byte(inner)
	var object map[string]json.RawMessage
	if json.Unmarshal(candidate, &object) != nil || object == nil {
		candidate = stripTrailingCommas(candidate)
		if json.Unmarshal(candidate, &object) != nil || object == nil {
			return nil, false
		}
	}
	compact, err := json.Marshal(object) // sorted keys, normalized whitespace
	if err != nil {
		return nil, false
	}
	return compact, true
}

// stripTrailingCommas removes commas directly preceding a closing brace or
// bracket outside JSON strings, tolerating whitespace between them. It runs
// only on the decoded content of a stringified arguments value, never on
// provider envelopes, so normal envelope parsing stays strict.
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case ',':
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // drop the trailing comma
			}
		}
		out = append(out, c)
	}
	return out
}

// captureSpreadParameters remembers envelope-foreign top-level members for
// action=call when the arguments object was absent (task 457). Detection is
// purely structural; ResolveCall decides against the target schema whether the
// merged form may dispatch.
func captureSpreadParameters(raw json.RawMessage, args *useCapabilityArgs) {
	if len(raw) == 0 {
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return
	}
	extras := make(map[string]json.RawMessage)
	cleanedKeys := 0
	for key, value := range envelope {
		if !capabilityEnvelopeKeys[key] {
			// tl1310: an envelope-foreign KEY may carry the same embedded
			// control characters as the arguments-object keys (the merged
			// form hits the same target schema gate); clean it at capture so
			// mergedSpreadArguments dispatches clean keys.
			cleanKey, stripped := stripControlCharacters(key)
			if stripped {
				cleanedKeys++
			}
			extras[cleanKey] = value
		}
	}
	if len(extras) > 0 {
		args.spreadParameters = extras
		args.healedArgumentKeys += cleanedKeys
	}
}

// promoteNestedCapabilityID recovers a capability_id the model nested inside
// the arguments object instead of putting it on the envelope top level (task
// 728: 58 of the 59 observed call failures had the shape {"action":"call",
// "arguments":{"capability_id":...,"arguments":{...}}}). It returns the
// promoted id and the candidate target arguments: the nested object minus
// capability_id, with a lone remaining "arguments" member unwrapped one level
// (that object is then a complete call envelope). A non-string or empty
// nested capability_id never promotes. Detection is purely structural;
// ResolveCall still gates the promoted form against the target schema and
// falls back to the original missing-id error when it does not pass.
//
// Task 798 extends the same family to its stringified and whole-envelope
// members: the arguments value may arrive as a JSON string wrapping the
// nested envelope (the 457 stringified habit one level deeper), and the
// promoted remainder may itself still be a complete call envelope (action
// nested alongside capability_id) — that remainder is unwrapped once with the
// task 212 detector instead of dispatching envelope keys as target parameters.
func promoteNestedCapabilityID(raw json.RawMessage) (string, json.RawMessage, bool) {
	if argumentsAbsent(raw) {
		return "", nil, false
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
		unwrapped, ok := unwrapStringifiedJSONObject(raw)
		if !ok {
			return "", nil, false
		}
		raw = unwrapped
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return "", nil, false
	}
	nested, exists := object["capability_id"]
	if !exists {
		return "", nil, false
	}
	var id string
	if json.Unmarshal(nested, &id) != nil {
		return "", nil, false
	}
	// Task 975: a nested capability_id may carry the same embedded control
	// characters as a top-level one; sanitize before the emptiness check so a
	// dirty-but-usable nested id still promotes.
	id, _ = sanitizeCapabilityID(id)
	if id == "" {
		return "", nil, false
	}
	remaining := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		if key != "capability_id" {
			remaining[key] = value
		}
	}
	if len(remaining) == 1 {
		if lone, ok := remaining["arguments"]; ok {
			if argumentsAbsent(lone) {
				return id, json.RawMessage(`{}`), true
			}
			return id, json.RawMessage(append([]byte(nil), lone...)), true
		}
	}
	merged, err := json.Marshal(remaining) // sorted keys, compacted values
	if err != nil {
		return "", nil, false
	}
	// Task 798: when the remainder is still a complete call envelope (action
	// nested next to the promoted capability_id), take its inner arguments one
	// level (task 212 detector) instead of handing envelope keys to the target
	// schema gate, which would only fail the promoted form.
	if inner, ok := detectDoubleEnvelopedArguments(merged); ok {
		return id, inner, true
	}
	return id, merged, true
}

// sanitizeCapabilityID strips ASCII control characters (task 975: the 10-18
// field screenshot showed capability_id "tool:talk\r_to\r_session" arriving
// with bare \r bytes, missing the registry with "not registered in this
// session" and dragging the whole parallel batch down with it). Embedded
// control characters are never legitimate inside a capability id — the
// namespaces are tool:/skill:/mcp-tool:/task:/workflow:/web:/lsp:/session:/
// memory: plus plain ASCII names — so removal is safe. ids without control
// characters pass through byte-for-byte after the same TrimSpace the parser
// always applied. The bool reports whether anything was removed so the caller
// can keep the heal observable instead of silently rewriting model output.
func sanitizeCapabilityID(id string) (string, bool) {
	cleaned, stripped := stripControlCharacters(id)
	return strings.TrimSpace(cleaned), stripped
}

// stripControlCharacters removes ASCII control characters (C0 range 0x00-0x1f
// and DEL 0x7f) from s. Bytes >= 0x80 pass untouched so multi-byte UTF-8 is
// never reinterpreted. The bool reports whether anything was removed so
// callers can keep the heal observable instead of silently rewriting model
// output. Shared by sanitizeCapabilityID (task 975) and the tl1310 argument
// key-name cleanup.
func stripControlCharacters(s string) (string, bool) {
	if !hasControlCharacter(s) {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

// sanitizeArgumentKeys strips control characters from JSON object KEY NAMES at
// every depth of an action=call arguments value (tl1310: the 10-31 ai-passport
// field report had "expected_revision" arrive as "expected\r_revision" and
// fail the target schema gate with a required-field error the model could not
// diagnose, feeding a 5+ retry loop). Values — including long prompt strings
// with legitimate \n/\r escapes — are preserved byte-for-byte; only key names
// are rewritten, and only when a control character is actually present in one.
// Returns the cleaned raw message and the number of keys rewritten; a count of
// zero always returns the input byte-identical. Keys that clean to empty are
// kept (never dropped) so the schema gate still reports the unknown field
// instead of a misleading missing-required diagnosis.
func sanitizeArgumentKeys(raw json.RawMessage) (json.RawMessage, int) {
	if len(raw) == 0 || !argumentKeysHaveControlChars(raw) {
		return raw, 0
	}
	return rebuildArgumentKeys(raw)
}

// argumentKeysHaveControlChars reports whether any object key at any depth of
// raw carries an embedded control character. Detection walks the structure
// without rewriting anything, so a payload whose keys are clean (including one
// whose values contain legitimate control escapes in long prompt strings)
// returns false and the caller keeps the input byte-identical.
func argumentKeysHaveControlChars(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &object) != nil {
			return false
		}
		for key, value := range object {
			if hasControlCharacter(key) || argumentKeysHaveControlChars(value) {
				return true
			}
		}
	case strings.HasPrefix(trimmed, "["):
		var array []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &array) != nil {
			return false
		}
		for _, item := range array {
			if argumentKeysHaveControlChars(item) {
				return true
			}
		}
	}
	return false
}

// rebuildArgumentKeys re-marshals the object graph replacing every key that
// carries control characters with its cleaned form. Values travel as
// json.RawMessage and are never re-encoded, so long prompt strings keep their
// exact bytes; clean subtrees keep their original bytes too. Key order follows
// map marshal (sorted) — acceptable because this path only runs on an
// already-polluted payload where byte identity is off the table. A count of
// zero with the input returned unchanged means re-marshaling failed; the call
// then proceeds exactly as before the tolerance existed.
func rebuildArgumentKeys(raw json.RawMessage) (json.RawMessage, int) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
			return raw, 0
		}
		rebuilt := make(map[string]json.RawMessage, len(object))
		cleaned := 0
		for key, value := range object {
			cleanKey, stripped := stripControlCharacters(key)
			if stripped {
				cleaned++
			}
			if inner, innerCleaned := rebuildArgumentKeys(value); innerCleaned > 0 {
				value = inner
				cleaned += innerCleaned
			}
			rebuilt[cleanKey] = value
		}
		merged, err := json.Marshal(rebuilt)
		if err != nil {
			return raw, 0
		}
		return merged, cleaned
	}
	if strings.HasPrefix(trimmed, "[") {
		var array []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &array) != nil {
			return raw, 0
		}
		rebuilt := make([]json.RawMessage, len(array))
		cleaned := 0
		for i, item := range array {
			rebuilt[i] = item
			if inner, innerCleaned := rebuildArgumentKeys(item); innerCleaned > 0 {
				rebuilt[i] = inner
				cleaned += innerCleaned
			}
		}
		merged, err := json.Marshal(rebuilt)
		if err != nil {
			return raw, 0
		}
		return merged, cleaned
	}
	return raw, 0
}

// hasControlCharacter reports whether id contains any C0 control character or
// DEL (0x00-0x1f, 0x7f). Bytes >= 0x80 pass untouched so multi-byte UTF-8
// names are never reinterpreted.
func hasControlCharacter(id string) bool {
	for i := 0; i < len(id); i++ {
		if c := id[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// capabilityControlOnlyIDError names the failure the generic missing-id error
// would misdiagnose: the model DID send a capability_id, but every byte of it
// was a control character so nothing usable remained after cleaning (task 975).
func capabilityControlOnlyIDError() error {
	return capabilityInputErrorf(`capability_id contained only control characters (e.g. \r, \n, \x00) and cannot name a capability; send a plain id such as "tool:talk_to_session"`)
}

func parseUseCapabilityArgs(raw json.RawMessage) (useCapabilityArgs, string, string, error) {
	var args useCapabilityArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, "", "", fmt.Errorf("invalid args: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	id, controlStripped := sanitizeCapabilityID(args.CapabilityID)
	if controlStripped {
		if id == "" {
			args.controlOnlyID = true
		} else {
			// Task 975: only an embedded-control cleanup counts as a heal —
			// a whitespace-only id was always empty after TrimSpace.
			args.healedControlChars = true
		}
	}
	// Task 996: when the canonical capability_id yields nothing usable, fall
	// back to the CONVERGED spelling-alias keys (ability_id / capabilityId —
	// the two variants actually observed failing). Canonical always wins; an
	// alias never overrides a present, usable capability_id. The heal is
	// recorded so ResolveCall can surface a model-visible spelling note —
	// unlike the 975 control-char cleanup the model CAN correct a key name.
	if id == "" {
		if aliasID, aliasKey := capabilityIDFromEnvelopeAlias(raw); aliasID != "" {
			id = aliasID
			args.healedAliasKey = aliasKey
			// An alias carrying the id means the call is NOT missing its
			// target; the control-only diagnosis would blame the wrong field.
			args.controlOnlyID = false
		}
	}
	// The 1-8 upper bound is enforced at resolve time (ResolveCall) where the
	// experimental_capability_groups flag is reachable: search keeps 1-8; list
	// with the group surface on accepts up to 50. Parse keeps only the sign
	// check so one flat parser serves both tiers.
	if args.Limit < 0 {
		return args, "", "", fmt.Errorf("limit must be between 1 and 8 when provided")
	}
	args.Offset = max(args.Offset, 0)
	if action == "call" {
		// Task 457: remember target parameters spread across the envelope
		// top level (message+to etc. with no arguments object) so ResolveCall
		// can attempt one schema-gated merge instead of dispatching {}.
		if argumentsAbsent(args.Arguments) {
			captureSpreadParameters(raw, &args)
		}
		if strings.HasPrefix(id, "mcp-tool:") {
			normalized, err := normalizeMCPToolArguments(args.Arguments)
			if err != nil {
				// Task 212: the arguments may be a stringified full call
				// envelope (the observed output-layer habit). Unwrap one level
				// instead of failing; ResolveCall still requires the unwrapped
				// value to satisfy the target schema before it dispatches.
				if inner, ok := detectDoubleEnvelopedArguments(args.Arguments); ok {
					args.Arguments = inner
					args.healedDoubleEnvelope = true
				} else if unwrapped, ok := unwrapStringifiedJSONObject(args.Arguments); ok {
					// Task 457: a stringified plain target-arguments object
					// (pretty-printed or single-line) parses instead of
					// failing; ResolveCall keeps the heal observable.
					args.Arguments = unwrapped
					args.healedStringified = true
				} else {
					return args, "", "", err
				}
			} else {
				args.Arguments = normalized
			}
		}
		// tl1310: strip control characters from argument KEY NAMES after every
		// tolerance path has settled the final arguments value (normalized
		// copy, unwrapped double envelope, stringified compact) so all of them
		// reach the target schema gate with clean keys. Zero-cost: a payload
		// without dirty keys returns byte-identical.
		if cleaned, cleanedKeys := sanitizeArgumentKeys(args.Arguments); cleanedKeys > 0 {
			args.Arguments = cleaned
			args.healedArgumentKeys += cleanedKeys
		}
	}
	return args, action, id, nil
}

// argumentsAbsent reports whether the arguments member carried no value at
// all. A present-but-empty object is NOT absent: rewriting it would betray
// calls whose target genuinely takes no parameters.
func argumentsAbsent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// normalizeMCPToolArguments accepts only an object. It deliberately does not
// unwrap JSON strings, rename fields, coerce values, or guess enums: schema
// mistakes must produce one precise repair contract instead of hidden behavior
// that differs between direct and proxied MCP calls. (Stringified objects are
// unwrapped by the parse-time tolerance in parseUseCapabilityArgs, which keeps
// the correction observable.)
func normalizeMCPToolArguments(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &object) != nil || object == nil {
		// Task 457: name the received shape and the expected envelope so one
		// round of feedback is enough to self-correct. Values never leak.
		return nil, fmt.Errorf("arguments for an MCP tool must be a JSON object; arrays, scalars, malformed JSON, and non-JSON strings are not supported. Actual arguments received: %s. Expected shape: {\"action\":\"call\",\"capability_id\":\"mcp-tool:<server>/<tool>\",\"arguments\":{ ...the target tool's own parameters... }}", describeActualArguments(raw))
	}
	return json.RawMessage(append([]byte(nil), trimmed...)), nil
}
