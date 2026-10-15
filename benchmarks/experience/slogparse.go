package main

// 任务 414 阶段 1（EBDD 体验评测集 · E 柱）：desktop.log 的 slog 文本行解析。
//
// 只做读取：所有打点都是产品代码已有的观测线（boot: stage timings、
// open-topic summary、tab switch timing、open session slow/failed、
// switch-tab summary、tab switch render、transcript first frame），
// 本文件不改变任何产品行为，只把人肉 grep 变成可重跑的读数。

import (
	"strconv"
	"strings"
	"time"
)

// slogLine 是一行 desktop.log 的最小结构化视图。
type slogLine struct {
	ts    time.Time
	level string
	msg   string
	attrs map[string]string // msg 之外的 key=value（值为去引号后的原文）
}

// parseSlogTextLine 解析 slog TextHandler 的一行输出。
// 格式：time=<RFC3339> level=INFO msg="带空格的消息" key=value key="带空格 值"。
// 值要么是裸 token（不含空格），要么是 Go strconv.Quote 风格的双引号串。
// 无法解析时间戳的行返回 ok=false（调用方按行丢弃）。
func parseSlogTextLine(line string) (slogLine, bool) {
	var out slogLine
	out.attrs = map[string]string{}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return out, false
	}
	i := 0
	n := len(line)
	for i < n {
		// 跳过字段间空白。
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		eq := strings.IndexByte(line[i:], '=')
		if eq < 0 {
			break // 尾部无 k=v 的杂质，忽略
		}
		key := line[i : i+eq]
		i += eq + 1
		if i >= n {
			out.attrs[key] = ""
			break
		}
		var val string
		if line[i] == '"' {
			// 带引号值：找到未被反斜杠转义的收尾引号，再 strconv.Unquote。
			j := i + 1
			for j < n {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == '"' {
					break
				}
				j++
			}
			if j > n {
				break // 引号未闭合：截断行，丢弃
			}
			unq, err := strconv.Unquote(line[i : j+1])
			if err != nil {
				unq = line[i+1 : j] // 容错：原样取内文
			}
			val = unq
			i = j + 1
		} else {
			// 裸值：读到下一个空白。
			j := i
			for j < n && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			val = line[i:j]
			i = j
		}
		switch key {
		case "time":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				out.ts = t
			}
		case "level":
			out.level = val
		case "msg":
			out.msg = val
		default:
			out.attrs[key] = val
		}
	}
	if out.msg == "" || out.ts.IsZero() {
		return out, false
	}
	return out, true
}

// parseKVList 解析 detail="tab=… seq=1 total=504ms history/bridge=498ms"
// 这类空格分隔的 k=v 列表（值可带引号）。返回不带单位后缀的原值字符串。
func parseKVList(s string) map[string]string {
	m := map[string]string{}
	i, n := 0, len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := s[i : i+eq]
		i += eq + 1
		if i >= n {
			m[key] = ""
			break
		}
		var val string
		if s[i] == '"' {
			j := i + 1
			for j < n {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == '"' {
					break
				}
				j++
			}
			if j > n {
				break
			}
			unq, err := strconv.Unquote(s[i : j+1])
			if err != nil {
				unq = s[i+1 : j]
			}
			val = unq
			i = j + 1
		} else {
			j := i
			for j < n && s[j] != ' ' && s[j] != '\t' {
				j++
			}
			val = s[i:j]
			i = j
		}
		m[key] = val
	}
	return m
}

// parseMs 从 "504" / "504ms" 形态的值里取毫秒数。解析失败返回 ok=false。
func parseMs(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "ms")
	if v == "" {
		return 0, false
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	return ms, true
}

// parseBootSummary 解析 bootTimer.summary() 的一行：
//
//	total=4754ms config=238ms extensions=2ms session=192ms provider=0ms
//	prompt=1669ms mcp=82ms agent:tools=2561ms agent=0ms controller=1ms finalize=6ms
//
// 返回 total 与各阶段毫秒表。畸形 summary 返回 ok=false。
func parseBootSummary(summary string) (totalMS int64, stages map[string]int64, ok bool) {
	stages = map[string]int64{}
	for k, v := range parseKVList(summary) {
		ms, ok := parseMs(v)
		if !ok {
			continue
		}
		if k == "total" {
			totalMS = ms
			continue
		}
		stages[k] = ms
	}
	if totalMS == 0 && len(stages) == 0 {
		return 0, nil, false
	}
	return totalMS, stages, true
}
