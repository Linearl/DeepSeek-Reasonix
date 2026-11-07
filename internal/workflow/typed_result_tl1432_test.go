package workflow

// tl1432 A：typed result 容错提取（引擎层单源 parseResultJSON 管线）。
//
// 缺陷：审计线判「严格 JSON.parse 一次失败即整轮报废（node 白跑）过度严格」。
// 修复：解析升级为三级提取——整体解析 → 首{末}朴素切片 → 最后一个平衡顶层
// 对象（jsonutil.LastJSONObject，goaleval 同款机制）；截断输出永不抢救（对象
// 未闭合=数据不完整，猜测补全会伪造结果），错误里点名截断嫌疑供定向重发与
// 排查。截断嫌疑的判定与提取共用一个扫描器（jsonutil.scanTopLevelObjects）。

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// sequenceRunner 按调用次序返回答案（越界重复最后一个），并捕获全部 prompt
// 与 Occurrence——B 段定向重发的断言基座。
type sequenceRunner struct {
	answers     []string
	prompts     []string
	occurrences []int
}

func (r *sequenceRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	r.prompts = append(r.prompts, req.Prompt)
	r.occurrences = append(r.occurrences, req.Occurrence)
	i := len(r.prompts) - 1
	if i >= len(r.answers) {
		i = len(r.answers) - 1
	}
	return r.answers[i], nil
}

// tl1432BRun 构造最小 typed-result 工作流并跑完，返回 runState 与 runAll 错误。
func tl1432BRun(t *testing.T, runner SubagentRunner, eng *Engine) (*runState, error) {
	t.Helper()
	doc := `{
	  "version": 1,
	  "name": "tl1432",
	  "nodes": [
	    {"id":"finder","type":"subagent","prompt":"find issues",
	     "result":[{"name":"findings","type":"array"}],
	     "findings_field":"findings"}
	  ],
	  "edges": []
	}`
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if eng == nil {
		eng = New(runner, nil, nil) // 零值 Engine：ResultParseRetries=0 → 默认重发 1 次
	} else {
		eng.Subagents = runner
	}
	run := newRunState(eng, wf)
	return run, run.runAll(context.Background())
}

