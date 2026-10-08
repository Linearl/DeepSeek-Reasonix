# -*- coding: utf-8 -*-
"""feedback 信号扫描脚本（任务 344-A，架构评审 4 条修订后落地）。

从低危结构化源自动产出反馈草稿，落 feedback-inbox 待人确认（人审门保留：
草稿不经人确认不入候选池）。判据固化在本脚本而不是技能文档里——技能
（ll-iteration-intake / collect_issues）只消费本脚本的产出，heartbeat 只管触发。

用法：
  python scripts/feedback_signal_scan.py                # dry-run：只打印将产的草稿
  python scripts/feedback_signal_scan.py --write        # 把草稿 md 写进 feedback-inbox
  python scripts/feedback_signal_scan.py --days 14      # 拉宽回看窗口（默认 7 天）
  python scripts/feedback_signal_scan.py --self-check   # 跑 D 留出样本回归集（判据改动后必跑）

信号源分级（一期只取低危结构化源，候选池 C-20260928-01 架构评审第 3 条）：
  1. crash-pending/*.json —— 排除 0 字节与已知 lifecycle 误报（白名单前置）
  2. crash-fatal/*.log    —— 0 字节空 log 是已知噪音；非空 log 是真信号
  3. desktop.log          —— 指定告警码（perf monitor threshold），按 metric 聚合成一条
  4. 非预期 recovery 副本 —— sessions 目录出现 -recovery- 命名的副本文件
  jsonl 工具失败属二期，且只采「失败+人工干预」模式——本脚本暂不实现。

信号签名（fingerprint）：fp:<source>:<error_type>:<pattern>:<规则版本>
  A 产稿带签名、ll-iteration-intake 按签名聚类（C）、supersede 判同类（B）、
  样本回归按规则版本分基线（D）。

已知噪音白名单（误报必须是 0；依据候选池 C-20260920-02 六条合并教训）：
  - crash-pending label=desktop.abnormal_exit.v2 —— lifecycle 残留误报
    （#10634 已 merged 上游修复，fork 侧追齐核对未做前维持白名单；核对后若
      真修了，把该项从白名单移除即可让真实残留重新报警）
  - crash-pending label=windows.webview2.process_failed —— 单条挂观察，
    同签名累计 >=OBSERVE_THRESHOLD 次才升草稿
  - crash-fatal 0 字节 log —— 无证据价值

隐私口径：草稿只含本地路径与时间戳（与意见箱既有条目同口径），不读会话
正文、不写密钥/凭据。
"""
import argparse
import datetime as dt
import glob
import hashlib
import json
import os
import re
import sys

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")

RULE_VERSION = "v1"
OBSERVE_THRESHOLD = 3  # webview2.process_failed 挂观察：复现 >=3 次才升草稿（任务 377 口径）

APPDATA = os.environ.get("APPDATA", "")
DEFAULT_HOME = os.path.join(APPDATA, "reasonix")

RECOVERY_RE = re.compile(r"-recovery-[0-9a-f]{16}")
PERF_ALERT_RE = re.compile(
    r"time=(?P<ts>\d{4}-\d{2}-\d{2}T[\d:.]+[+-]\d{2}:\d{2}).*?"
    r'msg="desktop: perf monitor threshold"\s+metric=(?P<metric>\w+)\s+'
    r"value=(?P<value>[\d.]+)\s+limit=(?P<limit>[\d.]+)"
)

# ---------------------------------------------------------------------------
# 已知噪音白名单（前置闸）：返回 (是否噪音/观察, 类别, 理由)
# ---------------------------------------------------------------------------

def classify_crash_json(data):
    """对一条 crash-pending json 做白名单分类。

    返回 (verdict, note)：verdict ∈ {"noise", "observe", "draft"}。
    """
    label = (data.get("label") or "").strip()
    if not label:
        return "draft", "无 label 字段（未知形态）"
    if label == "desktop.abnormal_exit.v2":
        return "noise", "lifecycle 残留误报（C-20260920-02 白名单；#10634 追齐核对前维持）"
    if label == "windows.webview2.process_failed":
        return "observe", "单条挂观察（任务 377：复现 >=%d 次再升草稿）" % OBSERVE_THRESHOLD
    return "draft", "未知 label，白名单未覆盖"


