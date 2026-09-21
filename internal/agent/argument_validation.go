package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

const maxArgumentValidationMessageBytes = 4 << 10

// applyArgumentValidation runs after a proxy has resolved to its concrete
// target and before hooks, permission, leases, subagents, or MCP tools/call.
func (a *Agent) applyResolvedTargetGates(plan *toolCallPlan) (toolOutcome, bool) {
	if blocked, early := a.applyDispatchGenerationGate(plan); early {
		return blocked, true
	}
	return a.applyArgumentValidation(plan)
}

func (a *Agent) applyArgumentValidation(plan *toolCallPlan) (toolOutcome, bool) {
	if plan == nil || plan.execTool == nil {
		return toolOutcome{}, false
	}
	normalized := tool.NormalizeArguments(plan.execArgs)
	plan.execArgs = normalized
	plan.permArgs = normalized
	plan.evidenceArgs = normalized
	result := tool.ValidateArguments(plan.execTool, normalized)
	failed := result.CompileErr != nil || len(result.Violations) > 0
	if a.capabilityAudit != nil {
		a.capabilityAudit.RecordArgumentValidation(failed, result.Skipped, false)
	}
	if result.Skipped || (result.CompileErr == nil && len(result.Violations) == 0) {
		return toolOutcome{}, false
	}
	return a.argumentValidationFailure(plan, result), true
}

// argumentValidationFailure reports an unexecuted call, never a permission
// refusal. Repeated failures are owned by the batch storm breaker.
func (a *Agent) argumentValidationFailure(plan *toolCallPlan, result tool.ArgumentValidationResult) toolOutcome {
	category := "schema"
	if result.CompileErr == nil {
		category = result.Violations[0].Keyword
	}
	msg := argumentValidationMessage(plan, result)
	a.noteCapabilityInvocation(plan.call.Name, json.RawMessage(plan.call.Arguments), errors.New(msg))
	return toolOutcome{output: msg, errMsg: argumentValidationSignature(plan.permName, result.Fingerprint, category)}
}

// diagnoseCapabilityInputFailure runs only after the resolver identifies an
// input error. Successful resolution and unavailable/authorization errors keep
// their historical behavior, even if an ignored envelope field is invalid.
func (a *Agent) diagnoseCapabilityInputFailure(plan *toolCallPlan, err error) toolOutcome {
	result := tool.ValidateArguments(plan.tool, json.RawMessage(plan.call.Arguments))
	if !result.Skipped && (result.CompileErr != nil || len(result.Violations) > 0) {
		if a.capabilityAudit != nil {
			a.capabilityAudit.RecordArgumentValidation(true, false, false)
		}
		return a.argumentValidationFailure(plan, result)
	}
	return toolOutcome{
		output: truncateValidationMessage(fmt.Sprintf("error: %v\nThe capability call was not executed. Correct the indicated input and retry; normal permission checks still apply.", err)),
		errMsg: firstLine(err.Error()),
	}
}

func hostValidateBeforeDispatch(target tool.Tool, args json.RawMessage, capabilityID string) (bool, string) {
	result := tool.ValidateArguments(target, args)
	if result.Skipped || (result.CompileErr == nil && len(result.Violations) == 0) {
		return false, ""
	}
	return true, argumentValidationMessage(&toolCallPlan{
		permName: target.Name(), execTool: target, execArgs: args,
		call:     provider.ToolCall{Name: "use_capability"},
		resolved: tool.ResolvedCall{CapabilityID: capabilityID},
	}, result)
}