func TestTypedResultDirectedRetry_TL1432(t *testing.T) {
	t.Run("首轮非JSON+定向重发合法→节点成功（集成验收①）", func(t *testing.T) {
		good := `{"findings":[{"where":"a.go:1","what":"x","evidence":"y","severity":"low"}],"count":1}`
		runner := &sequenceRunner{answers: []string{
			"抱歉，我的结论是分析文字而非JSON。",
			good,
		}}
		run, err := tl1432BRun(t, runner, nil)
		if err != nil {
			t.Fatalf("重发成功后 runAll 不应报错: %v", err)
		}
		if run.status["finder"] != stDone {
			t.Fatalf("finder 应为 stDone，实际 %v", run.status["finder"])
		}
		if len(runner.prompts) != 2 {
			t.Fatalf("应恰好 2 次调用（1 原发 + 1 重发，防循环），实际 %d", len(runner.prompts))
		}
		retryPrompt := runner.prompts[1]
		if !strings.Contains(retryPrompt, directedRetryInstruction) {
			t.Fatalf("重发 prompt 应带只输出 JSON 的定向指令:\n%s", retryPrompt)
		}
		if !strings.Contains(retryPrompt, "find issues") {
			t.Fatalf("重发 prompt 应带原 prompt 上下文:\n%s", retryPrompt)
		}
		if runner.occurrences[1] != 2 {
			t.Fatalf("重发 Occurrence 应为 2（面板登记 id 不复用，tl1324 ⑤），实际 %d", runner.occurrences[1])
		}
		if len(run.findings["finder"]) != 1 {
			t.Fatalf("重发成功后 findings 应正常落袋，实际 %v", run.findings["finder"])
		}
	})

	t.Run("首轮容错可提取（栅栏）→不触发重发（A 段先于 B 段）", func(t *testing.T) {
		fence := "```"
		fenced := fence + "json\n{\"findings\":[],\"count\":0}\n" + fence + "\n以上。"
		runner := &sequenceRunner{answers: []string{fenced}}
		_, err := tl1432BRun(t, runner, nil)
		if err != nil {
			t.Fatalf("容错提取应直接成功: %v", err)
		}
		if len(runner.prompts) != 1 {
			t.Fatalf("容错可提取时不应重发，实际调用 %d 次", len(runner.prompts))
		}
	})

	t.Run("两轮均非法→节点失败且错误含双次摘要（集成验收②）", func(t *testing.T) {
		runner := &sequenceRunner{answers: []string{
			"第一轮：纯自然语言，不含任何 JSON。",
			"重发后仍是纯自然语言答案。",
		}}
		run, err := tl1432BRun(t, runner, nil)
		if err == nil {
			t.Fatal("两轮均非法应判节点失败")
		}
		if run.status["finder"] != stAborted {
			t.Fatalf("finder 应为 stAborted，实际 %v", run.status["finder"])
		}
		if len(runner.prompts) != 2 {
			t.Fatalf("重发次数应为 1（共 2 次调用，防循环），实际 %d", len(runner.prompts))
		}
		for _, frag := range []string{"第一轮：纯自然语言，不含任何 JSON。", "重发后仍是纯自然语言答案。", "detail:", "first output:", "retry output:"} {
			if !strings.Contains(err.Error(), frag) {
				t.Fatalf("错误应含双次输出摘要（缺 %q）: %q", frag, err.Error())
			}
		}
		if !strings.Contains(err.Error(), "1 directed retry") {
			t.Fatalf("人话句应写明重发预算已花完（en 默认目录）: %q", err.Error())
		}
	})

	t.Run("重发预算可配置：ResultParseRetries=2 时最多 3 次调用", func(t *testing.T) {
		runner := &sequenceRunner{answers: []string{"还是不行1", "还是不行2", "还是不行3"}}
		eng := New(nil, nil, nil)
		eng.ResultParseRetries = 2
		_, err := tl1432BRun(t, runner, eng)
		if err == nil {
			t.Fatal("三轮均非法应失败")
		}
		if len(runner.prompts) != 3 {
			t.Fatalf("预算 2 应共 3 次调用，实际 %d", len(runner.prompts))
		}
		if !strings.Contains(err.Error(), "2 directed retry") {
			t.Fatalf("人话句应写明实际预算: %q", err.Error())
		}
	})

	t.Run("ResultParseRetries=-1 禁用重发：一次失败即报废（可回退到旧行为）", func(t *testing.T) {
		runner := &sequenceRunner{answers: []string{"纯散文。"}}
		eng := New(nil, nil, nil)
		eng.ResultParseRetries = -1
		_, err := tl1432BRun(t, runner, eng)
		if err == nil {
			t.Fatal("禁用重发后仍应失败")
		}
		if len(runner.prompts) != 1 {
			t.Fatalf("禁用重发应只调用 1 次，实际 %d", len(runner.prompts))
		}
	})

	t.Run("重发遇到世界错误→立即失败不吞错", func(t *testing.T) {
		worldErr := context.Canceled
		runner := &failSecondRunner{first: "坏输出", err: worldErr}
		eng := New(nil, nil, nil)
		_, err := tl1432BRun(t, runner, eng)
		if err == nil || err == worldErr {
			t.Fatalf("世界错误应包装后上抛，实际 %v", err)
		}
		if !strings.Contains(err.Error(), "subagent") || !strings.Contains(err.Error(), worldErr.Error()) {
			t.Fatalf("错误应保留世界错误链路: %q", err.Error())
		}
	})
}

// failSecondRunner 首次返回坏输出，第二次直接返回世界错误（重发遇超时/取消
// 的 fail-fast 钉）。
type failSecondRunner struct {
	first string
	err   error
	calls int
}

func (r *failSecondRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	r.calls++
	if r.calls == 1 {
		return r.first, nil
	}
	return "", r.err
}

