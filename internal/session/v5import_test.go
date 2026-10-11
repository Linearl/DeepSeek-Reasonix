package session

// Task 773 tests. The fixture builder constructs a synthetic upstream Desktop
// v5 store (by-id + header + workspace-state-v1.json + v4 framed logs across
// manifest revisions 1/2/3) entirely inside t.TempDir() — nothing touches a
// real %APPDATA% tree and nothing is committed into the repository. The
// frames are written by the fork's own production encoder, so the fixture is
// byte-format identical to what upstream writes.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontent"
)

type v5FixtureSession struct {
	id              string
	storageRevision int
	header          bool
	messages        []provider.Message
	title           string
	modelRef        string
	goalEvents      int
	// retraction removes the message with this id before the fold ends.
	retractID    string
	upsertID     string
	historyReset bool
	// unknownRequiredKind injects an event kind neither fork nor upstream
	// knows, marked required, to prove the importer refuses to misread.
	unknownRequiredKind bool
}

type v5Fixture struct {
	byIDRoot  string
	treeRoot  string
	projected string // registry workspace root for sess-project
	sessions  []v5FixtureSession
	registry  string // raw JSON; empty derives the default
}

func buildV5Fixture(t *testing.T, fixture v5Fixture) {
	t.Helper()
	byID := fixture.byIDRoot
	content := sessioncontent.New(filepath.Join(byID, ".content-v1"))
	for _, spec := range fixture.sessions {
		dir := filepath.Join(byID, spec.id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := V5Manifest{
			SchemaVersion:    V4SchemaVersion,
			Codec:            V4Codec,
			StorageRevision:  spec.storageRevision,
			ContentRoot:      sharedContentRoot,
			SessionID:        spec.id,
			CreatedAt:        time.Unix(1700000000, 0).UTC(),
			WriterGeneration: 1,
		}
		if err := writeManifest(filepath.Join(dir, "manifest.json"), Manifest{
			SchemaVersion: manifest.SchemaVersion, Codec: manifest.Codec,
			StorageRevision: manifest.StorageRevision, ContentRoot: manifest.ContentRoot,
			SessionID: manifest.SessionID, CreatedAt: manifest.CreatedAt, WriterGeneration: manifest.WriterGeneration,
		}); err != nil {
			t.Fatal(err)
		}
		if spec.header {
			header := V5Header{SchemaVersion: 1, SessionID: spec.id, CreatedAt: manifest.CreatedAt, CWD: "/fixture/workspace", Origin: "new"}
			raw, err := json.Marshal(header)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, V5ImportHeaderName), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if spec.storageRevision >= 3 {
			for _, aux := range []string{"events.offset-index.json", "storage.identity.json"} {
				if err := os.WriteFile(filepath.Join(dir, aux), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		commits := v5FixtureCommits(t, spec)
		var log bytes.Buffer
		if _, err := encodeV4Commits(context.Background(), &log, content, commits); err != nil {
			t.Fatalf("encode %s: %v", spec.id, err)
		}
		if err := os.WriteFile(filepath.Join(dir, currentLogName), log.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.registry != "" {
		dir := filepath.Join(fixture.treeRoot, "desktop")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workspace-state-v1.json"), []byte(fixture.registry), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// v5FixtureCommits folds a session spec into framed commits: one commit per
// logical group, sequences allocated continuously, writer generation 1.
func v5FixtureCommits(t *testing.T, spec v5FixtureSession) []Commit {
	t.Helper()
	wrap := func(kind string, payload any) Event {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return Event{Kind: kind, Payload: raw}
	}
	var events []Event
	sequence := uint64(0)
	nextID := 0
	add := func(event Event) {
		nextID++
		sequence++
		event.ID = event.Kind + "-" + spec.id + "-" + strconv.Itoa(nextID)
		event.Sequence = sequence
		events = append(events, event)
	}
	for _, message := range spec.messages {
		add(wrap("message/complete", struct {
			Message provider.Message `json:"message"`
		}{Message: message}))
	}
	if spec.title != "" {
		add(wrap("session/title", map[string]string{"title": spec.title}))
	}
	if spec.modelRef != "" {
		add(wrap("session/config", map[string]string{"modelRef": spec.modelRef, "modelIdentity": "identity-" + spec.modelRef}))
	}
	for i := 0; i < spec.goalEvents; i++ {
		add(wrap("goal/state", map[string]any{"text": "fixture goal", "generation": i}))
	}
	if spec.unknownRequiredKind {
		add(Event{Kind: "future/upstream-kind", Optional: false, Payload: json.RawMessage(`{}`)})
	}
	if len(events) == 0 {
		return nil
	}
	commit := Commit{
		SchemaVersion:    V4SchemaVersion,
		Codec:            V4Codec,
		RecordType:       "commit",
		ID:               "commit-" + spec.id,
		OperationID:      "operation-" + spec.id,
		OperationHash:    "hash-" + spec.id,
		FirstSequence:    1,
		EventCount:       len(events),
		TurnID:           "turn-" + spec.id,
		WriterGeneration: 1,
		CreatedAt:        time.Unix(1700000001, 0).UTC(),
		Events:           events,
	}
	return []Commit{commit}
}

func defaultV5Fixture(t *testing.T) v5Fixture {
	t.Helper()
	root := t.TempDir()
	large := provider.Message{Role: provider.RoleUser, Content: strings.Repeat("大体积消息 ", 30000)}
	return v5Fixture{
		byIDRoot:  filepath.Join(root, "desktop-sessions-v5", "by-id"),
		treeRoot:  root,
		projected: `D:\fixture\project`,
		sessions: []v5FixtureSession{
			{id: "sess-rev1", storageRevision: 1, header: true, title: "rev one", modelRef: "fixture-model",
				messages: []provider.Message{
					{Role: provider.RoleUser, Content: "hello from v5"},
					{Role: provider.RoleAssistant, Content: "hi from the fixture"},
				}},
			{id: "sess-rev2", storageRevision: 2, header: true,
				messages: []provider.Message{large}},
			{id: "sess-rev3", storageRevision: 3, goalEvents: 2,
				messages: []provider.Message{
					{Role: provider.RoleUser, Content: "third generation"},
				}},
			{id: "sess-noheader", storageRevision: 1,
				messages: []provider.Message{{Role: provider.RoleUser, Content: "no header file"}}},
		},
		registry: `{"version":3,"generation":7,"initialized":true,"workspaceIds":["global","p1"],
		  "workspaces":{
		    "global":{"id":"global","root":"","title":"Global","sessionIds":["sess-rev1","sess-rev2"],"visible":true},
		    "p1":{"id":"p1","root":"D:\\fixture\\project","title":"Project One","sessionIds":["sess-rev3"],"visible":true}},
		  "archivedSessionIds":["sess-noheader"],
		  "pendingCreates":{},"sessionStates":{},"sourceMappings":{},"pendingOperations":{},"recoveryEntries":{},"presentation":{}}`,
	}
}

func v5ImportTestOptions(fixture v5Fixture) V5ImportRunOptions {
	projectDir := filepath.Join(fixture.treeRoot, "import-target", "project")
	globalDir := filepath.Join(fixture.treeRoot, "import-target", "global")
	return V5ImportRunOptions{
		V5ImportOptions: V5ImportOptions{
			SourceRoot: fixture.byIDRoot,
			ResolveTarget: func(workspaceRoot string) (string, error) {
				if workspaceRoot == fixture.projected {
					return projectDir, nil
				}
				return globalDir, nil
			},
		},
		BackupRoot: filepath.Join(fixture.treeRoot, "backups"),
	}
}

func TestV5ImportPlanClassifiesFixture(t *testing.T) {
	fixture := defaultV5Fixture(t)
	buildV5Fixture(t, fixture)
	opts := v5ImportTestOptions(fixture)
	plan, err := PlanV5Import(context.Background(), opts.V5ImportOptions)
	if err != nil {
		t.Fatalf("PlanV5Import: %v", err)
	}
	if plan.ImportCount != 4 || plan.SkipCount != 0 || plan.FailCount != 0 {
		for _, entry := range plan.Entries {
			t.Logf("entry %s status=%s reason=%s", entry.SessionID, entry.Status, entry.Reason)
		}
		for _, failure := range plan.Failures {
			t.Logf("failure %s: %s", failure.SessionID, failure.Reason)
		}
		t.Fatalf("plan counts import=%d skip=%d fail=%d, want 4/0/0", plan.ImportCount, plan.SkipCount, plan.FailCount)
	}
	byID := map[string]V5PlanEntry{}
	for _, entry := range plan.Entries {
		byID[entry.SessionID] = entry
	}
	if got := byID["sess-rev3"]; got.WorkspaceRoot != fixture.projected {
		t.Fatalf("sess-rev3 routed to %q, want the project root", got.WorkspaceRoot)
	}
	if got := byID["sess-rev2"]; got.StorageRevision != 2 || got.MessageCount != 1 {
		t.Fatalf("sess-rev2 = rev%d/%d msgs, want rev2/1", got.StorageRevision, got.MessageCount)
	}
	if got := byID["sess-noheader"]; !got.HeaderMissing {
		t.Fatal("sess-noheader should report its missing header")
	}
	if got := byID["sess-rev1"]; got.Title != "rev one" || got.ModelRef != "fixture-model" {
		t.Fatalf("sess-rev1 title/model = %q/%q", got.Title, got.ModelRef)
	}
}

func TestV5ImportRunPublishesAndVerifies(t *testing.T) {
	fixture := defaultV5Fixture(t)
	buildV5Fixture(t, fixture)
	opts := v5ImportTestOptions(fixture)
	report, err := RunV5Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunV5Import: %v", err)
	}
	if !report.Verified {
		t.Fatalf("report not verified: %+v", report)
	}
	if !report.SourceUnmoved || report.SourceBefore == "" || report.SourceBefore != report.SourceAfter {
		t.Fatalf("source fingerprint moved: %q -> %q", report.SourceBefore, report.SourceAfter)
	}
	if report.Backup.Dir == "" || report.Backup.Files == 0 {
		t.Fatalf("backup missing: %+v", report.Backup)
	}
	if _, err := os.Stat(report.Backup.Dir); err != nil {
		t.Fatalf("backup dir: %v", err)
	}
	if report.ImportCount != 4 || report.SkipCount != 0 {
		t.Fatalf("report counts import=%d skip=%d, want 4/0", report.ImportCount, report.SkipCount)
	}
	if len(report.PublishedFiles) != 8 { // transcript + meta per session
		t.Fatalf("published %d files, want 8", len(report.PublishedFiles))
	}
	// 全链验收：枚举（目标文件在路由目录）、渲染（LoadSession 解出消息）、
	// 续写前置（fork 自己的加载器认这个 transcript）。
	globalTranscript := filepath.Join(fixture.treeRoot, "import-target", "global", "sess-rev1.jsonl")
	loaded, err := agent.LoadSession(globalTranscript)
	if err != nil {
		t.Fatalf("fork LoadSession on imported transcript: %v", err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "hello from v5" {
		t.Fatalf("loaded %d messages, first=%q", len(loaded.Messages), loaded.Messages[0].Content)
	}
	meta, ok, err := agent.LoadBranchMeta(globalTranscript)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	if meta.Name != "rev one" || meta.Model != "fixture-model" {
		t.Fatalf("meta name/model = %q/%q", meta.Name, meta.Model)
	}
	projectTranscript := filepath.Join(fixture.treeRoot, "import-target", "project", "sess-rev3.jsonl")
	if _, err := os.Stat(projectTranscript); err != nil {
		t.Fatalf("project-routed transcript missing: %v", err)
	}
	// 大体积 rev2 消息走内容池外置，导入后必须完整还原。
	loadedLarge, err := agent.LoadSession(filepath.Join(fixture.treeRoot, "import-target", "global", "sess-rev2.jsonl"))
	if err != nil {
		t.Fatalf("LoadSession sess-rev2 (content-pool payload): %v", err)
	}
	if len(loadedLarge.Messages) != 1 || len(loadedLarge.Messages[0].Content) < 100000 {
		t.Fatalf("large payload restored short: %d messages, %d chars", len(loadedLarge.Messages), len(loadedLarge.Messages[0].Content))
	}

	// 幂等：目标已存在的会话全部转为 skip，不重写任何文件。
	retry, err := RunV5Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("second RunV5Import: %v", err)
	}
	if retry.ImportCount != 0 || retry.SkipCount != 4 {
		t.Fatalf("retry counts import=%d skip=%d, want 0/4", retry.ImportCount, retry.SkipCount)
	}
}

func TestV5ImportRefusesUnknownRequiredKind(t *testing.T) {
	fixture := defaultV5Fixture(t)
	fixture.sessions = append(fixture.sessions, v5FixtureSession{
		id: "sess-future", storageRevision: 3, unknownRequiredKind: true,
		messages: []provider.Message{{Role: provider.RoleUser, Content: "from the future"}},
	})
	buildV5Fixture(t, fixture)
	opts := v5ImportTestOptions(fixture)
	plan, err := PlanV5Import(context.Background(), opts.V5ImportOptions)
	if err != nil {
		t.Fatalf("PlanV5Import: %v", err)
	}
	var entry *V5PlanEntry
	for i := range plan.Entries {
		if plan.Entries[i].SessionID == "sess-future" {
			entry = &plan.Entries[i]
		}
	}
	if entry == nil || entry.Status != "fail" {
		t.Fatalf("sess-future should fail the plan with a reason, got %+v", entry)
	}
	if entry != nil && !strings.Contains(entry.Reason, "future/upstream-kind") {
		t.Fatalf("failure reason should name the unknown kind, got %q", entry.Reason)
	}
	// 其余会话不受牵连。
	if plan.ImportCount != 4 {
		t.Fatalf("healthy sessions still import: %d", plan.ImportCount)
	}
}

func TestV5ImportSourceStoreUntouchedByteForByte(t *testing.T) {
	fixture := defaultV5Fixture(t)
	buildV5Fixture(t, fixture)
	ctx := context.Background()
	before, files, _, err := SourceTreeFingerprint(ctx, fixture.byIDRoot)
	if err != nil {
		t.Fatal(err)
	}
	opts := v5ImportTestOptions(fixture)
	if _, err := RunV5Import(ctx, opts); err != nil {
		t.Fatalf("RunV5Import: %v", err)
	}
	after, filesAfter, _, err := SourceTreeFingerprint(ctx, fixture.byIDRoot)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || files != filesAfter {
		t.Fatalf("v5 store changed: %s(%d files) -> %s(%d files)", before, files, after, filesAfter)
	}
}

func TestV5ImportRegistryStatusesRouteSkips(t *testing.T) {
	fixture := defaultV5Fixture(t)
	fixture.registry = `{"version":3,"generation":3,"initialized":true,"workspaceIds":["global"],
	  "workspaces":{"global":{"id":"global","root":"","title":"G","sessionIds":["sess-rev1"],"visible":true}},
	  "archivedSessionIds":[],
	  "pendingCreates":{"sess-rev2":{"operationId":"op","workspaceId":"global","sessionId":"sess-rev2"}},
	  "sessionStates":{"sess-rev3":{"lifecycle":"deleted","generation":2}},
	  "sourceMappings":{},"pendingOperations":{},"recoveryEntries":{},"presentation":{}}`
	buildV5Fixture(t, fixture)
	opts := v5ImportTestOptions(fixture)
	plan, err := PlanV5Import(context.Background(), opts.V5ImportOptions)
	if err != nil {
		t.Fatalf("PlanV5Import: %v", err)
	}
	byID := map[string]V5PlanEntry{}
	for _, entry := range plan.Entries {
		byID[entry.SessionID] = entry
	}
	if got := byID["sess-rev2"]; got.Status != "skip" || !strings.Contains(got.Reason, "reservation") {
		t.Fatalf("reserved session should skip: %+v", got)
	}
	if got := byID["sess-rev3"]; got.Status != "skip" || got.RegistryStatus != "deleted" {
		t.Fatalf("deleted session should skip: %+v", got)
	}
	if got := byID["sess-rev1"]; got.Status != "import" {
		t.Fatalf("active session should import: %+v", got)
	}
}

func TestV5ImportOpenV5StoreRejectsNonV5Roots(t *testing.T) {
	root := t.TempDir()
	if _, err := OpenV5Store(filepath.Join(root, "sessions")); err == nil {
		t.Fatal("a plain sessions dir must be rejected")
	}
	byID := filepath.Join(root, "desktop-sessions-v5", "by-id")
	if err := os.MkdirAll(byID, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenV5Store(byID)
	if err != nil {
		t.Fatalf("OpenV5Store: %v", err)
	}
	sessions, failures, err := store.Sessions()
	if err != nil || len(sessions) != 0 || len(failures) != 0 {
		t.Fatalf("empty store: sessions=%v failures=%v err=%v", sessions, failures, err)
	}
	// 会话 ID 校验：路径穿越与 Windows 保留名必须拒绝。
	for _, bad := range []string{"..", "con", "aux", "a/b", "a\\b", ".hidden"} {
		if err := ValidateV5ImportSessionID(bad); err == nil {
			t.Fatalf("session id %q must be rejected", bad)
		}
	}
	if err := ValidateV5ImportSessionID("9f61c3d2-session"); err != nil {
		t.Fatalf("normal id rejected: %v", err)
	}
}

func TestV5ImportFoldingUpserRetractReplace(t *testing.T) {
	// 折叠语义：complete 追加、upsert 按 id 替换、retract 删除、
	// history/replace 重启消息流——与上游投影和 fork 的 Derive 语义一致。
	fold := func(t *testing.T, events []Event) *V5Transcript {
		t.Helper()
		byID := map[string]int{}
		out := &V5Transcript{KindCounts: map[string]int{}}
		for _, event := range events {
			out.KindCounts[event.Kind]++
			if err := foldV5ImportEvent(out, byID, event); err != nil {
				t.Fatalf("fold %s: %v", event.Kind, err)
			}
		}
		return out
	}
	msg := func(id, text string) json.RawMessage {
		raw, err := json.Marshal(struct {
			Message provider.Message `json:"message"`
		}{Message: provider.Message{ID: id, Role: provider.RoleUser, Content: text}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	events := []Event{
		{Kind: "message/complete", Payload: msg("m1", "one")},
		{Kind: "message/complete", Payload: msg("m2", "two")},
		{Kind: "message/upsert", Payload: msg("m1", "one-edited")},
		{Kind: "message/retract", Payload: json.RawMessage(`{"messageIds":["m2"]}`)},
		{Kind: "history/replace", Payload: json.RawMessage(`{"messages":[{"id":"r1","role":"user","content":"reset"}]}`)},
	}
	out := fold(t, events)
	if len(out.Messages) != 1 {
		t.Fatalf("folded %d messages, want 1", len(out.Messages))
	}
	var only provider.Message
	if err := json.Unmarshal(out.Messages[0].Message, &only); err != nil {
		t.Fatal(err)
	}
	if only.ID != "r1" || only.Content != "reset" {
		t.Fatalf("history/replace did not restart the stream: %+v", only)
	}
}
