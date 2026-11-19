#!/bin/sh
# ll-workflow-core: auto-sync
# 由 ll-workflow-core init 路由落盘；每次提交时自动同步治理索引。
# - docs/ 下 Markdown 改动 → 重生成 docs/INDEX.md
# - docs/1.task/ 下任务包改动 → req-sync 同步 docs/1.task/requirements.md
# 幂等：可重复追加/覆盖安装，均以本文件为唯一事实源。

case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*)
    # Windows 环境：脚本安装在 %APPDATA%\reasonix\skills\ll-workflow-core
    SKILL_ROOT="${APPDATA:-$USERPROFILE/AppData/Roaming}/reasonix/skills/ll-workflow-core/templates/scripts"
    ;;
  *)
    SKILL_ROOT="${HOME}/.reasonix/skills/ll-workflow-core/templates/scripts"
    ;;
esac

md_changed=$(git diff --cached --name-only | grep -E '^docs/.*\.md$' || true)
task_changed=$(git diff --cached --name-only | grep -E '^docs/1\.task/' || true)

if [ -n "$md_changed" ]; then
  if [ -f "$SKILL_ROOT/generate-docs-index.js" ]; then
    node "$SKILL_ROOT/generate-docs-index.js" ./docs --apply >/dev/null 2>&1
    git add docs/INDEX.md
  fi
fi

if [ -n "$task_changed" ]; then
  if [ -f "$SKILL_ROOT/req-sync.js" ]; then
    (cd docs/1.task && node "$SKILL_ROOT/req-sync.js" sync --docs .. --apply >/dev/null 2>&1)
    git add docs/1.task/requirements.md
  fi
fi

exit 0
