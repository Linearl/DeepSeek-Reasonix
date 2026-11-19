<#
.SYNOPSIS
  验证文档保鲜定时扫描任务状态

.DESCRIPTION
  检查 Windows Task Scheduler 中文档保鲜定时任务的状态。
  显示任务名称、运行状态、触发时间、下次运行时间。

.PARAMETER TaskName
  定时任务名称。默认: DocFreshnessAudit。

.EXAMPLE
  .\verify-schedule.ps1
  验证默认任务。

.EXAMPLE
  .\verify-schedule.ps1 -TaskName "MyAudit"
  验证指定名称的任务。
#>

[CmdletBinding()]
param(
    [Parameter(HelpMessage = "定时任务名称")]
    [string]$TaskName = "DocFreshnessAudit"
)

$ErrorActionPreference = "Continue"

$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if (-not $task) {
    Write-Host "任务未找到: $TaskName" -ForegroundColor Red
    Write-Host "  使用 .\setup-schedule.ps1 创建任务"
    exit 1
}

Write-Host "定时任务状态" -ForegroundColor Cyan
Write-Host ("-" * 50)
Write-Host ("{0,-16}: {1}" -f "TaskName", $task.TaskName)
Write-Host ("{0,-16}: {1}" -f "State", $task.State)
Write-Host ("{0,-16}: {1}" -f "Description", $task.Description)

$task.Triggers | ForEach-Object {
    if ($_.StartBoundary) {
        Write-Host ("{0,-16}: Daily at {1}" -f "Trigger", $_.StartBoundary)
    }
}

try {
    $info = Get-ScheduledTaskInfo -TaskName $TaskName -ErrorAction Stop
    $lastRun = if ($info.LastRunTime.Year -lt 2000) { "从未运行" } else { $info.LastRunTime.ToString() }
    Write-Host ("{0,-16}: {1}" -f "NextRun", $info.NextRunTime)
    Write-Host ("{0,-16}: {1}" -f "LastRun", $lastRun)
    Write-Host ("{0,-16}: {1}" -f "LastResult", $info.LastTaskResult)
} catch {
    Write-Host "  无法获取运行信息: $_" -ForegroundColor Yellow
}

Write-Host ("-" * 50)

# Exit code: 0 = task exists and ready
if ($task.State -eq "Ready") {
    Write-Host "任务状态正常" -ForegroundColor Green
    exit 0
} else {
    Write-Host "任务状态异常: $($task.State)" -ForegroundColor Red
    exit 2
}
