package jsonutil

// tl1432 A2：尾逗号宽松修复器的单元钉。分级管线里它是第 4 级（提取 1-3 级
// 找不到已合法对象后的最后手段）；「无可修复输入逐字节原样返回」是调用方
// 跳过无效重解析、区分「修复过/没动过」的契约。
//
// 反向验证记录（2026-11-07）：把 RemoveTrailingCommas 换成恒等函数，本表
// 「修复」三行与 workflow 包 TestTypedResultFixtureTable_TL1432 的尾逗号行
// 全红；恢复后全绿——钉确实钉在修复行为上。

import (
	"encoding/json"
	"testing"
)

func TestRemoveTrailingCommas_TL1432(t *testing.T) {
	t.Run("失败形态夹具表", func(t *testing.T) {
		cases := []struct {
			name  string
			in    string
			want  string
			valid bool // want 能否被标准库解析（修复后必须可解析）
		}{
			{"对象尾逗号", `{"a":1,}`, `{"a":1}`, true},
			{"数组尾逗号", `{"a":[1,2,]}`, `{"a":[1,2]}`, true},
			{"闭前空白", "{\n  \"a\": 1 ,\n}", "{\n  \"a\": 1 \n}", true},
			{"嵌套双尾逗号", `{"a":[1,{"b":2,},],}`, `{"a":[1,{"b":2}]}`, true},
			{"字符串内逗号不动", `{"a":"x,y}","b":1,}`, `{"a":"x,y}","b":1}`, true},
			{"字符串内转义引号后的逗号不动", `{"a":"他说\"好,","c":1,}`, `{"a":"他说\"好,","c":1}`, true},
			{"成员分隔逗号保留", `{"a":1,"b":2}`, `{"a":1,"b":2}`, true},
			{"无可修复原样返回", `完全不是 JSON 的文本, 也不动`, `完全不是 JSON 的文本, 也不动`, false},
			{"干净对象逐字节原样", "{\n\t\"k\": \"v\"\n}", "{\n\t\"k\": \"v\"\n}", true},
		}
		for _, tc := range cases {
			got := RemoveTrailingCommas(tc.in)
			if got != tc.want {
				t.Fatalf("%s: 修复结果不符\n in:  %q\nwant: %q\n got:  %q", tc.name, tc.in, tc.want, got)
			}
			if tc.valid && !validJSON(got) {
				t.Fatalf("%s: 修复结果应是合法 JSON: %q", tc.name, got)
			}
		}
	})

	t.Run("无可修复输入返回原字符串（调用方靠同串跳过重解析）", func(t *testing.T) {
		in := `{"a":[1,2],"b":"c,d"}`
		if got := RemoveTrailingCommas(in); got != in {
			t.Fatalf("无尾逗号应原样返回\n in:  %q\ngot:  %q", in, got)
		}
	})
}

// validJSON 用标准库判合法（测试内直接用 encoding/json，不引第三方）。
func validJSON(s string) bool {
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}
