# Git 提交记录

此目录用于存储自动导出的 Git 提交历史记录。

## 导出方式

使用以下脚本导出提交记录：

```powershell
# 导出全部提交（按月份分组）
.\scripts\export-git-log.ps1

# 导出最近 1 周的提交到单个文件
.\scripts\export-git-log.ps1 -Since "1 week ago" -OutputFile "temp/recent.md"

# 导出指定作者的提交
.\scripts\export-git-log.ps1 -Author "AI Novel" -OutputFile "temp/author.md"

# 导出指定日期范围的提交
.\scripts\export-git-log.ps1 -StartDate "2026-07-01" -EndDate "2026-07-31"

# 过滤包含特定关键词的提交
.\scripts\export-git-log.ps1 -Grep "feat\(REQ" -OutputFile "temp/features.md"

# 不按日期分组（平铺模式）
.\scripts\export-git-log.ps1 -NoGroupByDay -OutputFile "temp/flat.md"
```

## 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-OutputDir` | 输出目录 | `docs_dev/5.git-commit` |
| `-OutputFile` | 输出单个文件（与 OutputDir 互斥） | - |
| `-StartDate` | 起始日期（ISO 8601 格式） | - |
| `-EndDate` | 结束日期 | - |
| `-Since` | Git log --since 参数 | - |
| `-Until` | Git log --until 参数 | - |
| `-Author` | 按作者过滤（部分匹配） | - |
| `-Grep` | 按提交说明过滤（正则表达式） | - |
| `-Branch` | 指定分支 | 当前分支 |
| `-NoGroupByDay` | 不按日期分组 | false |

## 文件格式

- 按月份分组时，生成 `YYYY-MM.md` 文件
- 单文件导出时，包含统计信息和明细表
- 编码：UTF-8 无 BOM
