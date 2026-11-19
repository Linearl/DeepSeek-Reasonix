<#
.SYNOPSIS
    导出 Git 提交信息，生成 Markdown 文档。

.DESCRIPTION
    扫描当前仓库的 git commit 记录，支持多种过滤和输出格式。
    - 默认按月份分组，每个月生成一个 YYYY-MM.md 文件
    - 支持时间范围、作者、关键词等过滤条件
    - 支持导出单个汇总文件

.PARAMETER OutputDir
    输出目录，默认为仓库根目录下的 docs_dev/5.git-commit

.PARAMETER OutputFile
    输出单个 Markdown 文件（与 OutputDir 互斥，优先级更高）

.PARAMETER StartDate
    起始日期（ISO 8601 格式：2026-01-01 或 2026-01-01T00:00:00）

.PARAMETER EndDate
    结束日期（ISO 8601 格式）

.PARAMETER Since
    Git log --since 参数（相对时间，如 "2 weeks ago", "2026-01-01"）

.PARAMETER Until
    Git log --until 参数

.PARAMETER Author
    按作者过滤（部分匹配，不区分大小写）

.PARAMETER Grep
    按提交说明过滤（正则表达式，部分匹配）

.PARAMETER Branch
    指定分支，默认为当前分支

.PARAMETER NoGroupByDay
    不按日期分组，所有提交平铺在月份下

.EXAMPLE
    # 导出全部提交
    .\scripts\export-git-log.ps1

.EXAMPLE
    # 导出最近 2 周的提交到单个文件
    .\scripts\export-git-log.ps1 -Since "2 weeks ago" -OutputFile "temp/recent.md"

.EXAMPLE
    # 导出指定作者在指定日期范围内的提交
    .\scripts\export-git-log.ps1 -StartDate "2026-07-01" -EndDate "2026-07-31" -Author "AI Novel"

.EXAMPLE
    # 导出包含 "feat" 关键词的提交
    .\scripts\export-git-log.ps1 -Grep "feat\(REQ" -OutputFile "temp/features.md"
#>
param(
    [string]$OutputDir = "",
    [string]$OutputFile = "",
    [string]$StartDate = "",
    [string]$EndDate = "",
    [string]$Since = "",
    [string]$Until = "",
    [string]$Author = "",
    [string]$Grep = "",
    [string]$Branch = "",
    [switch]$NoGroupByDay
)

$ErrorActionPreference = "Stop"

# 设置控制台编码为 UTF-8，确保 git 输出的中文不乱码
$prevOutputEncoding = [Console]::OutputEncoding
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

