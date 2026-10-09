#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""管理会话违规自检 (SKILL.md §4c).

用途
----
ll-iteration-parallel-dev 的铁律 §0.1 要求「管理对话不介入具体开发」。
本脚本把这条从「自觉」变成「可测」：统计指定会话的**变更类工具调用**次数。
管理会话的正确值应为 0（`todo_write` 与只读命令不计）。

判据来源（已对真实会话日志核实，非启发式）
------------------------------------------
reasonix 会话 jsonl 的 `role:"assistant"` 记录里，每个 tool_call 都带
`tool_recovery.read_only`——**由应用自己按命令内容判定**：

    {"role":"assistant","tool_calls":[
       {"id":"call_...","name":"read_file",
        "arguments":"{...}",
        "tool_recovery":{"state":"completed","read_only":true,...}}]}

实测分布（20260923-165725-collab.jsonl，2195 次工具调用）：
    bash       read_only=False   x595      <- 变更
    read_file  read_only=True    x535      <- 只读
    bash       read_only=True    x369      <- 只读  ← bash 两类都有，应用已替我们分好类
    edit_file  read_only=False   x198      <- 变更
    todo_write read_only=True    x 62
    write_file read_only=False   x 31
    use_capability read_only=False x 30

`read_only` 是**布尔值**（不是字符串）。另有约 17% 的调用 `tool_recovery` 为 `null`
（369/2195），此时回退到工具名白名单；`bash` 这类双用途工具若缺字段，默认计为
「未知」而不是猜——可用 --deep 查看其命令原文。

用法
----
    python session_violation_scan.py --session <管理会话名或jsonl路径>
    python session_violation_scan.py --list-sessions
    python session_violation_scan.py --session <名> --deep        # 打印命令原文（截断）
    python session_violation_scan.py --session <名> --json