func argumentValidationMessage(plan *toolCallPlan, result tool.ArgumentValidationResult) string {
	if result.CompileErr != nil {
		return truncateValidationMessage(fmt.Sprintf("host configuration error: tool %q has an invalid argument schema (schema fingerprint %s); execution was not dispatched. The host schema must be corrected; rewriting call arguments cannot fix it.", plan.permName, shortSchemaFingerprint(result.Fingerprint)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "argument validation failed for %q (schema fingerprint %s; remote_dispatched=false):", plan.permName, shortSchemaFingerprint(result.Fingerprint))
	for _, violation := range result.Violations {
		path := violation.Path
		if path == "" {
			path = "/"
		}
		fmt.Fprintf(&b, "\n- %s: %s; expected %s", path, violation.Keyword, violation.Expected)
	}
	// Task 216: show the received shape next to a minimal valid shape so one
	// round of feedback is enough to self-correct.
	if actual := describeActualArguments(plan.execArgs); actual != "" {
		fmt.Fprintf(&b, "\nActual arguments received: %s", actual)
	}
	if expected := minimalValidExample(plan.execTool); expected != "" {
		fmt.Fprintf(&b, "\nMinimal valid arguments example: %s", expected)
	}
	if id := strings.TrimSpace(plan.resolved.CapabilityID); id != "" {
		fmt.Fprintf(&b, "\nThe target was not executed. Correct the target parameters inside %s.arguments; keep the outer capability call envelope.", plan.call.Name)
		if strings.HasPrefix(id, "skill:") && plan.permName == "run_skill" {
			b.WriteString("\nUse this exact nested call shape:\n")
			b.WriteString(`{"action":"call","capability_id":"`)
			b.WriteString(escapeJSONString(id))
			b.WriteString(`","arguments":{"arguments":"specific review or implementation task"}}`)
		} else {
			fmt.Fprintf(&b, "\nInspect %q for its exact argument schema, if needed, then retry action=call with a JSON object matching it.", id)
		}
	} else {
		fmt.Fprintf(&b, "\nThe call was not executed. Pass the parameters for %s directly at the root of its input object, correct the indicated errors and retry.", plan.permName)
	}
	b.WriteString("\nNormal permission checks still apply.")
	if hasRedundantArgumentWrapper(plan.execTool, plan.execArgs) {
		b.WriteString("\nThe sole \"arguments\" wrapper does not match this tool's schema; its inner object matches the expected parameters. Remove that one wrapper from the target parameters when retrying; keep any outer capability call envelope.")
	}
	if _, enveloped := detectDoubleEnvelopedArguments(plan.execArgs); enveloped {
		b.WriteString("\nDouble-enveloped call detected: the arguments value is itself a full use_capability call envelope. Take the INNER \"arguments\" value and pass it directly as use_capability.arguments; capability_id and action belong on the use_capability level, not inside arguments.")
	}
	return truncateValidationMessage(b.String())
}

// describeActualArguments renders what the host received at the top level of
// the arguments value (task 216): a sorted key list for objects, a key list
// for JSON-string-wrapped objects. Anything that cannot be parsed as a single
// JSON value is described structurally only — echoing raw argument text here
// would leak argument values into the provider-visible error.
func describeActualArguments(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "(absent)"
	}
	if strings.HasPrefix(trimmed, "{") {
		if keys, ok := topLevelArgumentKeys(trimmed); ok {
			return "an object with top-level keys [" + strings.Join(keys, ", ") + "]"
		}
		return "(not a single parseable JSON object)"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		inner := strings.TrimSpace(s)
		if strings.HasPrefix(inner, "{") {
			if keys, ok := topLevelArgumentKeys(inner); ok {
				return "a JSON string wrapping an object with top-level keys [" + strings.Join(keys, ", ") + "]"
			}
		}
		return "a JSON string, not an object"
	}
	return "(not a JSON object)"
}

func topLevelArgumentKeys(objectJSON string) ([]string, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(objectJSON), &object) != nil || object == nil {
		return nil, false
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, true
}

// minimalValidExample builds the smallest arguments object that would satisfy
// the target schema: required properties only, with conservative placeholder
// values. It returns "" when required members cannot be determined safely
// (missing or combinational schema), leaving the guidance to the violation
// lines instead of inventing a wrong example.
func minimalValidExample(target tool.Tool) string {
	if target == nil {
		return ""
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(target.Schema(), &schema) != nil || string(schema["type"]) != `"object"` {
		return ""
	}
	for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "patternProperties", "dependencies", "dependentSchemas"} {
		if _, exists := schema[key]; exists {
			return ""
		}
	}
	var properties map[string]struct {
		Type string `json:"type"`
		Enum []any  `json:"enum"`
	}
	if json.Unmarshal(schema["properties"], &properties) != nil || properties == nil {
		return ""
	}
	var required []string
	if json.Unmarshal(schema["required"], &required) != nil || len(required) == 0 {
		return ""
	}
	example := make(map[string]any, len(required))
	for _, name := range required {
		spec, exists := properties[name]
		if !exists || spec.Type == "" {
			example[name] = "…"
			continue
		}
		switch spec.Type {
		case "string":
			if len(spec.Enum) > 0 {
				if s, ok := spec.Enum[0].(string); ok {
					example[name] = s
					continue
				}
			}
			example[name] = "…"
		case "integer", "number":
			example[name] = 0
		case "boolean":
			example[name] = false
		case "array":
			example[name] = []any{}
		case "object":
			example[name] = map[string]any{}
		default:
			example[name] = "…"
		}
	}
	b, err := json.Marshal(example)
	if err != nil {
		return ""
	}
	return string(b)
}

// hasRedundantArgumentWrapper is a conservative, value-free hint, not a
// transformation. Call only after the original arguments failed validation.
func hasRedundantArgumentWrapper(target tool.Tool, raw json.RawMessage) bool {
	if target == nil {
		return false
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(target.Schema(), &schema) != nil || string(schema["type"]) != `"object"` {
		return false
	}
	for _, key := range []string{"$ref", "$dynamicRef", "$recursiveRef", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "patternProperties", "dependencies", "dependentSchemas"} {
		if _, exists := schema[key]; exists {
			return false
		}
	}
	var props map[string]json.RawMessage
	if json.Unmarshal(schema["properties"], &props) != nil || props == nil {
		return false
	}
	if _, exists := props["arguments"]; exists {
		return false
	}
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) != 1 {
		return false
	}
	inner, exists := outer["arguments"]
	if !exists {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(inner, &object) != nil || object == nil {
		return false
	}
	result := tool.ValidateArguments(target, inner)
	return !result.Skipped && result.CompileErr == nil && len(result.Violations) == 0
}

func argumentValidationSignature(target, fingerprint, category string) string {
	return "argument_validation:" + target + ":" + shortSchemaFingerprint(fingerprint) + ":" + category
}

func shortSchemaFingerprint(fingerprint string) string {
	if len(fingerprint) <= 16 {
		return fingerprint
	}
	return fingerprint[:16]
}

func escapeJSONString(value string) string {
	b, _ := json.Marshal(value)
	if len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}

func truncateValidationMessage(message string) string {
	if len(message) <= maxArgumentValidationMessageBytes {
		return message
	}
	end := maxArgumentValidationMessageBytes - len("\n[truncated]")
	for end > 0 && !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end] + "\n[truncated]"
}
