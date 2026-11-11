package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

// tl1553 钉组：todo_write step_id 控制字符防御三件套。
//
// 现场（2026-11-11）：模型写入的 step_id 携带内嵌 \r（双 \r 形态，与
// use_capability capability_id 同机制族，tl1310 报告 §1——污染以转义形态
// 在 wire 抵达=生成内容），存储层原样入库；后续引用字节精确匹配永远 miss，
// 错误文案把裸 \r 字节直接拼进文本，模型读不到差异 → 重试必败死循环。
// 本文件锁五层验收：
//  1. SanitizeStepID 契约：单 \r/双 \r/\r\n/混合 C0/DEL 全形态清洗；
//     幂等（已清洗输入零变化+零报告）；UTF-8 多字节保真；
//  2. 匹配投影：MatchStepID 双侧清洗后比较，存量脏 baseline 与脏传入
//     两个方向都救活；实质不同的 id 不误配；命中时报告存储原样；
//  3. 写入清洗：receipt 录入（存储主入口）与 ops payload 两路都在入库前
//     剥控制字符——新写入不再产生脏 id；
//  4. 渲染可见化：错误文案里的脏 id 以 strconv.Quote 转义形态显示
//     （display==storage），干净 id 原样零变化；
//  5. resolveTodoParents 的 parent_id 引用同投影。

func TestSanitizeStepIDAllFormsAndIdempotent(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		want       string
		wantBool   bool
		wantBounce bool // 再洗一遍必须零变化（幂等钉）
	}{
		{name: "clean id untouched", in: "plan_step_01", want: "plan_step_01", wantBool: false},
		{
			name: "single CR mid-id (tl1310 single form)",
			in:   "plan\r_step_01",
			want: "plan_step_01", wantBool: true, wantBounce: true,
		},
		{
			name: "double CR mid-id (2026-11-11 field form)",
			in:   "plan\r\r_step\r\r_01",
			want: "plan_step_01", wantBool: true, wantBounce: true,
		},
		{
			name: "CRLF mix",
			in:   "plan\r\n_step\r\n_01",
			want: "plan_step_01", wantBool: true, wantBounce: true,
		},
		{
			name: "mixed control chars with DEL",
			in:   "pl\x00an\t_step\x7f_01",
			want: "plan_step_01", wantBool: true, wantBounce: true,
		},
		{
			name: "trailing CR trimmed by whitespace pass",
			in:   "plan_step_01\r",
			want: "plan_step_01", wantBool: true, wantBounce: true,
		},
		{name: "utf-8 multi-byte untouched", in: "任务\r一步", want: "任务一步", wantBool: true, wantBounce: true},
		{name: "empty id", in: "", want: "", wantBool: false},
		{name: "control-only id cleans to empty", in: "\r\r", want: "", wantBool: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, stripped := SanitizeStepID(test.in)
			if got != test.want || stripped != test.wantBool {
				t.Fatalf("SanitizeStepID(%q) = (%q, %v), want (%q, %v)", test.in, got, stripped, test.want, test.wantBool)
			}
			// 幂等钉：对已清洗输出再洗一遍必须 byte-identical 且零报告。
			bounce, bounceStripped := SanitizeStepID(got)
			if bounce != got || bounceStripped {
				t.Fatalf("idempotency: second pass = (%q, %v), want (%q, false)", bounce, bounceStripped, got)
			}
		})
	}
}

func TestMatchStepIDSanitizedProjection(t *testing.T) {
	todos := []TodoItem{
		{Content: "step one", Status: "completed", StepID: "plan\r\r_step\r\r_01"}, // 存量脏 baseline
		{Content: "step two", Status: "in_progress", StepID: "plan_step_02"},       // 干净
	}
	// 方向一：干净引用 vs 存量脏 id（清洗投影后命中）。
	match, ok := MatchStepID("plan_step_01", todos)
	if !ok || match.Index != 1 {
		t.Fatalf("clean citation vs dirty stored id: ok=%v match=%+v, want hit index 1", ok, match)
	}
	// 命中报告存储原样（投影不改存储字节）。
	if match.StepID != "plan\r\r_step\r\r_01" {
		t.Fatalf("match must report the stored id as-is, got %q", match.StepID)
	}
	// 方向二：脏 verbatim 引用 vs 干净存储（历史污染形态同样命中）。
	if _, ok := MatchStepID("plan\r_step_02", todos); !ok {
		t.Fatal("dirty citation vs clean stored id must hit via projection")
	}
	// 实质不同的 id 不误配。
	if _, ok := MatchStepID("plan_step_03", todos); ok {
		t.Fatal("unrelated id must not match")
	}
	// 与基线行为一致：干净对干净照常命中（tl1553 前的既有行为零回退）。
	if _, ok := MatchStepID("plan_step_02", todos); !ok {
		t.Fatal("clean-vs-clean must still hit")
	}
	if _, ok := MatchStepID("  ", todos); ok {
		t.Fatal("blank citation must not match")
	}
}

