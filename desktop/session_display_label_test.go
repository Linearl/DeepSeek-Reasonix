package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioncatalog"
)

// tl1330: transcript ids ("<stamp>-<provider>-<model>[-recovery-<hash>]") must
// never surface as user-facing labels. These tests pin the friendly chain:
// user title > sidecar topic title > listing preview > stamp carve >
// topic label > localized default, with the localized recovered marker on
// recovery-named files.
func TestSessionDisplayLabelForPathPrefersUserTitle(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "20261031-220415.123-mimo-token-plan-mimo-v2.6-flash.jsonl",
		"topic_label", "会话主题", "", "首条用户消息", time.Now())
	if err := setSessionTitle(dir, path, "用户起的名字"); err != nil {
		t.Fatalf("set session title: %v", err)
	}
	app := NewApp()
	got := app.sessionDisplayLabelForPath(path, "会话主题")
	if got != "用户起的名字" {
		t.Fatalf("label = %q, want the user-chosen title", got)
	}
}

func TestSessionDisplayLabelForPathChain(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	app := NewApp()

	// Sidecar topic title beats the listing preview.
	metaTitle := writeTopicSessionWithPrompt(t, dir, "20261031-220415.123-provider-model.jsonl",
		"topic_chain", "侧栏主题", "", "这是首条用户消息的预览文本", time.Now())
	if got := app.sessionDisplayLabelForPath(metaTitle, "话题标签"); got != "侧栏主题" {
		t.Fatalf("meta topic title chain = %q, want 侧栏主题", got)
	}

	// Preview (first user message) fills a missing topic title, clamped.
	path := filepath.Join(dir, "20261031-220500.000-provider-model.jsonl")
	long := strings.Repeat("很", 60)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
		Scope: "global", TopicID: "topic_preview", Preview: long,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	got := app.sessionDisplayLabelForPath(path, "")
	if runes := len([]rune(got)); runes != 41 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview label = %q (%d runes), want 40 runes + ellipsis", got, runes)
	}

	// No meta at all: the stamp carved from the file name, no raw id anywhere.
	// Carve shape matches the tl1076 contract: "20260927 03:00".
	bare := filepath.Join(dir, "20261031-220600.000-provider-model.jsonl")
	if got := app.sessionDisplayLabelForPath(bare, "话题标签"); got != "20261031 22:06" {
		t.Fatalf("stamp label = %q, want 20261031 22:06", got)
	}

	// Unstampable name: fall to the conversation (topic) label.
	plain := filepath.Join(dir, "session-a.jsonl")
	if got := app.sessionDisplayLabelForPath(plain, "话题标签"); got != "话题标签" {
		t.Fatalf("topic label fallback = %q, want 话题标签", got)
	}

	// Nothing at all: the localized default — never a file id.
	if got := app.sessionDisplayLabelForPath(filepath.Join(dir, "no-meta.jsonl"), ""); got != defaultTopicTitle {
		t.Fatalf("default label = %q, want %q", got, defaultTopicTitle)
	}

	// Empty path short-circuits to the topic label.
	if got := app.sessionDisplayLabelForPath("", "话题标签"); got != "话题标签" {
		t.Fatalf("empty path label = %q, want 话题标签", got)
	}
}

func TestSessionDisplayLabelForPathMarksRecoveryFiles(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	app := NewApp()

	recovery := filepath.Join(dir, "20261031-220415.123-mimo-token-plan-mimo-v2.6-flash-recovery-7503ae1e8c61e196.jsonl")
	got := app.sessionDisplayLabelForPath(recovery, "")
	if !agent.LooksLikeRecoveryFilename(recovery) {
		t.Fatalf("fixture path stopped looking like a recovery file: %q", recovery)
	}
	if !strings.HasSuffix(got, " (recovered)") {
		t.Fatalf("recovery label = %q, want the recovered marker", got)
	}
	// The internal id itself must stay out of the label.
	if strings.Contains(got, "mimo-v2.6-flash") || strings.Contains(got, "7503ae1e8c61e196") || strings.Contains(got, ".jsonl") {
		t.Fatalf("recovery label leaked the transcript id: %q", got)
	}

	normal := filepath.Join(dir, "20261031-220700.000-provider-model.jsonl")
	if got := app.sessionDisplayLabelForPath(normal, ""); strings.Contains(got, "recovered") {
		t.Fatalf("normal label = %q, want no recovered marker", got)
	}
}

