---
description: "文档保鲜审计——脚本使用说明（check-doc-freshness.py / setup-schedule / verify-schedule）"
update_time: 2026-07-01
---
# 脚本使用说明

## check-doc-freshness.py

文档保鲜扫描器——扫描最新版本过期文档。

```bash
# 基础扫描
python check-doc-freshness.py --repo-root <项目根>

# 查看帮助
python check-doc-freshness.py --help

# 常用参数
python check-doc-freshness.py \
  --repo-root "c:/path/to/project" \        # 项目根（必填）
  --audit-root "c:/path/to/04-auto_audit" \  # 审计目录（默认: repo_root/04-auto_audit/）
  --threshold-override 24 \                   # 临时覆盖阈值（小时）
  --force-full \                              # 强制全量审计（忽略去重）
  --dry-run                                   # 不更新 state.json
```

### 管理模式

```bash
# 永久忽略某文档
python check-doc-freshness.py --acknowledge "2.tech/xx.md=PERMANENT"

# 期限内忽略
python check-doc-freshness.py --acknowledge "2.tech/xx.md=2026-07-01"

# 取消忽略
python check-doc-freshness.py --revoke-ack "2.tech/xx.md"

# 记录审计通过（影响自适应乘数）
python check-doc-freshness.py --record-pass "2.tech/xx.md"

# 记录审计发现问题
python check-doc-freshness.py --record-fail "2.tech/xx.md"

# 标记审计完成（去重用）
python check-doc-freshness.py --mark-audited "2.tech/xx.md"
```

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `--repo-root` | Path | `.` | 项目根目录 |
| `--audit-root` | Path | `repo_root/04-auto_audit/` | 审计目录 |
| `--threshold-override` | int | - | 临时覆盖所有阈值（小时） |
| `--force-full` | flag | false | 强制全量审计，忽略去重 |
| `--dry-run` | flag | false | 不更新 state.json |
| `--output-dir` | Path | - | 覆盖输出目录 |
| `--acknowledge` | str | - | 标记文件为已确认 |
| `--revoke-ack` | str | - | 取消文件确认 |
| `--record-pass` | str | - | 记录审计通过 |
| `--record-fail` | str | - | 记录审计发现问题 |
| `--mark-audited` | str | - | 标记审计完成 |

---

## setup-schedule.ps1

创建 Windows 定时任务。

```powershell
# 默认配置
.\setup-schedule.ps1

# 查看帮助
Get-Help .\setup-schedule.ps1 -Detailed

# 自定义
.\setup-schedule.ps1 -RepoRoot "D:\my-project" -Time "03:00" -TaskName "MyAudit"
```

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-RepoRoot` | string | `.` | 项目根目录 |
| `-AuditRoot` | string | `RepoRoot/04-auto_audit/` | 审计目录 |
| `-TaskName` | string | `DocFreshnessAudit` | 任务名称 |
| `-Time` | string | `02:00` | 每日执行时间（HH:MM） |
| `-PythonExe` | string | `python` | Python 路径 |

---

## verify-schedule.ps1

验证定时任务状态。

```powershell
# 默认任务
.\verify-schedule.ps1

# 指定任务名
.\verify-schedule.ps1 -TaskName "MyAudit"

# 查看帮助
Get-Help .\verify-schedule.ps1
```

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `-TaskName` | string | `DocFreshnessAudit` | 任务名称 |

退出码：0 = 正常，1 = 任务不存在，2 = 状态异常
