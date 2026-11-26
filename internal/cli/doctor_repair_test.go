package cli

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/repair"
)

func TestDoctorRepairSkipsConfigStartupMutation(t *testing.T) {
	if !isDoctorRepairCommand([]string{"doctor", "repair", "--json"}) {
		t.Fatal("doctor repair was not recognized")
	}
	for _, args := range [][]string{{"doctor"}, {"doctor", "capabilities"}, {"run"}} {
		if isDoctorRepairCommand(args) {
			t.Fatalf("unexpected repair match for %v", args)
		}
	}
}

func TestDoctorRepairDryRunDoesNotMigrateConfig(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(root, "reasonix.toml")
	body := []byte("[[plugins]]\nname = \"legacy\"\ncommand = \"legacy\"\ntier = \"lazy\"\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor", "repair", "--root", root, "--json"}, "test"); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("dry run changed config:\n%s", got)
	}
}

// TestDoctorRepairUndoRestoresQuarantinedConfig pins the CLI undo entry
// end to end: repair --apply quarantines the invalid global config, then
// `doctor repair --undo` restores the exact quarantined bytes, consumes the
// backup, records Undone/UndoneAt durably, and creates no redo candidates
// because the undo displaced nothing.
func TestDoctorRepairUndoRestoresQuarantinedConfig(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	bad := []byte("[broken\n")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor", "repair", "--root", root, "--apply", "--json"}, "test"); code != 0 {
		t.Fatalf("repair exit code = %d", code)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("repair did not quarantine the invalid config: %v", err)
	}

	if code := Run([]string{"doctor", "repair", "--undo", "--json"}, "test"); code != 0 {
		t.Fatalf("undo exit code = %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bad) {
		t.Fatalf("undone config = %q, want quarantined bytes %q", got, bad)
	}
	last, err := repair.ReadLastRepair()
	if err != nil {
		t.Fatal(err)
	}
	if !last.Undone || last.UndoneAt == "" {
		t.Fatalf("undo receipt = %+v, want Undone with UndoneAt", last)
	}
	if len(last.Changes) != 1 || !last.Changes[0].Undone {
		t.Fatalf("undo receipt changes = %+v, want one undone change", last.Changes)
	}
	if _, err := os.Lstat(last.Changes[0].PreviousPath); !os.IsNotExist(err) {
		t.Fatalf("undo did not consume its backup %s: %v", last.Changes[0].PreviousPath, err)
	}
	redos, err := filepath.Glob(path + ".reasonix-redo-*")
	if err != nil || len(redos) != 0 {
		t.Fatalf("redo candidates = %v, %v; want none for a non-displacing undo", redos, err)
	}
}

// TestDoctorRepairUndoIsOneShot pins that the persisted Undone marker refuses
// a second --undo instead of recursing, and that the refused undo leaves the
// restored state untouched.
func TestDoctorRepairUndoIsOneShot(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "config.toml")
	bad := []byte("[broken\n")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"doctor", "repair", "--root", root, "--apply", "--json"}, "test"); code != 0 {
		t.Fatalf("repair exit code = %d", code)
	}
	if code := Run([]string{"doctor", "repair", "--undo", "--json"}, "test"); code != 0 {
		t.Fatalf("first undo exit code = %d", code)
	}
	if code := Run([]string{"doctor", "repair", "--undo", "--json"}, "test"); code != 1 {
		t.Fatalf("second undo exit code = %d, want 1 (already undone)", code)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bad) {
		t.Fatalf("refused undo changed restored config: %q, %v", got, err)
	}
}

// TestDoctorRepairUndoWithoutRepairFails pins that --undo with no recorded
// repair fails closed with a distinct exit code instead of pretending success.
func TestDoctorRepairUndoWithoutRepairFails(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	if code := Run([]string{"doctor", "repair", "--undo", "--json"}, "test"); code != 1 {
		t.Fatalf("undo without repair exit code = %d, want 1", code)
	}
}

// TestDoctorRepairUndoRejectsRepairFlags pins that --undo is a mode of its
// own and cannot be blended with the --apply repair modes.
func TestDoctorRepairUndoRejectsRepairFlags(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	for _, args := range [][]string{
		{"doctor", "repair", "--undo", "--apply"},
		{"doctor", "repair", "--undo", "--project"},
		{"doctor", "repair", "--undo", "--apply", "--project"},
	} {
		if code := Run(args, "test"); code != 2 {
			t.Fatalf("%v exit code = %d, want 2", args, code)
		}
	}
}
