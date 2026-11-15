# tl 1597 (part 3/3): safe worktree removal -- junction-uncoupling wrapper.
#
# This is the hard guard against the 2026-11-13 incident family: 6 times in
# one day the main repo's desktop/frontend/node_modules was gutted because a
# delete that started INSIDE a worktree traversed the node_modules / wailsjs
# junctions into the main repo's real files (git worktree remove, .NET
# Directory.Delete, recycle-bin deletes all traverse junctions).
#
# SAFETY CONTRACT (assertion-gated, every delete action is behind an
# assertion; refuse rather than guess):
#   A. target must resolve to a REGISTERED git worktree (never an arbitrary
#      directory), and must never be the main repo root itself;
#   B. BEFORE any removal: enumerate ALL reparse points under the target
#      (recursive, incl. hidden); uncouple each junction with
#      `cmd /c rmdir` (removes ONLY the link; never traverses into the
#      target -- this is the documented Windows junction semantics, unlike
#      recursive deletes / Remove-Item which have traversed historically);
#   C. ZERO-REPPOINT ASSERTION: re-scan after uncoupling; if any reparse
#      point remains, ABORT without deleting anything (hard requirement);
#   D. only after C passes: `git worktree remove --force` (plain files
#      only at this point; traversal is impossible), then `git worktree prune`;
#   E. AFTER removal: re-check main-repo node_modules health (.bin/tsc.cmd +
#      .pnpm entry count) against the pre-removal snapshot; any mismatch is
#      reported loudly as evidence that traversal still happened somewhere.
#
# INTEGRATION: existing worktree-retirement flows can call this instead of
#   bare `git worktree remove` (single arg = worktree path or name under
#   the worktrees container).
#
# USAGE:
#   powershell -ExecutionPolicy Bypass -File scripts\remove-worktree-safe.ps1 -Worktree wt-tl1234        # plan only? no: removes. Add -DryRun to plan.
#   powershell ... -File scripts\remove-worktree-safe.ps1 -Worktree wt-tl1234 -DryRun                    # enumerate junctions + plan, delete nothing
#   powershell ... -File scripts\remove-worktree-safe.ps1 -WorktreePath C:\...\worktrees\wt-tl1234
# OUTPUT: junction inventory, uncoupling log, zero-assertion result,
#   pre/post main-repo health snapshots, elapsed time.

param(
    # Worktree name (resolved under the worktrees container) or...
    [string]$Worktree,

    # ...absolute path of a registered worktree.
    [string]$WorktreePath,

    [switch]$DryRun
)

$ErrorActionPreference = "Stop"

function Get-MainRepoRoot {
    $lines = @(& git worktree list --porcelain)
    if ($LASTEXITCODE -ne 0) { throw "git worktree list failed (run inside the reasonix repo or a worktree)" }
    foreach ($line in $lines) {
        if ($line -like "worktree *") { return $line.Substring("worktree ".Length) }
    }
    throw "could not parse main repo root from git worktree list"
}

