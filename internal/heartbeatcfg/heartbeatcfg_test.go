package heartbeatcfg

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustProblems(t *testing.T, data string) []Problem {
	t.Helper()
	return Validate([]byte(data))
}

func kinds(problems []Problem) map[ProblemKind]int {
	out := make(map[ProblemKind]int)
	for _, p := range problems {
		out[p.Kind]++
	}
	return out
}

func findByField(problems []Problem, field string) *Problem {
	for i := range problems {
		if problems[i].Field == field {
			return &problems[i]
		}
	}
	return nil
}

// 合法配置：三轮实测里每一种 interval 形态（duration/纯分钟/cron/schedule）
// 都要零问题——validate 的零误报基线。
func TestValidateAcceptsEveryEngineIntervalForm(t *testing.T) {
	for _, interval := range []string{"5m", "1h", "30s", "30", "1h30m", "0 9 * * 1-5", "*/15 * * * *", "24h|daily@09:00", "12h|weekly:mon,fri@08:30", "1h|biweekly:wed@09:00", "24h|monthly:15@09:00", "24h|yearly:3-1@09:00"} {
		data := `{"schemaVersion":4,"revision":1,"tasks":[{"id":"t1","title":"T","prompt":"P","interval":` + quote(interval) + `,"enabled":true,"approvalMode":"yolo"}]}`
		if problems := mustProblems(t, data); len(problems) != 0 {
			t.Fatalf("interval %q: unexpected problems: %+v", interval, problems)
		}
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// 验收 2 第一类：Unmarshal 错误显式报告，逐字段。事故原样：createdAt 写成
// 字符串——必须报出 tasks[0].createdAt 与行号，而不是静默丢整个配置。
func TestValidateReportsWrongTypedCreatedAtPerField(t *testing.T) {
	data := "{\n  \"schemaVersion\": 4,\n  \"tasks\": [\n    {\"id\": \"a\", \"interval\": \"5m\", \"enabled\": true, \"createdAt\": \"yesterday\"}\n  ]\n}"
	problems := mustProblems(t, data)
	if len(problems) != 1 {
		t.Fatalf("problems=%+v, want exactly the createdAt one", problems)
	}
	p := findByField(problems, "tasks[0].createdAt")
	if p == nil {
		t.Fatalf("missing tasks[0].createdAt problem: %+v", problems)
	}
	if p.Kind != KindUnmarshal {
		t.Fatalf("kind=%v want unmarshal", p.Kind)
	}
	if p.Line != 4 {
		t.Fatalf("line=%d want 4", p.Line)
	}
	if !strings.Contains(p.Message, "expected a number") {
		t.Fatalf("message=%q want it to say a number was expected", p.Message)
	}
}

// Unmarshal 类第二形态：notifyChannels 字符串 vs *bool（事故第二轮的第二处
// 类型错）。bool-null 三态都要放行。
func TestValidateReportsNotifyChannelsTypeAndAllowsNull(t *testing.T) {
	bad := `{"schemaVersion":4,"tasks":[{"id":"a","interval":"5m","notifyChannels":"yes"}]}`
	problems := mustProblems(t, bad)
	p := findByField(problems, "tasks[0].notifyChannels")
	if p == nil || p.Kind != KindUnmarshal {
		t.Fatalf("notifyChannels problem missing/wrong: %+v", problems)
	}
	if !strings.Contains(p.Message, "true, false or null") {
		t.Fatalf("message=%q want tri-state hint", p.Message)
	}

	for _, v := range []string{"true", "false", "null"} {
		ok := `{"schemaVersion":4,"tasks":[{"id":"a","interval":"5m","notifyChannels":` + v + `}]}`
		if problems := mustProblems(t, ok); len(problems) != 0 {
			t.Fatalf("notifyChannels=%s: unexpected problems: %+v", v, problems)
		}
	}
}

// 一份配置里两个 task 各有一个坏字段：逐 task 独立解码让两处都报出来——
// 这正是旧写入器逐个吞掉错误要防的「一次只看见第一个错」。
func TestValidateReportsEachTaskTypeErrorIndependently(t *testing.T) {
	data := `{"schemaVersion":4,"tasks":[
		{"id":"a","interval":"5m","maxRuns":"3"},
		{"id":"b","interval":"5m","reuseSession":"sure"}
	]}`
	problems := mustProblems(t, data)
	if got := kinds(problems)[KindUnmarshal]; got != 2 {
		t.Fatalf("unmarshal problems=%d want 2: %+v", got, problems)
	}
	if findByField(problems, "tasks[0].maxRuns") == nil || findByField(problems, "tasks[1].reuseSession") == nil {
		t.Fatalf("per-field problems missing: %+v", problems)
	}
}

// 验收 2 第二类：重复 id 逐个报（首个登记，其后每个重复者一条）。
func TestValidateReportsDuplicateIDs(t *testing.T) {
	data := `{"schemaVersion":4,"tasks":[
		{"id":"dup","interval":"5m"},
		{"id":"other","interval":"5m"},
		{"id":"dup","interval":"1h"},
		{"id":"dup","interval":"30s"}
	]}`
	problems := mustProblems(t, data)
	dups := 0
	for _, p := range problems {
		if p.Kind == KindDuplicateID {
			dups++
			if p.TaskIndex != 2 && p.TaskIndex != 3 {
				t.Fatalf("duplicate reported at tasks[%d], want only 2 and 3", p.TaskIndex)
			}
			if !strings.Contains(p.Message, `duplicates tasks[0]`) {
				t.Fatalf("message=%q want the first-occurrence pointer", p.Message)
			}
		}
	}
	if dups != 2 {
		t.Fatalf("duplicate problems=%d want 2: %+v", dups, problems)
	}
}

// 验收 2 第三类：interval 格式——引擎永远无法触发的形态逐个报。
func TestValidateReportsUnfireableIntervals(t *testing.T) {
	for _, interval := range []string{"", "abc", "every 5m", "0 99 * * *", "weekly:xxx@09:00", "daily@25:00"} {
		data := `{"schemaVersion":4,"tasks":[{"id":"a","interval":` + quote(interval) + `}]}`
		problems := mustProblems(t, data)
		if len(problems) != 1 || problems[0].Kind != KindInterval {
			t.Fatalf("interval %q: problems=%+v want exactly one interval kind", interval, problems)
		}
		if !strings.Contains(problems[0].Message, "never fire") {
			t.Fatalf("interval %q: message=%q want the never-fire consequence", interval, problems[0].Message)
		}
	}
}

// schedule 形态的对齐：引擎 parseHeartbeatSchedule 要求 '<duration>|' 前缀
// （裸 "daily@09:00" 引擎同样永不触发，必须报）；schedule 段不认识时引擎
// 回退 '|' 前的 duration，validate 同步回退（"24h|weekly:xxx@09:00" 按 24h 跑）。
func TestValidateScheduleSuffixMatchesEngineAcceptance(t *testing.T) {
	rejected := []string{"|daily@09:00", "x|weekly:mon@09:00", "daily@09:00", "weekly:mon,fri@08:30", "monthly:15@09:00", "yearly:3-1@09:00"}
	for _, interval := range rejected {
		if ValidInterval(interval) {
			t.Fatalf("ValidInterval(%q)=true, engine rejects it (no schedule or bad clock)", interval)
		}
	}
	accepted := []string{"24h|daily@09:00", "1h|monthly:99@09:00", "30m|yearly:13-32@09:00", "5m|daily", "24h|weekly:xxx@09:00", "24h|daily@25:99", "24h|daily@xx:00"}
	for _, interval := range accepted {
		if !ValidInterval(interval) {
			t.Fatalf("ValidInterval(%q)=false, engine accepts (schedule ok, monthly/yearly fall back, or duration fallback)", interval)
		}
	}
}

// schema 过新：引擎 readConfigSnapshot 拒载，validate 同步报 schema 类。
func TestValidateReportsNewerSchemaVersion(t *testing.T) {
	data := `{"schemaVersion":99,"tasks":[]}`
	problems := mustProblems(t, data)
	if len(problems) != 1 || problems[0].Kind != KindSchema {
		t.Fatalf("problems=%+v want one schema problem", problems)
	}
}

// 语法错带行号：手编 JSON 最常见的逗号/引号错。
func TestValidateReportsSyntaxErrorWithLine(t *testing.T) {
	data := "{\n  \"tasks\": [\n    {\"id\": \"a\",,}\n  ]\n}"
	problems := mustProblems(t, data)
	if len(problems) != 1 || problems[0].Kind != KindUnmarshal {
		t.Fatalf("problems=%+v want one unmarshal problem", problems)
	}
	if problems[0].Line != 3 {
		t.Fatalf("line=%d want 3", problems[0].Line)
	}
}

// 空文件与 BOM：两个手编高频形态都要显式报而不是含糊的 syntax error。
func TestValidateReportsEmptyFileAndBOM(t *testing.T) {
	if problems := mustProblems(t, ""); len(problems) != 1 || !strings.Contains(problems[0].Message, "empty") {
		t.Fatalf("empty file problems=%+v", problems)
	}
	bom := "\uFEFF"
	if problems := mustProblems(t, bom+"{\"tasks\":[]}"); len(problems) != 1 || !strings.Contains(problems[0].Message, "BOM") {
		t.Fatalf("BOM problems=%+v", problems)
	}
}

// envelope 级类型错（revision 写成字符串）：报 envelope 字段并拦截后续扫描。
func TestValidateReportsEnvelopeTypeError(t *testing.T) {
	data := `{"schemaVersion":4,"revision":"one","tasks":[]}`
	problems := mustProblems(t, data)
	if len(problems) != 1 || problems[0].Kind != KindUnmarshal {
		t.Fatalf("problems=%+v", problems)
	}
	if problems[0].Field != "revision" {
		t.Fatalf("field=%q want revision", problems[0].Field)
	}
}

// task 不是对象（手编把 task 写成字符串）：报 tasks[i] 本身。
func TestValidateReportsNonObjectTask(t *testing.T) {
	data := `{"schemaVersion":4,"tasks":["just a string"]}`
	problems := mustProblems(t, data)
	if len(problems) != 1 || problems[0].Kind != KindUnmarshal {
		t.Fatalf("problems=%+v", problems)
	}
	if problems[0].Field != "tasks[0]" {
		t.Fatalf("field=%q want tasks[0]", problems[0].Field)
	}
}

// FormatProblems 的人类可读契约：编号 + [类别] + 字段 + 行号 + 说明。
func TestFormatProblemsHumanReadable(t *testing.T) {
	var buf bytes.Buffer
	FormatProblems(&buf, "cfg.json", []Problem{
		{Kind: KindUnmarshal, TaskIndex: 0, Field: "tasks[0].createdAt", Line: 4, Message: "expected a number, got a string"},
		{Kind: KindDuplicateID, TaskIndex: 2, TaskID: "dup", Field: "tasks[2].id", Message: "duplicates tasks[0]"},
	})
	out := buf.String()
	if !strings.Contains(out, "cfg.json: 2 problem(s)") {
		t.Fatalf("summary missing: %q", out)
	}
	if !strings.Contains(out, "1. [unmarshal] tasks[0].createdAt (line 4)") {
		t.Fatalf("numbered line missing: %q", out)
	}
	if !strings.Contains(out, "2. [duplicate-id] tasks[2].id") {
		t.Fatalf("second line missing: %q", out)
	}
}

// 与 desktop parseInterval 的黄金对齐用例：desktop 侧
// TestParseInterval* 钉同一组输入。改动任何一侧先对照这里。
func TestParseIntervalGoldenParityWithDesktop(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"30s", 30 * time.Second, false},
		{"30", 30 * time.Minute, false}, // bare number = minutes
		{"1h30m", 90 * time.Minute, false},
		{"24h|daily@09:00", 24 * time.Hour, false}, // '|' prefix is the duration part
		{"", 0, false},
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := ParseInterval(c.in)
		if c.err && err == nil {
			t.Fatalf("ParseInterval(%q) err=nil, want error", c.in)
		}
		if !c.err && (err != nil || got != c.want) {
			t.Fatalf("ParseInterval(%q)=%v,%v want %v", c.in, got, err, c.want)
		}
	}
}
