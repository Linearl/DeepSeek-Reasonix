# tl 1163: Windows Defender realtime-protection A/B cold-start experiment.
#
# measures reasonix-desktop cold starts by reading the per-boot breakdown lines
# that desktop/startup_metrics.go appends to startup-metrics.log (cwd of the
# app process), with realtime protection ON vs OFF.
#
# SAFETY CONTRACT (tl 1163 night red line: never run unattended):
#   - this script only touches Defender when -ToggleDefender is passed;
#   - with -ToggleDefender it requires elevation, records the ORIGINAL
#     preference first, and restores it in a finally block no matter how the
#     loop ends (Ctrl-C included). It asserts the restore before exiting;
#   - single-instance: one app at a time. The loop waits for full process exit
#     between runs, so the Wails single-instance lock is never contended.
#
# PRECONDITIONS:
#   - the app's working directory (this script passes $LogDir) must be
#     writable: startup-metrics.log lands there;
#   - in the app's Settings, close behavior must be "exit" (NOT "minimize to
#     tray"), otherwise CloseMainWindow hides instead of quitting and every
#     run degrades to the force-kill path (each run is then flagged KILLED and
#     should be discarded);
#   - expect warmup launches (discarded) so OS file cache / first-run
#     migrations don't skew group 1.
#
# USAGE (user present, never unattended):
#   # read-only (Defender untouched; still useful for p50/p90 baseline):
#   powershell -ExecutionPolicy Bypass -File scripts\defender-startup-experiment.ps1 -AppPath "C:\...\reasonix-desktop.exe" -Runs 10
#   # full A/B (elevated PowerShell required):
#   powershell -ExecutionPolicy Bypass -File scripts\defender-startup-experiment.ps1 -AppPath "C:\...\reasonix-desktop.exe" -Runs 10 -ToggleDefender
#
# OUTPUT: per-run lines + a p50/p90 summary per group, plus
#   defender-experiment-results.csv (group,run,pid,window_shown_ms,frontend_ready_ms,killed)
# next to this script for the tl 1163 report.

param(
  [Parameter(Mandatory = $true)][string]$AppPath,
  [int]$Runs = 10,
  [int]$WarmupRuns = 1,
  [switch]$ToggleDefender,
  [string]$LogDir = (Split-Path -Parent $MyInvocation.MyCommand.Path)
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $AppPath)) { throw "AppPath not found: $AppPath" }
$metricsLog = Join-Path $LogDir 'startup-metrics.log'

# --- Defender helpers -------------------------------------------------------

function Get-RTPState {
  # $true = realtime protection actively running.
  (Get-MpComputerStatus).RealTimeProtectionEnabled
}

function Assert-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if ($ToggleDefender -and -not (Assert-Admin)) {
  throw "-ToggleDefender needs an elevated PowerShell (start PowerShell as Administrator)."
}

# --- launch / close / measure ----------------------------------------------

function Wait-AppGone {
  param([System.Diagnostics.Process]$Proc, [int]$TimeoutSec = 20)
  if (-not $Proc) { return $true }
  if (-not $Proc.HasExited) {
    try { $null = $Proc.CloseMainWindow() } catch {}
    $null = $Proc.WaitForExit($TimeoutSec * 1000)
  }
  return [bool]$Proc.HasExited
}

function Invoke-ColdStart {
  # One cold start: launch the app, wait for its startup-metrics line (matched
  # by pid), close it. Returns $null when no metrics line appeared.
  param([string]$Name, [int]$TimeoutSec = 90)

  $proc = Start-Process -FilePath $AppPath -WorkingDirectory $LogDir -PassThru
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  $line = $null
  while ((Get-Date) -lt $deadline -and -not $line) {
    Start-Sleep -Milliseconds 500
    if ($proc.HasExited) { break }
    if (Test-Path $metricsLog) {
      $line = Select-String -Path $metricsLog -Pattern ("pid=" + $proc.Id + " ") -ErrorAction SilentlyContinue |
        Select-Object -Last 1
    }
  }

  if ($line) {
    # settle: let the boot summary window close (frontend_ready / hydrate
    # marks land seconds after window_shown) before tearing the app down
    Start-Sleep -Seconds 12
  }
  $killed = -not (Wait-AppGone -Proc $proc)
  if ($killed) {
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
  }

  if (-not $line) {
    Write-Host "  pid=$($proc.Id): NO METRICS LINE within ${TimeoutSec}s (crashed before frontend ready?)" -ForegroundColor Yellow
    return $null
  }

  $ws = $null; $fr = $null
  if ($line.Line -match ' window_shown=(\d+)ms') { $ws = [int]$Matches[1] }
  if ($line.Line -match ' frontend_ready=(\d+)ms') { $fr = [int]$Matches[1] }
  Write-Host ("  pid={0}: window_shown={1}ms frontend_ready={2}ms{3}" -f $proc.Id, $ws, $fr, $(if ($killed) { "  [KILLED - discard]" } else { "" }))
  return [pscustomobject]@{ Group = $Name; Pid = $proc.Id; WindowShownMs = $ws; FrontendReadyMs = $fr; Killed = $killed }
}

