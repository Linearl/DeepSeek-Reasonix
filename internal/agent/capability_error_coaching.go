package agent

// Task 877: capability 调用容错与纠错提醒 —— 报错文案升级为「纠错引导」。
//
// 8 型族识别表（无法定诊时落到 undiagnosed → action=search 指引）：
//  1. missing_id       顶层缺 capability_id
//  2. nested_id        capability_id 混进 arguments 内（参数混淆）
//  3. wrong_name       capability_id 真名错（未注册/拼写错）——提示最接近 catalog 真名
//  4. unshaped_id      裸名无命名空间前缀（tool:/mcp-tool:）
//  5. stringified_args arguments 值退化为 JSON 字符串
//  6. multiline_json   JSON 字符串内含换行缩进（多行退化）→ 提示单行紧凑 JSON
//  7. double_envelope  arguments 值是完整调用 envelope（双包）→ 目标参数直传
//  8. spread_params    目标参数拆到 envelope 顶层（message/to 拆行）
//
// use_capability 四变体容错记录（212/457/728/798）与合并线 920 归因都落在
// 这些族里；本件只升级「报错后的引导文案」，不新增容错行为。文案纪律沿用
// 725/798 先例：给可复制的修正例（直接给改好的参数形状），不指向文档；
// 无法定诊时给 action=search(query) 指引。成功路径零变化。

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// capabilityErrorKind 是 8 型族 + 两个路由态（自解释错误不加引导；wrong_name/
// unshaped_id 的引导挂在代理出口而非报错分类器）。
type capabilityErrorKind string

const (
	capabilityErrMissingID       capabilityErrorKind = "missing_id"
	capabilityErrNestedID        capabilityErrorKind = "nested_id"
	capabilityErrWrongName       capabilityErrorKind = "wrong_name"
	capabilityErrUnshapedID      capabilityErrorKind = "unshaped_id"
	capabilityErrStringifiedArgs capabilityErrorKind = "stringified_args"
	capabilityErrMultilineJSON   capabilityErrorKind = "multiline_json"
	capabilityErrDoubleEnvelope  capabilityErrorKind = "double_envelope"
	capabilityErrSpreadParams    capabilityErrorKind = "spread_params"
	capabilityErrUndiagnosed     capabilityErrorKind = "undiagnosed"
	// capabilityErrSelfExplanatory 标记报错文案本身已给出完整修复步骤
	// （decline 缺 reason、limit 越界、unknown action 等）——不再追加引导，
	// 避免噪声。
	capabilityErrSelfExplanatory capabilityErrorKind = "self_explanatory"
)

// capabilityEnvelopeShape 是从 use_capability 调用 envelope 结构性提取的
// 形态事实（只取键名与 id，不取参数值）。
type capabilityEnvelopeShape struct {
	action            string
	id                string
	nestedID          string
	argumentsString   string
	argumentsAsString bool
	multiline         bool
	doubleEnvelope    bool
	hasArguments      bool
	spreadKeys        []string
}

// inspectCapabilityEnvelope 从原始调用 arguments 提取形态。整包 JSON 字符串
// 与 arguments 成员字符串化两种退化都记入 argumentsAsString。
func inspectCapabilityEnvelope(raw json.RawMessage) capabilityEnvelopeShape {
	var s capabilityEnvelopeShape
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return s
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return s
		}
		s.argumentsAsString = true
		s.multiline = strings.Contains(text, "\n")
		s.argumentsString = text
		inner := strings.TrimSpace(text)
		if strings.HasPrefix(inner, "{") {
			var obj map[string]json.RawMessage
			if json.Unmarshal([]byte(inner), &obj) == nil {
				s.action = capabilityStringField(obj, "action")
				s.id = capabilityStringField(obj, "capability_id")
			}
		}
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &obj) != nil || obj == nil {
		return s
	}
	s.action = capabilityStringField(obj, "action")
	s.id = capabilityStringField(obj, "capability_id")
	for key := range obj {
		if key == "arguments" {
			s.hasArguments = true
			continue
		}
		if !capabilityEnvelopeKeys[key] {
			s.spreadKeys = append(s.spreadKeys, key)
		}
	}
	sort.Strings(s.spreadKeys)
	if rawArgs, ok := obj["arguments"]; ok {
		at := strings.TrimSpace(string(rawArgs))
		switch {
		case strings.HasPrefix(at, `"`):
			var text string
			if json.Unmarshal(rawArgs, &text) == nil {
				s.argumentsAsString = true
				s.multiline = strings.Contains(text, "\n")
				s.argumentsString = text
				s.nestedID = capabilityNestedStringID(text)
			}
		case strings.HasPrefix(at, "{"):
			var inner map[string]json.RawMessage
			if json.Unmarshal(rawArgs, &inner) == nil {
				s.nestedID = capabilityStringField(inner, "capability_id")
				if _, enveloped := detectDoubleEnvelopedArguments(rawArgs); enveloped {
					s.doubleEnvelope = true
				}
			}
		}
	}
	return s
}