def crash_fingerprint(data, path):
    label = (data.get("label") or "unknown").strip()
    etype = (data.get("errorType") or "unknown").strip()
    return "fp:crash-pending:%s:%s:%s" % (label, etype, RULE_VERSION)


def crash_evidence_id(path, data):
    """去重用证据 ID：文件名 + 事件时间。"""
    occurred = (data.get("occurredAt") or "").strip()
    return os.path.basename(path) + "|" + occurred


# ---------------------------------------------------------------------------
# 各信号源扫描
# ---------------------------------------------------------------------------

def scan_crash_dir(crash_dir, days, now=None):
    """扫描 crash-pending/*.json。返回 (drafts, noise_count, observe_count)。"""
    now = now or dt.datetime.now(dt.timezone.utc)
    cutoff = now - dt.timedelta(days=days)
    drafts, noise, observe = [], 0, 0
    observe_seen = {}
    for path in sorted(glob.glob(os.path.join(crash_dir, "*.json"))):
        try:
            if os.path.getsize(path) == 0:
                noise += 1  # 0 字节结构化文件同样无证据价值
                continue
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                data = json.load(f)
        except (OSError, ValueError):
            drafts.append({
                "source": "crash-pending", "verdict": "draft",
                "fingerprint": "fp:crash-pending:unreadable-json:%s:%s" % ("corrupt", RULE_VERSION),
                "evidence_id": os.path.basename(path),
                "title": "crash-pending 出现不可读 json",
                "evidence": "文件：%s（解析失败，可能半写）" % path,
                "note": "解析失败本身即信号（半写/损坏）",
            })
            continue
        # 时间过滤：occurredAt 优先，缺省退回文件 mtime
        when = None
        occ = (data.get("occurredAt") or "").strip()
        if occ:
            try:
                when = dt.datetime.fromisoformat(occ.replace("Z", "+00:00"))
            except ValueError:
                when = None
        if when is None:
            when = dt.datetime.fromtimestamp(os.path.getmtime(path), dt.timezone.utc)
        if when < cutoff:
            continue
        verdict, note = classify_crash_json(data)
        if verdict == "noise":
            noise += 1
            continue
        if verdict == "observe":
            fp = crash_fingerprint(data, path)
            observe_seen[fp] = observe_seen.get(fp, 0) + 1
            if observe_seen[fp] < OBSERVE_THRESHOLD:
                observe += 1
                continue
            note = "同签名已复现 %d 次，达观察阈值，升草稿" % observe_seen[fp]
        mhead = (data.get("message") or "").strip().splitlines()
        drafts.append({
            "source": "crash-pending",
            "verdict": "draft",
            "fingerprint": crash_fingerprint(data, path),
            "evidence_id": crash_evidence_id(path, data),
            "title": "crash-pending 新增 %s（%s）" % (
                (data.get("label") or "unknown"), (data.get("errorType") or "unknown")),
            "evidence": "文件：%s\n发生时间：%s\n包版本：%s\n摘要：%s" % (
                path, occ or "-", data.get("version") or "-",
                mhead[0] if mhead else "-"),
            "note": note,
        })
    return drafts, noise, observe


def scan_crash_fatal(crash_dir, days, now=None):
    """扫描 crash-fatal/*.log：0 字节=已知噪音；非空=真信号。"""
    now = now or dt.datetime.now(dt.timezone.utc)
    cutoff = now - dt.timedelta(days=days)
    drafts, noise = [], 0
    for path in sorted(glob.glob(os.path.join(crash_dir, "*.log"))):
        try:
            size = os.path.getsize(path)
            mtime = dt.datetime.fromtimestamp(os.path.getmtime(path), dt.timezone.utc)
        except OSError:
            continue
        if mtime < cutoff:
            continue
        if size == 0:
            noise += 1  # 空 fatal log：C-20260920-02 已知噪音
            continue
        head = ""
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                head = f.read(2000).strip().splitlines()[0] if size else ""
        except OSError:
            pass
        drafts.append({
            "source": "crash-fatal",
            "verdict": "draft",
            "fingerprint": "fp:crash-fatal:nonempty-log:%s" % RULE_VERSION,
            "evidence_id": os.path.basename(path),
            "title": "crash-fatal 出现非空 log（疑似真崩溃）",
            "evidence": "文件：%s（%d 字节，mtime %s）\n首行：%s" % (
                path, size, mtime.isoformat(), head[:200] or "-"),
            "note": "非空 fatal log = 有证据价值的真信号",
        })
    return drafts, noise


