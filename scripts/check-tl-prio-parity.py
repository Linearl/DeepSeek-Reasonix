#!/usr/bin/env python3
"""check-tl-prio-parity.py — tl1811 优先级校验对拍（防双实现漂移）。

前提扫描结论（20261125 tl1811 实施时全量核实）：fork 仓库内**不存在**
任何写 tl tasklist.db 的内置入口——
  - 唯一 tasks 表访问面 desktop/tasklist_panel.go 全程 mode=ro 仅 SELECT
    （文件头「只读红线」+ DSN 强制 mode=ro，探测句柄同）；
  - 任务书点名的 desktop/tasks_app.go / task_catalog.go 实为后台作业
    taskmonitor 存储（.reasonix/tasks FileStore），无优先级字段、不触碰
    tasks 表；
  - Wails bridge / CLI 子命令 / MCP 均无 tasklist 写绑定；
  - internal/config/edit.go（tl1746）明文 "writes stay with tl.py as the
    SSOT ... never unlocks a write path"。
即写入面只有 tl.py 一处（优先级规范化由其 _norm_prio 收口，20261125 起），
「双实现漂移」当前无代码载体——故本件最小落地 = 钉死对拍用例表 + 对拍
harness，不新建 Go/TS 校验实现（避免死代码与第二真相源）。

约定：未来任何写 tl DB 的内置入口（Go/TS）落地时——
  1) 优先级语义必须与下方 CASES/REJECTS 表一致（该表 = tl.py _norm_prio
     20261125 行为快照：P0-P3 + 符号映射 🟡⚠→P2 / 🔴❗→P1 / ⚪🟢→P3 +
     trim/空格/大小写容错 + 空串放行 + 非法拒绝）；
  2) 合入前跑本脚本与 tl.py 实测对拍（同输入同结果），漂移即红。

用法：
  python -I scripts/check-tl-prio-parity.py [--tl-py <tl.py 路径>]
tl.py 定位顺序：--tl-py > 环境变量 TL_PY > %APPDATA% 默认全局工作区路径。
tl.py 缺席时只做用例表结构自检（不判红，verdict=self-check）。
退出码：0=对拍通过/自检通过；1=对拍不一致；2=用法/加载错误。

输出信封（仿 internal/cli/output_contract.go verdict 语义）：
  verdict: ok | self-check | error；逐用例行 + 汇总。
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import sys

TOOL = "check-tl-prio-parity"

# 对拍用例表（tl.py _norm_prio 20261125 行为快照；钉表防漂移）。
# 元素：(输入, 期望)；期望 ∈ {"P0","P1","P2","P3",""}。
CASES = [
    # 合法标准集 + trim/空格/大小写容错
    ("P0", "P0"),
    ("p1", "P1"),
    (" P2 ", "P2"),
    ("p 3", "P3"),
    ("P3", "P3"),
    # 空与全空白 = 未标优先级，放行为空串
    ("", ""),
    ("   ", ""),
    # 历史符号映射（tl.py 映射序：🟡⚠→P2 / 🔴❗→P1 / ⚪🟢→P3；
    # 注意 ⚠️ 是 ⚠+变体选择符，按子串命中映射）
    ("🟡", "P2"),
    ("⚠", "P2"),
    ("⚠️", "P2"),
    ("🔴", "P1"),
    ("❗", "P1"),
    ("⚪", "P3"),
    ("🟢", "P3"),
    # 混合：符号命中即短路映射，余段丢弃（tl.py 行为如此，快照钉住）
    ("🟡p2", "P2"),
]

# 非法输入：tl.py _norm_prio 打印错误并 SystemExit(1)。
REJECTS = ["P9", "p4", "P2X", "高", "urgent", "2"]

VALID = {"P0", "P1", "P2", "P3", ""}


def structural_selfcheck() -> list[str]:
    """用例表结构自检（不依赖 tl.py，任何环境可跑）。"""
    problems: list[str] = []
    seen: set[str] = set()
    for raw, expect in CASES:
        if not isinstance(raw, str) or expect not in VALID:
            problems.append(f"非法表项: ({raw!r}, {expect!r})")
        if raw in seen:
            problems.append(f"重复输入: {raw!r}")
        seen.add(raw)
    if not REJECTS:
        problems.append("REJECTS 为空——拒绝面未钉")
    if any(not isinstance(r, str) or r == "" for r in REJECTS):
        problems.append("REJECTS 含空/非字符串项")
    if not CASES:
        problems.append("CASES 为空")
    return problems


def load_norm_prio(tl_path: str):
    """从 tl.py 文件路径加载 _norm_prio（进程内、纯函数、零 DB 触碰）。"""
    spec = importlib.util.spec_from_file_location("tl_parity_target", tl_path)
    if spec is None or spec.loader is None:
        raise ImportError(f"无法从 {tl_path} 构建 module spec")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)  # tl.py 模块级仅定义常量与 import _tl_lock（无副作用）
    fn = getattr(module, "_norm_prio", None)
    if fn is None:
        raise ImportError(f"{tl_path} 未找到 _norm_prio（tl.py 版本过旧或被重构，请更新对拍表基准）")
    return fn


def default_tl_py() -> str:
    appdata = os.environ.get("APPDATA", "")
    if appdata:
        return os.path.join(
            appdata, "reasonix", "global-workspace",
            "docs", "reasonix", "tasklist", "scripts", "tl.py",
        )
    return ""


def main(argv: list[str] | None = None) -> int:
    # 管道/重定向下 Python 回落 locale 编码（Windows=GBK），中文即 mojibake；
    # 统一抬到 UTF-8（工作区日志口径即 UTF-8）。
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8")
    parser = argparse.ArgumentParser(prog=TOOL, description=__doc__.split("\n")[0])
    parser.add_argument("--tl-py", dest="tl_py", default="", help="tl.py 路径（缺省按 TL_PY/默认工作区定位）")
    args = parser.parse_args(argv)

    problems = structural_selfcheck()
    if problems:
        print(json.dumps({"tool": TOOL, "verdict": "error", "detail": "用例表结构自检失败"}, ensure_ascii=False))
        for p in problems:
            print(f"  FAIL  {p}")
        return 1
    print(f"  PASS  用例表结构自检（{len(CASES)} 正例 + {len(REJECTS)} 拒绝例）")

    tl_path = args.tl_py or os.environ.get("TL_PY", "") or default_tl_py()
    if not tl_path or not os.path.isfile(tl_path):
        print(json.dumps({"tool": TOOL, "verdict": "self-check",
                          "detail": "tl.py 未找到——仅完成用例表自检，未做实测对拍"}, ensure_ascii=False))
        print(f"  SKIP  定位顺序 --tl-py > TL_PY > 默认工作区，最后尝试: {tl_path or '(空)'}")
        return 0

    try:
        norm_prio = load_norm_prio(tl_path)
    except Exception as exc:  # noqa: BLE001 — 加载失败按用法错误上报
        print(json.dumps({"tool": TOOL, "verdict": "error", "detail": f"tl.py 加载失败: {exc}"}, ensure_ascii=False))
        return 2
    print(f"  对拍目标: {tl_path}")

    failed: list[tuple[object, object, object]] = []
    for raw, expect in CASES:
        got = norm_prio(raw)
        mark = "PASS" if got == expect else "FAIL"
        if got != expect:
            failed.append((raw, expect, got))
        print(f"  {mark}  {raw!r} -> 期望 {expect!r} / 实得 {got!r}")

    for raw in REJECTS:
        try:
            got = norm_prio(raw)
        except SystemExit as exc:
            ok = exc.code == 1
            mark = "PASS" if ok else "FAIL"
            if not ok:
                failed.append((raw, "SystemExit(1)", f"SystemExit({exc.code!r})"))
            print(f"  {mark}  {raw!r} -> 期望拒绝 SystemExit(1) / 实得 {('SystemExit(%r)' % exc.code)!r}")
        else:
            failed.append((raw, "拒绝", f"放行为 {got!r}"))
            print(f"  FAIL  {raw!r} -> 期望拒绝 / 实得放行 {got!r}")

    total = len(CASES) + len(REJECTS)
    if failed:
        print(json.dumps({"tool": TOOL, "verdict": "error",
                          "detail": f"对拍不一致 {len(failed)}/{total}（tl.py 行为漂移或用例表过期——先实测 tl.py 定责，再改表或改实现）"}, ensure_ascii=False))
        return 1
    print(json.dumps({"tool": TOOL, "verdict": "ok", "detail": f"对拍一致 {total}/{total}"}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
