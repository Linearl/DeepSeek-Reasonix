<#
.SYNOPSIS
  导出工作流开始至今的 Git 提交为 Markdown 摘要。

.DESCRIPTION
  参考仓库根目录 scripts/export-git-log.ps1 的编码与日志解析方式，
  针对 ll-develop 的 C.3 场景做简化：
  - 输入 workflowStartAt（或从环境变量读取）
  - 导出该时间点至今的提交
  - 生成 Markdown 文本并可选保存到文件

.PARAMETER WorkflowStartAt
  工作流开始时间（ISO 8601）。示例：2026-03-12T14:05:00+08:00

.PARAMETER RepoRoot
  Git 仓库根路径，默认当前目录。

.PARAMETER OutputPath
  可选。若提供则保存 Markdown 到该文件；否则仅输出到控制台。

.PARAMETER NoPrint
  可选。提供 OutputPath 时如不希望控制台打印正文可开启。

.EXAMPLE
  .\tools\export-workflow-git-log.ps1 -WorkflowStartAt "2026-03-12T14:05:00+08:00"

.EXAMPLE
  .\tools\export-workflow-git-log.ps1 -WorkflowStartAt "2026-03-12T14:05:00+08:00" -OutputPath "temp\workflow-progress-summary.md"
#>
param(
  [string]$WorkflowStartAt = "",
  [string]$RepoRoot = "",
  [string]$OutputPath = "",
  [switch]$NoPrint
)

$ErrorActionPreference = "Stop"

# UTF-8 输出，避免中文乱码
$prevOutputEncoding = [Console]::OutputEncoding
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

try {
  if (-not $WorkflowStartAt) {
    $WorkflowStartAt = $env:AUTO_WORKFLOW_START_AT
  }

  if (-not $WorkflowStartAt) {
    throw "缺少 WorkflowStartAt。请通过 -WorkflowStartAt 传入，或设置环境变量 AUTO_WORKFLOW_START_AT。"
  }

  # 验证并标准化时间
  $startDt = [DateTimeOffset]::Parse($WorkflowStartAt)
  $startIso = $startDt.ToString("yyyy-MM-ddTHH:mm:sszzz")
  $nowIso = (Get-Date).ToString("yyyy-MM-ddTHH:mm:sszzz")

  if (-not $RepoRoot) {
    $RepoRoot = (Get-Location).Path
  }

  Push-Location $RepoRoot
  try {
    $gitCheck = git rev-parse --is-inside-work-tree 2>$null
    if ($gitCheck -ne "true") {
      throw "当前目录不是 Git 仓库：$RepoRoot"
    }

    $separator = "||SEP||"
    $logFormat = "%H${separator}%ai${separator}%an${separator}%s"

    # 仅导出 workflowStartAt 之后的提交（旧到新）
    $rawLog = git -c i18n.logOutputEncoding=utf-8 log --since="$startIso" --reverse --format="$logFormat"

    $commits = [System.Collections.ArrayList]::new()
    if ($rawLog) {
      foreach ($line in $rawLog) {
        $parts = $line -split [regex]::Escape($separator)
        if ($parts.Count -lt 4) { continue }

        $fullHash = $parts[0]
        $hash = if ($fullHash.Length -ge 8) { $fullHash.Substring(0, 8) } else { $fullHash }
        $dateStr = $parts[1].Trim()
        $author = $parts[2].Trim()
        $subject = $parts[3].Trim()

        [void]$commits.Add(@{
          Hash = $hash
          FullHash = $fullHash
          Date = $dateStr
          Author = $author
          Subject = $subject
        })
      }
    }

    # 汇总统计
    $total = $commits.Count
    $byAuthor = @{}
    foreach ($c in $commits) {
      if (-not $byAuthor.ContainsKey($c.Author)) {
        $byAuthor[$c.Author] = 0
      }
      $byAuthor[$c.Author]++
    }

    $lines = [System.Collections.ArrayList]::new()
    [void]$lines.Add("# 工作流提交导出摘要")
    [void]$lines.Add("")
    [void]$lines.Add("> 生成时间：$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')")
    [void]$lines.Add("> 时间范围：$startIso ~ $nowIso")
    [void]$lines.Add("> 提交总数：$total")
    [void]$lines.Add("")

    [void]$lines.Add("## 作者分布")
    [void]$lines.Add("")
    if ($byAuthor.Keys.Count -eq 0) {
      [void]$lines.Add("- 无提交记录")
    } else {
      foreach ($author in ($byAuthor.Keys | Sort-Object)) {
        [void]$lines.Add("- $author：$($byAuthor[$author])")
      }
    }
    [void]$lines.Add("")

    [void]$lines.Add("## 提交明细")
    [void]$lines.Add("")
    if ($total -eq 0) {
      [void]$lines.Add("- 在该时间范围内未检测到提交。")
    } else {
      [void]$lines.Add("| 时间 | Hash | 作者 | 提交说明 |")
      [void]$lines.Add("|------|------|------|----------|")
      foreach ($c in $commits) {
        $escapedSubject = ($c.Subject -replace '\\|', '\\|')
        [void]$lines.Add("| $($c.Date) | ``$($c.Hash)`` | $($c.Author) | $escapedSubject |")
      }
    }
    [void]$lines.Add("")

    $markdown = $lines -join [Environment]::NewLine

    if ($OutputPath) {
      $resolvedOutput = if ([System.IO.Path]::IsPathRooted($OutputPath)) {
        $OutputPath
      } else {
        Join-Path $RepoRoot $OutputPath
      }

      $outDir = Split-Path -Parent $resolvedOutput
      if ($outDir -and -not (Test-Path $outDir)) {
        New-Item -ItemType Directory -Path $outDir -Force | Out-Null
      }

      $utf8NoBom = New-Object System.Text.UTF8Encoding $false
      [System.IO.File]::WriteAllText($resolvedOutput, $markdown, $utf8NoBom)
      Write-Host "✓ 已保存到: $resolvedOutput" -ForegroundColor Green
    }

    if (-not $NoPrint) {
      Write-Output $markdown
    }
  }
  finally {
    Pop-Location
  }
}
finally {
  [Console]::OutputEncoding = $prevOutputEncoding
}
