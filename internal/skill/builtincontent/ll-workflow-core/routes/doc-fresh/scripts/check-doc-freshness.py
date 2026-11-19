#!/usr/bin/env python3
"""REQ-7210 文档保鲜扫描器 v4。

检测文档目录下过期文档（默认 docs/2.tech/，支持自定义目录）。
输出到 04-auto_audit/results/YYYY-MM-DD-NNNNNN/scan-result.json。

用法:
  # 默认扫描 docs/2.tech/
  python .claude/skills/doc-freshness-audit/scripts/check-doc-freshness.py

  # 自定义扫描目录
  python .claude/skills/doc-freshness-audit/scripts/check-doc-freshness.py --scan-dir docs/2.tech/architecture
  python .claude/skills/doc-freshness-audit/scripts/check-doc-freshness.py --scan-dir 02-dev/dev_11/docs/2.tech

  # 其他选项
  python .claude/skills/doc-freshness-audit/scripts/check-doc-freshness.py --threshold-override 24 --dry-run
  python .claude/skills/doc-freshness-audit/scripts/check-doc-freshness.py --acknowledge "2.tech/xx.md=2026-07-01"
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone, timedelta
from pathlib import Path
from typing import Any

import yaml

# ── 路径解析（从 repo_root + audit_root 推导，不再依赖 __file__）──

def _resolve_paths(repo_root: Path, audit_root_override: Path | None = None) -> dict[str, Path]:
    """解析 04-auto_audit/ 路径。优先 --audit-root，否则 repo_root/04-auto_audit/。"""
    audit_root = audit_root_override.resolve() if audit_root_override else (repo_root / "04-auto_audit")
    return {
        "audit_root": audit_root,
        "config_dir": audit_root / "config",
        "results_dir": audit_root / "results",
        "state_file": audit_root / "config" / "state.json",
        "config_file": audit_root / "config" / "document-freshness.yaml",
    }


# ── 工具函数 ────────────────────────────────────────────────────

def detect_latest_dev_version(repo_root: Path) -> str | None:
    """扫描 02-dev/ 下编号最高的 dev_XX。"""
    dev_root = repo_root / "02-dev"
    if not dev_root.is_dir():
        return None
    pattern = re.compile(r"^dev_(\d+)$")
    best_num = -1
    best_name = None
    for child in dev_root.iterdir():
        if not child.is_dir():
            continue
        m = pattern.match(child.name)
        if m and (num := int(m.group(1))) > best_num:
            best_num = num
            best_name = child.name
    return best_name


def load_config(config_path: Path) -> dict[str, Any]:
    with open(config_path, encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def match_threshold(rel_path: str, config: dict[str, Any]) -> int | None:
    """先查单文档覆盖，否则最长前缀匹配 category，返回 threshold_hours。"""
    overrides = config.get("overrides", {})
    if rel_path in overrides:
        return overrides[rel_path]
    best_prefix = ""
    best_threshold = config.get("default_threshold_hours", 168)
    for cat in config.get("categories", []):
        prefix = cat["prefix"]
        if rel_path.startswith(prefix) and len(prefix) > len(best_prefix):
            best_prefix = prefix
            best_threshold = cat["threshold_hours"]
    return best_threshold


def is_excluded(rel_path: str, config: dict[str, Any]) -> bool:
    for pattern in config.get("exclude", []):
        if _glob_match(rel_path, pattern):
            return True
    return False


def _glob_match(path: str, pattern: str) -> bool:
    if "**" not in pattern:
        return path == pattern
    suffix = pattern.replace("**/", "")
    if path.endswith(suffix) or path.endswith("/" + suffix):
        return True
    pat_parts = pattern.split("/")
    path_parts = path.split("/")
    return _match_glob_segments(path_parts, pat_parts)


def _match_glob_segments(parts: list[str], pats: list[str]) -> bool:
    pi = pp = 0
    star_last = -1
    star_pi = -1
    while pi < len(parts):
        if pp < len(pats) and (pats[pp] == "*" or pats[pp] == parts[pi]):
            pi += 1
            pp += 1
        elif pp < len(pats) and pats[pp] == "**":
            star_last = pp
            star_pi = pi
            pp += 1
        elif star_last != -1:
            pp = star_last + 1
            star_pi += 1
            pi = star_pi
        else:
            return False
    while pp < len(pats) and pats[pp] == "**":
        pp += 1
    return pp == len(pats)


# ── Git 查询 ────────────────────────────────────────────────────

def get_git_last_modified(repo_root: Path, file_path: str) -> tuple[str | None, str | None]:
    try:
        result = subprocess.run(
            ["git", "log", "--format=%aI %H", "--max-count=1", "--", file_path],
            cwd=str(repo_root),
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            parts = result.stdout.strip().split(" ", 1)
            return parts[0], (parts[1] if len(parts) > 1 else None)
    except (subprocess.TimeoutExpired, OSError):
        pass
    return None, None


# ── 文件扫描 ────────────────────────────────────────────────────

def scan_docs(docs_root: Path, repo_root: Path, config: dict[str, Any]) -> list[dict[str, Any]]:
    """扫描文档目录下的 .md 文件。

    docs_root 可以是：
    - 包含 2.tech/ 子目录的目录（如 docs/）
    - 直接是 2.tech/ 目录
    - 任意包含 .md 文件的目录
    """
    # 确定实际扫描的根目录
    if (docs_root / "2.tech").is_dir():
        scan_root = docs_root / "2.tech"
    else:
        scan_root = docs_root

    if not scan_root.is_dir():
        return []

    docs_prefix = str(scan_root).replace("\\", "/") + "/"
    files: list[dict[str, Any]] = []

    for md in sorted(scan_root.rglob("*.md")):
        abs_path = str(md)
        rel_path = abs_path.replace("\\", "/")
        if rel_path.startswith(docs_prefix):
            rel_path = rel_path[len(docs_prefix):]
        else:
            rel_path = str(md.relative_to(scan_root)).replace("\\", "/")

        if is_excluded(rel_path, config):
            continue

        last_mod, git_sha = get_git_last_modified(repo_root, abs_path)
        threshold_hours = match_threshold(rel_path, config)

        files.append({
            "rel_path": rel_path,
            "abs_path": abs_path,
            "last_modified": last_mod,
            "git_sha": git_sha or "",
            "threshold_hours": threshold_hours,
        })

    return files


# ── 状态持久化 ──────────────────────────────────────────────────

def load_state(state_file: Path) -> dict[str, Any]:
    if state_file.exists():
        try:
            with open(state_file, encoding="utf-8") as fh:
                return json.load(fh)
        except (json.JSONDecodeError, OSError):
            pass
    return {"version": 1, "last_scan": None, "entries": {}}


def save_state(state: dict[str, Any], state_file: Path) -> None:
    state_file.parent.mkdir(parents=True, exist_ok=True)
    with open(state_file, "w", encoding="utf-8") as fh:
        json.dump(state, fh, ensure_ascii=False, indent=2)


# ── 序号生成 ────────────────────────────────────────────────────

def get_next_sequence(today_str: str, results_dir: Path) -> int:
    """获取当天下一个序号。"""
    max_seq = 0
    if results_dir.is_dir():
        prefix = today_str + "-"
        for child in results_dir.iterdir():
            if child.is_dir() and child.name.startswith(prefix):
                try:
                    seq = int(child.name[len(prefix):])
                    max_seq = max(max_seq, seq)
                except ValueError:
                    pass
    return max_seq + 1


# ── 确认机制 ────────────────────────────────────────────────────

def _is_acknowledged(rel_path: str, entries: dict[str, Any], now: datetime) -> bool:
    prev = entries.get(rel_path)
    if not prev:
        return False
    if prev.get("acknowledged_permanent"):
        return True
    until = prev.get("acknowledged_until")
    if until:
        try:
            until_dt = datetime.fromisoformat(until)
            if now < until_dt:
                return True
        except (ValueError, TypeError):
            pass
    return False


# ── 自适应阈值 ──────────────────────────────────────────────────

def get_effective_threshold(
    rel_path: str, base_threshold: int, entries: dict[str, Any], config: dict[str, Any]
) -> float:
    """计算有效阈值 = base_threshold × multiplier。"""
    adaptive = config.get("adaptive", {})
    if not adaptive.get("enabled", True):
        return float(base_threshold)

    prev = entries.get(rel_path, {})
    multiplier = prev.get("multiplier", adaptive.get("base_multiplier", 1.0))
    max_mult = adaptive.get("max_multiplier", 8.0)
    return float(base_threshold) * min(multiplier, max_mult)


# ── 分类 ────────────────────────────────────────────────────────

def classify_files(
    files: list[dict[str, Any]], state: dict[str, Any], now: datetime,
    force_full: bool = False, config: dict[str, Any] | None = None,
) -> dict[str, Any]:
    stale_files = []
    current_files = []
    acknowledged_files = []
    entries = state.get("entries", {})
    cfg = config or {}

    for f in files:
        base_threshold = f["threshold_hours"]
        if base_threshold is None:
            current_files.append(f)
            continue
        if not f["last_modified"]:
            current_files.append(f)
            continue
        try:
            last_dt = datetime.fromisoformat(f["last_modified"])
        except (ValueError, TypeError):
            current_files.append(f)
            continue

        # 有效阈值 = 基准 × 乘数（force_full 时忽略乘数）
        eff_threshold = base_threshold if force_full else get_effective_threshold(
            f["rel_path"], base_threshold, entries, cfg
        )
        f["effective_threshold_hours"] = eff_threshold

        age = now - last_dt
        if age <= timedelta(hours=eff_threshold):
            current_files.append(f)
        elif _is_acknowledged(f["rel_path"], entries, now):
            acknowledged_files.append(f)
        else:
            stale_files.append(f)

    new_items = []
    recurring_items = []

    # 去重配置
    dedup_cfg = cfg.get("dedup", {})
    dedup_enabled = dedup_cfg.get("enabled", True)
    dedup_ttl = timedelta(days=dedup_cfg.get("ttl_days", 14))

    for f in stale_files:
        prev = entries.get(f["rel_path"])
        audited_sha = prev.get("audited_sha") if prev else None
        audited_at_str = prev.get("audited_at") if prev else None

        # 判断是否可以跳过：必须同时满足 ①启用去重 ②非force_full ③同SHA
        # ④TTL=0（永久）或仍在TTL窗口内
        can_skip = False
        if dedup_enabled and audited_sha and audited_sha == f["git_sha"] and not force_full:
            if dedup_ttl.total_seconds() == 0:
                can_skip = True  # ttl_days=0 → 永久跳过
            elif audited_at_str:
                try:
                    audited_at = datetime.fromisoformat(audited_at_str)
                    if now - audited_at < dedup_ttl:
                        can_skip = True
                except (ValueError, TypeError):
                    pass  # 无法解析时间，不跳过
        # dedup_enabled=False → can_skip 永远是 False，全量审计

        if can_skip:
            f["report_count"] = prev.get("report_count", 0) + 1
            f["first_reported"] = prev.get("first_reported", now.isoformat())
            recurring_items.append(f)
        else:
            f["report_count"] = 1
            f["first_reported"] = now.isoformat()
            new_items.append(f)

    return {
        "total_scanned": len(files),
        "stale_count": len(stale_files),
        "new_count": len(new_items),
        "recurring_count": len(recurring_items),
        "current_count": len(current_files),
        "acknowledged_count": len(acknowledged_files),
        "new_items": new_items,
        "recurring_items": recurring_items,
        "acknowledged_files": acknowledged_files,
    }


# ── 主入口 ──────────────────────────────────────────────────────

def main() -> int:
    parser = argparse.ArgumentParser(description="REQ-7210 文档保鲜扫描器 v3")
    parser.add_argument("--repo-root", type=Path, default=Path("."), help="仓库根目录")
    parser.add_argument("--dry-run", action="store_true", help="不更新 state.json")
    parser.add_argument("--threshold-override", type=int, default=None,
                        help="临时覆盖所有阈值（小时）")
    parser.add_argument("--acknowledge", type=str, default=None,
                        help="标记文件为已确认: rel_path=PERMANENT|YYYY-MM-DD")
    parser.add_argument("--revoke-ack", type=str, default=None,
                        help="取消文件确认: rel_path")
    parser.add_argument("--record-pass", type=str, default=None,
                        help="记录审计通过: rel_path（乘数×2）")
    parser.add_argument("--record-fail", type=str, default=None,
                        help="记录审计发现问题: rel_path（乘数重置为1.0）")
    parser.add_argument("--force-full", action="store_true",
                        help="强制全量审计（忽略自适应乘数）")
    parser.add_argument("--mark-audited", type=str, default=None,
                        help="标记文件已完成 AI 审计: rel_path（设置 audited_sha=当前 git_sha）")
    parser.add_argument("--audit-root", type=Path, default=None,
                        help="04-auto_audit/ 目录路径（默认: repo_root/04-auto_audit）")
    parser.add_argument("--output-dir", type=Path, default=None,
                        help="覆盖输出目录（用于手动指定）")
    parser.add_argument("--scan-dir", type=Path, default=None,
                        help="自定义扫描目录（相对于 repo-root，默认: docs/2.tech）")
    args = parser.parse_args()

    repo_root = args.repo_root.resolve()
    paths = _resolve_paths(repo_root, args.audit_root)
    CONFIG_FILE = paths["config_file"]
    STATE_FILE = paths["state_file"]
    RESULTS_DIR = paths["results_dir"]
    CONFIG_DIR = paths["config_dir"]

    config = load_config(CONFIG_FILE)

    # 确认/取消确认模式
    if args.acknowledge or args.revoke_ack:
        state = load_state(STATE_FILE)
        if args.acknowledge:
            parts = args.acknowledge.split("=", 1)
            rel_path = parts[0].strip()
            value = parts[1].strip() if len(parts) > 1 else "PERMANENT"
            entry = state["entries"].get(rel_path, {})
            if value.upper() == "PERMANENT":
                entry["acknowledged_permanent"] = True
                entry.pop("acknowledged_until", None)
                print(f"[OK] {rel_path} → 永久忽略")
            else:
                entry["acknowledged_until"] = value
                entry.pop("acknowledged_permanent", None)
                print(f"[OK] {rel_path} → 忽略至 {value}")
            state["entries"][rel_path] = entry
        if args.revoke_ack:
            rel_path = args.revoke_ack.strip()
            entry = state["entries"].get(rel_path, {})
            entry.pop("acknowledged_permanent", None)
            entry.pop("acknowledged_until", None)
            if entry:
                state["entries"][rel_path] = entry
            else:
                state["entries"].pop(rel_path, None)
            print(f"[OK] {rel_path} → 已取消确认")
        save_state(state, STATE_FILE)
        return 0

    # 记录审计通过/失败
    if args.record_pass or args.record_fail:
        state = load_state(STATE_FILE)
        ad = config.get("adaptive", {})
        strategy = ad.get("strategy", "progressive")
        max_mult = ad.get("max_multiplier", 4.0)
        base_mult = ad.get("base_multiplier", 1.0)
        if args.record_pass:
            rel_path = args.record_pass.strip()
            entry = state["entries"].get(rel_path, {})
            pc = entry.get("audit_pass_count", 0)
            om = entry.get("multiplier", base_mult)
            if strategy == "progressive":
                nm = ad.get("first_pass_multiplier", 2.0) if pc == 0 else om + ad.get("step_increment", 1.0)
            else:
                nm = om * ad.get("multiplier_growth", 2.0)
            nm = min(nm, max_mult)
            entry["multiplier"] = nm
            entry["audit_pass_count"] = pc + 1
            entry["last_audit_result"] = "pass"
            state["entries"][rel_path] = entry
            print(f"[OK] {rel_path} -> audit pass (x{om}->x{nm}, pass#{pc+1})")
            save_state(state, STATE_FILE)
        if args.record_fail:
            rel_path = args.record_fail.strip()
            entry = state["entries"].get(rel_path, {})
            entry["multiplier"] = base_mult
            entry["audit_pass_count"] = 0
            entry["last_audit_result"] = "fail"
            state["entries"][rel_path] = entry
            print(f"[OK] {rel_path} -> audit fail, multiplier reset to x{base_mult}")
            save_state(state, STATE_FILE)
        return 0

    # 标记审计完成
    if args.mark_audited:
        state = load_state(STATE_FILE)
        rel_path = args.mark_audited.strip()
        entry = state["entries"].get(rel_path, {})
        current_sha = entry.get("git_sha", "")
        if current_sha:
            entry["audited_sha"] = current_sha
            entry["audited_at"] = datetime.now(timezone.utc).isoformat()
            state["entries"][rel_path] = entry
            save_state(state, STATE_FILE)
            print(f"[OK] {rel_path} -> audited_sha={current_sha[:8]}")
        else:
            print(f"[WARN] {rel_path}: 无扫描记录，跳过")
        return 0

    # 覆盖阈值
    if args.threshold_override is not None:
        for cat in config.get("categories", []):
            cat["threshold_hours"] = args.threshold_override
        config["default_threshold_hours"] = args.threshold_override

    # 确定扫描目录
    if args.scan_dir:
        # 命令行参数优先
        scan_root = args.scan_dir.resolve()
        if not scan_root.is_dir():
            print(f"ERROR: 扫描目录不存在: {scan_root}", file=sys.stderr)
            return 1
        # 从 scan_root 提取 2.tech 的父目录作为 docs_root
        if scan_root.name == "2.tech":
            docs_root = scan_root.parent
        elif (scan_root / "2.tech").is_dir():
            docs_root = scan_root
        else:
            docs_root = scan_root
        dev_version = f"custom ({scan_root.name})"
    else:
        # 读取配置文件中的 scan_path
        config_scan_path = config.get("scan_path", "docs/2.tech")
        docs_root = repo_root / config_scan_path

        if not docs_root.is_dir():
            # 回退到 02-dev/dev_XX/docs/2.tech/
            dev_version = detect_latest_dev_version(repo_root)
            if not dev_version:
                print(f"ERROR: 扫描目录不存在: {docs_root}，且未找到 dev_XX 目录", file=sys.stderr)
                return 1
            docs_root = repo_root / "02-dev" / dev_version / "docs"
        else:
            dev_version = "root"

    tech_root = docs_root / "2.tech" if (docs_root / "2.tech").is_dir() else docs_root
    if not tech_root.is_dir():
        print(f"ERROR: 扫描目录不存在: {tech_root}", file=sys.stderr)
        return 1

    now = datetime.now(timezone.utc)
    today_str = now.strftime("%Y-%m-%d")
    seq = get_next_sequence(today_str, RESULTS_DIR)
    run_dir_name = f"{today_str}-{seq:06d}"

    if args.output_dir:
        run_dir = args.output_dir
    else:
        run_dir = RESULTS_DIR / run_dir_name

    run_dir.mkdir(parents=True, exist_ok=True)
    print(f"[{dev_version}] 扫描中... → {run_dir}", file=sys.stderr)

    files = scan_docs(docs_root, repo_root, config)
    state = load_state(STATE_FILE)

    result = classify_files(files, state, now)

    # 更新 state
    if not args.dry_run:
        state["last_scan"] = now.isoformat()
        stale_paths = set()
        for f in result["new_items"] + result["recurring_items"]:
            stale_paths.add(f["rel_path"])
            old_entry = state["entries"].get(f["rel_path"], {})
            state["entries"][f["rel_path"]] = {
                "last_modified": f["last_modified"],
                "git_sha": f["git_sha"],
                "audited_sha": old_entry.get("audited_sha"),
                "audited_at": old_entry.get("audited_at"),
                "first_reported": f.get("first_reported", now.isoformat()),
                "report_count": f.get("report_count", 1),
                "acknowledged_permanent": old_entry.get("acknowledged_permanent", False),
                "acknowledged_until": old_entry.get("acknowledged_until"),
            }
        save_state(state, STATE_FILE)

    # 构建输出
    max_files = config.get("audit", {}).get("max_files_per_run", 8)
    # 仅审计新/变更文档；同 SHA 重复项跳过（内容未变，上次审计结果仍有效）
    # --force-full 时一并审计重复项
    if args.force_full:
        combined = result["new_items"] + result["recurring_items"]
    else:
        combined = result["new_items"]
    audit_files = combined if max_files == 0 else combined[:max_files]

    def _build_entry(f: dict[str, Any]) -> dict[str, Any]:
        entry: dict[str, Any] = {
            "rel_path": f["rel_path"],
            "last_modified": f["last_modified"],
            "git_sha": f["git_sha"],
            "threshold_hours": f["threshold_hours"],
            "report_count": f.get("report_count", 1),
        }
        if f.get("last_modified"):
            try:
                last_dt = datetime.fromisoformat(f["last_modified"])
                entry["age_hours"] = round((now - last_dt).total_seconds() / 3600, 1)
            except (ValueError, TypeError):
                entry["age_hours"] = None
        return entry

    output = {
        "dev_version": dev_version,
        "scan_time": now.isoformat(),
        "run_dir": str(run_dir),
        "summary": {
            "total_scanned": result["total_scanned"],
            "stale": result["stale_count"],
            "new": result["new_count"],
            "recurring": result["recurring_count"],
            "recurring_skipped": 0 if args.force_full else result["recurring_count"],
            "current": result["current_count"],
        },
        "audit_files": [_build_entry(f) for f in audit_files],
        "all_stale": [
            {"rel_path": f["rel_path"], "age_hours": _build_entry(f).get("age_hours")}
            for f in result["new_items"] + result["recurring_items"]
        ],
    }

    # 写入 scan-result.json
    scan_result_path = run_dir / "scan-result.json"
    output_json = json.dumps(output, ensure_ascii=False, indent=2)
    with open(scan_result_path, "w", encoding="utf-8") as fh:
        fh.write(output_json)

    print(output_json)
    return 0


if __name__ == "__main__":
    sys.exit(main())
