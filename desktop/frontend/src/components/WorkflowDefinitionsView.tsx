// tl1300 —— 工作流定义视图（工作流面板的二级页签，懒加载 chunk）。
//
// 「定义目录管理」拍板形态：并入现有 WorkflowPanel 加「定义」页签，卡片列出
// 已存定义（名称 / 版本 / 描述 / 节点·门禁计数 / 运行 / 改名 / 删除），不新增
// 独立入口。数据面与 /workflow 斜杠命令、模板视图、agent workflow_run 工具
// 共享同一进程级 store（SSOT——清单来自 workflow 服务单例）。
//
// 能力面（最小版取舍）：
//   运行 = 与面板模板视图同一运行通道（RunPreview 披露确认 → RunTemplate），
//     运行即回调 onRunStarted（面板切回流程视图并选中该 run，进度同 tab 可见）；
//   删除 = 二段确认（第一击武装，再击执行；点别处/完成即解除）——项目树同款
//     纪律；head 文件 + 历史目录一并删除（服务端语义）；
//   改名 = 行内输入 + 客户端 slug 预检（与 Go store 同口径，workflowSlug）；
//   查看 = canonical JSON 只读展示（无编辑器——编辑走模板视图，见交付申报）。
// 写面（运行/删除/改名）服务端自检 workflowTemplates 开关；开关关闭时本视图
// 只读（卡片+查看），并给指路提示，绝不假装可点。

import { useCallback, useEffect, useState } from "react";
import { Eye, EyeOff, Pencil, Play, ShieldAlert, ShieldCheck, Trash2, X } from "lucide-react";
import { useT } from "../lib/i18n";
import type { DictKey } from "../locales/en";
import { app } from "../lib/bridge";
import { reportFrontendLog } from "../lib/frontendLog";
import { labFlagEnabled, onLabFlagsChange } from "../lib/labFlags";
import { definitionCounters, validateRename, type RenameProblem } from "../lib/workflowTemplates";
import type { WorkflowTemplateList, WorkflowTemplateRunPreview, WorkflowTemplateSummary } from "../lib/workflowTypes";

const COUNTER_KEYS: Record<"nodes" | "gates" | "subagents" | "confirms", DictKey> = {
  nodes: "workflowPanel.definitions.count.nodes",
  gates: "workflowPanel.definitions.count.gates",
  subagents: "workflowPanel.definitions.count.subagents",
  confirms: "workflowPanel.definitions.count.confirms",
};

const RENAME_PROBLEM_KEYS: Record<RenameProblem, DictKey> = {
  empty: "workflowPanel.definitions.renameEmpty",
  unchanged: "workflowPanel.definitions.renameUnchanged",
  taken: "workflowPanel.definitions.renameTaken",
};