def scan_desktop_log(log_path, days, now=None):
    """扫描 desktop.log 指定告警码（perf monitor threshold），按 metric 聚合。"""
    now = now or dt.datetime.now(dt.timezone.utc)
    cutoff = now - dt.timedelta(days=days)
    if not os.path.isfile(log_path):
        return [], 0
    per_metric = {}
    try:
        with open(log_path, "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                m = PERF_ALERT_RE.search(line)
                if not m:
                    continue
                try:
                    ts = dt.datetime.fromisoformat(m.group("ts"))
                except ValueError:
                    continue
                if ts.tzinfo is None:
                    ts = ts.replace(tzinfo=now.astimezone().tzinfo)
                if ts < cutoff:
                    continue
                metric = m.group("metric")
                slot = per_metric.setdefault(metric, {
                    "count": 0, "max_value": 0.0, "limit": m.group("limit"),
                    "first": ts, "last": ts})
                slot["count"] += 1
                slot["max_value"] = max(slot["max_value"], float(m.group("value")))
                slot["last"] = max(slot["last"], ts)
    except OSError:
        return [], 0
    drafts = []
    for metric in sorted(per_metric):
        slot = per_metric[metric]
        drafts.append({
            "source": "desktop.log",
            "verdict": "draft",
            "fingerprint": "fp:desktop.log:perf-threshold:%s:%s" % (metric, RULE_VERSION),
            "evidence_id": "%s|%s" % (metric, slot["last"].date().isoformat()),
            "title": "desktop.log 持续告警 perf threshold（%s 峰值 %.0f / limit %s）" % (
                metric, slot["max_value"], slot["limit"]),
            "evidence": "日志：%s\n时间窗：%s ~ %s（近 %d 天 %d 条，已按 metric 聚合）" % (
                log_path, slot["first"].isoformat(), slot["last"].isoformat(),
                days, slot["count"]),
            "note": "同签名跨日持续告警时，intake 聚类频次会放大优先级",
        })
    return drafts, 0


def scan_recovery(home, days, now=None):
    """扫描 sessions 目录的非预期 recovery 副本（-recovery-<16hex> 命名）。

    按副本分组（同一 `-recovery-<hex>` token 的主文件与 sidecar 算一个副本），
    草稿证据列副本清单而非逐文件，避免一个副本十几行刷屏。
    """
    now = now or dt.datetime.now(dt.timezone.utc)
    cutoff = now - dt.timedelta(days=days)
    copies = {}  # token -> [最新mtime, 代表文件, 文件数]
    sess_dirs = [os.path.join(home, "sessions")]
    sess_dirs += sorted(glob.glob(os.path.join(home, "projects", "*", "sessions")))
    for d in sess_dirs:
        if not os.path.isdir(d):
            continue
        for name in os.listdir(d):
            m = RECOVERY_RE.search(name)
            if not m:
                continue
            p = os.path.join(d, name)
            try:
                mtime = dt.datetime.fromtimestamp(os.path.getmtime(p), dt.timezone.utc)
            except OSError:
                continue
            if mtime < cutoff:
                continue
            slot = copies.setdefault(m.group(0), [mtime, p, 0])
            slot[2] += 1
            if mtime > slot[0]:
                slot[0] = mtime
    if not copies:
        return [], 0
    lines = ["副本 token %s：%d 个文件，最新写入 %s（%s）" % (
        tok, slot[2], slot[0].isoformat(), os.path.dirname(slot[1]))
        for tok, slot in sorted(copies.items())]
    stem_hash = hashlib.sha1(
        "|".join(sorted(copies)).encode("utf-8")).hexdigest()[:12]
    drafts = [{
        "source": "recovery",
        "verdict": "draft",
        "fingerprint": "fp:recovery:unexpected-copy:%s" % RULE_VERSION,
        "evidence_id": "%d|%s" % (len(copies), stem_hash),
        "title": "出现 %d 个非预期 recovery 会话副本" % len(copies),
        "evidence": "\n".join(lines),
        "note": "recovery 副本高频出现 = 崩溃/恢复链路异常的旁证（对照 352 窗口期副本分叉）",
    }]
    return drafts, 0


# ---------------------------------------------------------------------------
# 草稿落盘（人审门：confirmed: false，collect_issues 不收取未确认草稿）
# ---------------------------------------------------------------------------

DRAFT_TMPL = """---
at: {at}
category: bug
origin: signal-scan
fingerprint: {fingerprint}
evidence-id: {evidence_id}
confirmed: false
rule-version: {rule_version}
tags: [signal-scan, {source}]
---

# [signal-scan] {title}

- 信号签名：`{fingerprint}`
- 证据指针：
{evidence}

- 判据备注：{note}

> 本条由 `scripts/feedback_signal_scan.py` 自动产出（任务 344-A），**未经人确认
> 不入候选池**。人确认方式：把 frontmatter 的 `confirmed: false` 改为 `true`。
> 修复落地后由收取方写 `superseded-by:` 指向新条目（任务 344-B supersede 语义）。
"""


def inbox_has_draft(inbox, draft):
    """按 fingerprint+evidence-id 查重：inbox 已有同签名同证据的草稿则跳过。"""
    for path in glob.glob(os.path.join(inbox, "feedback-*.md")):
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                head = f.read(800)
        except OSError:
            continue
        if ("fingerprint: " + draft["fingerprint"]) in head and \
           ("evidence-id: " + draft["evidence_id"]) in head:
            return True
    return False


def write_drafts(inbox, drafts, now=None):
    now = now or dt.datetime.now(dt.timezone.utc)
    written = []
    for d in drafts:
        if inbox_has_draft(inbox, d):
            continue
        ts = now.strftime("%Y%m%d-%H%M%S")
        slug = re.sub(r"[^a-z0-9]+", "-", d["source"]).strip("-")
        path = os.path.join(inbox, "feedback-%s-signal-%s.md" % (ts, slug))
        for i in range(1, 50):
            if not os.path.exists(path):
                break
            path = os.path.join(inbox, "feedback-%s-signal-%s-%d.md" % (ts, slug, i))
        content = DRAFT_TMPL.format(
            at=now.strftime("%Y-%m-%dT%H:%M:%SZ"),
            fingerprint=d["fingerprint"], evidence_id=d["evidence_id"],
            rule_version=RULE_VERSION, source=d["source"],
            title=d["title"], evidence=d["evidence"], note=d["note"])
        with open(path, "w", encoding="utf-8", newline="\n") as f:
            f.write(content)
        written.append(path)
    return written


# ---------------------------------------------------------------------------
# D 留出样本回归集（self-check）
# ---------------------------------------------------------------------------

SAMPLES_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                           "feedback_signal_scan_samples")