// TestConfirmNodeNoDirectedRetry_TL1432：confirm 节点解析失败的语义是「未确认」
// （保留 findings、run 继续），不进定向重发——范围裁决钉。
func TestConfirmNodeNoDirectedRetry_TL1432(t *testing.T) {
	runner := &sequenceRunner{answers: []string{
		`{"findings":[{"where":"a.go:1","what":"x","evidence":"y","severity":"low"}],"count":1}`,
		"我确认过该缺陷， reproduced。（纯文字，非 JSON）",
	}}
	doc := `{
	  "version": 1,
	  "name": "tl1432-confirm",
	  "nodes": [
	    {"id":"finder","type":"subagent","prompt":"find issues",
	     "result":[{"name":"findings","type":"array"}],
	     "findings_field":"findings"},
	    {"id":"checker","type":"confirm","target":"finder","prompt":"reproduce"}
	  ],
	  "edges": [{"from":"finder","to":"checker"}]
	}`
	wf, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	run := newRunState(New(runner, nil, nil), wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("confirm 解析失败不应报废 run（unconfirmed 语义）: %v", err)
	}
	if len(runner.prompts) != 2 {
		t.Fatalf("confirm 不重发：应恰好 finder+checker 两次调用，实际 %d", len(runner.prompts))
	}
	if got := len(run.bag.snapshot()); got != 1 || run.bag.snapshot()[0].Status != FindingUnconfirmed {
		t.Fatalf("findings 应保持 unconfirmed，实际 %+v", run.bag.snapshot())
	}
}

func TestParseResultJSONTolerant_TL1432(t *testing.T) {
	t.Run("纯 JSON 原样通过（一级，现状回归）", func(t *testing.T) {
		obj, err := parseResultJSON(`{"findings":[],"count":0}`)
		if err != nil {
			t.Fatalf("纯 JSON 应直接通过: %v", err)
		}
		if obj["count"].(float64) != 0 {
			t.Fatalf("count 应为 0，实际 %v", obj["count"])
		}
	})

	t.Run("markdown 栅栏包裹+前后散文（三级平衡扫描剥栅栏）", func(t *testing.T) {
		answer := "这是我的分析结论。\n\n```json\n{\"findings\":[{\"where\":\"a.go:1\",\"what\":\"x\",\"evidence\":\"y\",\"severity\":\"low\"}],\"count\":1}\n```\n\n以上是结果，请查收。"
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("栅栏包裹应被平衡扫描剥掉: %v", err)
		}
		if len(obj["findings"].([]any)) != 1 {
			t.Fatalf("应取到 findings，实际 %v", obj)
		}
	})

	t.Run("前后噪音含花括号（示例引用在前真答案在后，取最后一个）", func(t *testing.T) {
		answer := `我考虑过 {"outcome":"retry"} 这种写法，最终结果如下：{"findings":[{"where":"b.go:2","what":"y","evidence":"z","severity":"high"}],"count":1}`
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("多对象噪音应由平衡扫描取最后一个: %v", err)
		}
		if _, has := obj["findings"]; !has {
			t.Fatalf("应取到尾部真答案（含 findings），实际 %v", obj)
		}
	})

	t.Run("嵌套平衡+字符串内花括号不被误配", func(t *testing.T) {
		answer := `{"outer":{"inner":{"note":"这里有个 } 字符"}}} 完毕，上文的 } 只是字符串内容`
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("嵌套+字符串内花括号应正确配对: %v", err)
		}
		if _, has := obj["outer"]; !has {
			t.Fatalf("应取到完整嵌套对象，实际 %v", obj)
		}
	})

	t.Run("对象后散文含右括号（朴素切片失败，平衡扫描兜住）", func(t *testing.T) {
		answer := `{"count":7} —— 注意上面的 } 只是笔记结尾`
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("尾随散文含 } 应由平衡扫描兜住: %v", err)
		}
		if obj["count"].(float64) != 7 {
			t.Fatalf("count 应为 7，实际 %v", obj)
		}
	})

	t.Run("Final answer: 前缀+栅栏组合（finalAnswerBody 先剥前缀）", func(t *testing.T) {
		answer := "过程中想了很多。\nFinal answer: ```json\n{\"count\":3}\n```"
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("Final answer: + 栅栏组合应解析成功: %v", err)
		}
		if obj["count"].(float64) != 3 {
			t.Fatalf("count 应为 3，实际 %v", obj)
		}
	})

	t.Run("截断输出永不抢救，错误点名截断嫌疑", func(t *testing.T) {
		answer := `{"findings":[{"where":"c.go:3"}],"count":1,"coverage":[1,2`
		obj, err := parseResultJSON(answer)
		if err == nil {
			t.Fatalf("截断 JSON 不得被抢救（补全=伪造结果），实际 %v", obj)
		}
		if !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("错误应点名截断嫌疑，实际 %q", err.Error())
		}
	})

	t.Run("纯散文失败且不误报截断", func(t *testing.T) {
		obj, err := parseResultJSON("我没有找到任何问题，一切正常。")
		if err == nil {
			t.Fatalf("纯散文应失败，实际 %v", obj)
		}
		if strings.Contains(err.Error(), "truncated") {
			t.Fatalf("纯散文不应报截断嫌疑，实际 %q", err.Error())
		}
		if !strings.Contains(err.Error(), "answer is not a JSON object") {
			t.Fatalf("错误应保留既有文案主干，实际 %q", err.Error())
		}
	})

	t.Run("结构校验：提取结果必须能解成 map 才按正常结果走", func(t *testing.T) {
		// 三级提取的每一级都以「解出 map[string]any」为通过判据；解不出
		// （如纯散文、无闭合对象）即失败。既有边界钉：顶层是数组时，既有
		// 朴素切片本就会取到数组内的首个对象（tl1432 前行为如此，不在本件
		// 收窄范围——如需类型收紧另开件，不随容错修复夹带行为变更）。
		fence := "```"
		answer := fence + "json\n[{\"findings\":[]}]\n" + fence
		obj, err := parseResultJSON(answer)
		if err != nil {
			t.Fatalf("既有朴素切片行为（数组包对象取内层）应保持: %v", err)
		}
		if _, has := obj["findings"]; !has {
			t.Fatalf("应取到数组内对象，实际 %v", obj)
		}
		// 截断的对象连 map 都解不出 → 失败（这正是「可校验」的含义）。
		if _, err := parseResultJSON(`{"a":1,"b":[1,2`); err == nil {
			t.Fatal("截断对象应失败")
		}
	})
}