function errorText(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/** 行内改名的暂存态（value 未裁剪，提交时裁剪；problem 实时预检结论）。 */
interface RenameDraft {
  name: string;
  value: string;
  problem: RenameProblem | null;
}

export function WorkflowDefinitionsView({ onRunStarted }: { onRunStarted: (runId: string) => void }) {
  const t = useT();
  const [list, setList] = useState<WorkflowTemplateList | null>(null);
  const [writeEnabled, setWriteEnabled] = useState(() => labFlagEnabled("workflowTemplates"));
  const [notice, setNotice] = useState<{ tone: "ok" | "error"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  // 删除二段确认：armed = 已武装待再击的名字（同时只武装一张卡）。
  const [armedDelete, setArmedDelete] = useState<string | null>(null);
  const [renameDraft, setRenameDraft] = useState<RenameDraft | null>(null);
  const [sourceOf, setSourceOf] = useState<string | null>(null);
  const [sourceText, setSourceText] = useState<string | null>(null);
  const [preview, setPreview] = useState<WorkflowTemplateRunPreview | null>(null);

  useEffect(() => onLabFlagsChange(() => setWriteEnabled(labFlagEnabled("workflowTemplates"))), []);

  const fail = useCallback((what: string, err: unknown) => {
    const detail = errorText(err);
    reportFrontendLog("workflowDefinitions", `${what} failed`, detail);
    setNotice({ tone: "error", text: t("workflowPanel.templates.error", { detail }) });
  }, [t]);

  const refreshList = useCallback(async () => {
    try {
      setList(await app.WorkflowTemplates());
    } catch (err) {
      fail("list", err);
    }
  }, [fail]);

  useEffect(() => {
    void refreshList();
  }, [refreshList]);

  const names = (list?.templates ?? []).map((tpl) => tpl.name);

  // ── 运行：与模板视图同一通道（preview 披露 → run）──────────────────────
  const askRun = useCallback(async (name: string) => {
    try {
      setPreview(await app.WorkflowTemplateRunPreview(name));
    } catch (err) {
      fail("preview", err);
    }
  }, [fail]);

  const confirmRun = useCallback(async () => {
    if (!preview) return;
    setBusy(true);
    try {
      const res = await app.WorkflowTemplateRun(preview.name);
      reportFrontendLog("workflowDefinitions", "run started", `${preview.name} ${res.runId}`);
      setPreview(null);
      onRunStarted(res.runId);
    } catch (err) {
      fail("run", err);
    } finally {
      setBusy(false);
    }
  }, [preview, fail, onRunStarted]);

  // ── 删除：二段确认（第一击武装 → 再击执行）───────────────────────────
  const onDeleteClicked = useCallback((name: string) => {
    // 再击同一张卡 = 确认执行；其余情况只是换武装目标。
    if (armedDelete === name) {
      setArmedDelete(null);
      setBusy(true);
      app.WorkflowTemplateDelete(name)
        .then(() => {
          setNotice({ tone: "ok", text: t("workflowPanel.definitions.deleted", { name }) });
          reportFrontendLog("workflowDefinitions", "deleted", name);
          if (sourceOf === name) {
            setSourceOf(null);
            setSourceText(null);
          }
          return refreshList();
        })
        .catch((err) => fail("delete", err))
        .finally(() => setBusy(false));
      return;
    }
    setArmedDelete(name);
  }, [armedDelete, fail, refreshList, sourceOf, t]);

  // ── 改名：行内编辑 + slug 预检（提交后服务端仍权威复核）────────────────
  const beginRename = useCallback((tpl: WorkflowTemplateSummary) => {
    setArmedDelete(null);
    setRenameDraft({ name: tpl.name, value: tpl.name, problem: null });
  }, []);

  const renameValue = renameDraft?.value ?? "";
  const onRenameInput = useCallback((value: string) => {
    setRenameDraft((draft) => draft ? { ...draft, value, problem: validateRename(draft.name, value, names) } : draft);
  }, [names]);

  const commitRename = useCallback(async () => {
    if (!renameDraft || renameDraft.problem !== null) return;
    const next = renameDraft.value.trim();
    setBusy(true);
    try {
      await app.WorkflowTemplateRename(renameDraft.name, next);
      setNotice({ tone: "ok", text: t("workflowPanel.definitions.renamed", { name: next }) });
      reportFrontendLog("workflowDefinitions", "renamed", `${renameDraft.name} -> ${next}`);
      setRenameDraft(null);
      if (sourceOf === renameDraft.name) {
        setSourceOf(null);
        setSourceText(null);
      }
      await refreshList();
    } catch (err) {
      fail("rename", err);
    } finally {
      setBusy(false);
    }
  }, [renameDraft, fail, refreshList, sourceOf, t]);

  // ── 查看：canonical JSON 只读（无编辑面）─────────────────────────────
  const toggleSource = useCallback(async (name: string) => {
    if (sourceOf === name) {
      setSourceOf(null);
      setSourceText(null);
      return;
    }
    try {
      const src = await app.WorkflowTemplateSource(name);
      setSourceOf(name);
      setSourceText(src.document);
    } catch (err) {
      fail("open", err);
    }
  }, [sourceOf, fail]);

  const cards = list?.templates ?? [];
  const invalid = writeEnabled ? (list?.invalid ?? []) : [];

  return (
    <div className="wf-tpl" data-testid="wf-defs">
      <aside className="wf-tpl__list">
        {!writeEnabled && (
          <p className="wf-tpl__note" data-testid="wf-defs-hint">{t("workflowPanel.definitions.disabledHint")}</p>
        )}
        {list && cards.length === 0 && invalid.length === 0 && (
          <p className="wf-tpl__empty" data-testid="wf-defs-empty">{t("workflowPanel.definitions.empty")}</p>
        )}
        {cards.map((tpl) => {
          const renaming = renameDraft?.name === tpl.name;
          const armed = armedDelete === tpl.name;
          return (
            <div
              key={tpl.name}
              className={sourceOf === tpl.name ? "wf-tpl__item wf-def-card wf-tpl__item--active" : "wf-tpl__item wf-def-card"}
              data-testid="wf-def-card"
            >
              <div className="wf-def-card__head">
                <span className="wf-tpl__item-name">{tpl.name}</span>
                <span className="wf-tpl__item-meta">{tpl.version}</span>
              </div>
              {(tpl.description ?? "").trim() !== "" ? (
                <span className="wf-tpl__item-meta wf-def-card__desc">{tpl.description}</span>
              ) : (
                <span className="wf-tpl__item-meta wf-def-card__desc">{t("workflowPanel.definitions.noDescription")}</span>
              )}
              <span className="wf-tpl__item-meta" data-testid="wf-def-counts">
                {definitionCounters(tpl).map((c) => t(COUNTER_KEYS[c.kind], { n: String(c.n) })).join(" · ")}
              </span>
              {writeEnabled && !renaming && (
                <div className="wf-def-card__actions">
                  <button
                    type="button"
                    className="wf-panel__action"
                    disabled={busy}
                    onClick={() => void askRun(tpl.name)}
                    data-testid="wf-def-run"
                  >
                    <Play size={13} aria-hidden="true" />
                    {t("workflowPanel.templates.run")}
                  </button>
                  <button
                    type="button"
                    className="wf-panel__action"
                    disabled={busy}
                    onClick={() => beginRename(tpl)}
                    data-testid="wf-def-rename"
                  >
                    <Pencil size={13} aria-hidden="true" />
                    {t("workflowPanel.definitions.rename")}
                  </button>
                  <button
                    type="button"
                    className={armed ? "wf-panel__action wf-def-card__delete wf-def-card__delete--armed" : "wf-panel__action wf-def-card__delete"}
                    disabled={busy}
                    aria-pressed={armed}
                    onClick={() => onDeleteClicked(tpl.name)}
                    data-testid="wf-def-delete"
                    data-armed={armed || undefined}
                  >
                    <Trash2 size={13} aria-hidden="true" />
                    {t(armed ? "workflowPanel.definitions.deleteConfirm" : "workflowPanel.definitions.delete")}
                  </button>
                  <button
                    type="button"
                    className="wf-panel__action"
                    aria-pressed={sourceOf === tpl.name}
                    onClick={() => void toggleSource(tpl.name)}
                    data-testid="wf-def-view"
                  >
                    {sourceOf === tpl.name ? <EyeOff size={13} aria-hidden="true" /> : <Eye size={13} aria-hidden="true" />}
                    {t(sourceOf === tpl.name ? "workflowPanel.definitions.sourceHide" : "workflowPanel.definitions.source")}
                  </button>
                </div>
              )}
              {renaming && (
                <div className="wf-def-rename" data-testid="wf-def-rename-row">
                  <input
                    className="wf-def-rename__input"
                    value={renameValue}
                    autoFocus
                    aria-label={t("workflowPanel.definitions.renamePlaceholder")}
                    data-testid="wf-def-rename-input"
                    onChange={(e) => onRenameInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && renameDraft?.problem === null) void commitRename();
                      if (e.key === "Escape") setRenameDraft(null);
                    }}
                  />
                  <button
                    type="button"
                    className="wf-panel__action"
                    disabled={busy || renameDraft?.problem !== null}
                    onClick={() => void commitRename()}
                    data-testid="wf-def-rename-save"
                  >
                    {t("workflowPanel.definitions.renameSave")}
                  </button>
                  <button
                    type="button"
                    className="wf-panel__action"
                    onClick={() => setRenameDraft(null)}
                    data-testid="wf-def-rename-cancel"
                  >
                    {t("workflowPanel.definitions.renameCancel")}
                  </button>
                  {renameDraft?.problem && (
                    <span className="wf-tpl__err" data-testid="wf-def-rename-problem">{t(RENAME_PROBLEM_KEYS[renameDraft.problem])}</span>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {invalid.map((inv) => (
          <div key={inv.file} className="wf-tpl__item wf-tpl__item--invalid wf-def-card" data-testid="wf-defs-invalid" title={inv.message}>
            <div className="wf-def-card__head">
              <span className="wf-tpl__item-name">{inv.file}</span>
              <span className="wf-tpl__badge">{t("workflowPanel.templates.invalidBadge")}</span>
            </div>
            <span className="wf-tpl__item-meta wf-def-card__desc">{t("workflowPanel.definitions.invalidHint")}</span>
          </div>
        ))}
        {notice && (
          <p className={notice.tone === "ok" ? "wf-tpl__ok" : "wf-tpl__err"} data-testid="wf-defs-notice">{notice.text}</p>
        )}
      </aside>
      <section className="wf-tpl__main">
        {sourceOf === null ? (
          <p className="wf-tpl__empty">{t("workflowPanel.definitions.title")}</p>
        ) : (
          <div className="wf-def-source" data-testid="wf-defs-source">
            <div className="wf-def-source__bar">
              <span className="wf-tpl__item-name">{sourceOf}</span>
              <span className="wf-tpl__spacer" />
              <button
                type="button"
                className="wf-panel__action"
                onClick={() => { setSourceOf(null); setSourceText(null); }}
                aria-label={t("common.close")}
                data-testid="wf-defs-source-close"
              >
                <X size={13} aria-hidden="true" />
              </button>
            </div>
            <pre className="wf-def-source__pre"><code>{sourceText ?? ""}</code></pre>
            <p className="wf-tpl__note">{t("workflowPanel.definitions.sourceReadOnly")}</p>
          </div>
        )}
      </section>
      {preview && (
        <div className="wf-tpl__confirm" role="dialog" aria-modal="true" aria-label={t("workflowPanel.templates.confirm.title")} data-testid="wf-defs-confirm">
          <h3>{t("workflowPanel.templates.confirm.title")} · {preview.name} {preview.version}</h3>
          <p className="wf-tpl__note">
            {preview.workspace
              ? t("workflowPanel.templates.confirm.workspace", { path: preview.workspace })
              : t("workflowPanel.templates.confirm.noWorkspace")}
          </p>
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.gates")}</div>
          {preview.gates.length === 0 ? (
            <p className="wf-tpl__note">{t("workflowPanel.templates.confirm.noGates")}</p>
          ) : (
            preview.gates.map((g) => (
              <div key={g.nodeId} className={g.denied ? "wf-tpl__gate wf-tpl__gate--denied" : "wf-tpl__gate"} data-testid="wf-defs-gate">
                {g.denied ? <ShieldAlert size={13} aria-hidden="true" /> : <ShieldCheck size={13} aria-hidden="true" />}
                <span className="wf-tpl__item-meta">{g.nodeId}</span>
                <code>{g.command}</code>
                {g.denied && <span className="wf-tpl__err">{t("workflowPanel.templates.confirm.denied")}</span>}
              </div>
            ))
          )}
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.subagents")}</div>
          {preview.subagents.map((sa) => (
            <div key={sa.nodeId} className="wf-tpl__gate">
              <span className="wf-tpl__item-meta">{sa.nodeId}</span>
              <span>
                {sa.readOnly
                  ? t("workflowPanel.templates.confirm.readOnly")
                  : sa.allTools
                    ? t("workflowPanel.templates.confirm.allTools")
                    : (sa.tools ?? []).join(", ")}
                {sa.timeout ? ` · timeout ${sa.timeout}` : ""}
                {sa.maxSteps ? ` · max_steps ${sa.maxSteps}` : ""}
              </span>
            </div>
          ))}
          <div className="wf-tpl__confirm-section">{t("workflowPanel.templates.confirm.budget")}</div>
          <p className="wf-tpl__note">
            {preview.budget
              ? [preview.budget.tokens ? `tokens ${preview.budget.tokens}` : "", preview.budget.rounds ? `rounds ${preview.budget.rounds}` : "", preview.budget.wall ? `wall ${preview.budget.wall}` : ""].filter(Boolean).join(" · ")
              : t("workflowPanel.templates.confirm.noBudget")}
          </p>
          <p className="wf-tpl__warn">{t("workflowPanel.templates.confirm.warning")}</p>
          {preview.blocked && <p className="wf-tpl__err">{t("workflowPanel.templates.confirm.blocked")}</p>}
          <div className="wf-tpl__confirm-actions">
            <button type="button" className="wf-panel__action" onClick={() => setPreview(null)} data-testid="wf-defs-confirm-cancel">
              {t("workflowPanel.templates.confirm.cancel")}
            </button>
            <button type="button" className="wf-panel__action wf-tpl__primary" disabled={preview.blocked || busy} onClick={() => void confirmRun()} data-testid="wf-defs-confirm-run">
              <Play size={13} aria-hidden="true" />
              {t("workflowPanel.templates.confirm.run")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
