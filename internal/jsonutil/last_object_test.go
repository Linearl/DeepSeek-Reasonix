package jsonutil

import "testing"

// 钉 LastJSONObject 的既有语义（goaleval.parseVerdict 依赖）+ tl1432 A 新增的
// HasUnclosedObject 截断检测。两出口共用一个扫描器（scanTopLevelObjects），
// 这里把两个出口的语义各案钉全。
func TestLastJSONObject(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // "" = not ok
	}{
		{"纯 JSON 原样返回", `{"a":1}`, `{"a":1}`},
		{"散文包裹单对象", "好的，结果如下：\n{\"a\":1}\n以上。", `{"a":1}`},
		{"markdown 栅栏包裹", "```json\n{\"a\":1}\n```\n完成", `{"a":1}`},
		{"栅栏语言标记不影响", "```json\n{\"a\":{\"b\":2}}\n```", `{"a":{"b":2}}`},
		{"示例引用在前真答案在后取最后一个", `I considered {"outcome":"continue"} … Final: {"outcome":"complete"}`, `{"outcome":"complete"}`},
		{"字符串内的花括号不参与配对", `{"a":"}","b":1} 尾注 }`, `{"a":"}","b":1}`},
		{"嵌套平衡", `{"outer":{"inner":{"deep":1}}} 完成`, `{"outer":{"inner":{"deep":1}}}`},
		{"对象后散文含右括号", `{"a":1} —— 上面的 } 只是笔记`, `{"a":1}`},
		{"尾部未闭合片段被忽略（返回前一个完整对象）", `{"x":1} {"trunc`, `{"x":1}`},
		{"无对象", "没有花括号的散文", ""},
		{"空串", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LastJSONObject(tc.text)
			if tc.want == "" {
				if ok {
					t.Fatalf("应提取失败，实际 %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("LastJSONObject = %q (ok=%v), want %q", got, ok, tc.want)
			}
		})
	}
}

func TestHasUnclosedObject(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"完整对象", `{"a":1}`, false},
		{"散文+完整对象", "结果：\n{\"a\":1}\n完毕", false},
		{"对象中途截断", `{"a":1,"b":[1,2`, true},
		{"对象未闭合且尾随散文", `{"a":1} {"trunc`, true},
		{"字符串未闭合", `{"a":"unterminated`, true},
		{"纯散文", "no braces at all", false},
		{"空串", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasUnclosedObject(tc.text); got != tc.want {
				t.Fatalf("HasUnclosedObject = %v, want %v", got, tc.want)
			}
		})
	}
}