// TestTypedResultFixtureTable_TL1432：任务书失败形态夹具表（表驱动，A2 补全
// 尾逗号级）。wantMethod 断言「哪一级救回」——分级管线的每一级都必须有钉，
// 否则撤掉某级时表不会红。单引号行按设计不修复（引号改写会改写语义，
// 伪造成本 > 收益，该形态走 tl1432 B 定向重发治疗）；截断行永不抢救。
func TestTypedResultFixtureTable_TL1432(t *testing.T) {
	fence := "```"
	cases := []struct {
		name       string
		answer     string
		wantOK     bool
		wantMethod string // wantOK 时断言救回的级
		check      func(t *testing.T, obj map[string]any)
	}{
		{"纯JSON(一级strict现状回归)", `{"findings":[],"count":0}`, true, parseMethodStrict, nil},
		{"围栏+前后散文(二级朴素切片)", "分析如下。\n\n" + fence + "json\n{\"findings\":[],\"count\":1}\n" + fence + "\n以上。", true, parseMethodSlice, nil},
		{"前散文含示例对象(三级平衡扫描取最后)", `我考虑过 {"outcome":"retry"}，最终：{"count":2}`, true, parseMethodScan, nil},
		{"尾逗号_对象内(四级宽松修复)", `{"findings":[],"count":3,}`, true, parseMethodLenient, func(t *testing.T, obj map[string]any) {
			if obj["count"].(float64) != 3 {
				t.Fatalf("修复后 count 应为 3，实际 %v", obj["count"])
			}
		}},
		{"尾逗号_数组内(四级宽松修复)", `{"findings":[{"where":"a"}],}`, true, parseMethodLenient, func(t *testing.T, obj map[string]any) {
			if len(obj["findings"].([]any)) != 1 {
				t.Fatalf("修复后 findings 应为 1 条，实际 %v", obj["findings"])
			}
		}},
		{"尾逗号+围栏+前后散文(三级提取失败四级修复兜住)", "说明。\n" + fence + "json\n{\"count\":4,}\n" + fence + "\n尾注。", true, parseMethodLenient, nil},
		{"尾逗号+前噪括号(朴素切片报废、平衡扫描候选被修复)", `noise {"x":1} 最终：{"count":5,}`, true, parseMethodLenient, func(t *testing.T, obj map[string]any) {
			if obj["count"].(float64) != 5 {
				t.Fatalf("应取到尾部真答案 count=5，实际 %v", obj["count"])
			}
		}},
		{"单引号_按设计不修复(留给B定向重发)", `{'findings':[],'count':6}`, false, "", nil},
		{"截断_永不抢救且错误点名", `{"findings":[{"where":"c"}],"count":7,"cov":[1,2`, false, "", nil},
		{"纯散文_失败", "没找到任何问题。", false, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj, method, err := parseResultJSONWithMethod(tc.answer)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("应提取成功: %v", err)
				}
				if method != tc.wantMethod {
					t.Fatalf("救回的级应为 %s，实际 %s", tc.wantMethod, method)
				}
				if tc.check != nil {
					tc.check(t, obj)
				}
				return
			}
			if err == nil {
				t.Fatalf("应判失败，实际解出 %v", obj)
			}
			if !strings.Contains(err.Error(), "answer is not a JSON object") {
				t.Fatalf("错误文案主干应保留: %q", err.Error())
			}
			if strings.Contains(tc.answer, "cov") && !strings.Contains(err.Error(), "truncated") {
				t.Fatalf("截断行错误应点名截断嫌疑: %q", err.Error())
			}
		})
	}
}

