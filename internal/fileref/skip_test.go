package fileref

import (
	"path/filepath"
	"testing"
)

func TestSkipEntryHidesBuildOutputDirectories(t *testing.T) {
	for _, name := range []string{"target", "build", "dist", "__pycache__", ".venv", "node_modules"} {
		if !SkipEntry(name, name, true) {
			t.Errorf("%q is a build output and must stay out of file pickers", name)
		}
	}
}

func TestSkipEntryKeepsSourceDirectories(t *testing.T) {
	for _, name := range []string{"src", "internal", "docs", "targets", "buildkite"} {
		if SkipEntry(name, name, true) {
			t.Errorf("%q is not a build output and must remain browsable", name)
		}
	}
}

func TestSkipEntryIgnoresBuildNamesOnFiles(t *testing.T) {
	if SkipEntry("cmd/build", "build", false) {
		t.Error("a file named build is source, not a build directory")
	}
}

func TestSearchSkipsGeneratedClassFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "main", "java", "App.java"))
	writeFile(t, filepath.Join(root, "target", "classes", "App.class"))

	got := resultPaths(Search(root, "app", 50))
	for _, path := range got {
		if path == "target/classes/App.class" {
			t.Fatalf("generated output leaked into @ results: %v", got)
		}
	}
	if len(got) == 0 {
		t.Fatalf("the source file should still match: %v", got)
	}
}

// #10006: the file panel must reflect the real disk layout. tmp/bin/stage are
// generic top-level names a user workspace may legitimately keep real work in,
// so the panel predicate keeps them while the @-search walker may skip them.
func TestSkipEntryForPanelKeepsGenericTopLevelDirectories(t *testing.T) {
	for _, name := range []string{"tmp", "bin", "stage"} {
		if !SkipEntry(name, name, true) {
			t.Errorf("%q should stay out of @-search results", name)
		}
		if SkipEntryForPanel(name, name, true) {
			t.Errorf("%q must remain visible in the file panel (#10006)", name)
		}
	}
}

// Generated output inside the Reasonix repository stays hidden everywhere.
func TestSkipEntryForPanelStillHidesRepoGeneratedOutput(t *testing.T) {
	for _, rel := range []string{"desktop/frontend/wailsjs", "npm/.stage", "site/.astro"} {
		if !SkipEntryForPanel(rel, filepath.Base(rel), true) {
			t.Errorf("%q is generated output and must stay hidden in the panel", rel)
		}
	}
	if !SkipEntryForPanel("node_modules", "node_modules", true) {
		t.Error("node_modules must stay hidden in the panel")
	}
}