// capabilityStringField reads a non-empty string field from a decoded object.
func capabilityStringField(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// capabilityNestedStringID reads capability_id from a stringified object.
func capabilityNestedStringID(text string) string {
	inner := strings.TrimSpace(text)
	if !strings.HasPrefix(inner, "{") {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(inner), &obj) != nil {
		return ""
	}
	return capabilityStringField(obj, "capability_id")
}

// classifyCapabilityInputError 按 8 型族识别一次 capability 输入错误。
// 错误文案优先（部分族只能由文案判定），结构事实兜底。
func classifyCapabilityInputError(errText string, raw json.RawMessage) capabilityErrorKind {
	switch {
	case strings.Contains(errText, "is not registered in this session"),
		strings.Contains(errText, "unknown capability_id"):
		return capabilityErrWrongName
	case strings.Contains(errText, "needs a shaped capability id"):
		return capabilityErrUnshapedID
	case strings.Contains(errText, "reason is required"),
		strings.Contains(errText, "limit must be"),
		strings.Contains(errText, "query is required"),
		strings.Contains(errText, "unknown action"):
		return capabilityErrSelfExplanatory
	case strings.Contains(errText, "capability_id is required"):
		// 728 形态：提升失败回退到 missing-id 报错时，即使结构上像双包，
		// 正确的引导也是把嵌套的 capability_id 提到顶层，而不是拆内层。
		if inspectCapabilityEnvelope(raw).nestedID != "" {
			return capabilityErrNestedID
		}
		return capabilityErrMissingID
	}
	shape := inspectCapabilityEnvelope(raw)
	switch {
	case shape.argumentsAsString && shape.multiline:
		return capabilityErrMultilineJSON
	case shape.argumentsAsString:
		return capabilityErrStringifiedArgs
	case shape.doubleEnvelope:
		return capabilityErrDoubleEnvelope
	case shape.id == "" && shape.nestedID != "":
		return capabilityErrNestedID
	case shape.id == "":
		// 顶层缺 id 是阻断性错误：即使参数还被拆在顶层，也先修 id。
		return capabilityErrMissingID
	case !shape.hasArguments && len(shape.spreadKeys) > 0:
		return capabilityErrSpreadParams
	}
	return capabilityErrUndiagnosed
}

// capabilityInputCorrection 是 capabilityInputError 报错面（diagnose 入口）
// 的追加引导。self_explanatory 与代理出口族（wrong_name/unshaped_id）返回空。
func capabilityInputCorrection(errText string, raw json.RawMessage) string {
	kind := classifyCapabilityInputError(errText, raw)
	if kind == capabilityErrSelfExplanatory ||
		kind == capabilityErrWrongName || kind == capabilityErrUnshapedID {
		return ""
	}
	return capabilityCorrectionFor(kind, inspectCapabilityEnvelope(raw))
}

// capabilityCorrectionFor 为已识别的型族生成可复制修正例。参数值一律用
// 占位符（与 minimalValidExample 同纪律），绝不回显模型传入的值。
func capabilityCorrectionFor(kind capabilityErrorKind, shape capabilityEnvelopeShape) string {
	action := shape.action
	if action == "" {
		action = "call"
	}
	id := "<exact catalog id>"
	if shape.id != "" {
		// 935：引号构造一律走 strconv.Quote（stdlib 引号器，自带成对引号与
		// 转义），不再手动 `"`+escapeJSONString(x)+`"` 拼接——后者被
		// CodeQL go/unsafe-quoting 判为「json.Marshal 输出流入引号拼接」
		// （escapeJSONString 内部即 json.Marshal）。
		id = strconv.Quote(shape.id)
	}
	searchHint := ` If the exact id is unclear, run {"action":"search","query":"<topic>","limit":5} first.`
	switch kind {
	case capabilityErrMissingID:
		return fmt.Sprintf(`Corrected call (single line): {"action":%s,"capability_id":%s,"arguments":{ ...the target tool's own parameters... }} — capability_id must be a TOP-LEVEL field of use_capability, never inside arguments.%s`,
			strconv.Quote(action), id, searchHint)
	case capabilityErrNestedID:
		nested := "<the nested id>"
		if shape.nestedID != "" {
			nested = strconv.Quote(shape.nestedID)
		}
		return fmt.Sprintf(`capability_id was found INSIDE arguments; hoist it to the top level: {"action":"call","capability_id":%s,"arguments":{ ...the target tool's own parameters... }}`,
			nested)
	case capabilityErrStringifiedArgs, capabilityErrMultilineJSON:
		msg := `The "arguments" value arrived as a JSON string, not an object. Parse it and pass ONE single-line compact JSON object: {"action":"call","capability_id":` + id + `,"arguments":{ ...the target tool's own parameters... }}`
		if kind == capabilityErrMultilineJSON {
			msg += " Remove the newlines/indentation: tool-call arguments must be single-line compact JSON."
		}
		return msg
	case capabilityErrDoubleEnvelope:
		return fmt.Sprintf(`The arguments value is itself a full use_capability envelope (double envelope). Take the INNER "arguments" object and pass it directly: {"action":"call","capability_id":%s,"arguments":{ ...the inner object's members... }} — capability_id and action stay at the use_capability top level only.`,
			id)
	case capabilityErrSpreadParams:
		parts := make([]string, 0, len(shape.spreadKeys))
		for _, key := range shape.spreadKeys {
			parts = append(parts, strconv.Quote(key)+`:"…"`)
		}
		return fmt.Sprintf(`The target parameters [%s] were spread across the envelope top level. Pack them into ONE arguments object: {"action":"call","capability_id":%s,"arguments":{%s}}`,
			strings.Join(shape.spreadKeys, ", "), id, strings.Join(parts, ","))
	case capabilityErrUndiagnosed:
		return `If the failure cause is unclear, run {"action":"search","query":"<keywords>","limit":5} to find the exact capability_id, then action=inspect it before retrying.`
	}
	return ""
}

// capabilityValidationCorrection 是 schema 校验失败面（argumentValidationMessage
// 尾部）的追加引导，只对 use_capability 调用生效。direct 工具调用与
// hostValidateBeforeDispatch 的合成调用（call.Name 同为 use_capability）都
// 走这里；后者 plan.call.Arguments 为空，形态检查自然落空。
func capabilityValidationCorrection(plan *toolCallPlan) string {
	if plan == nil || plan.call.Name != "use_capability" {
		return ""
	}
	shape := inspectCapabilityEnvelope(json.RawMessage(plan.call.Arguments))
	id := "<exact catalog id>"
	if shape.id != "" {
		// 935：引号构造一律走 strconv.Quote（stdlib 引号器，自带成对引号与
		// 转义），不再手动 `"`+escapeJSONString(x)+`"` 拼接——后者被
		// CodeQL go/unsafe-quoting 判为「json.Marshal 输出流入引号拼接」
		// （escapeJSONString 内部即 json.Marshal）。
		id = strconv.Quote(shape.id)
	}
	// 整包或 arguments 成员字符串化：Actual 行已点名 "a JSON string wrapping…"，
	// 这里补可复制的单行修正例。
	if shape.argumentsAsString {
		msg := `Pass the use_capability call as a single-line compact JSON OBJECT (never a string): {"action":"call","capability_id":` + id + `,"arguments":{ ...the target tool's own parameters... }}`
		if shape.multiline {
			msg += " Remove the newlines/indentation."
		}
		return msg
	}
	// 双包：既有 "Double-enveloped call detected" 提示之外补可复制例。
	if shape.doubleEnvelope {
		return fmt.Sprintf(`Copyable fix: {"action":"call","capability_id":%s,"arguments":{ ...the INNER object's members... }}`, id)
	}
	// spread：arguments 成员缺失且顶层有 envelope 外键（自愈合并失败后
	// gate 报 required 违规时，模型看不到自己的 message/to 被丢在顶层）。
	if !shape.hasArguments && len(shape.spreadKeys) > 0 {
		parts := make([]string, 0, len(shape.spreadKeys))
		for _, key := range shape.spreadKeys {
			parts = append(parts, strconv.Quote(key)+`:"…"`)
		}
		return fmt.Sprintf(`The target parameters [%s] were spread across the envelope top level. Pack them into ONE arguments object: {"action":"call","capability_id":%s,"arguments":{%s}}`,
			strings.Join(shape.spreadKeys, ", "), id, strings.Join(parts, ","))
	}
	return ""
}

// capabilityNameCandidates 汇总可建议的真名：catalog 全量 id + 注册表工具名
// （catalog 缺席时的测试/降级回退）。
func (t *UseCapabilityTool) capabilityNameCandidates() []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 32)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, entry := range t.currentCatalog().Entries {
		add(entry.ID)
	}
	if t.registry != nil {
		for _, name := range t.registry.AllNames() {
			add("tool:" + name)
		}
	}
	sort.Strings(out)
	return out
}

