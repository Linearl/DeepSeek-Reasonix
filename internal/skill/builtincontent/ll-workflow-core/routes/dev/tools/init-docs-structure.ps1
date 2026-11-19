<#!
.SYNOPSIS
  初始化目标工作目录下的编号化 docs 结构。

.DESCRIPTION
  用于 ll-develop 的 A.1.2 阶段：
  - 创建 docs 最小目录骨架
  - 创建 version 相关目录（0.version_plan/plan/{version}, 1.task/B.todo/{version}, 1.task/B.2.done/{version}）
  - 生成版本计划模板与专题任务模板
  - 生成 docs/README.md 与 docs/INDEX.md（若缺失）

.PARAMETER TargetDevDir
  目标工作目录（例如 workspace/dev_09 或 release/v09）。

.PARAMETER Version
  两位版本号（例如 09）。可选；未传时尝试从目录名中的版本号推导，失败则使用 00。

.PARAMETER PlanLayers
  req/plan 下预创建的层级目录。默认：03,04,05,06。

.PARAMETER Force
  覆盖已存在的模板文件。

.EXAMPLE
  .\tools\init-docs-structure.ps1 -TargetDevDir "workspace/dev_09"

.EXAMPLE
  .\tools\init-docs-structure.ps1 -TargetDevDir "release/v09" -Version "09" -Force
#>
param(
  [Parameter(Mandatory = $true)]
  [string]$TargetDevDir,

  [string]$Version = "",

  [string[]]$PlanLayers = @("03", "04", "05", "06"),

  [switch]$Force
)

$ErrorActionPreference = "Stop"

function New-DirectoryIfMissing {
  param([Parameter(Mandatory = $true)][string]$Path)

  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path -Force | Out-Null
    return $true
  }

  return $false
}

function Write-Utf8NoBom {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)][string]$Content
  )

  $dir = Split-Path -Parent $Path
  if ($dir) {
    New-DirectoryIfMissing -Path $dir | Out-Null
  }

  $utf8NoBom = New-Object System.Text.UTF8Encoding $false
  [System.IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

function Convert-TemplateText {
  param(
    [Parameter(Mandatory = $true)][string]$TemplateText,
    [Parameter(Mandatory = $true)][string]$VersionValue,
    [Parameter(Mandatory = $true)][string]$DateValue,
    [Parameter(Mandatory = $true)][string]$OwnerValue
  )

  $rendered = $TemplateText.Replace("__VERSION__", $VersionValue)
  $rendered = $rendered.Replace("__DATE__", $DateValue)
  $rendered = $rendered.Replace("__OWNER__", $OwnerValue)
  $rendered = $rendered.Replace("__TOPIC_SLUG__", ("dev_{0}-topic-sample" -f $VersionValue))
  $rendered = $rendered.Replace("__TOPIC_NAME__", ("dev_{0} 示例专题" -f $VersionValue))

  return $rendered
}

$targetDevPath = if ([System.IO.Path]::IsPathRooted($TargetDevDir)) {
  [System.IO.Path]::GetFullPath($TargetDevDir)
} else {
  [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $TargetDevDir))
}

$leafName = Split-Path -Leaf $targetDevPath
if (-not $Version) {
  if ($leafName -match '^(?:dev_|v)?(\d+)$') {
    $Version = $Matches[1]
  } elseif ($leafName -match '(\d{2,})') {
    $Version = $Matches[1]
  } else {
    $Version = "00"
  }
}

if ($Version -match '^\d+$') {
  $Version = $Version.PadLeft(2, '0')
}

$skillRoot = Split-Path -Parent $PSScriptRoot
$templateRoot = Join-Path $skillRoot "templates"
$versionTemplatePath = Join-Path $templateRoot "version-plan-template.md"
$topicTemplatePath = Join-Path $templateRoot "topic-task-template.md"

$docsRoot = Join-Path $targetDevPath "docs"

$createdDirs = [System.Collections.ArrayList]::new()
$generatedFiles = [System.Collections.ArrayList]::new()

New-DirectoryIfMissing -Path $targetDevPath | Out-Null
if (New-DirectoryIfMissing -Path $docsRoot) {
  [void]$createdDirs.Add($docsRoot)
}

# 1) 创建 docs 目录骨架（编号化结构）
$docsDirs = @(
  "0.version_plan",
  "0.version_plan/plan",
  "0.version_plan/template",
  "1.task",
  "1.task/A.inactive",
  "1.task/B.todo",
  "1.task/B.2.done",
  "1.task/template",
  "2.tech",
  "2.tech/architecture",
  "2.tech/api",
  "2.tech/guide",
  "2.tech/development",
  "2.tech/template",
  "3.analysis",
  "3.analysis/evidence",
  "3.analysis/diagnosis",
  "3.analysis/report",
  "4.misc",
  "4.misc/references",
  "5.git-commit",
  "6.changelog",
  "7.weekly",
  "8.test",
  "8.test/case",
  "8.test/scripts",
  "8.test/template"
)