退出码：0 = 干净；1 = 发现变更类调用（越界）；2 = 找不到会话或读取出错。
"""

from __future__ import annotations

import argparse
import collections
import datetime
import json
import os
import re
import sys
from pathlib import Path

try:  # Windows 控制台中文输出兜底
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:  # pragma: no cover
    pass

# ── 工具分类（read_only 缺失时的回退） ────────────────────────────────────────

MUTATING_TOOLS = {
    "write_file", "edit_file", "Edit", "MultiEdit", "str_replace", "str_replace_editor",
    "apply_patch", "notebook_edit", "delete_file", "move_file", "create_file",
}

READONLY_TOOLS = {
    "read_file", "bash_output", "todo_write", "ls", "glob", "grep", "search",
    "web_fetch", "web_search", "get_session_status", "read_session_tail",
    "list_addressable_sessions", "read_session", "get_goal", "wait",
}

# 双用途：read_only 字段缺失时不猜，计为 unknown（可用 --deep 看命令）
AMBIGUOUS_TOOLS = {"bash", "pwsh", "shell", "run_code", "use_capability", "tool_recovery"}

# bash 命令的变更特征（仅在 --deep 且 read_only 缺失时使用）
BASH_MUTATE_PATTERNS = [
    (r"(?<![0-9&])>>?\s*[^&\s|]", "输出重定向写文件"),
    (r"\bgit\s+(add|commit|push|merge|rebase|reset|apply|stash|clean|checkout|switch|tag|rm)\b", "git 写操作"),
    (r"\bgit\s+worktree\s+(add|remove|prune)\b", "git worktree 变更"),
    (r"\bsed\s+-i", "sed 原地编辑"),
    (r"\bperl\s+-i", "perl 原地编辑"),
    (r"\b(rm|rmdir|mv|cp|mkdir|touch|truncate|ln)\b", "文件系统变更"),
    (r"\b(del|erase|copy|move|ren|md|rd)\b", "Windows 文件系统变更"),
    (r"\b(Remove-Item|New-Item|Set-Content|Add-Content|Out-File|Clear-Content)\b", "PowerShell 写文件"),
    (r"\btee\b", "tee 写文件"),
    (r"\b(npm|pnpm|yarn|bun)\s+(install|add|remove|i)\b", "包管理器写操作"),
    (r"\bgo\s+(generate|mod\s+(tidy|download))\b", "依赖/生成物变更"),
    (r"\bpip\s+install\b", "pip 安装"),
    (r"python[0-9.]*\s+-c\s+.*(open\s*\(.*['\"][wa]|\.write\s*\()", "python 写文件"),
    (r"<<-?\s*['\"]?\w+", "heredoc 写文件"),
]

BASH_BUILD_PATTERNS = [
    (r"\bgo\s+(build|test|vet)\b", "go 构建/测试"),
    (r"\b(npm|pnpm|yarn|bun)\s+(run\s+)?(build|test|tsc|typecheck|lint)\b", "前端构建/测试"),
    (r"\bnpx\s+tsx\b", "tsx 测试"),
    (r"\bmake\b|\bcargo\s+(build|test)\b", "构建/测试"),
]

NOISE_READONLY_HINT = re.compile(
    r"^\s*(git\s+(status|log|diff|show|branch|rev-parse|merge-base|ls-files|grep)\b"
    r"|ls\b|cat\b|head\b|tail\b|wc\b|find\b|grep\b|rg\b|pwd\b|which\b|type\b|echo\b"
    r"|Get-Content\b|Select-String\b|Get-ChildItem\b|Get-FileHash\b|Test-Path\b)"
)


class Counts:
    __slots__ = ("mutating", "readonly", "unknown")

    def __init__(self) -> None:
        self.mutating = 0
        self.readonly = 0
        self.unknown = 0


def default_root() -> Path:
    env = os.environ.get("REASONIX_HOME")
    if env:
        return Path(env)
    appdata = os.environ.get("APPDATA")
    if appdata:
        return Path(appdata) / "reasonix"
    return Path.home() / ".reasonix"


def candidate_sessions(root: Path) -> list[Path]:
    """收集可能的会话 jsonl（主 sessions/ 与各 project 的 sessions/）。"""
    out: list[Path] = []
    skip = (".events.jsonl", ".turns.jsonl", ".conflicts.jsonl")
    patterns = [root / "sessions", *(root / "projects").glob("*/sessions")] if (root / "projects").is_dir() \
        else [root / "sessions"]
    for d in patterns:
        if not d.is_dir():
            continue
        for p in sorted(d.glob("*.jsonl")):
            if p.name.endswith(skip):
                continue
            out.append(p)
    return out


def resolve_session(root: Path, needle: str) -> Path | None:
    direct = Path(needle)
    if direct.is_file():
        return direct
    hits = [p for p in candidate_sessions(root) if needle in p.name or needle == p.stem]
    if not hits:
        return None
    hits.sort(key=lambda p: p.stat().st_mtime, reverse=True)
    return hits[0]


def extract_command(tc: dict) -> str:
    raw = tc.get("arguments")
    if isinstance(raw, dict):
        for key in ("command", "cmd", "script", "code"):
            if isinstance(raw.get(key), str):
                return raw[key]
        return json.dumps(raw, ensure_ascii=False)
    if isinstance(raw, str):
        try:
            obj = json.loads(raw)
            if isinstance(obj, dict):
                for key in ("command", "cmd", "script", "code"):
                    if isinstance(obj.get(key), str):
                        return obj[key]
        except Exception:
            pass
        return raw
    return ""


def classify_bash(cmd: str) -> tuple[str, str]:
    """返回 (类别, 原因)。类别 ∈ {mutate, build, readonly, unknown}。"""
    if not cmd.strip():
        return "unknown", "空命令"
    for pat, why in BASH_MUTATE_PATTERNS:
        if re.search(pat, cmd):
            return "mutate", why
    for pat, why in BASH_BUILD_PATTERNS:
        if re.search(pat, cmd):
            return "build", why
    if NOISE_READONLY_HINT.match(cmd):
        return "readonly", "只读命令"
    return "unknown", "未能判定（建议人工看原文）"


def scan(path: Path, deep: bool) -> dict:
    per_tool: dict[str, Counts] = {}
    # 工具名 -> {判定依据: 次数}，用于说明每个数字是怎么来的
    mutating_detail: dict[str, collections.Counter] = {}
    bash_cmds: list[str] = []
    build_calls = 0
    assistant_turns = 0
    tool_calls = 0
    bad_lines = 0

    with path.open("r", encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if '"tool_calls"' not in line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                bad_lines += 1
                continue
            if rec.get("role") != "assistant":
                continue
            calls = rec.get("tool_calls")
            if not isinstance(calls, list) or not calls:
                continue
            assistant_turns += 1
            for tc in calls:
                if not isinstance(tc, dict):
                    continue
                name = tc.get("name") or "<unnamed>"
                tool_calls += 1
                c = per_tool.setdefault(name, Counts())

                recovery = tc.get("tool_recovery")
                ro = recovery.get("read_only") if isinstance(recovery, dict) else None

                if ro is False:
                    c.mutating += 1
                    mutating_detail.setdefault(name, collections.Counter())["应用判定"] += 1
                elif ro is True:
                    c.readonly += 1
                else:
                    # tool_recovery 为 null 或字段缺失 → 回退（实测约占 17%）
                    if name in MUTATING_TOOLS:
                        c.mutating += 1
                        mutating_detail.setdefault(name, collections.Counter())["工具名回退"] += 1
                    elif name in READONLY_TOOLS:
                        c.readonly += 1
                    elif name in AMBIGUOUS_TOOLS and name in ("bash", "pwsh", "shell"):
                        kind, why = classify_bash(extract_command(tc))
                        if kind == "mutate":
                            c.mutating += 1
                            mutating_detail.setdefault(name, collections.Counter())[f"命令特征:{why}"] += 1
                        elif kind == "build":
                            build_calls += 1
                            c.readonly += 1
                        elif kind == "readonly":
                            c.readonly += 1
                        else:
                            c.unknown += 1
                    else:
                        c.unknown += 1

                if deep and name in ("bash", "pwsh", "shell"):
                    cmd = extract_command(tc).strip().replace("\n", " ⏎ ")
                    if len(cmd) > 160:
                        cmd = cmd[:160] + "…"
                    if cmd:
                        bash_cmds.append(cmd)

    totals = Counts()
    for c in per_tool.values():
        totals.mutating += c.mutating
        totals.readonly += c.readonly
        totals.unknown += c.unknown

    return {
        "path": str(path),
        "assistant_turns": assistant_turns,
        "tool_calls": tool_calls,
        "bad_lines": bad_lines,
        "per_tool": per_tool,
        "totals": totals,
        "mutating_detail": mutating_detail,
        "bash_cmds": bash_cmds,
        "build_calls": build_calls,
    }


def toolstats_of(path: Path) -> dict | None:
    ts = Path(str(path) + ".toolstats.json")
    if not ts.is_file():
        return None
    try:
        return json.loads(ts.read_text(encoding="utf-8", errors="replace"))
    except Exception:
        return None


def report(res: dict, deep: bool, as_json: bool, max_lines: int) -> int:
    totals: Counts = res["totals"]
    per_tool: dict[str, Counts] = res["per_tool"]
    violated = totals.mutating > 0

    if as_json:
        print(json.dumps({
            "session": res["path"],
            "assistant_turns": res["assistant_turns"],
            "tool_calls": res["tool_calls"],
            "mutating": totals.mutating,
            "readonly": totals.readonly,
            "unknown": totals.unknown,
            "verdict": "violation" if violated else "clean",
            "per_tool": {k: {"mutating": v.mutating, "readonly": v.readonly, "unknown": v.unknown}
                         for k, v in sorted(per_tool.items())},
        }, ensure_ascii=False, indent=2))
        return 1 if violated else 0

    print("=" * 68)
    print("管理会话违规自检（SKILL.md §4c）")
    print("=" * 68)
    print(f"会话文件 : {res['path']}")
    print(f"助手轮次 : {res['assistant_turns']}   工具调用 : {res['tool_calls']}")
    if res["bad_lines"]:
        print(f"⚠️ 解析失败行 : {res['bad_lines']}（已跳过，结果可能偏低）")
    print()
    print(f"{'工具':<26}{'变更':>6}{'只读':>6}{'未知':>6}")
    print("-" * 68)
    for name, c in sorted(per_tool.items(), key=lambda kv: (-kv[1].mutating, kv[0])):
        flag = "  ← 越界" if c.mutating else ""
        print(f"{name:<26}{c.mutating:>6}{c.readonly:>6}{c.unknown:>6}{flag}")
    print("-" * 68)
    print(f"{'合计':<26}{totals.mutating:>6}{totals.readonly:>6}{totals.unknown:>6}")

    if totals.mutating:
        print()
        print("--- 变更类调用明细（按判定依据拆开，便于识别误报）---")
        shown = 0
        for name, reasons in sorted(res["mutating_detail"].items(),
                                    key=lambda kv: -sum(kv[1].values())):
            total = sum(reasons.values())
            parts = " / ".join(f"{k} {v}" for k, v in reasons.most_common())
            print(f"  {name}: {total} 次   [{parts}]")
            shown += 1
            if shown >= max_lines:
                print("  …（更多见 --json）")
                break

    if deep:
        cmds = res["bash_cmds"]
        if cmds:
            print()
            print("--- bash 命令原文（--deep，仅供人工判断）---")
            print("    注意：这是该会话的**全部** bash 调用，不分只读/变更。")
            for i, cmd in enumerate(cmds[:max_lines], 1):
                print(f"  {i:>3}. {cmd}")
            if len(cmds) > max_lines:
                print(f"  …另有 {len(cmds) - max_lines} 条（用 --max-print 调整）")

    ts = toolstats_of(Path(res["path"]))
    if ts and isinstance(ts.get("tools"), dict):
        print()
        print("--- 附带 toolstats（应用侧计数）---")
        print("    ⚠️ 实测与 jsonl 计数**严重不一致**（同一会话 bash：toolstats 181 vs jsonl 1059），")
        print("       疑为近端窗口或不完整统计。仅作存在性参考，**不要用它替代上面的计数**。")
        for name, v in sorted(ts["tools"].items()):
            if isinstance(v, dict):
                print(f"      {name:<24} calls={v.get('calls')}  hardErrors={v.get('hardErrors')}")

    print()
    print("=" * 68)
    if violated:
        print(f"⚠️ 变更类调用 {totals.mutating} 次 / 只读 {totals.readonly} 次")
        print("   本判据**只对管理会话有意义**（管理会话应为 0）。")
        print("   若你扫的是开发 / 合并会话，出现大量变更调用是**正常的**，不是违规。")
        print("   处置（确认是管理会话后）：排除只读定位误判；确认越界则立即停手，")
        print("   把该工作整段转派给 wt 线，并如实告知已产生的改动。")
        print("   同一批次连续 2 次巡检越界 → 必须在下次向用户汇报中明示。")
    else:
        print("✅ 判定：干净 —— 未发现变更类调用（管理会话的期望值）")
    if totals.unknown:
        print(f"ℹ️ 另有 {totals.unknown} 次无法判定（tool_recovery 为 null 且工具双用途）；")
        print("   需人工确认时加 --deep 查看命令原文。")
    print("=" * 68)
    return 1 if violated else 0


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(
        description="管理会话违规自检：统计指定会话的变更类工具调用（SKILL.md §4c）")
    ap.add_argument("--session", help="会话名（子串匹配）或 jsonl 绝对路径")
    ap.add_argument("--root", help="reasonix 根目录（默认 %%APPDATA%%\\reasonix）")
    ap.add_argument("--list-sessions", action="store_true", help="列出可用的会话文件")
    ap.add_argument("--deep", action="store_true", help="打印 bash 命令原文（截断）供人工判断")
    ap.add_argument("--json", action="store_true", dest="as_json", help="输出 JSON")
    ap.add_argument("--max-print", type=int, default=15, help="明细最多打印条数（默认 15）")
    args = ap.parse_args(argv)

    root = Path(args.root) if args.root else default_root()

    if args.list_sessions:
        sessions = candidate_sessions(root)
        if not sessions:
            print(f"未找到会话文件（root={root}）", file=sys.stderr)
            return 2
        sessions.sort(key=lambda p: p.stat().st_mtime, reverse=True)
        for p in sessions[:40]:
            ts = datetime.datetime.fromtimestamp(p.stat().st_mtime).strftime("%m-%d %H:%M")
            print(f"{ts}  {p.stat().st_size/1024:>9,.0f}KB  {p.name}")
        if len(sessions) > 40:
            print(f"…另有 {len(sessions) - 40} 个")
        return 0

    if not args.session:
        ap.error("需要 --session（或 --list-sessions）")

    path = resolve_session(root, args.session)
    if path is None:
        print(f"找不到会话：{args.session}（root={root}）", file=sys.stderr)
        print("用 --list-sessions 查看可用会话名。", file=sys.stderr)
        return 2

    try:
        res = scan(path, args.deep)
    except OSError as exc:
        print(f"读取失败：{exc}", file=sys.stderr)
        return 2
    return report(res, args.deep, args.as_json, args.max_print)


if __name__ == "__main__":
    raise SystemExit(main())