// closestCapabilityNames 找出与错名最接近的 ≤limit 个候选。判据（按强弱）：
// 本地名（末段）完全相等（命名空间写错）→ 距离 0；否则 Levenshtein 距离
// 受 len/3+1（上限 3）容差约束，防乱推荐。
func (t *UseCapabilityTool) closestCapabilityNames(wrongID string, limit int) []string {
	wrongLocal := capabilityLocalName(wrongID)
	if wrongLocal == "" || limit <= 0 {
		return nil
	}
	type scored struct {
		id   string
		dist int
	}
	tolerance := len(wrongLocal)/3 + 1
	if tolerance > 3 {
		tolerance = 3
	}
	var hits []scored
	for _, candidate := range t.capabilityNameCandidates() {
		if candidate == wrongID {
			continue
		}
		candLocal := capabilityLocalName(candidate)
		if candLocal == "" {
			continue
		}
		wl := strings.ToLower(wrongLocal)
		cl := strings.ToLower(candLocal)
		var dist int
		switch {
		case wl == cl:
			dist = 0
		default:
			dist = levenshteinDistance(wl, cl)
			if dist > tolerance {
				continue
			}
		}
		hits = append(hits, scored{id: candidate, dist: dist})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].id < hits[j].id
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.id)
	}
	return out
}

