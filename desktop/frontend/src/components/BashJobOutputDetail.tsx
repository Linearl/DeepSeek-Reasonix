// tl1479 — capsule 内后台 bash 任务的输出详情视图（用户视角）。
//
// 数据通路：App.JobOutputForTab → Controller.JobOutput → jobs.Manager
// .SnapshotOutputForSession。Go 侧快照读是非消费的（bash_output 的增量游标
// 不被动），且服务端已截到 64KiB 尾部；本组件只负责呈现。
//
// 三件事：
// 1. 自动刷新节流——仅运行中轮询（BASH_OUTPUT_REFRESH_MS 一跳），上一跳
//    未返回（in-flight）则本跳跳过；终态后不再轮询（终态那一跳已带最终
//    输出）。测试可经 refreshMs prop 注入更快的节奏，生产默认值不变。
// 2. 跟随滚动可开关——开（默认）＝每次新内容都钉在底部；关＝视口不动，
//    供用户回看历史。显式开关语义：手动滚动不偷改开关状态。
// 3. 复制——把去噪后的全文写进剪贴板（与视图显示同一份文本）。
//
// 用户视角可读：输出经 stripTerminalControlSequences 去掉 ANSI/控制序列
// （复用终端组件族的 lib/terminalOutput），不是原始 JSON；前端再自护一道
// 显示上限（BASH_OUTPUT_DISPLAY_MAX_CHARS，尾部截断 + 提示），极端大输出
// 也不会把 popover 渲染拖死。
//
// 组件是纯展示：数据入口经 props 注入（onJobOutput），与 onCancelJob 同一
// 模式，tsx harness 无需加载 bridge 模块图即可打桩。Absent prop 时胶囊
// bash 行不出现「输出」入口，字节级等价 pre-1479。

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft } from "lucide-react";
import { useT, type Translator } from "../lib/i18n";
import { stripTerminalControlSequences } from "../lib/terminalOutput";
import type { JobOutputView, JobView } from "../lib/types";

// 渲染上限：Go 侧每次快照已限 64KiB，此处只是防御性的第二道闸（例如旧版
// 后端或异常路径给了超长文本），只保留尾部并给出截断提示。
export const BASH_OUTPUT_DISPLAY_MAX_CHARS = 200_000;

// 自动刷新节奏（毫秒）。1s 一跳足够跟上构建/测试日志；节流靠 in-flight
// 守卫：上一跳没回来就不发下一跳，慢盘/大数据不会堆积并发请求。
export const BASH_OUTPUT_REFRESH_MS = 1000;

// displaySlice caps the rendered text to its tail. Pure helper so the perf
// pin can hold without mounting the component.
export function displaySlice(text: string): { shown: string; truncated: boolean } {
  if (text.length <= BASH_OUTPUT_DISPLAY_MAX_CHARS) return { shown: text, truncated: false };
  return { shown: text.slice(-BASH_OUTPUT_DISPLAY_MAX_CHARS), truncated: true };
}

// jobStatusLabel maps the job's machine status to the capsule's existing
// vocabulary; unknown statuses fall back to the raw text (same rule as
// endedCapsuleStatusKey).
export function jobStatusLabel(status: string, t: Translator): string {
  switch (status) {
    case "running":
      return t("subagent.phase.running");
    case "done":
      return t("composer.capsuleStatusCompleted");
    case "failed":
      return t("composer.capsuleStatusFailed");
    case "interrupted":
      return t("composer.capsuleStatusInterrupted");
    case "killed":
      return t("composer.bashOutputStatusKilled");
    default:
      return status;
  }
}

