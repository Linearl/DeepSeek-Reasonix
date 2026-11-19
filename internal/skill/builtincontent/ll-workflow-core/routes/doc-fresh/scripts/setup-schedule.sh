#!/usr/bin/env bash
# REQ-7210 文档保鲜系统——创建每日定时扫描任务（Linux cron）
# 用法: ./setup-schedule.sh [-r REPO_ROOT] [-a AUDIT_ROOT] [-t HH:MM] [-n TASK_NAME]
set -euo pipefail

REPO_ROOT="."
AUDIT_ROOT=""
TASK_NAME="DocFreshnessAudit"
TIME="02:00"
PYTHON_EXE="python3"
DRY_RUN=false

usage() {
    cat << 'EOF'
创建文档保鲜定时扫描任务（Linux cron）

用法:
  ./setup-schedule.sh [选项]

选项:
  -r PATH   项目根目录路径（默认: .）
  -a PATH   04-auto_audit/ 目录路径（默认: REPO_ROOT/04-auto_audit/）
  -t HH:MM  每日执行时间（默认: 02:00）
  -n NAME   定时任务标识名（默认: DocFreshnessAudit）
  --dry-run 仅打印将要添加的 crontab 条目，不实际写入
  -h        显示此帮助

示例:
  ./setup-schedule.sh
  ./setup-schedule.sh -r /opt/my-project -t 03:00
  ./setup-schedule.sh -n MyAudit --dry-run
EOF
    exit 0
}

# Parse args
while [[ $# -gt 0 ]]; do
    case "$1" in
        -r) REPO_ROOT="$2"; shift 2 ;;
        -a) AUDIT_ROOT="$2"; shift 2 ;;
        -t) TIME="$2"; shift 2 ;;
        -n) TASK_NAME="$2"; shift 2 ;;
        --dry-run) DRY_RUN=true; shift ;;
        -h|--help) usage ;;
        *) echo "未知参数: $1"; usage ;;
    esac
done

# Resolve paths
REPO_PATH="$(cd "$REPO_ROOT" && pwd)"
if [[ -z "$AUDIT_ROOT" ]]; then
    AUDIT_ROOT="$REPO_PATH/04-auto_audit"
fi

if [[ ! -d "$AUDIT_ROOT" ]]; then
    echo "错误: 审计目录不存在: $AUDIT_ROOT"
    exit 1
fi

SCANNER="$AUDIT_ROOT/scripts/check-doc-freshness.py"
if [[ ! -f "$SCANNER" ]]; then
    echo "错误: 扫描脚本不存在: $SCANNER"
    exit 1
fi

# Validate time
if [[ ! "$TIME" =~ ^[0-9]{2}:[0-9]{2}$ ]]; then
    echo "错误: 时间格式无效: $TIME（应为 HH:MM）"
    exit 1
fi

HOUR="${TIME%%:*}"
MINUTE="${TIME##*:}"
CRON_ENTRY="$MINUTE $HOUR * * * cd \"$REPO_PATH\" && $PYTHON_EXE \"$SCANNER\" --repo-root \"$REPO_PATH\" --audit-root \"$AUDIT_ROOT\" # $TASK_NAME"

echo "创建定时任务..."
echo "  项目根:   $REPO_PATH"
echo "  审计目录: $AUDIT_ROOT"
echo "  标识名:   $TASK_NAME"
echo "  执行时间: 每日 $TIME"
echo "  cron条目: $CRON_ENTRY"

if $DRY_RUN; then
    echo ""
    echo "（dry-run 模式，未实际写入 crontab）"
    exit 0
fi

# Remove existing entry if present
TMPFILE="$(mktemp)"
crontab -l 2>/dev/null | grep -v "# $TASK_NAME" > "$TMPFILE" || true
echo "$CRON_ENTRY" >> "$TMPFILE"
crontab "$TMPFILE"
rm -f "$TMPFILE"

echo ""
echo "任务创建成功"
echo "  验证: ./verify-schedule.sh -n $TASK_NAME"