try {
    # 定位仓库根目录（避免 git rev-parse 对中文路径返回乱码）
    $gitCheck = git rev-parse --is-inside-work-tree 2>$null
    if ($gitCheck -ne "true") {
        throw "当前目录不是 Git 仓库"
    }

    # 使用当前工作目录作为仓库根目录
    $repoRoot = (Get-Location).Path

    # 设置默认输出路径
    if (-not $OutputDir -and -not $OutputFile) {
        $OutputDir = Join-Path $repoRoot "docs_dev\5.git-commit"
    }

    # 构建 git log 命令
    $gitArgs = @("log", "-c", "i18n.logOutputEncoding=utf-8")

    if ($Branch) {
        $gitArgs += $Branch
    }

    # 添加过滤参数
    if ($Since) {
        $gitArgs += "--since=$Since"
    }

    if ($Until) {
        $gitArgs += "--until=$Until"
    }

    if ($StartDate) {
        # 验证日期格式
        try {
            $startDt = [DateTimeOffset]::Parse($StartDate)
            $gitArgs += "--since=$StartDate"
        } catch {
            throw "无效的起始日期格式: $StartDate"
        }
    }

    if ($EndDate) {
        try {
            $endDt = [DateTimeOffset]::Parse($EndDate)
            $gitArgs += "--until=$EndDate"
        } catch {
            throw "无效的结束日期格式: $EndDate"
        }
    }

    if ($Author) {
        $gitArgs += "--author=$Author"
    }

    if ($Grep) {
        $gitArgs += "--grep=$Grep"
        $gitArgs += "--extended-regexp"
    }

    # 设置输出格式
    $separator = "||SEP||"
    $logFormat = "%H${separator}%ai${separator}%an${separator}%s"
    $gitArgs += "--format=$logFormat"
    $gitArgs += "--reverse"

    # 执行 git log
    # 注意：不能用数组方式传递 -c 参数，需要拼接命令字符串
    $gitLogCmd = "git -c i18n.logOutputEncoding=utf-8 log"

    if ($Branch) {
        $gitLogCmd += " $Branch"
    }

    if ($Since) {
        $gitLogCmd += " --since=`"$Since`""
    }

    if ($Until) {
        $gitLogCmd += " --until=`"$Until`""
    }

    if ($StartDate) {
        $gitLogCmd += " --since=`"$StartDate`""
    }

    if ($EndDate) {
        $gitLogCmd += " --until=`"$EndDate`""
    }

    if ($Author) {
        $gitLogCmd += " --author=`"$Author`""
    }

    if ($Grep) {
        $gitLogCmd += " --grep=`"$Grep`" --extended-regexp"
    }

    $gitLogCmd += " --format=`"$logFormat`" --reverse"

    $rawLog = Invoke-Expression $gitLogCmd

    if (-not $rawLog) {
        Write-Warning "没有找到任何符合条件的 Git 提交记录"
        exit 0
    }

    # 解析提交记录
    $allCommits = [System.Collections.ArrayList]::new()

    foreach ($line in $rawLog) {
        $parts = $line -split [regex]::Escape($separator)
        if ($parts.Count -lt 4) { continue }

        $hash      = $parts[0].Substring(0, [Math]::Min(8, $parts[0].Length))
        $fullHash  = $parts[0]
        $dateStr   = $parts[1].Trim()
        $author    = $parts[2].Trim()
        $subject   = $parts[3].Trim()

        # 解析日期
        $dateParts = $dateStr -split '[\s\-\:]'
        $yearMonth = "$($dateParts[0])-$($dateParts[1])"
        $day       = $dateParts[2]
        $time      = "$($dateParts[3]):$($dateParts[4]):$($dateParts[5])"

        [void]$allCommits.Add(@{
            Hash      = $hash
            FullHash  = $fullHash
            Date      = $dateStr
            Day       = $day
            Time      = $time
            Author    = $author
            Subject   = $subject
            YearMonth = $yearMonth
        })
    }

    $totalCount = $allCommits.Count

    # 按月份分组
    $commitsByMonth = @{}
    foreach ($c in $allCommits) {
        if (-not $commitsByMonth.ContainsKey($c.YearMonth)) {
            $commitsByMonth[$c.YearMonth] = [System.Collections.ArrayList]::new()
        }
        [void]$commitsByMonth[$c.YearMonth].Add($c)
    }

    # 生成 Markdown
    if ($OutputFile) {
        # 输出到单个文件
        $lines = [System.Collections.ArrayList]::new()

        # 标题和统计
        [void]$lines.Add("# Git 提交记录汇总")
        [void]$lines.Add("")
        [void]$lines.Add("> 自动生成于 $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')")
        if ($Since -or $StartDate) {
            $timeRange = if ($Since) { $Since } else { $StartDate }
            [void]$lines.Add("> 时间范围：从 $timeRange 开始")
        }
        if ($Until -or $EndDate) {
            $timeRange = if ($Until) { $Until } else { $EndDate }
            [void]$lines.Add("> 时间范围：到 $timeRange 结束")
        }
        if ($Author) {
            [void]$lines.Add("> 作者：$Author")
        }
        if ($Grep) {
            [void]$lines.Add("> 过滤条件：$Grep")
        }
        [void]$lines.Add("> 提交总数：$totalCount")
        [void]$lines.Add("")

        # 作者分布统计
        $byAuthor = @{}
        foreach ($c in $allCommits) {
            if (-not $byAuthor.ContainsKey($c.Author)) {
                $byAuthor[$c.Author] = 0
            }
            $byAuthor[$c.Author]++
        }

        [void]$lines.Add("## 作者分布")
        [void]$lines.Add("")
        foreach ($author in ($byAuthor.Keys | Sort-Object)) {
            [void]$lines.Add("- $author：$($byAuthor[$author]) 条")
        }
        [void]$lines.Add("")

        # 提交明细
        [void]$lines.Add("## 提交明细")
        [void]$lines.Add("")
        [void]$lines.Add("| 时间 | Hash | 作者 | 提交说明 |")
        [void]$lines.Add("|------|------|------|----------|")

        foreach ($c in $allCommits) {
            $escapedSubject = $c.Subject -replace '\|', '\|'
            [void]$lines.Add("| $($c.Date) | ``$($c.Hash)`` | $($c.Author) | $escapedSubject |")
        }
        [void]$lines.Add("")

        # 写入文件
        $resolvedOutput = if ([System.IO.Path]::IsPathRooted($OutputFile)) {
            $OutputFile
        } else {
            Join-Path $repoRoot $OutputFile
        }

        $outDir = Split-Path -Parent $resolvedOutput
        if ($outDir -and -not (Test-Path $outDir)) {
            New-Item -ItemType Directory -Path $outDir -Force | Out-Null
        }

        $utf8NoBom = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllLines($resolvedOutput, $lines.ToArray(), $utf8NoBom)

        Write-Host "✓ 已导出到：$resolvedOutput" -ForegroundColor Green
        Write-Host "  共 $totalCount 条提交" -ForegroundColor Cyan
    } else {
        # 按月份输出到多个文件
        if (-not (Test-Path $OutputDir)) {
            New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
        }

        $fileCount = 0
        foreach ($month in ($commitsByMonth.Keys | Sort-Object)) {
            $monthCommits = $commitsByMonth[$month]
            $year = $month.Substring(0, 4)
            $mon  = $month.Substring(5, 2)

            $lines = [System.Collections.ArrayList]::new()
            [void]$lines.Add("# Git 提交记录 — ${year} 年 ${mon} 月")
            [void]$lines.Add("")
            [void]$lines.Add("> 自动生成于 $(Get-Date -Format 'yyyy-MM-dd HH:mm')，共 $($monthCommits.Count) 条提交。")
            [void]$lines.Add("")

            if ($NoGroupByDay) {
                # 平铺模式
                [void]$lines.Add("| 时间 | Hash | 作者 | 提交说明 |")
                [void]$lines.Add("|------|------|------|----------|")

                foreach ($c in $monthCommits) {
                    $escapedSubject = $c.Subject -replace '\|', '\|'
                    [void]$lines.Add("| $($c.Date) | ``$($c.Hash)`` | $($c.Author) | $escapedSubject |")
                }
            } else {
                # 按日期分组
                $byDay = [ordered]@{}
                foreach ($c in $monthCommits) {
                    $dayKey = $c.Day
                    if (-not $byDay.Contains($dayKey)) {
                        $byDay[$dayKey] = [System.Collections.ArrayList]::new()
                    }
                    [void]$byDay[$dayKey].Add($c)
                }

                foreach ($day in $byDay.Keys) {
                    [void]$lines.Add("## ${month}-${day}")
                    [void]$lines.Add("")
                    [void]$lines.Add("| 时间 | Hash | 作者 | 提交说明 |")
                    [void]$lines.Add("|------|------|------|----------|")

                    foreach ($c in $byDay[$day]) {
                        $escapedSubject = $c.Subject -replace '\|', '\|'
                        [void]$lines.Add("| $($c.Time) | ``$($c.Hash)`` | $($c.Author) | $escapedSubject |")
                    }
                    [void]$lines.Add("")
                }
            }

            # 写入文件（UTF-8 无 BOM）
            $filePath = Join-Path $OutputDir "${month}.md"
            $utf8NoBom = New-Object System.Text.UTF8Encoding $false
            [System.IO.File]::WriteAllLines($filePath, $lines.ToArray(), $utf8NoBom)

            $fileCount++
            Write-Host "  ✓ ${month}.md ($($monthCommits.Count) 条提交)" -ForegroundColor Green
        }

        Write-Host ""
        Write-Host "完成：共导出 $totalCount 条提交到 $fileCount 个文件" -ForegroundColor Cyan
        Write-Host "目录：$OutputDir" -ForegroundColor Cyan
    }
} finally {
    # 恢复原始控制台编码
    [Console]::OutputEncoding = $prevOutputEncoding
}