// tl1330 集成面：同 topic 多活跃会话的侧栏子行（≥2 个持有控制器的会话才拆
// 行，757 合同）不得再以 transcript id 为标签。recovery 副本行携带「已恢复」
// 标记以便与主行区分。
func TestProjectTreeSessionChildRowsUseFriendlyLabels(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	topicID := "topic_child_labels"
	topicTitle := "多活跃会话"
	main := writeTopicSessionWithPrompt(t, dir, "session-main.jsonl", topicID, topicTitle, "", "main prompt", time.Now().Add(-time.Hour))
	fork := writeTopicSessionWithPrompt(t, dir, "20261031-220415.123-mimo-token-plan-mimo-v2.6-flash-recovery-7503ae1e8c61e196.jsonl", topicID, topicTitle, "", "fork prompt", time.Now())

	app := NewApp()
	runner := func() *blockingRunner {
		return &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	}
	ctrlMain := control.New(control.Options{Runner: runner(), SessionDir: dir, SessionPath: main, Label: "main", Sink: event.Discard})
	ctrlFork := control.New(control.Options{Runner: runner(), SessionDir: dir, SessionPath: fork, Label: "fork", Sink: event.Discard})
	defer ctrlMain.Close()
	defer ctrlFork.Close()

	app.detachedSessions[sessionRuntimeKey(main)] = &WorkspaceTab{
		ID: "detached", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID: topicID, TopicTitle: topicTitle, SessionPath: main, Ctrl: ctrlMain, Ready: true,
		sink: &tabEventSink{tabID: "detached", app: app}, disabledMCP: map[string]ServerView{},
	}
	app.tabs["visible"] = &WorkspaceTab{
		ID: "visible", Scope: "global", WorkspaceRoot: globalTabWorkspaceRoot(),
		TopicID: topicID, TopicTitle: topicTitle, SessionPath: fork, Ctrl: ctrlFork, Ready: true,
		sink: &tabEventSink{tabID: "visible", app: app}, disabledMCP: map[string]ServerView{},
	}
	app.tabOrder = []string{"visible"}
	app.activeTabID = "visible"

	nodes := app.ListProjectTree()
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one global topic", nodes)
	}
	topic := nodes[0].Children[0]
	if len(topic.Children) != 2 {
		t.Fatalf("topic children = %#v, want two session rows", topic.Children)
	}
	labelByPath := map[string]string{}
	for _, child := range topic.Children {
		labelByPath[sessionRuntimeKey(child.SessionPath)] = child.Label
	}
	if labelByPath[sessionRuntimeKey(main)] != topicTitle {
		t.Fatalf("main row label = %q, want %q", labelByPath[sessionRuntimeKey(main)], topicTitle)
	}
	forkLabel := labelByPath[sessionRuntimeKey(fork)]
	if !strings.HasSuffix(forkLabel, " (recovered)") {
		t.Fatalf("fork row label = %q, want the recovered marker", forkLabel)
	}
	if strings.Contains(forkLabel, "recovery-7503") || strings.Contains(forkLabel, "mimo") || strings.Contains(forkLabel, ".jsonl") {
		t.Fatalf("fork row label leaked the transcript id: %q", forkLabel)
	}
}

// tl1330 同机制面：新会话「复用最近会话目录」选项的来源行（TabBar newmenu）
// 与历史面板索引占位卡也不再裸奔 transcript id。
func TestLatestSessionDisplayTitleNeverCarriesTranscriptID(t *testing.T) {
	const stampName = "20261031-220415.123-mimo-token-plan-mimo-v2.6-flash-recovery-7503ae1e8c61e196.jsonl"
	if got := latestSessionDisplayTitle("用户标题", "", "/x/"+stampName); got != "用户标题" {
		t.Fatalf("custom title = %q", got)
	}
	if got := latestSessionDisplayTitle("", "话题标题", "/x/"+stampName); got != "话题标题" {
		t.Fatalf("topic title = %q", got)
	}
	if got := latestSessionDisplayTitle("", "", "/x/"+stampName); got != "20261031 22:04" {
		t.Fatalf("stamp fallback = %q, want 20261031 22:04", got)
	}
	// Unstampable name: empty — the FE omits the line, no id, no guess.
	if got := latestSessionDisplayTitle("", "", "/x/session-a.jsonl"); got != "" {
		t.Fatalf("no-info fallback = %q, want empty", got)
	}
}

func TestSessionMetaFromCatalogIndexedPlaceholderIsFriendly(t *testing.T) {
	record := sessioncatalog.SessionRecord{
		Path:       "/x/20261031-220415.123-mimo-token-plan-mimo-v2.6-flash-recovery-7503ae1e8c61e196.jsonl",
		TurnsState: sessioncatalog.TurnsUnknown,
	}
	meta := sessionMetaFromCatalog(record, false, false)
	if meta.Preview != "History is being indexed — 20261031 22:04" {
		t.Fatalf("indexed placeholder = %q, want the stamp form", meta.Preview)
	}
	record.Path = "/x/session-a.jsonl"
	meta = sessionMetaFromCatalog(record, false, false)
	if meta.Preview != "History is being indexed" {
		t.Fatalf("indexed placeholder = %q, want plain status text", meta.Preview)
	}
}
