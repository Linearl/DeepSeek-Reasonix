<#
.SYNOPSIS
  创建文档保鲜定时扫描任务（Windows Task Scheduler）

.DESCRIPTION
  REQ-7210 文档保鲜系统——创建每日定时扫描任务。
  默认每天 02:00 执行 check-doc-freshness.py 扫描。

.PARAMETER RepoRoot
  项目根目录路径。默认: 当前目录。

.PARAMETER AuditRoot
  04-auto_audit/ 目录路径。默认: RepoRoot/04-auto_audit/。

.PARAMETER TaskName
  定时任务名称。默认: DocFreshnessAudit。

.PARAMETER Time
  每日执行时间（HH:MM）。默认: 02:00。

.PARAMETER PythonExe
  Python 可执行文件路径。默认: python（使用 PATH 中的 python）。

.EXAMPLE
  .\setup-schedule.ps1
  使用默认值创建定时任务。

.EXAMPLE
  .\setup-schedule.ps1 -RepoRoot "D:\my-project" -Time "03:00"
  指定项目根目录和执行时间。

.EXAMPLE
  .\setup-schedule.ps1 -TaskName "MyAudit" -PythonExe "C:\Python311\python.exe"
  自定义任务名称和 Python 路径。
#>

[CmdletBinding()]
param(
    [Parameter(HelpMessage = "项目根目录路径")]
    [string]$RepoRoot = ".",

    [Parameter(HelpMessage = "04-auto_audit/ 目录路径")]
    [string]$AuditRoot = "",

    [Parameter(HelpMessage = "定时任务名称")]
    [string]$TaskName = "DocFreshnessAudit",

    [Parameter(HelpMessage = "每日执行时间（HH:MM）")]
    [string]$Time = "02:00",

    [Parameter(HelpMessage = "Python 可执行文件路径")]
    [string]$PythonExe = "python"
)

$ErrorActionPreference = "Stop"

# Resolve paths
$RepoPath = (Resolve-Path $RepoRoot).Path
if (-not $AuditRoot) {
    $AuditRoot = Join-Path $RepoPath "04-auto_audit"
}
if (-not (Test-Path $AuditRoot)) {
    Write-Error "审计目录不存在: $AuditRoot"
    exit 1
}

$ScannerScript = Join-Path $AuditRoot "scripts\check-doc-freshness.py"
if (-not (Test-Path $ScannerScript)) {
    Write-Error "扫描脚本不存在: $ScannerScript"
    exit 1
}

# Validate time format
if ($Time -notmatch '^\d{2}:\d{2}$') {
    Write-Error "时间格式无效: $Time（应为 HH:MM）"
    exit 1
}

Write-Host "创建定时任务..." -ForegroundColor Cyan
Write-Host "  项目根: $RepoPath"
Write-Host "  审计目录: $AuditRoot"
Write-Host "  任务名称: $TaskName"
Write-Host "  执行时间: 每日 $Time"
Write-Host "  Python: $PythonExe"
Write-Host "  扫描脚本: $ScannerScript"

# Build action
$action = New-ScheduledTaskAction `
    -Execute $PythonExe `
    -Argument "`"$ScannerScript`" --repo-root `"$RepoPath`" --audit-root `"$AuditRoot`""

# Build trigger
$trigger = New-ScheduledTaskTrigger -Daily -At $Time

# Build settings (run even on battery, don't stop on battery)
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -MultipleInstances IgnoreNew

# Remove existing task if present
$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "  警告: 任务 '$TaskName' 已存在，将覆盖"
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}

# Register task
$task = Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -Description "REQ-7210 文档保鲜每日扫描（$RepoPath）" `
    -Force

Write-Host ""
Write-Host "任务创建成功:" -ForegroundColor Green
Write-Host "  TaskName : $($task.TaskName)"
Write-Host "  State    : $($task.State)"

$info = Get-ScheduledTaskInfo -TaskName $TaskName
Write-Host "  NextRun  : $($info.NextRunTime)"