foreach ($relative in $docsDirs) {
  $targetDir = Join-Path $docsRoot $relative
  if (New-DirectoryIfMissing -Path $targetDir) {
    [void]$createdDirs.Add($targetDir)
  }
}

# 2) 版本相关目录
$doneVersionDir = Join-Path $docsRoot (Join-Path "1.task/B.2.done" $Version)
if (New-DirectoryIfMissing -Path $doneVersionDir) {
  [void]$createdDirs.Add($doneVersionDir)
}

$todoVersionDir = Join-Path $docsRoot (Join-Path "1.task/B.todo" $Version)
if (New-DirectoryIfMissing -Path $todoVersionDir) {
  [void]$createdDirs.Add($todoVersionDir)
}

$planVersionDir = Join-Path $docsRoot (Join-Path "0.version_plan/plan" $Version)
if (New-DirectoryIfMissing -Path $planVersionDir) {
  [void]$createdDirs.Add($planVersionDir)
}

foreach ($layer in $PlanLayers) {
  if (-not ($layer -match '^\d+$')) { continue }
  $layerDir = Join-Path $docsRoot (Join-Path "0.version_plan/plan" $layer)
  if (New-DirectoryIfMissing -Path $layerDir) {
    [void]$createdDirs.Add($layerDir)
  }
}

# 3) 生成模板文件
$today = Get-Date -Format "yyyy-MM-dd"
$owner = if ($env:USERNAME) { $env:USERNAME } else { "TBD" }

if (Test-Path -LiteralPath $versionTemplatePath) {
  $planTargetPath = Join-Path $planVersionDir ("dev_{0}_plan.md" -f $Version)
  if ($Force -or -not (Test-Path -LiteralPath $planTargetPath)) {
    $planTemplateText = Get-Content -LiteralPath $versionTemplatePath -Raw -Encoding UTF8
    $planContent = Convert-TemplateText -TemplateText $planTemplateText -VersionValue $Version -DateValue $today -OwnerValue $owner
    Write-Utf8NoBom -Path $planTargetPath -Content $planContent
    [void]$generatedFiles.Add($planTargetPath)
  }
}

if (Test-Path -LiteralPath $topicTemplatePath) {
  $topicTargetPath = Join-Path $docsRoot "1.task/template/topic-task-template.md"
  if ($Force -or -not (Test-Path -LiteralPath $topicTargetPath)) {
    $topicTemplateText = Get-Content -LiteralPath $topicTemplatePath -Raw -Encoding UTF8
    $topicContent = Convert-TemplateText -TemplateText $topicTemplateText -VersionValue $Version -DateValue $today -OwnerValue $owner
    Write-Utf8NoBom -Path $topicTargetPath -Content $topicContent
    [void]$generatedFiles.Add($topicTargetPath)
  }
}

# 4) docs 根说明文件
$docsReadmePath = Join-Path $docsRoot "README.md"
if ($Force -or -not (Test-Path -LiteralPath $docsReadmePath)) {
  $docsReadme = @"
# docs 目录说明

该目录由 ll-workflow-core / dev route 的初始化脚本生成（或补齐）最小结构。

- 当前版本：dev_$Version
- 初始化时间：$(Get-Date -Format "yyyy-MM-dd HH:mm:ss")

建议先完成：

1. docs/0.version_plan/plan/$Version/dev_${Version}_plan.md
2. docs/1.task/template/topic-task-template.md（复制后创建第一个专题任务包）
3. docs/INDEX.md（按目录实际内容补齐索引入口）
"@
  Write-Utf8NoBom -Path $docsReadmePath -Content $docsReadme
  [void]$generatedFiles.Add($docsReadmePath)
}

$docsIndexPath = Join-Path $docsRoot "INDEX.md"
if ($Force -or -not (Test-Path -LiteralPath $docsIndexPath)) {
  $docsIndex = @"
# docs 索引

  - 0.version_plan/
  - 1.task/
  - 2.tech/
  - 3.analysis/
  - 4.misc/
  - 5.git-commit/
  - 6.changelog/
  - 7.weekly/
  - 8.test/
"@
  Write-Utf8NoBom -Path $docsIndexPath -Content $docsIndex
  [void]$generatedFiles.Add($docsIndexPath)
}

Write-Host "docs structure initialization completed." -ForegroundColor Green
Write-Host "  targetDevDir: $targetDevPath"
Write-Host "  version:      $Version"
Write-Host "  docsRoot:     $docsRoot"

if ($createdDirs.Count -gt 0) {
  Write-Host "`n[Created Directories]" -ForegroundColor Cyan
  foreach ($d in $createdDirs) {
    Write-Host "- $d"
  }
}

if ($generatedFiles.Count -gt 0) {
  Write-Host "`n[Generated Files]" -ForegroundColor Cyan
  foreach ($f in $generatedFiles) {
    Write-Host "- $f"
  }
}

Write-Host "`nNext: complete version plan and at least one topic task doc before entering A.2." -ForegroundColor Yellow