export function BashJobOutputDetail({
  job,
  tabId,
  onJobOutput,
  onBack,
  refreshMs = BASH_OUTPUT_REFRESH_MS,
}: {
  job: JobView;
  /** 所属 runtime 的 tab id；"" = 当前激活控制器（App 侧自己解析）。 */
  tabId: string;
  onJobOutput?: (tabId: string, jobID: string) => Promise<JobOutputView>;
  onBack: () => void;
  /** 测试注入用；生产保持默认节奏。 */
  refreshMs?: number;
}) {
  const t = useT();
  const [text, setText] = useState("");
  const [status, setStatus] = useState(job.status || "running");
  const [found, setFound] = useState(true);
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [follow, setFollow] = useState(true);
  const [copied, setCopied] = useState(false);
  const inflightRef = useRef(false);
  const scrollRef = useRef<HTMLPreElement>(null);
  const followRef = useRef(true);
  followRef.current = follow;
  const copiedTimerRef = useRef<number | null>(null);

  const pull = useCallback(async () => {
    if (!onJobOutput || inflightRef.current) return;
    inflightRef.current = true;
    try {
      const view = await onJobOutput(tabId, job.id);
      setText(view.text ?? "");
      if (view.status) setStatus(view.status);
      setFound(view.found !== false);
      setFailed(false);
    } catch {
      setFailed(true);
    } finally {
      inflightRef.current = false;
      setLoaded(true);
    }
  }, [onJobOutput, tabId, job.id]);

  // 首屏立即拉一次（历史输出），之后只在运行中按节拍轮询；终态即停。
  useEffect(() => {
    void pull();
  }, [pull]);
  useEffect(() => {
    if (status !== "running") return;
    const timer = window.setInterval(() => void pull(), refreshMs);
    return () => window.clearInterval(timer);
  }, [status, pull, refreshMs]);

  // 跟随滚动：开启时每次新内容把视口钉回底部。显式开关——手动回看历史
  // 不偷改状态，用户重新打开开关才恢复跟随。
  useLayoutEffect(() => {
    if (!followRef.current) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [text]);

  useEffect(() => () => {
    if (copiedTimerRef.current !== null) window.clearTimeout(copiedTimerRef.current);
  }, []);

  const readable = useMemo(() => stripTerminalControlSequences(text), [text]);
  const { shown, truncated } = useMemo(() => displaySlice(readable), [readable]);

  const copy = async () => {
    if (!readable) return;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(readable);
      } else {
        const area = document.createElement("textarea");
        area.value = readable;
        area.style.position = "fixed";
        area.style.opacity = "0";
        document.body.appendChild(area);
        area.select();
        document.execCommand("copy");
        area.remove();
      }
      setCopied(true);
      if (copiedTimerRef.current !== null) window.clearTimeout(copiedTimerRef.current);
      copiedTimerRef.current = window.setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  };

  const viewState = !loaded
    ? "loading"
    : failed
      ? "failed"
      : !found
        ? "missing"
        : shown.length === 0
          ? "empty"
          : "ready";

  return (
    <div className="capsule-bash-detail" data-capsule-bash-detail={job.id} data-capsule-bash-state={viewState} data-capsule-bash-status={status}>
      <header className="capsule-panel__header capsule-panel__header--detail">
        <button type="button" className="capsule-panel__back" aria-label={t("composer.capsuleBack")} onClick={onBack}>
          <ArrowLeft size={13} aria-hidden="true" />
          {t("composer.capsuleBack")}
        </button>
        <strong className="capsule-panel__detail-title">{job.detail || job.label || job.id}</strong>
      </header>
      <div className="capsule-bash-detail__toolbar">
        <span className="capsule-bash-detail__status" data-capsule-bash-status-label="true">{jobStatusLabel(status, t)}</span>
        <button
          type="button"
          className="capsule-bash-detail__toggle"
          aria-pressed={follow}
          data-capsule-bash-follow={follow ? "on" : "off"}
          title={t("composer.bashOutputFollow")}
          onClick={() => setFollow((v) => !v)}
        >
          {t("composer.bashOutputFollow")}
        </button>
        <button
          type="button"
          className="capsule-bash-detail__copy"
          data-capsule-bash-copy="true"
          disabled={viewState !== "ready"}
          onClick={() => void copy()}
        >
          {copied ? t("composer.bashOutputCopied") : t("common.copy")}
        </button>
      </div>
      <pre ref={scrollRef} className="capsule-bash-detail__text" data-capsule-bash-detail-text={viewState} tabIndex={0}>
        {viewState === "loading"
          ? t("composer.bashOutputLoading")
          : viewState === "failed"
            ? t("composer.bashOutputFailed")
            : viewState === "missing"
              ? t("composer.bashOutputFailed")
              : viewState === "empty"
                ? t("composer.bashOutputEmpty")
                : shown}
      </pre>
      {truncated && (
        <div className="capsule-bash-detail__notice" role="note" data-capsule-bash-truncated="true">
          {t("composer.bashOutputTruncated", { n: BASH_OUTPUT_DISPLAY_MAX_CHARS.toLocaleString() })}
        </div>
      )}
    </div>
  );
}