// TestTypedResultRescueLog_TL1432：容错提取防静默钉（tl1432 A2）——非严格级
// 救回时 completeSubagent 必须落一条含「容错提取」的 WARN（含 node 与 method），
// 且恰好一条（重发预检走静默 wrapper，完成尾是唯一权威日志点）；严格解析成功
// 不落此日志。
func TestTypedResultRescueLog_TL1432(t *testing.T) {
	cases := []struct {
		name       string
		answer     string
		wantMethod string // 非空 = 断言出现 WARN 且含该 method；空 = 断言无 WARN
	}{
		{"围栏救回_tolerant-slice", "说明。\n```json\n{\"findings\":[],\"count\":1}\n```", "tolerant-slice"},
		{"尾逗号救回_tolerant-lenient-comma", `{"findings":[],"count":2,}`, "tolerant-lenient-comma"},
		{"严格解析成功_无WARN", `{"findings":[],"count":3}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			defer slog.SetDefault(prev)
			runner := &sequenceRunner{answers: []string{tc.answer}}
			if _, err := tl1432BRun(t, runner, nil); err != nil {
				t.Fatalf("runAll 不应失败: %v", err)
			}
			logs := buf.String()
			if tc.wantMethod == "" {
				if strings.Contains(logs, "容错提取") {
					t.Fatalf("严格成功不应落容错提取 WARN，实际: %s", logs)
				}
				return
			}
			// 按行计数：日志文案本身含「容错提取」两次（容错提取——…容错提取
			// 成功），按子串数会把一条钉成两条。
			n := 0
			for _, ln := range strings.Split(logs, "\n") {
				if strings.Contains(ln, "容错提取") {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("容错提取 WARN 应恰好一条（防重复/防静默），实际 %d 条: %s", n, logs)
			}
			for _, frag := range []string{tc.wantMethod, "node=finder", "WARN"} {
				if !strings.Contains(logs, frag) {
					t.Fatalf("WARN 应含 %q，实际: %s", frag, logs)
				}
			}
		})
	}
}
