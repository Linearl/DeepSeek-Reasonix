package main

// Task 1095 (跨项目影子条目): topic ids are process-global conversation
// identities. The repair pass (repairIndexedSessionTopicsWithGate) that runs
// per directory can index ONE topic id into TWO workspaces' sidebar lists —
// the user-visible symptom is the same conversation rendered as two rows, one
// in the project tree and one in Global, with no second physical session file
// anywhere (协调线 2026-10-23 实测: 物理文件唯一).
//
// Root cause pinned by this test: the repair's skip was
// `indexed in THIS list AND has a local title`. A topic already filed under
// ANOTHER workspace (its title lives in that workspace's title map, so this
// one reads titleless) fell through every gate — foreign filing scope check
// reads the session sidecar, not the registry — and got prepended into the
// second workspace's index anyway.
//
// This test pins the ownership rule: repair never adds a topic to a
// workspace's index when another workspace already holds it; the topic's page
// stays on the scope its session metadata claims (catalog filing is
// authoritative, 983), and existing local indexing is unaffected. The check
// runs on every pass, so when the foreign filing later disappears the first
// repair pass owns the topic again.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

func TestRepairDoesNotShadowTopicIndexedInAnotherWorkspace(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "Cooking"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	globalDir := config.SessionDir()
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	projectDir := config.ProjectSessionDir(projectRoot)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project sessions: %v", err)
	}

	const topicID = "topic_20261022-074604_db35032a8b35b723"

	// The conversation's own filing: its sidecar (scope=project, this root)
	// claims the project — the catalog (983) keeps the row there. Only the
	// PROJECT's registry index entry is missing; Global's index already holds
	// the topic from an older filing, with the title in the GLOBAL title map.
	projectSession := writeLegacySession(t, projectDir, "20261022-074607.708825100-collab.jsonl", "cooking replay", time.Now().Add(-30*time.Minute))
	if err := agent.SaveBranchMetaPreserveUpdated(projectSession, agent.BranchMeta{
		ID:            agent.BranchID(projectSession),
		CreatedAt:     time.Now().Add(-time.Hour),
		UpdatedAt:     time.Now().Add(-30 * time.Minute),
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       topicID,
		TopicTitle:    "烹饪复盘分享-续",
		Turns:         1,
		Preview:       "cooking replay",
	}); err != nil {
		t.Fatalf("save project meta: %v", err)
	}
	if err := setTopicTitle("", topicID, "烹饪复盘分享-续"); err != nil {
		t.Fatalf("seed global title: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		next := uniqueStrings(append([]string{topicID}, f.GlobalTopics...))
		if sameStringList(next, f.GlobalTopics) {
			return false, nil
		}
		f.GlobalTopics = next
		return true, nil
	}); err != nil {
		t.Fatalf("seed global filing: %v", err)
	}
	markTopicIndexRepairDone(projectDir)

	// The project-scoped repair pass must NOT add the topic to the project's
	// own index — Global's index already owns it. Drop the completed-pass
	// marker so the repair actually re-runs (its signature deliberately
	// ignores mtimes; only a structural change invalidates it).
	if err := os.Remove(filepath.Join(projectDir, topicIndexRepairMarker)); err != nil {
		t.Fatalf("invalidate repair marker: %v", err)
	}
	if topicIndexRepairDone(projectDir) {
		t.Fatalf("repair marker still valid after invalidation")
	}
	if repaired := migrateLegacySessionsIntoGlobalTopics(projectDir); len(repaired) != 0 {
		t.Fatalf("project repair pass reported repairs: %#v, want none (topic owned by Global)", repaired)
	}
	f := loadProjectsFile()
	if i := projectIndexByRoot(f.Projects, projectRoot); i < 0 {
		t.Fatalf("project %q not registered", projectRoot)
	} else if containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project topics = %#v, must not contain topic %q already indexed under Global", f.Projects[i].Topics, topicID)
	}
	if !containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics = %#v, want the pre-existing filing %q preserved", f.GlobalTopics, topicID)
	}

	// Inverse direction: a project-rooted physical session whose topic is
	// already indexed under the PROJECT must not be repaired into Global's
	// index either (the global repair pass scans only Global's own session
	// dir; foreign-sidecar sessions are skipped by the pre-existing scope
	// gate, and this test keeps that contract honest alongside the new
	// cross-workspace ownership check).
	secondProjectRoot := t.TempDir()
	if err := addProject(secondProjectRoot, "Second"); err != nil {
		t.Fatalf("add second project: %v", err)
	}
	const projectOwnedID = "topic_project_owned_000000000001"
	secondDir := config.ProjectSessionDir(secondProjectRoot)
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("mkdir second project sessions: %v", err)
	}
	projectSession2 := writeLegacySession(t, secondDir, "project-shadow.jsonl", "project replay", time.Now().Add(-time.Hour))
	if err := agent.SaveBranchMetaPreserveUpdated(projectSession2, agent.BranchMeta{
		ID:            agent.BranchID(projectSession2),
		CreatedAt:     time.Now().Add(-2 * time.Hour),
		UpdatedAt:     time.Now().Add(-time.Hour),
		Scope:         "project",
		WorkspaceRoot: secondProjectRoot,
		TopicID:       projectOwnedID,
		TopicTitle:    "项目已有话题",
		Turns:         1,
		Preview:       "project replay",
	}); err != nil {
		t.Fatalf("save second project meta: %v", err)
	}
	if err := setTopicTitle(secondProjectRoot, projectOwnedID, "项目已有话题"); err != nil {
		t.Fatalf("seed second project title: %v", err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, secondProjectRoot)
		if i < 0 {
			return false, nil
		}
		next := uniqueStrings(append([]string{projectOwnedID}, f.Projects[i].Topics...))
		if sameStringList(next, f.Projects[i].Topics) {
			return false, nil
		}
		f.Projects[i].Topics = next
		return true, nil
	}); err != nil {
		t.Fatalf("seed second project filing: %v", err)
	}
	// The second project's repair pass must not steal the topic into Global.
	if err := os.Remove(filepath.Join(secondDir, topicIndexRepairMarker)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("invalidate second project marker: %v", err)
	}
	if err := os.Chtimes(projectSession2, time.Now(), time.Now()); err != nil {
		t.Fatalf("touch second project session: %v", err)
	}
	if repaired := migrateLegacySessionsIntoGlobalTopics(secondDir); len(repaired) != 0 {
		t.Fatalf("second project repair pass reported repairs: %#v, want none", repaired)
	}
	f = loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, projectOwnedID) {
		t.Fatalf("globalTopics = %#v, must not contain topic already indexed under project", f.GlobalTopics)
	}
	i2 := projectIndexByRoot(f.Projects, secondProjectRoot)
	if i2 < 0 {
		t.Fatalf("second project %q not registered", secondProjectRoot)
	} else if !containsDesktopString(f.Projects[i2].Topics, projectOwnedID) {
		t.Fatalf("second project topics = %#v, want %q preserved", f.Projects[i2].Topics, projectOwnedID)
	}
}
