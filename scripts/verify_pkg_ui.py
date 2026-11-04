#!/usr/bin/env python3
"""verify_pkg_ui — 装机后 UI 验证自动化（tl1382）.

把出包 handoff 的「装机后验证点」表交给 CDP（Chrome DevTools Protocol）逐项
断言，输出 PASS / FAIL / NEEDS_HUMAN（需人工）三态报告，替代人工逐项肉眼
检查。三态语义：

    PASS        CDP 断言通过（DOM / computed style / 日志计数）
    FAIL        CDP 断言失败——这是装机验证要抓的回归
    NEEDS_HUMAN 无法自动化的面（真截图鼠标行为 / 主题视觉观感 / 需要人为
                构造坏环境的项），报告保留操作与预期原文供人工执行

组成（职责分离，沿用 ui-verify.mjs「清单解析归脚本、执行归驱动」的分工，
tl1382 补上 CDP 执行腿）：
    runner   本脚本，通用：解析验证表、连 CDP、执行 spec 断言、出报告
    spec     scripts/verify_pkg_ui_specs/<包>.json，每包一份断言清单（数据，
             可代码评审；click_text 受运行时危险动作 denylist 约束）
    handoff  docs/handoff/fork开发-包<N>部署安装handoff-*.md 的 §装机后验证
             点表（| 验证 | 操作 | 预期 |），验证表是状态来源

用法：
    python scripts/verify_pkg_ui.py run --spec scripts/verify_pkg_ui_specs/pkg71-v1.38.3-20261103.json \
        --handoff <handoff.md> --report <out.md>
    python scripts/verify_pkg_ui.py plan --spec <spec.json> --handoff <handoff.md>
    python scripts/verify_pkg_ui.py probe

依赖：python playwright（desktop-automation 技能线已具备）；被验证的桌面端
需打开 CDP 调试端口（设置→实验→experimental_cdp_debug_port，重启生效），
端点写入 <REASONIX_HOME|APPDATA>/reasonix/logs/desktop/cdp-endpoint.txt。

spec 断言 JS 契约：snippet 必须是 `(() => { ... return {pass: <bool>,
detail: "<证据>"}; })()` 形式的表达式；detail 进报告。
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
sys.stderr.reconfigure(encoding="utf-8")

PASS, FAIL, NEEDS_HUMAN = "PASS", "FAIL", "NEEDS_HUMAN"

# click_text 的危险动作 denylist：装机验证只点导航/面板切换类按钮，凡是
# 视觉文本命中下列词根的按钮一律拒绝执行（spec 评审是第一道线，这里是
# 运行时兜底——「删除选中对话」「重启并更新」「强制归档」这类动作永远不该
# 出现在自动化里）。
CLICK_TEXT_DENY = ("删除", "回滚", "重启", "强制", "清空", "停用", "卸载", "注销", "重置", "撕锁")

ENDPOINT_FILE_NAME = os.path.join("logs", "desktop", "cdp-endpoint.txt")


# ── 端点解析 ────────────────────────────────────────────────────────────────


def resolve_endpoint_file() -> Path | None:
    """与 desktop/cdp_debug_port.go 的 cdpDebugEndpointFile 对齐：env 覆盖优先，
    其次 REASONIX_HOME，最后 %APPDATA%/reasonix。"""
    custom = os.environ.get("REASONIX_CDP_ENDPOINT_FILE", "").strip()
    if custom:
        return Path(custom)
    home = os.environ.get("REASONIX_HOME", "").strip()
    if home:
        return Path(home) / ENDPOINT_FILE_NAME
    appdata = os.environ.get("APPDATA", "").strip()
    if appdata:
        return Path(appdata) / "reasonix" / ENDPOINT_FILE_NAME
    return None


def read_endpoint(path: Path | None) -> tuple[str, str]:
    """返回 (endpoint "host:port", 来源说明)；读不到/未就绪时 endpoint 为 ""。"""
    if path is None or not path.is_file():
        return "", f"端点文件不存在: {path}"
    text = path.read_text(encoding="utf-8", errors="replace").strip()
    if not text or text == "pending":
        return "", f"端点未就绪（文件内容: {text!r}）；CDP 开关打开后需等浏览器子进程上报端口"
    if ":" not in text:
        return "", f"端点文件内容无法解析: {text!r}"
    return text.splitlines()[0].strip(), str(path)


def http_json(base: str, path: str, timeout: float = 4.0):
    import urllib.request

    with urllib.request.urlopen(f"http://{base}{path}", timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


# ── handoff 验证表解析 ──────────────────────────────────────────────────────


def split_row(line: str) -> list[str]:
    return [c.strip() for c in line.strip().strip("|").split("|")]


def is_separator(cells: list[str]) -> bool:
    return all(re.fullmatch(r":?-{2,}:?", c) for c in cells if c)


def parse_handoff(md_path: Path) -> list[dict]:
    """解析 handoff 里的「装机后验证点」表：表头含 验证+操作 的表格，每行
    一个验证项 {验证, 操作, 预期}。"""
    rows: list[dict] = []
    lines = md_path.read_text(encoding="utf-8", errors="replace").splitlines()
    i = 0
    while i < len(lines):
        line = lines[i].lstrip(">").strip()
        if not line.startswith("|"):
            i += 1
            continue
        header = split_row(line)
        if i + 1 < len(lines) and is_separator(split_row(lines[i + 1])) and "验证" in header and "操作" in header:
            col = {name: idx for idx, name in enumerate(header)}
            j = i + 2
            while j < len(lines):
                cells = split_row(lines[j].lstrip(">").strip())
                if not lines[j].lstrip(">").strip().startswith("|"):
                    break
                if is_separator(cells):
                    j += 1
                    continue
                rows.append({
                    "验证": cells[col["验证"]] if col["验证"] < len(cells) else "",
                    "操作": cells[col["操作"]] if col.get("操作") is not None and col["操作"] < len(cells) else "",
                    "预期": cells[col["预期"]] if col.get("预期") is not None and col["预期"] < len(cells) else "",
                })
                j += 1
            i = j
        else:
            i += 1
    return rows


# ── spec ────────────────────────────────────────────────────────────────────


def resolve_handoff_path(spec: dict, spec_path: Path, override: str | None) -> Path | None:
    """handoff 路径解析：--handoff 覆盖 > spec.handoff（绝对 / 相对 CWD /
    相对 spec 所在仓库根，即 spec_path.parents[2]）。找不到返回 None。"""
    if override:
        p = Path(override)
        return p if p.is_file() else None
    raw = str(spec.get("handoff", "")).strip()
    if not raw:
        return None
    p = Path(raw)
    if p.is_absolute():
        return p if p.is_file() else None
    for base in (Path.cwd(), spec_path.parents[2] if len(spec_path.parents) > 2 else spec_path.parent, spec_path.parent):
        cand = base / p
        if cand.is_file():
            return cand
    return None


def load_spec(path: Path) -> dict:
    spec = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(spec.get("checks"), list) or not spec["checks"]:
        raise SystemExit(f"spec 无 checks: {path}")
    for chk in spec["checks"]:
        kind = chk.get("kind")
        if kind not in ("eval", "actions", "log", "manual"):
            raise SystemExit(f"check {chk.get('id')!r}: 非法 kind {kind!r}（eval/actions/log/manual）")
        if not chk.get("id"):
            raise SystemExit("每个 check 必须有 id")
    return spec


def match_rows_to_checks(rows: list[dict], checks: list[dict]) -> tuple[list[tuple[dict, dict | None]], list[dict]]:
    """验证表驱动：每行按 spec 的 match 关键词（验证列子串命中）配一个 check；
    没配上的行 → NEEDS_HUMAN；配上了行但没被任何行引用的 check 返回出来供
    --all / 无 handoff 模式使用。"""
    assigned: list[tuple[dict, dict | None]] = []
    used: set[int] = set()
    for row in rows:
        hit = None
        for idx, chk in enumerate(checks):
            if idx in used:
                continue
            keywords = chk.get("match") or [chk["id"]]
            if any(k in row["验证"] for k in keywords):
                hit = chk
                used.add(idx)
                break
        assigned.append((row, hit))
    unused = [c for i, c in enumerate(checks) if i not in used]
    return assigned, unused


# ── 报告 ────────────────────────────────────────────────────────────────────


class Report:
    def __init__(self) -> None:
        self.entries: list[dict] = []
        self.meta: dict = {}

    def add(self, item: str, status: str, detail: str, evidence: str = "") -> None:
        self.entries.append({"item": item, "status": status, "detail": detail, "evidence": evidence})

    def counts(self) -> dict:
        out = {PASS: 0, FAIL: 0, NEEDS_HUMAN: 0}
        for e in self.entries:
            out[e["status"]] += 1
        return out

    def render(self) -> str:
        c = self.counts()
        lines = [
            "# 装机后 UI 验证报告（verify_pkg_ui · tl1382）",
            "",
            f"- 包: {self.meta.get('package', '?')}",
            f"- CDP 端点: {self.meta.get('endpoint', '?')}（{self.meta.get('browser', '?')}）",
            f"- 验证表: {self.meta.get('handoff', '(仅 spec)')}",
            f"- 时间: {time.strftime('%Y-%m-%d %H:%M:%S')}",
            "",
            f"**结论: {len(self.entries)} 项中 {c[PASS]} PASS / {c[FAIL]} FAIL / {c[NEEDS_HUMAN]} 需人工**"
            + ("（存在 FAIL，装机验证不通过）" if c[FAIL] else ""),
            "",
            "| 验证点 | 状态 | 证据/详情 |",
            "|---|---|---|",
        ]
        for e in self.entries:
            ev = f" `{e['evidence']}`" if e["evidence"] else ""
            detail = e["detail"].replace("|", "\\|").replace("\n", " ")
            lines.append(f"| {e['item']} | {e['status']} | {detail}{ev} |")
        lines.append("")
        humans = [e for e in self.entries if e["status"] == NEEDS_HUMAN]
        if humans:
            lines.append("## 人工清单（自动化不覆盖，按操作/预期逐条肉眼）")
            lines.append("")
            for e in humans:
                lines.append(f"- {e['item']}: {e['detail']}")
            lines.append("")
        return "\n".join(lines)


def write_report(rep: Report, report_path: Path | None) -> None:
    text = rep.render()
    print(text)
    if report_path:
        report_path.parent.mkdir(parents=True, exist_ok=True)
        report_path.write_text(text, encoding="utf-8")
        print(f"[verify_pkg_ui] 报告已写: {report_path}")


# ── CDP 执行器 ──────────────────────────────────────────────────────────────


class CdpRunner:
    def __init__(self, endpoint: str, evidence: Path, per_check_timeout: float) -> None:
        self.endpoint = endpoint
        self.evidence = evidence
        self.timeout = per_check_timeout
        self.page = None
        self._pw = None
        self._browser = None
        self.navigated_settings = False

    def connect(self) -> str:
        from playwright.sync_api import sync_playwright

        self._pw = sync_playwright().start()
        self._browser = self._pw.chromium.connect_over_cdp(f"http://{self.endpoint}")
        ctx = self._browser.contexts[0]
        pages = list(ctx.pages)
        target = None
        for pg in pages:
            if "wails.localhost" in pg.url:
                target = pg
                break
        if target is None:
            for pg in pages:
                if (pg.title() or "").strip() == "Reasonix":
                    target = pg
                    break
        if target is None and pages:
            target = pages[0]
        if target is None:
            raise RuntimeError("CDP 上找不到页面 target（桌面端未完成启动？）")
        self.page = target
        return f"page={target.url}"

    def browser_version(self) -> str:
        try:
            return str(http_json(self.endpoint, "/json/version").get("Browser", "?"))
        except Exception:
            return "?"

    def close(self) -> None:
        try:
            if self._browser is not None:
                self._browser.close()
        finally:
            if self._pw is not None:
                self._pw.stop()

    def screenshot(self, name: str) -> str:
        self.evidence.mkdir(parents=True, exist_ok=True)
        out = self.evidence / f"{name}.png"
        try:
            self.page.screenshot(path=str(out))
            return out.name
        except Exception as exc:  # 截图失败不改变断言结论
            return f"(截图失败: {exc})"

    # ── eval ──
    def run_eval_js(self, js: str) -> tuple[bool, str]:
        """spec JS 契约：返回 {pass, detail}。返回值异常按 FAIL 处理。"""
        result = self.page.evaluate(js)
        if not isinstance(result, dict) or "pass" not in result:
            return False, f"spec JS 未按契约返回 {{pass, detail}}: {result!r}"
        return bool(result["pass"]), str(result.get("detail", ""))

    # ── actions ──
    def _click_text(self, selector: str, text: str, exact: bool = False, strict: bool = True) -> str:
        """在 selector 命中的元素里按可见文本点击。危险动作 denylist 兜底。"""
        if any(d in text for d in CLICK_TEXT_DENY):
            raise RuntimeError(f"click_text 命中危险动作 denylist，拒绝执行: {text!r}")
        handle = self.page.evaluate_handle(
            """([sel, text, exact]) => {
              const els = [...document.querySelectorAll(sel)];
              const hit = els.find(e => {
                const t = (e.textContent || '').trim();
                return exact ? t === text : t.includes(text);
              });
              return hit || null;
            }""",
            [selector, text, exact],
        )
        el = handle.as_element()
        if el is None:
            msg = f"click_text 未命中: {selector} 内文本 {text!r}"
            if strict:
                raise RuntimeError(msg)
            return msg
        el.click(timeout=4000)
        return f"clicked {text!r}"

    def run_actions(self, chk: dict, name: str) -> tuple[bool, str, str]:
        steps = chk.get("steps") or []
        for i, step in enumerate(steps):
            if "press" in step:
                self.page.keyboard.press(step["press"])
            elif "click" in step:
                self.page.click(step["click"], timeout=4000)
            elif "click_text" in step:
                ct = step["click_text"]
                self._click_text(ct["selector"], ct["text"], bool(ct.get("exact", False)))
            elif "wait_ms" in step:
                self.page.wait_for_timeout(int(step["wait_ms"]))
            elif "eval" in step:
                self.page.evaluate(step["eval"])
            else:
                return False, f"step {i} 非法: {step!r}", ""
        passed, detail = self.run_eval_js(chk["assert_js"])
        evidence = self.screenshot(name) if chk.get("screenshot", True) else ""
        if "settings" in json.dumps(steps, ensure_ascii=False) or any(
            "settings-center__navitem" in json.dumps(s, ensure_ascii=False) for s in steps
        ):
            self.navigated_settings = True
        return passed, detail, evidence

    def restore_workspace(self) -> str:
        """settings 类 actions 跑完后把界面还原回工作台（best effort）。"""
        if not self.navigated_settings:
            return ""
        try:
            back = self.page.locator("#management-back")
            if back.count() > 0 and back.first.is_visible():
                back.first.click(timeout=3000)
                self.page.wait_for_timeout(500)
                return "已还原回工作台"
        except Exception as exc:
            return f"还原失败（不影响结论）: {exc}"
        return "未发现设置面板返回键（可能未进入设置）"


def run_log_check(chk: dict) -> tuple[bool, str]:
    """desktop.log 子串计数（与 ui-verify.mjs log 同约定：time= 打头的时间过滤）。"""
    path = Path(chk["log_file"]) if chk.get("log_file") else None
    if path is None:
        base = resolve_endpoint_file()
        if base is None:
            return False, "log_file 未指定且端点文件路径无法推导"
        path = base.parent / "desktop.log"
    if not path.is_file():
        return False, f"日志不存在: {path}"
    patterns = chk.get("patterns") or []
    if not patterns:
        return False, "log check 需要 patterns"
    since = str(chk.get("since", "")).strip()
    tail = int(chk.get("tail_lines", 0))
    lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    if tail > 0:
        lines = lines[-tail:]
    counts = {p: 0 for p in patterns}
    hits: list[str] = []
    for line in lines:
        m = re.search(r"time=(\d{4}-\d{2}-\d{2}T\d{2}:\d{2})", line)
        if since and m and m.group(1) < since:
            continue
        for p in patterns:
            if p in line:
                counts[p] += 1
                if len(hits) < 3:
                    hits.append(line.strip()[:160])
    min_hits = int(chk.get("min_hits", 1))
    total = sum(counts.values())
    detail = f"counts={counts} (min_hits={min_hits})"
    if hits:
        detail += " | " + " / ".join(hits[:2])
    return total >= min_hits, detail


# ── 主流程 ──────────────────────────────────────────────────────────────────


def cmd_run(args) -> int:
    spec_path = Path(args.spec)
    spec = load_spec(spec_path)
    checks: list[dict] = spec["checks"]

    handoff_path = None if args.no_handoff else resolve_handoff_path(spec, spec_path, args.handoff)
    if not args.no_handoff and not handoff_path and spec.get("handoff"):
        print(f"[verify_pkg_ui] 注: spec.handoff 未找到（{spec['handoff']}），按仅 spec 模式执行；可用 --handoff 指定")

    if handoff_path:
        rows = parse_handoff(handoff_path)
        if not rows:
            raise SystemExit(f"handoff 里没解析到验证点表（需表头含 验证+操作 的表格）: {handoff_path}")
        assigned, unused = match_rows_to_checks(rows, checks)
        if unused and not args.all:
            print(f"[verify_pkg_ui] 注: {len(unused)} 个 spec check 未被验证表引用（--all 可强制执行）: "
                  + ", ".join(c["id"] for c in unused))
    else:
        assigned = [(None, chk) for chk in checks]
        unused = []

    endpoint, src = (args.endpoint, "--endpoint 参数") if args.endpoint else read_endpoint(
        Path(args.endpoint_file) if args.endpoint_file else resolve_endpoint_file()
    )
    if not endpoint:
        print(f"[verify_pkg_ui] CDP 端点不可用: {src}", file=sys.stderr)
        print("提示: 装机包需先打开 设置→实验→CDP 调试端口（重启生效）；"
              "或用 --endpoint host:port / REASONIX_CDP_ENDPOINT_FILE 指定。", file=sys.stderr)
        return 2

    rep = Report()
    rep.meta = {
        "package": spec.get("package", spec_path.stem),
        "handoff": str(handoff_path or "(仅 spec)"),
        "endpoint": endpoint,
        "browser": "",
    }

    evidence = Path(args.evidence) if args.evidence else (Path(args.report).parent / "verify_pkg_ui-evidence") if args.report else Path("verify_pkg_ui-evidence")
    runner: CdpRunner | None = None
    try:
        try:
            version = http_json(endpoint, "/json/version")
            rep.meta["browser"] = str(version.get("Browser", "?"))
        except Exception as exc:
            print(f"[verify_pkg_ui] CDP 端点连不上 http://{endpoint}: {exc}", file=sys.stderr)
            return 2
        runner = CdpRunner(endpoint, evidence, args.per_check_timeout)
        runner.connect()

        stamp = time.strftime("%H%M%S")
        for idx, (row, chk) in enumerate(assigned):
            item = row["验证"] if row else (chk["id"] if chk else "?")
            if chk is None:
                rep.add(item, NEEDS_HUMAN,
                        f"未写自动化断言（操作: {row['操作']}；预期: {row['预期']}）")
                continue
            kind = chk["kind"]
            name = f"{stamp}-{chk['id']}"
            if kind == "manual":
                rep.add(item, NEEDS_HUMAN, chk.get("why", "人工面"))
                continue
            if kind == "log":
                try:
                    passed, detail = run_log_check(chk)
                except Exception as exc:
                    passed, detail = False, f"log check 异常: {exc}"
                rep.add(item, PASS if passed else FAIL, detail)
                continue
            # eval / actions 走 CDP
            try:
                if kind == "eval":
                    passed, detail = runner.run_eval_js(chk["js"])
                    evidence_name = runner.screenshot(name) if chk.get("screenshot", True) else ""
                elif kind == "actions":
                    passed, detail, evidence_name = runner.run_actions(chk, name)
                else:
                    passed, detail, evidence_name = False, f"未处理的 kind {kind}", ""
            except Exception as exc:
                passed, detail = False, f"执行异常: {type(exc).__name__}: {exc}"
                evidence_name = ""
            rep.add(item, PASS if passed else FAIL, detail, evidence_name)

        restore_note = runner.restore_workspace()
        if restore_note:
            print(f"[verify_pkg_ui] {restore_note}")
    finally:
        if runner is not None:
            runner.close()

    write_report(rep, Path(args.report) if args.report else None)
    return 1 if rep.counts()[FAIL] else 0


def cmd_plan(args) -> int:
    spec = load_spec(Path(args.spec))
    checks = spec["checks"]
    handoff_path = resolve_handoff_path(spec, Path(args.spec), args.handoff)
    if handoff_path:
        rows = parse_handoff(handoff_path)
        assigned, unused = match_rows_to_checks(rows, checks)
        print(f"验证表: {handoff_path}（{len(rows)} 行）  spec: {len(checks)} checks")
        for row, chk in assigned:
            state = chk["id"] if chk else "NEEDS_HUMAN(无断言)"
            print(f"  [{row['验证']}] -> {state}")
        if unused:
            print("未引用的 checks: " + ", ".join(c["id"] for c in unused) + "（--all 强制执行）")
    else:
        print("无 handoff，将执行全部 spec checks:")
        for c in checks:
            print(f"  {c['id']} ({c['kind']})")
    endpoint, src = read_endpoint(Path(args.endpoint_file) if args.endpoint_file else resolve_endpoint_file())
    print(f"CDP 端点: {endpoint or '(不可用: ' + src + ')'}")
    return 0


def cmd_probe(_args) -> int:
    endpoint, src = read_endpoint(Path(_args.endpoint_file) if _args.endpoint_file else resolve_endpoint_file())
    if not endpoint:
        print(f"端点不可用: {src}")
        return 2
    try:
        version = http_json(endpoint, "/json/version")
        targets = http_json(endpoint, "/json/list")
        pages = [t.get("url", "?") for t in targets if t.get("type") == "page"]
        print(f"端点 {endpoint}（{src}）: {version.get('Browser')}  页面: {pages}")
        return 0
    except Exception as exc:
        print(f"端点 {endpoint} 连不上: {exc}")
        return 2


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0], formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)

    def common(p):
        p.add_argument("--endpoint", help="CDP 端点 host:port（默认读端点文件）")
        p.add_argument("--endpoint-file", help="端点文件路径（默认 REASONIX_CDP_ENDPOINT_FILE 或安装默认路径）")

    p_run = sub.add_parser("run", help="执行验证并出三态报告")
    p_run.add_argument("--spec", required=True, help="spec JSON 路径")
    p_run.add_argument("--handoff", help="出包 handoff md（默认取 spec.handoff）")
    p_run.add_argument("--no-handoff", action="store_true", help="忽略 spec.handoff，只跑 spec checks")
    p_run.add_argument("--all", action="store_true", help="验证表未引用的 spec check 也执行")
    p_run.add_argument("--report", help="报告输出 md 路径（默认打印 stdout）")
    p_run.add_argument("--evidence", help="证据目录（默认报告同目录 verify_pkg_ui-evidence）")
    p_run.add_argument("--per-check-timeout", type=float, default=20.0, help="单 check 超时秒")
    common(p_run)
    p_run.set_defaults(func=cmd_run)

    p_plan = sub.add_parser("plan", help="不连 CDP，打印验证表→断言映射")
    p_plan.add_argument("--spec", required=True)
    p_plan.add_argument("--handoff")
    common(p_plan)
    p_plan.set_defaults(func=cmd_plan)

    p_probe = sub.add_parser("probe", help="端点连通性检查")
    common(p_probe)
    p_probe.set_defaults(func=cmd_probe)

    args = ap.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
