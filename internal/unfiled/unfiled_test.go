package unfiled

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileFlagsMissingPrompt(t *testing.T) {
	inputs := []Input{
		{Turn: 1, Time: time.Now(), Prompt: "帮我修一下登录超时", MsgIndex: 5},
		{Turn: 2, Time: time.Now(), Prompt: "这条丢失了", MsgIndex: 9},
	}
	// Filed message: transcript user messages carry host wrappers around the
	// user's words, so the checkpoint prompt appears as a substring.
	filed := []string{
		"<context-state>window 50%</context-state>\n\n<current-time>now</current-time>\n\n帮我修一下登录超时\n\n<tools>...</tools>",
		"另一条已入档",
	}
	got := Reconcile(inputs, filed)
	if len(got) != 1 {
		t.Fatalf("expected 1 unfiled entry, got %d: %+v", len(got), got)
	}
	if got[0].Prompt != "这条丢失了" || got[0].Turn != 2 {
		t.Fatalf("wrong entry: %+v", got[0])
	}
	if got[0].AnchorIndex != 9 {
		t.Fatalf("anchor index = %d, want 9 (checkpoint msgIndex)", got[0].AnchorIndex)
	}
}

func TestReconcileWhitespaceFold(t *testing.T) {
	inputs := []Input{{Turn: 3, Prompt: "第一行\n  第二行\t第三行", MsgIndex: 2}}
	filed := []string{"<wrap>\n第一行\r\n第二行 第三行\n</wrap>"}
	if got := Reconcile(inputs, filed); len(got) != 0 {
		t.Fatalf("whitespace-folded match should file the input, got %+v", got)
	}
}

func TestReconcileSkipsSystemPrompts(t *testing.T) {
	inputs := []Input{
		{Turn: 1, Prompt: SystemRecoveryPrompt, MsgIndex: 1},
		{Turn: 2, Prompt: "The app updated and restarted itself while this autopilot session was working. Continue from where it stopped; do not restate the plan or wait for input.", MsgIndex: 4},
		{Turn: 3, Prompt: "The process restarted while this autopilot goal was running. Continue it from where it stopped; do not restate the plan or wait for input.", MsgIndex: 6},
		{Turn: 4, Prompt: "[跨会话消息] 来自 contact_id=sc_x → 发至 contact_id=sc_y\n正文", MsgIndex: 8},
		{Turn: 5, Prompt: "[合并消息 ×2]  ── 合并自 inbox 条目 abc", MsgIndex: 10},
		{Turn: 6, Prompt: "[跨会话回信提醒] 有 1 封要求回信", MsgIndex: 12},
		{Turn: 7, Prompt: "<context-state>compaction</context-state>", MsgIndex: 14},
		{Turn: 8, Prompt: "", MsgIndex: 16},
	}
	if got := Reconcile(inputs, nil); len(got) != 0 {
		t.Fatalf("system/host prompts must never be unfiled gaps, got %+v", got)
	}
}

func TestIsSystemPrompt(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{SystemRecoveryPrompt, true},
		{"[跨会话消息] x", true},
		{"[合并消息 ×3] x", true},
		{"用户的真实输入", false},
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		if got := IsSystemPrompt(tc.prompt); got != tc.want {
			t.Errorf("IsSystemPrompt(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

// TestReconcileDuplicateIdenticalPrompts pins the 去重铁律 from the other
// side: once the text is visible anywhere in the transcript, no checkpoint
// carrying the same text is a gap — identical copies must not resurface.
func TestReconcileDuplicateIdenticalPrompts(t *testing.T) {
	inputs := []Input{
		{Turn: 1, Prompt: "同样的文本", MsgIndex: 3},
		{Turn: 2, Prompt: "同样的文本", MsgIndex: 7},
	}
	if got := Reconcile(inputs, []string{"<wrap>同样的文本</wrap>"}); len(got) != 0 {
		t.Fatalf("text already visible in transcript must file every identical checkpoint, got %+v", got)
	}
	if got := Reconcile(inputs, nil); len(got) != 2 {
		t.Fatalf("with no transcript copy both turns are gaps, got %+v", got)
	}
}

func TestReconcileSortedByAnchor(t *testing.T) {
	inputs := []Input{
		{Turn: 9, Prompt: "后丢的", MsgIndex: 30},
		{Turn: 4, Prompt: "先丢的", MsgIndex: 10},
	}
	got := Reconcile(inputs, nil)
	if len(got) != 2 || got[0].Turn != 4 || got[1].Turn != 9 {
		t.Fatalf("entries must be oldest-anchor first: %+v", got)
	}
}

func TestReadCheckpointInputs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// v2-shaped record (matches the 2026-10-20 incident sample).
	write("turn-224.json", `{"schemaVersion":2,"turn":224,"time":"2026-10-20T10:10:35.8356989+08:00","prompt":"新建一个任务","msgIndex":3822,"files":[],"coverage":"none","expiredFilePayload":true}`)
	write("turn-225.json", `{"turn":225,"time":"2026-10-20T10:11:34Z","prompt":`+quote(SystemRecoveryPrompt)+`,"msgIndex":3825}`)
	// Corrupt JSON must be skipped, not fatal.
	write("turn-226.json", `{"turn":226,`)
	// Unrelated files must be ignored.
	write("blobs", "not a checkpoint")
	if err := os.MkdirAll(filepath.Join(dir, "turns"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ReadCheckpointInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed checkpoints, got %d: %+v", len(got), got)
	}
	if got[0].Turn != 224 || got[0].MsgIndex != 3822 || got[0].Prompt != "新建一个任务" {
		t.Fatalf("v2 record parsed wrong: %+v", got[0])
	}
	if want, _ := time.Parse(time.RFC3339, "2026-10-20T10:11:34Z"); !got[1].Time.Equal(want) {
		t.Fatalf("time parsed wrong: %v", got[1].Time)
	}
}

func TestReadCheckpointInputsMissingDir(t *testing.T) {
	got, err := ReadCheckpointInputs(filepath.Join(t.TempDir(), "absent.ckpt"))
	if err != nil || got != nil {
		t.Fatalf("missing dir = (%v, %v), want (nil, nil)", got, err)
	}
	got, err = ReadCheckpointInputs("")
	if err != nil || got != nil {
		t.Fatalf("empty dir = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestReadCheckpointInputsReadOnly pins the phase-0 contract: scanning a
// checkpoint directory must not change a single byte of it (checkpoint.New
// runs GC on construction, which is exactly why this reader exists).
func TestReadCheckpointInputsReadOnly(t *testing.T) {
	dir := t.TempDir()
	body := `{"turn":1,"time":"2026-10-20T10:10:35Z","prompt":"原文","msgIndex":4}`
	if err := os.WriteFile(filepath.Join(dir, "turn-1.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, dir)
	if _, err := ReadCheckpointInputs(dir); err != nil {
		t.Fatal(err)
	}
	after := snapshotDir(t, dir)
	if len(before) != len(after) {
		t.Fatalf("directory changed during read: %d -> %d entries", len(before), len(after))
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("file %s changed during read", name)
		}
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}