def self_check():
    """跑留出样本回归集：负样本必须被白名单拦下（误报=0），正样本必须出草稿。

    样本集文件名约定：<expect>-<序号>-<说明>.<ext>
      expect ∈ noise | draft；ext=json → crash-pending 分类器；ext=log → crash-fatal。
    另含内置用例：perf 告警行解析、recovery 命名匹配（纯函数，不需 fixture）。
    返回退出码：0=全过，1=有失败。
    """
    failures = []
    passed = 0

    def check(name, cond, detail=""):
        nonlocal passed
        if cond:
            passed += 1
            print("PASS %s" % name)
        else:
            failures.append(name)
            print("FAIL %s %s" % (name, detail))

    jsons = sorted(glob.glob(os.path.join(SAMPLES_DIR, "*.json")))
    logs = sorted(glob.glob(os.path.join(SAMPLES_DIR, "*.log")))
    if len(jsons) + len(logs) == 0:
        print("FAIL 样本集为空：%s 不存在或无样本" % SAMPLES_DIR)
        return 1

    for path in jsons:
        name = os.path.basename(path)
        expect = name.split("-", 1)[0]  # noise | draft | observe
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            data = json.load(f)
        verdict, note = classify_crash_json(data)
        check("crash:%s" % name, verdict == expect,
              "期望 %s 实得 %s（%s）" % (expect, verdict, note))

    for path in logs:
        name = os.path.basename(path)
        expect = name.split("-", 1)[0]
        size = os.path.getsize(path)
        is_empty_noise = (expect == "noise")
        check("fatal:%s" % name, (size == 0) == is_empty_noise,
              "期望 %s 实得 size=%d" % (expect, size))

    # 内置用例：perf 告警行
    line_ok = ('time=2026-10-02T12:53:06.310+08:00 level=WARN msg="desktop: perf monitor threshold" '
               'metric=eventsMb value=1120.05 limit=512 suppressed=3 sinceMinutes=19')
    m = PERF_ALERT_RE.search(line_ok)
    check("perf:解析告警行", m is not None and m.group("metric") == "eventsMb"
          and m.group("limit") == "512")
    check("perf:普通行不误配",
          PERF_ALERT_RE.search('time=2026-10-02T12:00:00.000+08:00 level=INFO msg="boot: stage timings" total=3400') is None)

    # 内置用例：recovery 命名（clean_recovery_copies.py 约定：名称含 -recovery- 且后接 16 位 hex）
    check("recovery:匹配副本命名",
          RECOVERY_RE.search("20260907-045906.topicname-recovery-1234567890abcdef.jsonl") is not None)
    check("recovery:普通会话不匹配",
          RECOVERY_RE.search("20260907-045906.999197700-deepseek-v4-flash.jsonl") is None)

    total = passed + len(failures)
    print("---")
    print("样本回归：%d/%d 通过（规则版本 %s）" % (passed, total, RULE_VERSION))
    if failures:
        print("失败项：%s" % ", ".join(failures))
        return 1
    return 0


