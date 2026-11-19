#!/usr/bin/env bash
# REQ-7210 文档保鲜系统——验证定时任务状态（Linux cron）
# 用法: ./verify-schedule.sh [-n TASK_NAME]
set -euo pipefail

TASK_NAME="DocFreshnessAudit"

usage() {
    cat << 'EOF'
验证文档保鲜定时任务状态（Linux cron）

用法:
  ./verify-schedule.sh [选项]

选项:
  -n NAME   定时任务标识名（默认: DocFreshnessAudit）
  -h        显示此帮助

示例:
  ./verify-schedule.sh
  ./verify-schedule.sh -n MyAudit
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -n) TASK_NAME="$2"; shift 2 ;;
        -h|--help) usage ;;
        *) echo "未知参数: $1"; usage ;;
    esac
done

echo "定时任务状态"
echo "--------------------------------------------------"
printf "%-16s: %s\n" "标识名" "$TASK_NAME"

ENTRY="$(crontab -l 2>/dev/null | grep "# $TASK_NAME" || true)"

if [[ -z "$ENTRY" ]]; then
    echo ""
    echo "任务未找到: $TASK_NAME"
    echo "  使用 ./setup-schedule.sh 创建任务"
    exit 1
fi

printf "%-16s: %s\n" "cron条目" "$ENTRY"

# Parse schedule from entry
SCHEDULE="$(echo "$ENTRY" | awk '{print $1, $2, $3, $4, $5}')"
printf "%-16s: %s\n" "调度" "$SCHEDULE"

# Check if cron service is running
if command -v systemctl &>/dev/null; then
    if systemctl is-active --quiet cron 2>/dev/null || systemctl is-active --quiet crond 2>/dev/null; then
        printf "%-16s: %s\n" "cron服务" "运行中"
    else
        printf "%-16s: %s\n" "cron服务" "未运行（任务不会执行！）"
    fi
elif pgrep -x cron >/dev/null 2>&1 || pgrep -x crond >/dev/null 2>&1; then
    printf "%-16s: %s\n" "cron服务" "运行中"
else
    printf "%-16s: %s\n" "cron服务" "未知（无法检测）"
fi

echo "--------------------------------------------------"
echo "任务状态正常"

exit 0
