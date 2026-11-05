package workflow

// tl1432 A：typed result 容错提取（引擎层单源 parseResultJSON 管线）。
//
// 缺陷：审计线判「严格 JSON.parse 一次失败即整轮报废（node 白跑）过度严格」。
// 修复：解析升级为三级提取——整体解析 → 首{末}朴素切片 → 最后一个平衡顶层
// 对象（jsonutil.LastJSONObject，goaleval 同款机制）；截断输出永不抢救（对象
// 未闭合=数据不完整，猜测补全会伪造结果），错误里点名截断嫌疑供定向重发与
// 排查。截断嫌疑的判定与提取共用一个扫描器（jsonutil.scanTopLevelObjects）。

import (
	"strings"
	"testing"
)

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