# ---------------------------------------------------------------------------

def main():
    ap = argparse.ArgumentParser(description="feedback 信号扫描（任务 344-A，默认 dry-run）")
    ap.add_argument("--home", default=DEFAULT_HOME, help="ReasonixHome（默认 %%APPDATA%%\\reasonix）")
    ap.add_argument("--inbox", default=None, help="feedback-inbox 覆盖（默认 <home>\\feedback-inbox）")
    ap.add_argument("--days", type=int, default=7, help="回看窗口天数（默认 7）")
    ap.add_argument("--write", action="store_true", help="真正写入草稿（默认 dry-run 只打印）")
    ap.add_argument("--self-check", action="store_true", help="跑 D 留出样本回归集")
    args = ap.parse_args()

    if args.self_check:
        sys.exit(self_check())

    home = args.home
    inbox = args.inbox or os.path.join(home, "feedback-inbox")
    now = dt.datetime.now(dt.timezone.utc)

    all_drafts, total_noise = [], 0
    d1, n1, o1 = scan_crash_dir(os.path.join(home, "crash-pending"), args.days, now)
    d2, n2 = scan_crash_fatal(os.path.join(home, "crash-fatal"), args.days, now)
    d3, _ = scan_desktop_log(os.path.join(home, "logs", "desktop", "desktop.log"), args.days, now)
    d4, _ = scan_recovery(home, args.days, now)
    observe_note = "%d 条观察中" % o1 if o1 else "无"
    all_drafts = d1 + d2 + d3 + d4
    total_noise = n1 + n2

    fresh = []
    for d in all_drafts:
        dup = inbox_has_draft(inbox, d) if os.path.isdir(inbox) else False
        d["dup"] = dup
        if not dup:
            fresh.append(d)

    print("feedback 信号扫描 %s（窗口 %d 天，规则 %s）" % (
        now.strftime("%Y-%m-%d %H:%M UTC"), args.days, RULE_VERSION))
    print("白名单拦下已知噪音 %d 条；观察中 %s" % (total_noise, observe_note))
    print("草稿 %d 条（其中 %d 条 inbox 已有同签名同证据，跳过）" % (
        len(all_drafts), len(all_drafts) - len(fresh)))
    for d in all_drafts:
        mark = "已有 " if d["dup"] else ("将写 " if args.write else "建议 ")
        print("[%s%s] %s\n    签名: %s\n    证据: %s" % (
            mark, d["source"], d["title"], d["fingerprint"],
            d["evidence"].replace("\n", " | ")))
    if args.write and fresh:
        if not os.path.isdir(inbox):
            os.makedirs(inbox, exist_ok=True)
        written = write_drafts(inbox, fresh, now)
        print("已写入 %d 条草稿（confirmed: false，待人确认）：" % len(written))
        for p in written:
            print("  " + p)
    elif not args.write:
        print("dry-run：未写任何文件。确认无误后加 --write 落草稿。")
    return 0


if __name__ == "__main__":
    main()
