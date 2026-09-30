#!/usr/bin/env bash
# switch-version.sh — 把 Reasonix 的 NSIS 安装包拆成"版本目录三件套"，为快速切换版本做准备。
#
# 本脚本只做前半段（解包 + 铺设），因为真正的切换必须由 Reasonix 会话里的
# restart_update 工具完成（它要改写 current.json 指针并重启应用）。
#
# 用法：
#   switch-version.sh --installer <安装包.exe> [--version v1.38.3-20260930-1520]
#                     [--root <安装根目录>] [--force] [--keep-tmp]
#
#   --installer  安装包路径；省略时自动在 <root>/versions/*/ 里找最新的 *installer*.exe
#   --version    目标版本目录名（默认取安装包内 CLI 的版本串，如 1.38.3-20260930-1520 → v1.38.3-20260930-1520）
#   --root       安装根目录（含 current.json 与 versions/ 的那层）；省略时从正在运行的 reasonix-desktop 推断
#   --force      目标版本目录已存在时允许覆盖（默认拒绝，防止覆盖正在运行的版本）
#   --keep-tmp   保留临时解包目录以便排查
#
# 环境变量：
#   SEVEN_ZIP    7-Zip 可执行文件路径（默认 "C:/Program Files/7-Zip/7z.exe"）
set -euo pipefail

SEVEN_ZIP="${SEVEN_ZIP:-/c/Program Files/7-Zip/7z.exe}"
REQUIRED_MEMBERS=(reasonix-desktop.exe reasonix-cli.exe reasonix-update-helper.exe)

INSTALLER=""
VERSION=""
ROOT=""
FORCE=0
KEEP_TMP=0

die() { printf '错误: %s\n' "$*" >&2; exit 1; }
info() { printf '%s\n' "$*"; }

usage() {
  # 打印文件开头的连续注释块（去掉 #），遇到第一行非注释即停
  awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --installer) INSTALLER="${2:-}"; shift 2 ;;
    --version)   VERSION="${2:-}";   shift 2 ;;
    --root)      ROOT="${2:-}";      shift 2 ;;
    --force)     FORCE=1;            shift ;;
    --keep-tmp)  KEEP_TMP=1;         shift ;;
    -h|--help)   usage; exit 0 ;;
    *)           die "未知参数: $1（用 --help 看用法）" ;;
  esac
done

[ -x "$SEVEN_ZIP" ] || die "找不到 7-Zip：$SEVEN_ZIP（可用环境变量 SEVEN_ZIP 指定）"

# 从正在运行的桌面进程推断安装根目录（进程路径形如 <root>/versions/<ver>/reasonix-desktop.exe）
detect_root() {
  local winpath unix_path
  winpath="$(powershell -NoProfile -Command \
    "(Get-Process -Name reasonix-desktop -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty Path)" \
    2>/dev/null | tr -d '\r')" || true
  [ -n "${winpath:-}" ] || return 1
  unix_path="$(cygpath -u "$winpath" 2>/dev/null)" || return 1
  dirname "$(dirname "$unix_path")"
}

if [ -z "$ROOT" ]; then
  ROOT="$(detect_root)" || die "无法推断安装根目录（Reasonix 桌面版没在运行？），请用 --root 指定"
fi
[ -d "$ROOT" ] || die "安装根目录不存在：$ROOT"
[ -f "$ROOT/current.json" ] || die "$ROOT 下没有 current.json，这不像 Reasonix 安装根目录"
VERSIONS_DIR="$ROOT/versions"
[ -d "$VERSIONS_DIR" ] || die "$ROOT 下没有 versions/ 目录"

# 定位安装包
if [ -z "$INSTALLER" ]; then
  INSTALLER="$(ls -t "$VERSIONS_DIR"/*/*installer*.exe 2>/dev/null | head -1 || true)"
  [ -n "$INSTALLER" ] || die "没找到安装包，请用 --installer 指定"
  info "自动选中安装包: $INSTALLER"
fi
[ -f "$INSTALLER" ] || die "安装包不存在：$INSTALLER"

# 解包到临时目录
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-unpack.XXXXXX")"
cleanup() { [ "$KEEP_TMP" = 1 ] || rm -rf "$TMP_DIR"; }
trap cleanup EXIT

info "解包到 $TMP_DIR ..."
"$SEVEN_ZIP" x -y -o"$TMP_DIR" "$INSTALLER" >/dev/null || die "7-Zip 解包失败"

for member in "${REQUIRED_MEMBERS[@]}"; do
  [ -f "$TMP_DIR/$member" ] || die "安装包里缺少 $member（解包结果不完整）"
done

# 读版本串：reasonix 1.38.3-20260930-1520 → 1.38.3-20260930-1520
RAW_VERSION="$("$TMP_DIR/reasonix-cli.exe" --version 2>/dev/null | head -1 | tr -d '\r')"
PRODUCT_VERSION="$(printf '%s' "$RAW_VERSION" | awk '{print $2}')"
[ -n "$PRODUCT_VERSION" ] || die "无法从安装包内的 CLI 读出版本串（--version 输出: ${RAW_VERSION:-空}）"
info "安装包内版本: $RAW_VERSION"

if [ -z "$VERSION" ]; then
  VERSION="v$PRODUCT_VERSION"
fi
printf '%s' "$VERSION" | grep -Eq '^v[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.-]+)?$' \
  || die "版本目录名不合法：$VERSION（需形如 v1.38.3 或 v1.38.3-20260930-1520）"

TARGET_DIR="$VERSIONS_DIR/$VERSION"
if [ -d "$TARGET_DIR" ] && [ -n "$(ls -A "$TARGET_DIR" 2>/dev/null)" ] && [ "$FORCE" != 1 ]; then
  die "目标目录已存在且非空：$TARGET_DIR（确认可覆盖后加 --force）"
fi
mkdir -p "$TARGET_DIR"

info "铺设三件套到 $TARGET_DIR ..."
for member in "${REQUIRED_MEMBERS[@]}"; do
  cp -f "$TMP_DIR/$member" "$TARGET_DIR/$member"
done

# 校验：md5 必须与解包源一致
for member in "${REQUIRED_MEMBERS[@]}"; do
  src_hash="$(md5sum "$TMP_DIR/$member" | awk '{print $1}')"
  dst_hash="$(md5sum "$TARGET_DIR/$member" | awk '{print $1}')"
  [ "$src_hash" = "$dst_hash" ] || die "$member 拷贝后 md5 不一致（$src_hash != $dst_hash）"
done
info "三件套已就位，md5 校验通过"

cat <<EOF

下一步（必须在 Reasonix 会话里用 restart_update 工具做，脚本无法代替）：
  1. restart_update list_versions            # 确认 $VERSION 变成 healthy
  2. restart_update set_target($VERSION)
  3. restart_update execute                  # 写完 current.json 指针并重启应用

回滚：restart_update set_target(<旧版本名>) + execute（旧版本目录不会被自动清理）
EOF
