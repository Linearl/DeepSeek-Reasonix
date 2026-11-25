package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// ciCI-shaped fixture at the tl-incident scale: a heartbeat_task_list-sized
// log (~78KB) that looksLikeCILog classifies as CI output, so the summarizer
// replaces the raw wholesale.
func ciShapedFixture(lines int) string {
	var b strings.Builder
	b.WriteString("##teamcity[testFailed name='case-0']\n")
	for i := range lines {
		fmt.Fprintf(&b, "task-row-%d ok\n", i)
	}
	b.WriteString("exit status 1\nTAIL-SENTINEL-🧪\n")
	return b.String()
}

// tl1561b 验收①+②+③（有路径分支）：CI 摘要替换大返回时必须归档完整原文且
// 摘要头带可读路径；旧文案「full original retained locally」是无路径空承诺，
// 退役后不得再现。按提示 read_file 跨调用读回归档，尾部哨兵可达。
func TestCISummaryReplacementArchivesReadableOriginal(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	raw := ciShapedFixture(6000) // ~78KB，与 20261111 heartbeat_task_list 事故同量级
	if !looksLikeCILog(raw) {
		t.Fatal("fixture must classify as CI-shaped for this pin")
	}
	body, _, original := a.boundProviderVisibleResult(raw, "heartbeat_task_list", "call-ci78k", "{}")
	if original == "" {
		t.Fatal("CI summary replacement did not retain the original")
	}
	if !strings.Contains(body, "CI/log summary") {
		t.Fatalf("CI-shaped output was not summarized: %.200s", body)
	}
	if strings.Contains(body, "full original retained locally") {
		t.Fatalf("retired no-path promise resurfaced: %.400s", body)
	}
	idx := strings.Index(body, "full original saved to ")
	if idx < 0 {
		t.Fatalf("summary header names no archived original: %.400s", body)
	}
	rest := body[idx+len("full original saved to "):]
	end := strings.IndexByte(rest, ';')
	if end < 0 {
		t.Fatalf("summary header path unparsable: %.400s", body)
	}
	archive := strings.TrimSpace(rest[:end])
	if !strings.Contains(archive, toolOutputArchiveDirName) {
		t.Fatalf("summary header path %q is not inside the archive directory", archive)
	}
	// 验收①：落盘原件逐字节等于完整原文。
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("archived original not readable at %q: %v", archive, err)
	}
	if string(got) != raw {
		t.Fatalf("archived original differs: got %d bytes want %d", len(got), len(raw))
	}
	// 验收②：跨调用恢复——另一个调用（read_file）按提示路径窗口读回，尾部哨兵可达。
	read, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin is not registered")
	}
	reached := false
	for start := 1; start <= 20000; start += 2000 {
		out, err := read.Execute(t.Context(), []byte(fmt.Sprintf(`{"path":%q,"offset":%d,"limit":2000}`, archive, start)))
		if err != nil {
			t.Fatalf("cross-call read_file on archive failed at offset %d: %v", start, err)
		}
		if strings.Contains(out, "TAIL-SENTINEL-🧪") {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatal("cross-call read_file windowed paging never reached the tail sentinel in the archive")
	}
}

// tl1561b 验收③（无路径分支）：归档不可用（无会话临时目录）时，摘要头必须
// 诚实说明原件未保留——不承诺一个读不到的本地副本。
func TestCISummaryHeaderStaysHonestWithoutArchive(t *testing.T) {
	a, _ := newArchiveTestAgent(t, false)
	raw := ciShapedFixture(6000)
	body, _, _ := a.boundProviderVisibleResult(raw, "heartbeat_task_list", "call-noarchive", "{}")
	if !strings.Contains(body, "CI/log summary (original not retained)") {
		t.Fatalf("header must say the original was not retained: %.400s", body)
	}
	if strings.Contains(body, "saved to") || strings.Contains(body, "retained locally") {
		t.Fatalf("header promises an unavailable recovery channel: %.400s", body)
	}
}

// tl1561b 验收④（零回退）：非 CI 文本不摘要、不归档、字节级原样直通——
// 摘要替换与归档都只在 CI 形态命中时发生。
func TestNonCIOutputPassesThroughUntouched(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	raw := strings.Repeat("plain task row 界\n", 5000) // ~85KB, no CI shape
	if looksLikeCILog(raw) {
		t.Fatal("plain fixture must not classify as CI-shaped")
	}
	body, _, original := a.boundProviderVisibleResult(raw, "heartbeat_task_list", "call-plain", "{}")
	if body != raw {
		t.Fatalf("non-CI output was rewritten: got %d bytes want %d", len(body), len(raw))
	}
	if original != "" {
		t.Fatal("non-CI output must not retain a separate original")
	}
	if strings.Contains(body, "CI/log summary") {
		t.Fatalf("non-CI output was summarized: %.200s", body)
	}
}

// 替换即归档的 lockstep 钉：CI 形态命中但原件很小（teamcity 行无大小门槛）时，
// 只要摘要替换发生，归档与路径提示就必须同时存在——不存在「替换了却无路径」
// 的组合。
func TestSmallCISummaryAlsoArchivesWithHeaderPath(t *testing.T) {
	a, _ := newArchiveTestAgent(t, true)
	raw := "##teamcity[testFailed name='tiny']\nline2\nline3\n"
	if !looksLikeCILog(raw) {
		t.Fatal("teamcity fixture must classify as CI-shaped regardless of size")
	}
	body, _, original := a.boundProviderVisibleResult(raw, "bash", "call-ci-small", "{}")
	if !strings.Contains(body, "CI/log summary") {
		t.Fatalf("small CI-shaped output was not summarized: %.200s", body)
	}
	if original == "" {
		t.Fatal("small CI replacement did not retain the original")
	}
	idx := strings.Index(body, "full original saved to ")
	if idx < 0 {
		t.Fatalf("small summary header names no archived original: %.400s", body)
	}
	rest := body[idx+len("full original saved to "):]
	end := strings.IndexByte(rest, ';')
	if end < 0 {
		t.Fatalf("small summary header path unparsable: %.400s", body)
	}
	archive := strings.TrimSpace(rest[:end])
	got, err := os.ReadFile(archive)
	if err != nil || string(got) != raw {
		t.Fatalf("small archive differs: err=%v got=%d want=%d", err, len(got), len(raw))
	}
}