func TestReceiptFromToolCallSanitizesTodoWriteStepIDs(t *testing.T) {
	args := json.RawMessage(`{"todos":[
		{"content":"one","status":"completed","step_id":"plan\r\r_step\r\r_01"},
		{"content":"two","status":"pending","step_id":"clean_step_02","parent_id":"plan\r_step\r_01"}
	]}`)
	r := ReceiptFromToolCall("todo_write", args, true, true)
	if len(r.Todos) != 2 {
		t.Fatalf("todos recorded = %d, want 2", len(r.Todos))
	}
	if r.Todos[0].StepID != "plan_step_01" {
		t.Fatalf("stored step_id = %q, want cleaned form", r.Todos[0].StepID)
	}
	if r.Todos[1].ParentID != "plan_step_01" {
		t.Fatalf("stored parent_id = %q, want cleaned form", r.Todos[1].ParentID)
	}
	if r.Todos[1].StepID != "clean_step_02" {
		t.Fatalf("clean step_id rewritten: %q", r.Todos[1].StepID)
	}
}

func TestApplyTodoOpsSanitizedIDsAndVisibleErrors(t *testing.T) {
	baseline := []TodoItem{
		{Content: "one", Status: "completed", StepID: "plan\r\r_step\r\r_01"}, // 存量脏
		{Content: "two", Status: "in_progress", StepID: "plan_step_02"},
	}
	// 脏 op.step_id 引用存量脏 id（双侧同形态）与干净重写形态都要命中。
	applied, err := ApplyTodoOps(baseline, []TodoOp{
		{Op: "delete", StepID: "plan\r\r_step\r\r_01"},
	})
	if err != nil {
		t.Fatalf("dirty op.step_id vs dirty stored id: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("delete via dirty id did not hit, list=%+v", applied)
	}
	applied, err = ApplyTodoOps(baseline, []TodoOp{
		{Op: "move", StepID: "plan_step_01", AfterStepID: "plan_step_02"},
	})
	if err != nil {
		t.Fatalf("clean rewrite vs dirty stored id: %v", err)
	}
	if applied[1].StepID != "plan\r\r_step\r\r_01" {
		t.Fatalf("move must keep the stored id bytes, got %q", applied[1].StepID)
	}
	// miss 时的错误文案可见化：脏 known id 以转义形态出现，无裸控制字节。
	_, err = ApplyTodoOps(baseline, []TodoOp{{Op: "delete", StepID: "nope"}})
	if err == nil {
		t.Fatal("unknown step_id must error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"plan\r\r_step\r\r_01"`) {
		t.Fatalf("error must render the dirty stored id with visible escapes, got: %s", msg)
	}
	if strings.ContainsRune(strings.ReplaceAll(msg, `\r`, ""), '\r') {
		t.Fatalf("error must not carry raw CR bytes, got: %q", msg)
	}
	// insert 的 item.step_id 在入库前清洗。
	applied, err = ApplyTodoOps(nil, []TodoOp{{Op: "insert", Item: &TodoOpItem{Content: "new", Status: "pending", StepID: "fresh\r\r_step\r\r_03"}}})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if applied[0].StepID != "fresh_step_03" {
		t.Fatalf("inserted step_id = %q, want cleaned", applied[0].StepID)
	}
}

func TestResolveTodoParentsSanitizedProjection(t *testing.T) {
	todos := []TodoItem{
		{Content: "parent", Status: "pending", StepID: "plan\r\r_step\r\r_01"},
		{Content: "child", Status: "pending", StepID: "plan_step_02", ParentID: "plan_step_01"},
	}
	parents, err := resolveTodoParents(todos)
	if err != nil {
		t.Fatalf("clean parent_id vs dirty stored id: %v", err)
	}
	if parents[1] != 0 {
		t.Fatalf("parents = %v, want child -> 0", parents)
	}
	// miss 时错误文案可见化（原 %q parent_id 渲染不变，但裸字节被 Go %q 转义）。
	todos = append(todos, TodoItem{Content: "orphan", Status: "pending", StepID: "plan_step_03", ParentID: "missing_step"})
	if _, err := resolveTodoParents(todos); err == nil {
		t.Fatal("orphan parent_id must error")
	}
}

func TestVisibleStepIDRendering(t *testing.T) {
	if got := VisibleStepID("plan_step_01"); got != "plan_step_01" {
		t.Fatalf("clean id must render byte-identical, got %q", got)
	}
	dirty := "plan\r\r_step\r\r_01"
	got := VisibleStepID(dirty)
	if !strings.Contains(got, `\r`) {
		t.Fatalf("dirty id must render the control chars as visible escapes, got %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("rendered form must not carry raw CR bytes, got %q", got)
	}
	joined := VisibleStepIDJoin([]string{"a", dirty, ""})
	if !strings.Contains(joined, `"plan\r\r_step\r\r_01"`) || !strings.HasPrefix(joined, "a, ") {
		t.Fatalf("VisibleStepIDJoin = %q, want clean ids raw and dirty id escaped", joined)
	}
}
