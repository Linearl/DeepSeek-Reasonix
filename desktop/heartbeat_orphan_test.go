package main

import (
	"os"
	"testing"
)

// TestHeartbeatTopicShellByCreatedAt pins the #9614 empty-shell signal: a topic
// whose topic-state record has CreatedAtMS == 0 (heartbeat created it but never
// landed real history) is a shell to archive; any topic with a real non-zero
// creation time holds history and must never be treated as a shell.
func TestHeartbeatTopicShellByCreatedAt(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()

	if err := createTopicState(root, "topic-shell", defaultTopicTitle, topicTitleSourceAuto, 0); err != nil {
		t.Fatalf("create empty-shell topic: %v", err)
	}
	if err := createTopicState(root, "topic-real", defaultTopicTitle, topicTitleSourceAuto, 999); err != nil {
		t.Fatalf("create real topic: %v", err)
	}
	// Give topic-real an actual session file so the hardened "has any session
	// file" fallback sees it as a real topic (not a shell). Without it, the
	// fallback would treat a non-indexed topic as a shell regardless of sqlite.
	if err := os.MkdirAll(desktopSessionDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTopicSession(t, desktopSessionDir(root), "real.jsonl", "topic-real", "Real topic", root)

	app := NewApp()
	engine := newHeartbeatEngine(app)

	if !engine.heartbeatTopicIsShell("topic-shell", root) {
		t.Fatal("empty-shell topic (CreatedAtMS==0) should be detected as a shell")
	}
	if engine.heartbeatTopicIsShell("topic-real", root) {
		t.Fatal("real topic (CreatedAtMS>0, has session file) must not be treated as a shell")
	}
	if engine.heartbeatTopicIsShell("topic-unknown", root) {
		t.Fatal("unknown topic must not be treated as a shell")
	}
	if engine.heartbeatTopicIsShell("", root) {
		t.Fatal("empty topic id must not be treated as a shell")
	}
}

// TestTrashOrphanHeartbeatTopicsSkipsKeptTask ensures the #9614 cascade only
// fires for tasks actually removed from the panel, never for a task the user
// kept. Kept tasks' topics must not be archived.
func TestTrashOrphanHeartbeatTopicsSkipsKeptTask(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()

	if err := createTopicState(root, "topic-kept", defaultTopicTitle, topicTitleSourceAuto, 999); err != nil {
		t.Fatalf("create kept topic: %v", err)
	}

	app := NewApp()
	engine := newHeartbeatEngine(app)

	kept := []HeartbeatTask{{ID: "task-kept", TopicID: "topic-kept"}}
	engine.tasks = kept

	// Same task in the new list → no removal, shell check must be skipped so a
	// real topic is never archived by a no-op.
	engine.trashOrphanHeartbeatTopicsLocked(kept, kept)

	// The real topic was kept in the new list → both lists contain it, so
	// nothing is removed and the real topic is untouched.
	if engine.heartbeatTopicIsShell("topic-kept", root) {
		t.Fatal("kept task topic should retain its real history")
	}
}
