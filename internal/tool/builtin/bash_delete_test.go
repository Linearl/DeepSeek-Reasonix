package builtin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sandbox"
	"reasonix/internal/secrets"
	"reasonix/internal/tool"
)

// execCommandOutput runs path with args and returns its combined output. Used
// by the safety probe in the incident test.
func execCommandOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// deleteGateTerminal is a host terminal stub: it records that the command
// reached the execution path. The guard must refuse before this is consulted.
type deleteGateTerminal struct{ calls int }

func (r *deleteGateTerminal) RunCommand(context.Context, string, string, time.Duration, map[string]string) (string, bool, error) {
	r.calls++
	return "stub", true, nil
}

func deleteBash(shell sandbox.Shell, root string, terminal *deleteGateTerminal) bash {
	return bash{shell: shell, workDir: root, sb: sandbox.Spec{WriteRoots: []string{root}}, terminal: terminal}
}

func bashShell() sandbox.Shell { return sandbox.Shell{Kind: sandbox.ShellBash, Path: "bash"} }

// TestBashDestructiveTargetGate pins the refusal side: every recursive delete
// whose target is not a literal path strictly inside the granted roots is
// refused before launch, with the same cause codes as upstream PR11786.
func TestBashDestructiveTargetGate(t *testing.T) {
	secrets.SetFilterSubprocessEnv(false)
	t.Cleanup(func() { secrets.SetFilterSubprocessEnv(false) })
	root := t.TempDir()
	outside := t.TempDir()
	cases := []struct {
		command string
		ps      bool
		code    string
	}{
		{`rm -rf "$HOME"`, false, CodeDeleteNonliteral}, {`rm -rf ~`, false, CodeDeleteNonliteral}, {`rm -rf /`, false, CodeDestructiveTarget},
		{`rm -rf "$target"`, false, CodeDeleteNonliteral}, {`rm -rf "${target}/child"`, false, CodeDeleteNonliteral},
		{`rm --recursive '` + filepath.ToSlash(outside) + `'`, false, CodeDestructiveTarget}, {`rm -rf ..`, false, CodeDestructiveTarget},
		{`rm -rf .`, false, CodeDestructiveTarget}, {`cd ..; rm -rf child`, false, CodeDestructiveTarget},
		{`f=build; rm -rf build`, false, CodeDeleteSequence},
		{`rm -rf --unknown build`, false, CodeDeleteOption},
		{`bash -c 'rm -rf ..'`, false, CodeDestructiveTarget},
		{`xargs rm -rf`, false, CodeDeleteNonliteral},
		{`find .. -delete`, false, CodeDestructiveTarget},
		{`Remove-Item -Recurse -Force $HOME`, true, CodeDeleteNonliteral},
		{`Remove-Item -Recurse -Force $env:USERPROFILE`, true, CodeDeleteNonliteral},
		{`Remove-Item -Recurse -Force ~`, true, CodeDeleteNonliteral},
		{`Remove-Item -Recurse -Force 'C:\'`, true, CodeDestructiveTarget},
		{`Remove-Item -Recurse -Force '` + filepath.ToSlash(outside) + `'`, true, CodeDestructiveTarget},
		{`Remove-Item -Recurse -Force ..`, true, CodeDestructiveTarget},
		{`Remove-Item -Recurse -Force .`, true, CodeDestructiveTarget},
		{`rm -r -fo $target`, true, CodeDeleteNonliteral},
		{`rd /s C:\`, true, CodeDestructiveTarget},
		{`iex 'Remove-Item -Recurse ..'`, true, CodeDestructiveTarget},
		{`powershell -Command 'Remove-Item -Recurse ..'`, true, CodeDestructiveTarget},
		{`cmd /c rd /s /q ..`, true, CodeDestructiveTarget},
		{`Get-ChildItem -Recurse .. | Remove-Item -Force`, true, CodeDeleteNonliteral},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			sh := bashShell()
			if tc.ps {
				sh = sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershellPath(t)}
			}
			terminal := &deleteGateTerminal{}
			b := deleteBash(sh, root, terminal)
			args, _ := json.Marshal(map[string]string{"command": tc.command})
			res, err := b.ExecuteDetailed(t.Context(), args)
			code, ok := deleteRefusalCode(err)
			if !ok || code != tc.code {
				t.Fatalf("want refusal %s, got %v", tc.code, err)
			}
			if terminal.calls != 0 || res.Execution.State != tool.ShellStateNotRun || res.Execution.MutationRisk != tool.ShellMutationNotStarted {
				t.Fatalf("command reached execution: calls=%d, execution=%+v", terminal.calls, res.Execution)
			}
		})
	}
}

// TestBashDeleteGateAllowsBoundedCleanup pins the false-positive side: normal
// delete usage — non-recursive removals, literal in-root recursive cleanup,
// literal Test-Path conditionals, literal pipelines, plain non-delete commands
// — must reach execution untouched.
func TestBashDeleteGateAllowsBoundedCleanup(t *testing.T) {
	secrets.SetFilterSubprocessEnv(false)
	t.Cleanup(func() { secrets.SetFilterSubprocessEnv(false) })
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		ps      bool
	}{
		{`rm -rf child`, false},
		{`rm -rf build && make`, false},
		{`cd app && rm -rf dist 2>/dev/null`, false},
		{`rm -rf build || true`, false},
		{`rm "$f"`, false},
		{`rm tree`, false},
		{`rmdir /absolute/empty`, false},
		{`git status`, false},
		{`Remove-Item -LiteralPath child -Recurse -Force`, true},
		{`Remove-Item -Recurse -Force child -ErrorAction SilentlyContinue`, true},
		{`if (Test-Path child) { Remove-Item -Recurse -Force child -ErrorAction SilentlyContinue }`, true},
		{`Set-Location app; Remove-Item -Recurse dist`, true},
		{`'child' | Remove-Item -Force`, true},
		{`[IO.Directory]::Delete('child')`, true},
		{`[IO.Directory]::Delete('child',$false)`, true},
		{`(Get-Item child).Delete()`, true},
		{`Remove-Item $f`, true},
		{`Remove-Item style.css`, true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			sh := bashShell()
			if tc.ps {
				sh = sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershellPath(t)}
			}
			terminal := &deleteGateTerminal{}
			b := deleteBash(sh, root, terminal)
			args, _ := json.Marshal(map[string]string{"command": tc.command})
			if _, err := b.ExecuteDetailed(t.Context(), args); err != nil {
				t.Fatalf("bounded cleanup refused: %v", err)
			}
			if terminal.calls != 1 {
				t.Fatalf("command did not reach execution: calls=%d", terminal.calls)
			}
		})
	}
}

// TestDeleteIncidentHomeSurvives is the positive end-to-end case for the exact
// upstream incident: a failed assignment to the read-only $home variable
// followed by `Remove-Item -Recurse -Force $home`. The command must be refused
// before launch, the (fixture) home directory must survive, and no following
// statement may execute. The fixture is wired in via USERPROFILE/HOME so that a
// guard regression deletes the fixture instead of the real home — a probe
// asserts the override took effect before the dangerous command is attempted.
func TestDeleteIncidentHomeSurvives(t *testing.T) {
	powershell := powershellPath(t)
	root := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "keep.txt"), []byte("neutral fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(key, home)
	}
	// Safety probe: refuse to run the delete attempt unless $home in a child
	// PowerShell provably resolves to the fixture, never the real profile.
	out, err := execCommandOutput(t.Context(), powershell, `-NoProfile`, `-Command`, `Write-Output $home`)
	if err != nil {
		t.Fatalf("probe launch: %v", err)
	}
	if got := strings.TrimSpace(strings.ReplaceAll(out, "/", `\`)); !strings.EqualFold(got, home) {
		t.Fatalf("probe: child $home = %q, want fixture %q — refusing to continue", got, home)
	}

	command := `$home = Join-Path $env:TEMP 'cf-p6-manual'; if (Test-Path $home) { Remove-Item -Recurse -Force $home }; Set-Content executed.txt executed`
	b := bash{shell: sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershell}, workDir: root, sb: sandbox.Spec{WriteRoots: []string{root}}}
	args, _ := json.Marshal(map[string]string{"command": command})
	if _, err := b.ExecuteDetailed(t.Context(), args); err == nil {
		t.Fatal("incident command was not refused")
	} else if code, _ := deleteRefusalCode(err); code != CodeDeleteSequence {
		t.Fatalf("incident refusal code = %v, want %s", err, CodeDeleteSequence)
	}
	if _, err := os.Stat(filepath.Join(home, "keep.txt")); err != nil {
		t.Fatalf("home fixture damaged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "executed.txt")); !os.IsNotExist(err) {
		t.Fatalf("following statement executed: %v", err)
	}
}

// TestDeleteGateRejectsNestedDirectoryChange: a delete inside a command
// substitution cannot certify its cwd, so it is refused even with a literal
// in-root target.
func TestDeleteGateRejectsNestedDirectoryChange(t *testing.T) {
	terminal := &deleteGateTerminal{}
	root := t.TempDir()
	b := deleteBash(bashShell(), root, terminal)
	args, _ := json.Marshal(map[string]string{"command": `echo "$(cd ..; rm -rf child)"`})
	if _, err := b.ExecuteDetailed(t.Context(), args); err == nil {
		t.Fatal("nested delete was not refused")
	} else if code, _ := deleteRefusalCode(err); code != CodeDeleteSequence {
		t.Fatalf("refusal = %v, want %s", err, CodeDeleteSequence)
	}
	if terminal.calls != 0 {
		t.Fatalf("nested delete reached execution: calls=%d", terminal.calls)
	}
}

// TestDeleteGateRefusalCarriesCode: the model-facing error text carries a
// stable (refusal: shell.*) identity so a refusal is distinguishable from an
// execution failure.
func TestDeleteGateRefusalCarriesCode(t *testing.T) {
	root := t.TempDir()
	b := deleteBash(bashShell(), root, nil)
	err := b.refuseDestructiveDelete(t.Context(), bashShell(), `rm -rf "$HOME"`)
	if err == nil || !strings.Contains(err.Error(), "(refusal: "+CodeDeleteNonliteral+")") {
		t.Fatalf("refusal identity missing: %v", err)
	}
}

// TestDeleteGateIgnoresOrdinaryScriptErrors keeps the guard in its lane: a
// command with shell syntax errors is a syntax refusal, but non-delete
// commands with unusual (still parsable) shapes pass.
func TestDeleteGateIgnoresOrdinaryScriptErrors(t *testing.T) {
	root := t.TempDir()
	b := deleteBash(bashShell(), root, nil)
	if err := b.refuseDestructiveDelete(t.Context(), bashShell(), `rm -rf '`); err == nil {
		t.Fatal("unparsable command was not refused")
	} else if code, _ := deleteRefusalCode(err); code != CodeShellSyntax {
		t.Fatalf("refusal = %v, want %s", err, CodeShellSyntax)
	}
	for _, command := range []string{`echo hi`, ``, `>out`, `x=1`} {
		if err := b.refuseDestructiveDelete(t.Context(), bashShell(), command); err != nil {
			t.Fatalf("%q refused: %v", command, err)
		}
	}
}

// TestDeletePowerShellGoldenCauses mirrors upstream PR11786's pwsh golden
// table (semantics对照，见 handoff-903.md): same cause codes for the same
// PowerShell shapes, and the same "allowed" shapes stay allowed.
func TestDeletePowerShellGoldenCauses(t *testing.T) {
	sh := sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershellPath(t)}
	root := t.TempDir()
	b := deleteBash(sh, root, nil)
	for _, tc := range []struct {
		source string
		cause  string
	}{
		{`Remove-Item -Recurse '`, CodeShellSyntax},
		{`Remove-Item -Recurse child`, ""},
		{`Remove-Item -Recurse .`, CodeDestructiveTarget},
		{`Remove-Item -Recurse *`, CodeDeleteNonliteral},
		{`Remove-Item -Recurse $target`, CodeDeleteNonliteral},
		{`$x=1; Remove-Item -Recurse child`, CodeDeleteSequence},
		{`while ($false) { Remove-Item -Recurse child }`, CodeDeleteSequence},
		{`if (Test-Path child) { Remove-Item -Recurse child }`, ""},
		{`'child' | Remove-Item -Force`, ""},
		{`Get-ChildItem child | Remove-Item -Force`, CodeDeleteNonliteral},
		{`[IO.Directory]::Delete('..',$true)`, CodeDestructiveTarget},
		{`Set-Alias z Remove-Item; z -Recurse child`, CodeDeleteSequence},
		{`$PSDefaultParameterValues['Remove-Item:Recurse']=$true; Remove-Item -Force ..`, CodeDeleteSequence},
		{`Remove-Item -Recurse:$false $f`, ""},
		{`iex ('Remove-Item ' + '-Recurse ..')`, CodeDestructiveTarget},
	} {
		t.Run(tc.source, func(t *testing.T) {
			err := b.refuseDestructiveDelete(t.Context(), sh, tc.source)
			if tc.cause == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if code, ok := deleteRefusalCode(err); !ok || code != tc.cause {
				t.Fatalf("refusal = %v, want cause %s", err, tc.cause)
			}
		})
	}
}

