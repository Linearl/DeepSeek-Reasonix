package evidence

import (
	"strconv"
	"strings"
)

// tl1553: todo_write step_id 控制字符防御（三件套：写入清洗 / 匹配投影 /
// 渲染可见化）。
//
// 2026-11-11 todo_write 现场实测：模型写入的 step_id 携带嵌入控制字符
// （tokenizer 在下划线切分点吐 \r，与 use_capability capability_id 同机制
// 族，见 tl1310 报告），存储层原样入库；后续引用与字节精确匹配必然 miss，
// 而错误文案与 todo_read 回显把裸 \r 字节打进文本——裸控制字节在模型视角
// 不可见（tokenizer 视作空白），模型看到的是「干净形态」，于是「我传的就
// 是这个」→ 重试必败 → 死循环（四种编码重试全败，最终用户全量重写 todos
// 才绕出）。display==storage 的落差正是放大器：显示干净形态让模型永远看
// 不到自己错在哪。
//
// 三层各自的职责：
//  1. 写入清洗（SanitizeStepID 用于 todo_write/ops 入口与 receipt 录入）：
//     新写入不再产生脏 id——与 internal/agent 的 sanitizeCapabilityID
//     （task 975 / tl1310）同一机制、同一字节语义（剥 C0 0x00-0x1f 与 DEL
//     0x7f，>=0x80 原样，保 UTF-8；幂等：干净输入零变化）。
//  2. 匹配投影（MatchStepID / ops indexOf / resolveTodoParents）：双侧清洗
//     后比较——不改存储字节，救活存量脏 baseline（模型传干净形态也能命中）
//     与脏传入（verbatim 复制的历史污染形态同样命中）。
//  3. 渲染可见化（VisibleStepID）：错误文案与 todo_read 回显中的 id 含控制
//     字符时以 strconv.Quote 转义形态显示（\r 渲染为字面两字符），干净 id
//     原样——模型永远能看到存储里的真实形态，display==storage 落差消失。

// stepIDHasControlCharacter reports whether s contains any C0 control
// character (0x00-0x1f) or DEL (0x7f). Bytes >= 0x80 pass untouched so
// multi-byte UTF-8 ids are never reinterpreted. Same byte semantics as
// internal/agent hasControlCharacter (task 975); kept local because evidence
// must not import agent (agent -> tool -> evidence is the dependency
// direction).
func stepIDHasControlCharacter(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// SanitizeStepID strips embedded ASCII control characters and trims outer
// whitespace from a todo step id. Embedded control characters are never
// legitimate inside a step id (ids are plan_step_NN style ASCII or host-minted
// auto_<hash> forms), so removal is safe; bytes >= 0x80 pass through so a
// UTF-8 id survives byte-for-byte. Idempotent by construction: a clean input
// short-circuits on the detection scan and TrimSpace of a trimmed string is
// the string itself — the bool reports whether anything was stripped so the
// caller can keep the heal observable instead of silently rewriting output.
func SanitizeStepID(s string) (string, bool) {
	if !stepIDHasControlCharacter(s) {
		return strings.TrimSpace(s), false
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String()), true
}

// VisibleStepID renders a step id for a model-facing text surface (error
// messages, todo_read echo). A clean id renders byte-identical; an id with
// embedded control characters renders in strconv.Quote form so the invisible
// bytes become visible escapes ("plan\r_step" shows the \r literally) —
// without this the raw bytes are invisible to the model, which reads the
// polluted id as its clean form and can never see what it got wrong
// (display==storage gap, the tl1553 retry-loop amplifier). Storage bytes are
// never changed by rendering.
func VisibleStepID(id string) string {
	if id == "" || !stepIDHasControlCharacter(id) {
		return id
	}
	return strconv.Quote(id)
}

// VisibleStepIDJoin joins step ids for a model-facing list, rendering each id
// through VisibleStepID. Clean lists render exactly like strings.Join;
// polluted ids surface their control characters as visible escapes.
func VisibleStepIDJoin(ids []string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = VisibleStepID(id)
	}
	return strings.Join(parts, ", ")
}