function Get-Percentile {
  param([int[]]$Values, [int]$P)
  if (-not $Values -or $Values.Count -eq 0) { return $null }
  $sorted = $Values | Sort-Object
  $rank = [Math]::Ceiling($P / 100.0 * $sorted.Count)
  if ($rank -lt 1) { $rank = 1 }
  return $sorted[[int]($rank - 1)]
}

function Invoke-RunGroup {
  param([string]$Name, [int]$Count, [int]$Warmup, [System.Collections.Generic.List[object]]$Sink)
  Write-Host "== group $Name : $Count measured runs (+$Warmup warmup) =="
  for ($i = 0; $i -lt $Warmup; $i++) {
    Write-Host "  warmup $([int]($i + 1))/$Warmup (discarded)"
    Invoke-ColdStart -Name "$Name-warmup" | Out-Null
  }
  for ($i = 0; $i -lt $Count; $i++) {
    Write-Host "  run $([int]($i + 1))/$Count"
    $r = Invoke-ColdStart -Name $Name
    if ($r) { $Sink.Add($r) }
  }
}

function Show-GroupStats {
  param([string]$Name, [System.Collections.Generic.List[object]]$Rows)
  Write-Host ("-- {0}: measured n={1}" -f $Name, $Rows.Count)
  $ws = @($Rows | Where-Object { -not $_.Killed -and $_.WindowShownMs } | ForEach-Object { [int]$_.WindowShownMs })
  $fr = @($Rows | Where-Object { -not $_.Killed -and $_.FrontendReadyMs } | ForEach-Object { [int]$_.FrontendReadyMs })
  if ($ws.Count) { Write-Host ("   window_shown  ms: p50={0} p90={1} min={2} max={3}" -f (Get-Percentile $ws 50), (Get-Percentile $ws 90), ($ws | Measure-Object -Minimum).Minimum, ($ws | Measure-Object -Maximum).Maximum) }
  if ($fr.Count) { Write-Host ("   frontend_ready ms: p50={0} p90={1} min={2} max={3}" -f (Get-Percentile $fr 50), (Get-Percentile $fr 90), ($fr | Measure-Object -Minimum).Minimum, ($fr | Measure-Object -Maximum).Maximum) }
}

# --- main --------------------------------------------------------------------

$results = New-Object System.Collections.Generic.List[object]

Write-Host "App: $AppPath"
Write-Host "App cwd / log dir: $LogDir"
Write-Host "Defender realtime protection now: $(Get-RTPState)"

if (-not $ToggleDefender) {
  Invoke-RunGroup -Name 'on' -Count $Runs -Warmup $WarmupRuns -Sink $results
}
else {
  $initialDisabled = (Get-MpPreference).DisableRealtimeMonitoring
  Write-Host "Initial DisableRealtimeMonitoring preference: $initialDisabled"
  try {
    Invoke-RunGroup -Name 'on' -Count $Runs -Warmup $WarmupRuns -Sink $results

    Write-Host "== disabling realtime protection =="
    Set-MpPreference -DisableRealtimeMonitoring $true
    Start-Sleep -Seconds 2
    Write-Host "RealTimeProtectionEnabled now: $(Get-RTPState)"

    Invoke-RunGroup -Name 'off' -Count $Runs -Warmup 0 -Sink $results
  }
  finally {
    Write-Host "== restoring realtime protection (finally) =="
    Set-MpPreference -DisableRealtimeMonitoring $initialDisabled
    Start-Sleep -Seconds 2
    Write-Host "DisableRealtimeMonitoring restored to: $((Get-MpPreference).DisableRealtimeMonitoring)"
    Write-Host "RealTimeProtectionEnabled now: $(Get-RTPState)"
  }
}

# --- summary -----------------------------------------------------------------

$onRows  = New-Object System.Collections.Generic.List[object]
$offRows = New-Object System.Collections.Generic.List[object]
foreach ($r in $results) {
  if ($r.Group -eq 'on') { $onRows.Add($r) } elseif ($r.Group -eq 'off') { $offRows.Add($r) }
}
Show-GroupStats -Name 'GROUP on' -Rows $onRows
if ($ToggleDefender) { Show-GroupStats -Name 'GROUP off' -Rows $offRows }

$csvPath = Join-Path $LogDir 'defender-experiment-results.csv'
$results | Select-Object Group, Pid, WindowShownMs, FrontendReadyMs, Killed |
  Export-Csv -Path $csvPath -NoTypeInformation -Encoding UTF8
Write-Host "Per-run rows: $csvPath"
if ($ToggleDefender) {
  if ((Get-MpPreference).DisableRealtimeMonitoring -ne $initialDisabled) {
    Write-Host "WARNING: preference did not match the initial value - restore manually with: Set-MpPreference -DisableRealtimeMonitoring <initial>" -ForegroundColor Red
  } else {
    Write-Host "Defender preference restore verified."
  }
}