// TestDeleteParserNeverEvaluates guards the analysis channel itself: the
// source handed to the host parser is data, never executed — a payload that
// would create a marker file must not create it, and must not contaminate the
// parser for the next request.
func TestDeleteParserNeverEvaluates(t *testing.T) {
	sh := sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershellPath(t)}
	root := t.TempDir()
	marker := filepath.Join(root, "marker")
	source := `Set-Content -LiteralPath '` + marker + `' injected; function Analyze-DeleteSource { }; Remove-Item -Recurse child`
	if _, err := analyzePowerShellDelete(t.Context(), sh, source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source evaluated: %v", err)
	}
	a, err := analyzePowerShellDelete(t.Context(), sh, `Remove-Item -Recurse child`)
	if err != nil || len(a.Calls) != 1 || a.Calls[0].Name != "Remove-Item" {
		t.Fatalf("source contaminated parser: %+v %v", a, err)
	}
}

// TestDeletePowerShellUnicode: literal targets survive the JSON round trip
// through the host parser, so a unicode directory name is judged as itself.
func TestDeletePowerShellUnicode(t *testing.T) {
	sh := sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershellPath(t)}
	target := "中文-fixture"
	analysis, err := analyzePowerShellDelete(t.Context(), sh, "Remove-Item -Recurse '"+target+"'")
	if err != nil || len(analysis.Calls) != 1 || len(analysis.Calls[0].Args) != 2 || analysis.Calls[0].Args[1] != target {
		t.Fatalf("unicode analysis = %+v, err=%v", analysis, err)
	}
}