// capabilityLocalName 提取 id 的本地名（末段）：mcp-tool:server/tool → tool，
// tool:remember → remember，裸名原样。
func capabilityLocalName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if i := strings.LastIndexAny(id, ":/"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// capabilityNameCorrection 是「真名错/裸名」两族的代理出口引导：命中最接近
// catalog 真名就给可复制重试例，无论命中与否都给 search(query) 占引用。
// 返回空串表示无可建议（本地名为空等）。
func (t *UseCapabilityTool) capabilityNameCorrection(wrongID string) string {
	wrongID = strings.TrimSpace(wrongID)
	if capabilityLocalName(wrongID) == "" {
		return ""
	}
	var b strings.Builder
	suggestions := t.closestCapabilityNames(wrongID, 3)
	if len(suggestions) > 0 {
		quoted := make([]string, 0, len(suggestions))
		for _, s := range suggestions {
			quoted = append(quoted, strconv.Quote(s))
		}
		fmt.Fprintf(&b, "Did you mean %s? Retry with capability_id=%s, for example: {\"action\":\"call\",\"capability_id\":%s,\"arguments\":{ ...that tool's own parameters... }}.",
			strings.Join(quoted, " or "), quoted[0], quoted[0])
	} else {
		fmt.Fprintf(&b, "No close catalog id matches %q.", wrongID)
	}
	// %q 自带成对引号与转义，直引本地名即可（此前再套一层 escapeJSONString
	// 是双重转义：含引号/反斜杠的本地名会被转义两次）。
	fmt.Fprintf(&b, " To list candidates, run {\"action\":\"search\",\"query\":%q,\"limit\":5} then action=inspect the exact id.",
		capabilityLocalName(wrongID))
	return b.String()
}

// levenshteinDistance 是标准编辑距离（小写字符串间；名字都很短，O(n·m) 可忽略）。
func levenshteinDistance(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(minInt(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