function Get-RegisteredWorktreePaths {
    # Returns the list of registered worktree paths (main repo first),
    # normalized to backslash form so they compare equal to [IO.Path]::GetFullPath
    # output (git prints forward slashes even on Windows).
    $out = @()
    $lines = @(& git worktree list --porcelain)
    foreach ($line in $lines) {
        if ($line -like "worktree *") {
            $p = $line.Substring("worktree ".Length).Replace('/', '\')
            $out += , [IO.Path]::GetFullPath($p).TrimEnd('\')
        }
    }
    return $out
}

function Get-NodeModulesStats {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stats = @{
        Exists      = (Test-Path -LiteralPath $Path -PathType Container)
        TscCmd      = $false
        PnpmEntries = 0
    }
    if (-not $stats.Exists) { return $stats }
    $stats.TscCmd = (Test-Path -LiteralPath (Join-Path $Path ".bin\tsc.cmd") -PathType Leaf)
    $pnpmDir = Join-Path $Path ".pnpm"
    if (Test-Path -LiteralPath $pnpmDir -PathType Container) {
        $stats.PnpmEntries = @(Get-ChildItem -LiteralPath $pnpmDir -Force -Directory).Count
    }
    return $stats
}

function Get-ReparsePoints {
    # All reparse points (junctions/symlinks) anywhere under $Root.
    # DELIBERATELY a manual stack walk, NOT Get-ChildItem -Recurse: PS5.1
    # recursion FOLLOWS junctions, so the enumerator itself would traverse
    # into the main repo through wt junctions and could list main-repo
    # reparse points as "ours" (the scanner becoming a traversal victim).
    # Here: record a reparse-point dir, but never descend into it.
    param([Parameter(Mandatory = $true)][string]$Root)
    $found = @()
    $stack = [System.Collections.Stack]::new()
    $stack.Push($Root)
    while ($stack.Count -gt 0) {
        $dir = $stack.Pop()
        $children = @(Get-ChildItem -LiteralPath $dir -Force -ErrorAction SilentlyContinue)
        foreach ($c in $children) {
            if ($c.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                $found += , $c
                continue   # never descend into a reparse point
            }
            if ($c.PSIsContainer) { $stack.Push($c.FullName) }
        }
    }
    return $found
}

function Assert-MainNodeModulesIntact {
    param(
        [Parameter(Mandatory = $true)][hashtable]$Before,
        [Parameter(Mandatory = $true)][string]$MainRepoRoot
    )
    $nm = Join-Path $MainRepoRoot "desktop\frontend\node_modules"
    $after = Get-NodeModulesStats -Path $nm
    $problems = @()
    if ($before.TscCmd -and -not $after.TscCmd) { $problems += ".bin/tsc.cmd vanished from main repo during removal (TRAVERSAL HAPPENED)" }
    if ($before.PnpmEntries -gt 0 -and $after.PnpmEntries -lt $before.PnpmEntries) {
        $problems += (".pnpm entries shrank {0} -> {1} during removal (TRAVERSAL HAPPENED)" -f $before.PnpmEntries, $after.PnpmEntries)
    }
    if ($problems.Count -gt 0) {
        Write-Host "MAIN-REPO INTEGRITY BROKEN:" -ForegroundColor Red
        foreach ($p in $problems) { Write-Host ("  - " + $p) -ForegroundColor Red }
        Write-Host "recover: scripts\check-node_modules-health.ps1 -Action Restore"
        throw "post-removal main-repo node_modules integrity check FAILED"
    }
    Write-Host ("[post-check] main repo node_modules intact: tsc.cmd={0} .pnpm={1}" -f $after.TscCmd, $after.PnpmEntries)
}

# --- resolve target ----------------------------------------------------------

$mainRepoRoot = Get-MainRepoRoot
$registered = Get-RegisteredWorktreePaths

if (-not $Worktree -and -not $WorktreePath) { throw "pass -Worktree <name> or -WorktreePath <absolute path>" }
$target = $WorktreePath
if (-not $target) {
    # name form: resolve by exact leaf-name match against registrations
    $resolved = @($registered | Where-Object { (Split-Path -Leaf $_) -eq $Worktree })
    if ($resolved.Count -eq 0) { throw "no registered worktree named '$Worktree' (registered: $($registered -join ', '))" }
    if ($resolved.Count -gt 1) { throw "ambiguous worktree name '$Worktree' matches $($resolved.Count) registrations" }
    $target = $resolved[0]
}
$target = [IO.Path]::GetFullPath($target).TrimEnd('\', '/')

if ($registered -notcontains $target) {
    throw "target is NOT a registered git worktree: $target (registered: $($registered -join ', '))"
}
if ($target -ieq $mainRepoRoot) {
    throw "refusing: target IS the main repo root ($mainRepoRoot)"
}
if ((Test-Path -LiteralPath $target -PathType Container) -eq $false) {
    Write-Host "target directory no longer exists on disk; will run 'git worktree prune' only."
    if ($DryRun) { Write-Host "DRY-RUN: git worktree prune (skipped)"; exit 0 }
    & git worktree prune
    exit 0
}

Write-Host "== tl1597 safe worktree removal =="
Write-Host ("target : $target")
Write-Host ("main   : $mainRepoRoot")
Write-Host ("dry-run: {0}" -f [bool]$DryRun)

# --- pre snapshots -----------------------------------------------------------

$nmBefore = Get-NodeModulesStats -Path (Join-Path $mainRepoRoot "desktop\frontend\node_modules")
Write-Host ("[pre-check] main repo node_modules: tsc.cmd={0} .pnpm entries={1}" -f $nmBefore.TscCmd, $nmBefore.PnpmEntries)

$junctions = Get-ReparsePoints -Root $target
Write-Host ("[pre-check] reparse points under target: {0}" -f $junctions.Count)
foreach ($j in $junctions) {
    Write-Host ("  - " + $j.FullName.Substring($target.Length + 1) + " -> " + ($j.Target -join ',') + " [" + $j.LinkType + "]")
}

if ($DryRun) {
    Write-Host "DRY-RUN plan:"
    foreach ($j in $junctions) {
        Write-Host ("  1. cmd /c rmdir `"" + $j.FullName + "`"   # uncouple junction only (never traverses)")
    }
    Write-Host ("  2. re-scan reparse points; assert count == 0 (current: {0})" -f $junctions.Count)
    Write-Host "  3. git worktree remove --force <target>"
    Write-Host "  4. git worktree prune"
    Write-Host "  5. post-check main repo node_modules vs pre snapshot"
    Write-Host "DRY-RUN: nothing uncoupled or removed. Re-run without -DryRun to execute."
    exit 0
}

$sw = [System.Diagnostics.Stopwatch]::StartNew()

# --- B: uncouple every junction with cmd rmdir (link-only delete) ------------

foreach ($j in $junctions) {
    Write-Host ("uncoupling: " + $j.FullName)
    & cmd /c rmdir "$($j.FullName)"
    if ($LASTEXITCODE -ne 0) { throw "rmdir failed to uncouple junction: $($j.FullName) (exit $LASTEXITCODE)" }
}

# --- C: ZERO-REPPOINT ASSERTION (hard gate before any tree deletion) ---------

$remaining = Get-ReparsePoints -Root $target
if ($remaining.Count -ne 0) {
    Write-Host "ZERO-REPPOINT ASSERTION FAILED; remaining reparse points:" -ForegroundColor Red
    foreach ($j in $remaining) { Write-Host ("  - " + $j.FullName) -ForegroundColor Red }
    throw "refusing to remove the worktree: reparse points remain after uncoupling ($($remaining.Count)). Nothing was deleted. Investigate manually."
}
Write-Host "[assert] reparse points == 0: PASS"

# --- D: only now remove the worktree ----------------------------------------

& git worktree remove --force $target
if ($LASTEXITCODE -ne 0) {
    # worktree is now junction-free; a remove failure means plain files are
    # locked/undeletable -- report, do NOT improvise our own recursive delete
    # (an ad-hoc rmtree here would be a new member of the traversal family).
    throw "git worktree remove --force failed (exit $LASTEXITCODE). Junctions are already uncoupled; resolve file locks manually, then re-run this script (it will prune)."
}
& git worktree prune
$sw.Stop()

# --- E: post-removal main-repo integrity -------------------------------------

Assert-MainNodeModulesIntact -Before $nmBefore -MainRepoRoot $mainRepoRoot
Write-Host ("safe removal DONE in {0}s: junctions uncoupled={1}, worktree removed, main repo intact" -f $sw.Elapsed.TotalSeconds.ToString("0.0"), $junctions.Count)
