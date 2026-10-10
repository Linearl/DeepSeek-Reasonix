#!/usr/bin/env python3
"""events 膨胀处置盘点（任务 373 R4 备料）——只读，绝不修改任何文件。

遍历 Reasonix 用户状态根下的 projects/，对每个 live 会话事件日志
（*.events.jsonl，跳过 .trash 与 *-recovery-* 副本，口径与桌面侧
walkProjectsBytesMB 一致）统计：

  events   事件日志大小（瘦身对象）
  live     主转录 jsonl 大小（live 内容代理，fold 后的 log ≈ live）
  reclaim  回收上限 = max(0, events - live)
  判定      是否超自动闸（factor 倍数 / cap MiB，从 config.toml 读取）
  占用      .lease.lock / .lease.json 是否在（在 = 可能忙，compact 会安全拒绝）

输出：按回收上限降序的清单 + 现成的 `reasonix session compact` 命令清单。
脚本只 stat / 读 config / 读 lease 文件名，从不执行 compact——真正执行由人
（或用户授权的会话）粘贴命令完成，或在桌面存储面板点「修复」。

用法：
  python scripts/events_disposal_plan.py                 # 默认状态根
  python scripts/events_disposal_plan.py --root <dir>    # 指定状态根
  python scripts/events_disposal_plan.py --min-reclaim-mb 10
"""

import argparse
import os
import sys
import time

EVENTS_SUFFIX = ".events.jsonl"
DEFAULT_FACTOR = 4.0
DEFAULT_CAP_MB = 0


def default_state_root():
    appdata = os.environ.get("APPDATA")
    if appdata:
        return os.path.join(appdata, "reasonix")
    home = os.path.expanduser("~")
    return os.path.join(home, ".config", "reasonix")


def read_rotation_settings(root):
    """从 config.toml 读取自动闸阈值（解析失败用内置默认 4x / 无 cap）。"""
    factor, cap_mb = DEFAULT_FACTOR, DEFAULT_CAP_MB
    path = os.path.join(root, "config.toml")
    try:
        with open(path, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line.startswith("events_rotation_factor"):
                    try:
                        factor = float(line.split("=", 1)[1].split("#", 1)[0].strip())
                    except ValueError:
                        pass
                elif line.startswith("events_rotation_cap_mb"):
                    try:
                        cap_mb = int(float(line.split("=", 1)[1].split("#", 1)[0].strip()))
                    except ValueError:
                        pass
    except OSError:
        pass
    return factor, cap_mb


def scan(root):
    """返回 [(events_bytes, live_bytes, path, mtime)]，口径同桌面侧 live-growth。"""
    projects = os.path.join(root, "projects")
    rows = []
    for dirpath, dirnames, filenames in os.walk(projects):
        dirnames[:] = [d for d in dirnames if d != ".trash"]
        for name in filenames:
            if not name.endswith(EVENTS_SUFFIX) or "-recovery-" in name:
                continue
            path = os.path.join(dirpath, name)
            try:
                events = os.path.getsize(path)
                mtime = os.path.getmtime(path)
            except OSError:
                continue
            live_path = path[: -len(EVENTS_SUFFIX)] + ".jsonl"
            try:
                live = os.path.getsize(live_path)
            except OSError:
                live = 0
            rows.append((events, live, path, mtime))
    return rows


def main():
    parser = argparse.ArgumentParser(description="events 膨胀处置盘点（只读）")
    parser.add_argument("--root", default=default_state_root(), help="Reasonix 用户状态根")
    parser.add_argument("--min-reclaim-mb", type=float, default=10.0,
                        help="列入处置清单的最小回收上限（MiB，默认 10）")
    args = parser.parse_args()

    factor, cap_mb = read_rotation_settings(args.root)
    rows = scan(args.root)
    if not rows:
        print(f"no live events logs under {args.root}")
        return 0

    total_events = sum(r[0] for r in rows)
    total_live = sum(r[1] for r in rows)
    mib = 1024.0 * 1024.0

    print(f"state root : {args.root}")
    print(f"gate       : auto factor {factor:g}x, cap {cap_mb} MiB (0 = off)")
    print(f"totals     : {len(rows)} live logs, {total_events/mib:.1f} MiB events, "
          f"{total_live/mib:.1f} MiB live, aggregate ratio {total_events/max(total_live,1):.2f}")
    print()

    enriched = []
    for events, live, path, mtime in rows:
        reclaim = max(0, events - live)
        over_factor = events > max(256 * 1024, int(live * factor))
        over_cap = cap_mb > 0 and events > cap_mb * 1024 * 1024
        live_over_cap = cap_mb > 0 and live > cap_mb * 1024 * 1024
        lease = os.path.exists(path + ".lease.lock") or os.path.exists(path + ".lease.json")
        enriched.append({
            "events": events, "live": live, "path": path, "reclaim": reclaim,
            "over": over_factor or over_cap, "live_over_cap": live_over_cap,
            "lease": lease, "mtime": mtime,
        })

    enriched.sort(key=lambda r: (-r["reclaim"], -r["events"]))
    print(f"{'events':>9} {'live':>9} {'reclaim':>9} {'ratio':>6}  {'gate':<4} {'futile':<6} {'lease':<5} mtime              path")
    for r in enriched:
        if r["reclaim"] < args.min_reclaim_mb * mib and not r["over"]:
            continue
        ratio = r["events"] / max(r["live"], 1)
        stamp = time.strftime("%Y-%m-%d %H:%M", time.localtime(r["mtime"]))
        print(f"{r['events']/mib:9.1f} {r['live']/mib:9.1f} {r['reclaim']/mib:9.1f} "
              f"{ratio:6.2f}  {'OVER' if r['over'] else '':<4} "
              f"{'YES' if r['live_over_cap'] else '':<6} "
              f"{'busy?' if r['lease'] else '':<5} {stamp}  {r['path']}")

    plan = [r for r in enriched
            if r["reclaim"] >= args.min_reclaim_mb * mib and r["over"] and not r["lease"]]
    total_reclaim = sum(r["reclaim"] for r in plan)
    print()
    print(f"处置清单（回收 >= {args.min_reclaim_mb:g} MiB 且超闸且无 lease 占用）："
          f"{len(plan)} 项，预期回收上限 {total_reclaim/mib:.1f} MiB"
          f"（fold 后 log≈live，实际回收以命令输出为准）")
    if any(r["live_over_cap"] for r in enriched):
        futile = [r for r in enriched if r["live_over_cap"]]
        print(f"注意：{len(futile)} 项 live 内容本身超 cap（futile=YES）——修复能回收"
              f" events−live 差值，但修完仍超 cap，属「修复无效类」，别指望修到 cap 之下。")
    if plan:
        hot = [r for r in plan if time.time() - r["mtime"] < 24 * 3600]
        if hot:
            print()
            print(f"注意：{len(hot)} 项 24h 内仍有写入（可能正被桌面/serve 打开）——"
                  f"compact 前确认已关闭；CLI 遇忙会话会安全拒绝（session is not idle），不会损坏数据。")
        print()
        print("# 逐条执行（会话须闲置；CLI 会安全拒绝忙会话）：")
        for r in plan:
            print(f'reasonix session compact "{r["path"]}"')
    return 0


if __name__ == "__main__":
    sys.exit(main())
